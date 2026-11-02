package agent_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/mrYush/hint/internal/agent"
	"github.com/mrYush/hint/pkg/agentapi"
)

// countingPreamble rebuilds the prefix from how many tool calls the
// conversation holds — a stand-in for "which rules have been touched".
type countingPreamble struct {
	calls int
	err   error
}

func (p *countingPreamble) Prefix(_ context.Context, messages []agentapi.Message) ([]agentapi.Message, error) {
	p.calls++
	if p.err != nil {
		return nil, p.err
	}
	n := 0
	for _, m := range messages {
		n += len(m.ToolCalls)
	}
	return []agentapi.Message{agentapi.SystemMessage(fmt.Sprintf("prefix with %d calls", n))}, nil
}

func TestRunTurn_PreambleRebuildsPrefixBeforeEveryRequest(t *testing.T) {
	p := &fakeProvider{scripts: []script{
		toolRound(toolCallEvent("c1", "echo", `{"text":"a"}`)),
		{events: []agentapi.ChatEvent{delta("done"), done(agentapi.FinishStop)}},
	}}
	pre := &countingPreamble{}
	a := agent.New(p, agent.WithTools(echoTool{}), agent.WithPreamble(pre))

	events := collect(t, a.RunTurn(context.Background(), question("q")))

	if end := lastEvent(events, agentapi.EventTurnEnd); end == nil || end.FinishReason != agentapi.FinishStop {
		t.Fatalf("turn_end = %+v", end)
	}
	if len(p.calls) != 2 || pre.calls != 2 {
		t.Fatalf("provider calls = %d, preamble calls = %d, want 2 and 2", len(p.calls), pre.calls)
	}
	// The first request carries the prefix built from an empty
	// conversation, the second the one rebuilt after the tool round —
	// in place of the caller's system message, not in addition to it.
	for i, want := range []string{"prefix with 0 calls", "prefix with 1 calls"} {
		msgs := p.calls[i].Messages
		if msgs[0].Role != agentapi.RoleSystem || msgs[0].Text() != want {
			t.Errorf("request %d opens with %q, want %q", i, msgs[0].Text(), want)
		}
		if msgs[1].Role != agentapi.RoleUser || msgs[1].Text() != "q" {
			t.Errorf("request %d: the user message must follow the prefix directly: %+v", i, msgs[1])
		}
	}
}

func TestRunTurn_PreambleFailureKeepsThePrefix(t *testing.T) {
	p := &fakeProvider{scripts: []script{{events: []agentapi.ChatEvent{delta("ok"), done(agentapi.FinishStop)}}}}
	a := agent.New(p, agent.WithPreamble(&countingPreamble{err: errors.New("rules unreadable")}))

	events := collect(t, a.RunTurn(context.Background(), question("q")))

	if end := lastEvent(events, agentapi.EventTurnEnd); end == nil || end.FinishReason != agentapi.FinishStop {
		t.Fatalf("turn_end = %+v, want FinishStop: a failed prefix is not a failed turn", end)
	}
	if e := lastEvent(events, agentapi.EventError); e == nil || !strings.Contains(e.Err.Message, "rules unreadable") {
		t.Fatalf("error event = %+v, want the preamble's failure reported", e)
	}
	if got := p.calls[0].Messages[0].Text(); got != "you are a test assistant" {
		t.Errorf("request opens with %q, want the caller's own system prompt", got)
	}
}

// shiftingPreamble returns one prefix on its first call and a different,
// stable one afterwards. A refresh that changes the prefix discards the
// usage figure the last request reported, so a test that wants the
// proactive trigger to see real usage needs the prefix to settle.
type shiftingPreamble struct{ calls int }

func (p *shiftingPreamble) Prefix(context.Context, []agentapi.Message) ([]agentapi.Message, error) {
	p.calls++
	text := "prefix one"
	if p.calls > 1 {
		text = "prefix two"
	}
	return []agentapi.Message{agentapi.SystemMessage(text)}, nil
}

