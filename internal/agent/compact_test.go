package agent

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/mrYush/hint/pkg/agentapi"
)

// This file tests the package's unexported machinery directly (white-box:
// package agent, not agent_test) because the interesting behavior —
// occupancy tracking, the drop-thinking shortcut, the prefix/body/tail
// split — lives in unexported helpers that agent_test.go's public-API tests
// can only exercise indirectly. RunTurn-level coverage of compaction lives
// there too (proactive trigger, reactive retry, the fatal double-overflow
// case) since those need the full scripted-provider loop.

type stubCompactor struct {
	summary string
	err     error
	calls   int
	got     []agentapi.Message
}

func (s *stubCompactor) Compact(_ context.Context, messages []agentapi.Message) (string, error) {
	s.calls++
	s.got = messages
	return s.summary, s.err
}

// thinkingAwareEstimator is deterministic and cheap to reason about in a
// test: it reports a large size while any message still carries a
// PartThinking part, and a small size once none do. That isolates the
// drop-thinking shortcut in [Agent.compact] from the real charEstimator's
// arithmetic, which a test would otherwise have to reverse-engineer.
type thinkingAwareEstimator struct{ big, small int64 }

func (e thinkingAwareEstimator) Estimate(messages []agentapi.Message) int64 {
	for _, m := range messages {
		for _, p := range m.Content {
			if p.Kind == agentapi.PartThinking {
				return e.big
			}
		}
	}
	return e.small
}

func lastAgentEvent(events []agentapi.Event, kind agentapi.EventKind) *agentapi.Event {
	for i := len(events) - 1; i >= 0; i-- {
		if events[i].Kind == kind {
			return &events[i]
		}
	}
	return nil
}

// TestCompact_ThinkingOnlyShortcut checks that dropping PartThinking alone,
// without ever calling the Compactor, is enough when the estimator says so
// — the contract already promises thinking is dropped first, and a real
// LLM summary call is wasted work if that alone clears the threshold.
func TestCompact_ThinkingOnlyShortcut(t *testing.T) {
	compactor := &stubCompactor{summary: "should not be used"}
	a := &Agent{
		limits:    Limits{MaxContextTokens: 100, CompactThreshold: 0.8},
		compactor: compactor,
		estimator: thinkingAwareEstimator{big: 1000, small: 10},
	}

	messages := []agentapi.Message{
		agentapi.SystemMessage("sys"),
		agentapi.UserMessage("q"),
		{Role: agentapi.RoleAssistant, Content: []agentapi.ContentPart{
			agentapi.Thinking("long internal reasoning"),
			agentapi.Text("answer"),
		}},
	}

	out := make(chan agentapi.Event, 10)
	result, err := a.compact(context.Background(), messages, out)
	close(out)
	if err != nil {
		t.Fatalf("compact: %v", err)
	}
	if compactor.calls != 0 {
		t.Errorf("Compactor called %d times, want 0 — dropping thinking should have been enough", compactor.calls)
	}
	for _, m := range result {
		if m.Thinking() != "" {
			t.Errorf("message %+v still carries a thinking part", m)
		}
	}
	if result[2].Text() != "answer" {
		t.Errorf("assistant text = %q, want %q (only thinking must be dropped)", result[2].Text(), "answer")
	}

	events := collectEventChan(t, out)
	comp := lastAgentEvent(events, agentapi.EventCompaction)
	if comp == nil {
		t.Fatal("no EventCompaction emitted")
	}
	if comp.Compaction.Summary != "" || comp.Compaction.MessagesReplaced != 0 {
		t.Errorf("compaction = %+v, want an empty summary and 0 messages replaced", comp.Compaction)
	}
}

// collectEventChan drains an already-closed or about-to-close buffered
// channel; used where the channel was pre-buffered rather than consumed
// concurrently.
func collectEventChan(t *testing.T, ch <-chan agentapi.Event) []agentapi.Event {
	t.Helper()
	var events []agentapi.Event
	for ev := range ch {
		events = append(events, ev)
	}
	return events
}

