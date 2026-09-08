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