// TestRunTurn_CompactionSummarySurvivesPreambleRefresh is #32's B1: the
// prefix is rebuilt before every request, and the summary a compaction put
// right after it must not be swept away with the prompt. The fourth request
// is the one that matters — the refresh before it is the first to run
// after the summary exists.
func TestRunTurn_CompactionSummarySurvivesPreambleRefresh(t *testing.T) {
	compactor := &stubCompactor{summary: "SUMMARY"}
	p := &fakeProvider{scripts: []script{
		{events: []agentapi.ChatEvent{
			toolCallEvent("c1", "echo", `{"text":"a"}`),
			usageEvent(90), // 90/100: over the 80% threshold, so a later iteration compacts
			done(agentapi.FinishToolCalls),
		}},
		{events: []agentapi.ChatEvent{
			toolCallEvent("c2", "echo", `{"text":"b"}`),
			usageEvent(90),
			done(agentapi.FinishToolCalls),
		}},
		{events: []agentapi.ChatEvent{
			toolCallEvent("c3", "echo", `{"text":"c"}`),
			done(agentapi.FinishToolCalls),
		}},
		{events: []agentapi.ChatEvent{delta("done"), done(agentapi.FinishStop)}},
	}}
	pre := &shiftingPreamble{}
	a := agent.New(p, agent.WithTools(echoTool{}), agent.WithCompactor(compactor),
		agent.WithPreamble(pre),
		agent.WithLimits(agent.Limits{MaxIterations: 10, MaxContextTokens: 100, CompactThreshold: 0.8}))

	events := collect(t, a.RunTurn(context.Background(), history4()))

	if end := lastEvent(events, agentapi.EventTurnEnd); end == nil || end.FinishReason != agentapi.FinishStop {
		t.Fatalf("turn_end = %+v, want FinishStop", end)
	}
	if compactor.calls != 1 {
		t.Fatalf("Compactor called %d times, want 1", compactor.calls)
	}
	if len(p.calls) != 4 {
		t.Fatalf("provider called %d times, want 4", len(p.calls))
	}

	// The request right after the compaction carries the summary where
	// compaction put it: after the prefix.
	third := p.calls[2].Messages
	if len(third) < 2 || !third[1].Summary || third[1].Text() != "SUMMARY" {
		t.Fatalf("third request = %+v, want [prefix, SUMMARY, ...]", third)
	}

	// The refresh before the fourth request is the first one to run with
	// the summary in the history; it must leave it alone.
	fourth := p.calls[3].Messages
	if fourth[0].Text() != "prefix two" {
		t.Fatalf("fourth request opens with %q, want the stable prefix", fourth[0].Text())
	}
	if len(fourth) < 2 || !fourth[1].Summary || fourth[1].Text() != "SUMMARY" {
		t.Fatalf("fourth request = %+v, want the summary to survive the refresh", fourth)
	}
}

// TestRunTurn_SummaryAtStartOfHistorySurvivesPreamble covers the resumed
// session: the CLI prepends the run's preamble in front of a stored
// conversation that opens with a compaction summary.
func TestRunTurn_SummaryAtStartOfHistorySurvivesPreamble(t *testing.T) {
	p := &fakeProvider{scripts: []script{{events: []agentapi.ChatEvent{delta("ok"), done(agentapi.FinishStop)}}}}
	pre := &countingPreamble{}
	a := agent.New(p, agent.WithPreamble(pre))

	history := []agentapi.Message{
		agentapi.SystemMessage("you are a test assistant"),
		agentapi.SummaryMessage("earlier: the user asked about main.go"),
		agentapi.UserMessage("carry on"),
	}
	collect(t, a.RunTurn(context.Background(), history))

	msgs := p.calls[0].Messages
	if msgs[0].Text() != "prefix with 0 calls" {
		t.Fatalf("request opens with %q, want the rebuilt prefix", msgs[0].Text())
	}
	if len(msgs) != 3 || !msgs[1].Summary || msgs[1].Text() != "earlier: the user asked about main.go" {
		t.Fatalf("request = %+v, want [prefix, summary, user]", msgs)
	}
	if msgs[2].Text() != "carry on" {
		t.Errorf("request[2] = %q, want the user message", msgs[2].Text())
	}
}