// TestCompact_SummarizesBodyBetweenPrefixAndTail checks the split: a
// leading system message is kept verbatim, the latest user exchange
// (including its tool round) is kept verbatim as the tail, and only the
// middle — the Compactor's input — is replaced by one system-role summary.
func TestCompact_SummarizesBodyBetweenPrefixAndTail(t *testing.T) {
	compactor := &stubCompactor{summary: "SUMMARY"}
	a := &Agent{
		limits:    Limits{MaxContextTokens: 0, CompactThreshold: 0.8},
		compactor: compactor,
		estimator: charEstimator{},
	}

	messages := []agentapi.Message{
		agentapi.SystemMessage("sys"),
		agentapi.UserMessage("first question"),
		agentapi.AssistantMessage("first answer"),
		agentapi.UserMessage("second question"),
		{Role: agentapi.RoleAssistant, ToolCalls: []agentapi.ToolCall{
			{ID: "c1", Name: "echo", Arguments: json.RawMessage(`{}`)},
		}},
		{Role: agentapi.RoleTool, ToolCallID: "c1", Content: []agentapi.ContentPart{agentapi.Text("result")}},
	}

	out := make(chan agentapi.Event, 10)
	result, err := a.compact(context.Background(), messages, out)
	close(out)
	if err != nil {
		t.Fatalf("compact: %v", err)
	}

	if compactor.calls != 1 {
		t.Fatalf("Compactor called %d times, want 1", compactor.calls)
	}
	// The body handed to the Compactor must be exactly the first exchange —
	// never the leading system prompt, never the still-open second exchange.
	if len(compactor.got) != 2 || compactor.got[0].Text() != "first question" || compactor.got[1].Text() != "first answer" {
		t.Fatalf("Compactor got %+v, want [first question, first answer]", compactor.got)
	}

	if len(result) != 5 {
		t.Fatalf("result has %d messages, want 5 (system, summary, user, assistant+tool_calls, tool)", len(result))
	}
	if result[0].Text() != "sys" {
		t.Errorf("result[0] = %+v, want the original system prompt", result[0])
	}
	if result[1].Role != agentapi.RoleSystem || result[1].Text() != "SUMMARY" {
		t.Errorf("result[1] = %+v, want a system message carrying the summary", result[1])
	}
	if result[2].Text() != "second question" {
		t.Errorf("result[2] = %+v, want the tail's anchoring user message", result[2])
	}

	events := collectEventChan(t, out)
	comp := lastAgentEvent(events, agentapi.EventCompaction)
	if comp == nil || comp.Compaction.Summary != "SUMMARY" || comp.Compaction.MessagesReplaced != 2 {
		t.Fatalf("compaction event = %+v, want Summary=SUMMARY, MessagesReplaced=2", comp)
	}
}

// TestCompact_EmptyBodyIsFatal checks the loop's actual protection against
// an endless compact-and-retry cycle: when there is nothing between the
// system prefix and the still-open tail, compact refuses to pretend it
// helped.
func TestCompact_EmptyBodyIsFatal(t *testing.T) {
	compactor := &stubCompactor{summary: "should not be called"}
	a := &Agent{
		limits:    Limits{MaxContextTokens: 0, CompactThreshold: 0.8},
		compactor: compactor,
		estimator: charEstimator{},
	}
	messages := []agentapi.Message{
		agentapi.SystemMessage("sys"),
		agentapi.UserMessage("only question"),
	}

	out := make(chan agentapi.Event, 10)
	_, err := a.compact(context.Background(), messages, out)
	close(out)

	if err == nil {
		t.Fatal("compact must fail when there is nothing to summarize")
	}
	if agentapi.KindOf(err) != agentapi.ErrContextOverflow {
		t.Errorf("KindOf(err) = %q, want %q", agentapi.KindOf(err), agentapi.ErrContextOverflow)
	}
	if compactor.calls != 0 {
		t.Errorf("Compactor called %d times, want 0", compactor.calls)
	}
}

// TestCompact_PropagatesCompactorError checks that a failing Compactor call
// (e.g. the summarizing provider itself erroring) is reported as-is rather
// than swallowed.
func TestCompact_PropagatesCompactorError(t *testing.T) {
	want := errors.New("summary provider is down")
	compactor := &stubCompactor{err: want}
	a := &Agent{
		limits:    Limits{MaxContextTokens: 0, CompactThreshold: 0.8},
		compactor: compactor,
		estimator: charEstimator{},
	}
	messages := []agentapi.Message{
		agentapi.SystemMessage("sys"),
		agentapi.UserMessage("first"),
		agentapi.AssistantMessage("first answer"),
		agentapi.UserMessage("second"),
	}

	out := make(chan agentapi.Event, 10)
	_, err := a.compact(context.Background(), messages, out)
	close(out)

	if !errors.Is(err, want) {
		t.Errorf("compact error = %v, want it to wrap %v", err, want)
	}
}

func TestCharEstimator(t *testing.T) {
	e := charEstimator{}
	messages := []agentapi.Message{agentapi.UserMessage("12345678")} // 8 chars
	if got, want := e.Estimate(messages), int64(2); got != want {
		t.Errorf("Estimate() = %d, want %d", got, want)
	}
}
