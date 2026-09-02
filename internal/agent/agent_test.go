package agent_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/mrYush/hint/internal/agent"
	"github.com/mrYush/hint/pkg/agentapi"
)

// This file exercises Agent purely through its public API — New, the
// Option constructors, and RunTurn — the same position a consumer of
// internal/agent (cmd/hint, later a session runner) is in.

// script is one Stream call of a fakeProvider; the last script repeats when
// calls outnumber scripts, so an iteration-limit test can script "always a
// tool call" without listing 25 identical entries.
type script struct {
	events []agentapi.ChatEvent
}

// fakeProvider replays scripts and records every request it received, so a
// test can assert what the loop actually sent on a retry or after a
// compaction — the same shape as router_test.go's fakeProvider.
type fakeProvider struct {
	scripts []script
	calls   []agentapi.ChatRequest
}

func (f *fakeProvider) Name() string { return "fake" }

func (f *fakeProvider) Stream(ctx context.Context, req agentapi.ChatRequest) (<-chan agentapi.ChatEvent, error) {
	f.calls = append(f.calls, req)
	idx := len(f.calls) - 1
	if idx >= len(f.scripts) {
		idx = len(f.scripts) - 1
	}
	s := f.scripts[idx]
	ch := make(chan agentapi.ChatEvent)
	go func() {
		defer close(ch)
		for _, ev := range s.events {
			select {
			case ch <- ev:
			case <-ctx.Done():
				return
			}
		}
	}()
	return ch, nil
}

// blockingProvider delivers `before`, then blocks until ctx is cancelled and
// replies with the shape WP0.3 pinned for cancellation: ChatDone/
// FinishCanceled if something was already delivered, ChatError/ErrCanceled
// otherwise. It lets a test cancel deterministically once it has observed
// the events it wanted, instead of racing a timer against the loop.
type blockingProvider struct {
	before []agentapi.ChatEvent
	calls  int
}

func (f *blockingProvider) Name() string { return "fake" }

func (f *blockingProvider) Stream(ctx context.Context, req agentapi.ChatRequest) (<-chan agentapi.ChatEvent, error) {
	f.calls++
	ch := make(chan agentapi.ChatEvent)
	go func() {
		defer close(ch)
		for _, ev := range f.before {
			select {
			case ch <- ev:
			case <-ctx.Done():
				return
			}
		}
		<-ctx.Done()
		if len(f.before) > 0 {
			ch <- agentapi.ChatEvent{Kind: agentapi.ChatDone, FinishReason: agentapi.FinishCanceled}
		} else {
			ch <- agentapi.ChatEvent{Kind: agentapi.ChatError, Err: agentapi.NewError(agentapi.ErrCanceled, "canceled")}
		}
	}()
	return ch, nil
}

// echoTool mirrors pkg/agentapi/contract_test.go's echoTool: a minimal
// third-party tool built against nothing but the exported API.
type echoTool struct{}

func (echoTool) Name() string        { return "echo" }
func (echoTool) Description() string { return "Echo the given text back." }
func (echoTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"text":{"type":"string"}},"required":["text"]}`)
}
func (echoTool) Class() agentapi.ActionClass { return agentapi.ClassRead }
func (echoTool) Run(ctx context.Context, callID string, args json.RawMessage) (agentapi.ToolResult, error) {
	var in struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return agentapi.ErrorResult(callID, "echo", "invalid arguments"), nil
	}
	return agentapi.TextResult(callID, "echo", in.Text), nil
}

// brokenTool always fails its own machinery, never the model-visible
// action: Run returns a non-nil error, which the contract says aborts the
// turn rather than producing a result the model gets to see.
type brokenTool struct{}

func (brokenTool) Name() string                 { return "broken" }
func (brokenTool) Description() string          { return "Always fails." }
func (brokenTool) InputSchema() json.RawMessage { return json.RawMessage(`{"type":"object"}`) }
func (brokenTool) Class() agentapi.ActionClass  { return agentapi.ClassRead }
func (brokenTool) Run(context.Context, string, json.RawMessage) (agentapi.ToolResult, error) {
	return agentapi.ToolResult{}, errors.New("machinery broke")
}

// cancelingTool calls cancel before returning, so a test can deterministically
// cancel the turn's context from inside a tool batch instead of racing a
// goroutine against runTools.
type cancelingTool struct{ cancel context.CancelFunc }

func (t cancelingTool) Name() string                 { return "cancel" }
func (t cancelingTool) Description() string          { return "Cancels the turn." }
func (t cancelingTool) InputSchema() json.RawMessage { return json.RawMessage(`{"type":"object"}`) }
func (t cancelingTool) Class() agentapi.ActionClass  { return agentapi.ClassRead }
func (t cancelingTool) Run(_ context.Context, callID string, _ json.RawMessage) (agentapi.ToolResult, error) {
	t.cancel()
	return agentapi.TextResult(callID, "cancel", "ok"), nil
}

func delta(text string) agentapi.ChatEvent {
	return agentapi.ChatEvent{Kind: agentapi.ChatTextDelta, Text: text}
}

func done(reason agentapi.FinishReason) agentapi.ChatEvent {
	return agentapi.ChatEvent{Kind: agentapi.ChatDone, FinishReason: reason}
}

func usageEvent(input int64) agentapi.ChatEvent {
	return agentapi.ChatEvent{Kind: agentapi.ChatUsage, Usage: &agentapi.Usage{InputTokens: input}}
}

func toolCallEvent(id, name, args string) agentapi.ChatEvent {
	return agentapi.ChatEvent{Kind: agentapi.ChatToolCall, Call: &agentapi.ToolCall{
		ID: id, Name: name, Arguments: json.RawMessage(args),
	}}
}

func failure(kind agentapi.ErrorKind) agentapi.ChatEvent {
	return agentapi.ChatEvent{Kind: agentapi.ChatError, Err: agentapi.NewError(kind, "scripted failure")}
}

func question(text string) []agentapi.Message {
	return []agentapi.Message{
		agentapi.SystemMessage("you are a test assistant"),
		agentapi.UserMessage(text),
	}
}

// collect drains ch until it closes, failing the test if that takes too
// long — a bug that deadlocks the loop should fail fast, not hang `go test`.
func collect(t *testing.T, ch <-chan agentapi.Event) []agentapi.Event {
	t.Helper()
	var events []agentapi.Event
	deadline := time.After(5 * time.Second)
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				return events
			}
			events = append(events, ev)
		case <-deadline:
			t.Fatal("timed out collecting events")
		}
	}
}

func kinds(events []agentapi.Event) []agentapi.EventKind {
	out := make([]agentapi.EventKind, len(events))
	for i, e := range events {
		out[i] = e.Kind
	}
	return out
}

func lastEvent(events []agentapi.Event, kind agentapi.EventKind) *agentapi.Event {
	for i := len(events) - 1; i >= 0; i-- {
		if events[i].Kind == kind {
			return &events[i]
		}
	}
	return nil
}

func countEvents(events []agentapi.Event, kind agentapi.EventKind) int {
	n := 0
	for _, e := range events {
		if e.Kind == kind {
			n++
		}
	}
	return n
}

func TestRunTurn_PlainText(t *testing.T) {
	p := &fakeProvider{scripts: []script{{events: []agentapi.ChatEvent{
		delta("hel"), delta("lo"), usageEvent(10), done(agentapi.FinishStop),
	}}}}
	a := agent.New(p)

	events := collect(t, a.RunTurn(context.Background(), question("hi")))

	if got, want := kinds(events)[0], agentapi.EventTurnStart; got != want {
		t.Errorf("first event = %q, want %q", got, want)
	}
	end := lastEvent(events, agentapi.EventTurnEnd)
	if end == nil || end.FinishReason != agentapi.FinishStop {
		t.Fatalf("turn_end = %+v, want FinishStop", end)
	}
	if countEvents(events, agentapi.EventTextDelta) != 2 {
		t.Errorf("want 2 text deltas, got %d", countEvents(events, agentapi.EventTextDelta))
	}
	msg := lastEvent(events, agentapi.EventMessage)
	if msg == nil || msg.Message.Text() != "hello" {
		t.Fatalf("assistant message = %+v, want text %q", msg, "hello")
	}
	if len(p.calls) != 1 {
		t.Errorf("provider called %d times, want 1", len(p.calls))
	}
}

func TestRunTurn_MultiTool(t *testing.T) {
	p := &fakeProvider{scripts: []script{
		{events: []agentapi.ChatEvent{
			toolCallEvent("c1", "echo", `{"text":"a"}`),
			toolCallEvent("c2", "echo", `{"text":"b"}`),
			done(agentapi.FinishToolCalls),
		}},
		{events: []agentapi.ChatEvent{delta("done"), done(agentapi.FinishStop)}},
	}}
	a := agent.New(p, agent.WithTools(echoTool{}))

	events := collect(t, a.RunTurn(context.Background(), question("do two things")))

	end := lastEvent(events, agentapi.EventTurnEnd)
	if end == nil || end.FinishReason != agentapi.FinishStop {
		t.Fatalf("turn_end = %+v, want FinishStop", end)
	}
	if countEvents(events, agentapi.EventToolStart) != 2 || countEvents(events, agentapi.EventToolEnd) != 2 {
		t.Errorf("want 2 tool_start/tool_end, got %d/%d",
			countEvents(events, agentapi.EventToolStart), countEvents(events, agentapi.EventToolEnd))
	}
	if len(p.calls) != 2 {
		t.Fatalf("provider called %d times, want 2", len(p.calls))
	}
	// The second Stream call must have seen both tool results appended.
	second := p.calls[1].Messages
	if len(second) < 4 {
		t.Fatalf("second request has %d messages, want at least 4", len(second))
	}
}

func TestRunTurn_IterationLimit(t *testing.T) {
	calls := 0
	// ct counts its own invocations so the test can assert the (limit+1)th
	// round of tool calls never runs.
	ct := countingTool{count: &calls}

	p := &fakeProvider{scripts: []script{{events: []agentapi.ChatEvent{
		toolCallEvent("c", "count", `{}`),
		done(agentapi.FinishToolCalls),
	}}}}
	a := agent.New(p, agent.WithTools(ct), agent.WithLimits(agent.Limits{
		MaxIterations: 3, MaxContextTokens: 0, CompactThreshold: 0.8,
	}))

	events := collect(t, a.RunTurn(context.Background(), question("loop forever")))

	end := lastEvent(events, agentapi.EventTurnEnd)
	if end == nil || end.FinishReason != agentapi.FinishError {
		t.Fatalf("turn_end = %+v, want FinishError", end)
	}
	errEv := lastEvent(events, agentapi.EventError)
	if errEv == nil || errEv.Err.Kind != agentapi.ErrTurnLimit {
		t.Fatalf("error = %+v, want ErrTurnLimit", errEv)
	}
	if len(p.calls) != 3 {
		t.Errorf("provider called %d times, want 3 (MaxIterations)", len(p.calls))
	}
	if calls != 3 {
		t.Errorf("tool ran %d times, want 3 — a 4th round must not run", calls)
	}
}

func TestRunTurn_UnknownToolContinuesTurn(t *testing.T) {
	p := &fakeProvider{scripts: []script{
		{events: []agentapi.ChatEvent{
			toolCallEvent("c1", "does-not-exist", `{}`),
			done(agentapi.FinishToolCalls),
		}},
		{events: []agentapi.ChatEvent{delta("ok"), done(agentapi.FinishStop)}},
	}}
	a := agent.New(p)

	events := collect(t, a.RunTurn(context.Background(), question("call a ghost tool")))

	end := lastEvent(events, agentapi.EventTurnEnd)
	if end == nil || end.FinishReason != agentapi.FinishStop {
		t.Fatalf("turn_end = %+v, want FinishStop (turn must continue)", end)
	}
	toolEnd := lastEvent(events, agentapi.EventToolEnd)
	if toolEnd == nil || !toolEnd.Result.IsError {
		t.Fatalf("tool_end = %+v, want an IsError result", toolEnd)
	}
	if len(p.calls) != 2 {
		t.Errorf("provider called %d times, want 2", len(p.calls))
	}
}

func TestRunTurn_ToolMachineryErrorAbortsTurn(t *testing.T) {
	p := &fakeProvider{scripts: []script{{events: []agentapi.ChatEvent{
		toolCallEvent("c1", "broken", `{}`),
		done(agentapi.FinishToolCalls),
	}}}}
	a := agent.New(p, agent.WithTools(brokenTool{}))

	events := collect(t, a.RunTurn(context.Background(), question("break")))

	end := lastEvent(events, agentapi.EventTurnEnd)
	if end == nil || end.FinishReason != agentapi.FinishError {
		t.Fatalf("turn_end = %+v, want FinishError", end)
	}
	if len(p.calls) != 1 {
		t.Errorf("provider called %d times, want 1 (no retry after machinery abort)", len(p.calls))
	}
}

func TestRunTurn_FinishLengthAndContentFilterSkipTools(t *testing.T) {
	for _, reason := range []agentapi.FinishReason{agentapi.FinishLength, agentapi.FinishContentFilter} {
		t.Run(string(reason), func(t *testing.T) {
			p := &fakeProvider{scripts: []script{{events: []agentapi.ChatEvent{
				delta("partial"), done(reason),
			}}}}
			a := agent.New(p, agent.WithTools(echoTool{}))

			events := collect(t, a.RunTurn(context.Background(), question("q")))

			end := lastEvent(events, agentapi.EventTurnEnd)
			if end == nil || end.FinishReason != reason {
				t.Fatalf("turn_end = %+v, want %q", end, reason)
			}
			if countEvents(events, agentapi.EventToolStart) != 0 {
				t.Error("no tool must run when the turn did not ask for tool_calls")
			}
			if len(p.calls) != 1 {
				t.Errorf("provider called %d times, want 1", len(p.calls))
			}
		})
	}
}

func TestRunTurn_CancelMidStream(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	p := &blockingProvider{before: []agentapi.ChatEvent{delta("partial")}}
	a := agent.New(p)

	ch := a.RunTurn(ctx, question("q"))
	var events []agentapi.Event
	for ev := range ch {
		events = append(events, ev)
		if ev.Kind == agentapi.EventTextDelta {
			cancel()
		}
	}

	end := lastEvent(events, agentapi.EventTurnEnd)
	if end == nil || end.FinishReason != agentapi.FinishCanceled {
		t.Fatalf("turn_end = %+v, want FinishCanceled", end)
	}
}

func TestRunTurn_CancelBetweenToolCalls(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	p := &fakeProvider{scripts: []script{{events: []agentapi.ChatEvent{
		toolCallEvent("c1", "cancel", `{}`),
		toolCallEvent("c2", "echo", `{"text":"never"}`),
		done(agentapi.FinishToolCalls),
	}}}}
	a := agent.New(p, agent.WithTools(cancelingTool{cancel: cancel}, echoTool{}))

	events := collect(t, a.RunTurn(ctx, question("q")))

	end := lastEvent(events, agentapi.EventTurnEnd)
	if end == nil || end.FinishReason != agentapi.FinishCanceled {
		t.Fatalf("turn_end = %+v, want FinishCanceled", end)
	}
	if len(p.calls) != 1 {
		t.Errorf("provider called %d times, want 1 (no Stream after a mid-batch cancel)", len(p.calls))
	}
	// The second call's result must be reported as canceled, not run.
	toolEnds := 0
	for _, e := range events {
		if e.Kind == agentapi.EventToolEnd {
			toolEnds++
		}
	}
	if toolEnds != 1 {
		t.Errorf("want 1 tool_end (only the canceling tool actually ran), got %d", toolEnds)
	}
}

// stubCompactor is a minimal Compactor for RunTurn-level tests: it never
// makes a real provider call, so a test controls exactly when compaction
// "succeeds" and with what summary text.
type stubCompactor struct {
	summary string
	calls   int
}

func (s *stubCompactor) Compact(context.Context, []agentapi.Message) (string, error) {
	s.calls++
	return s.summary, nil
}

// history4 builds a 4-message conversation with one finished exchange
// (user1/assistant1) followed by a fresh, unanswered user question — the
// shape every compaction test below needs so there is something between
// the system prefix and the still-open tail worth summarizing.
func history4() []agentapi.Message {
	return []agentapi.Message{
		agentapi.SystemMessage("you are a test assistant"),
		agentapi.UserMessage("first question"),
		agentapi.AssistantMessage("first answer"),
		agentapi.UserMessage("second question"),
	}
}

func TestRunTurn_ProactiveCompaction(t *testing.T) {
	compactor := &stubCompactor{summary: "SUMMARY"}
	p := &fakeProvider{scripts: []script{
		{events: []agentapi.ChatEvent{
			toolCallEvent("c1", "echo", `{"text":"x"}`),
			usageEvent(90), // 90/100 = 90% occupancy, over the 80% threshold
			done(agentapi.FinishToolCalls),
		}},
		{events: []agentapi.ChatEvent{delta("done"), done(agentapi.FinishStop)}},
	}}
	a := agent.New(p, agent.WithTools(echoTool{}), agent.WithCompactor(compactor),
		agent.WithLimits(agent.Limits{MaxIterations: 10, MaxContextTokens: 100, CompactThreshold: 0.8}))

	events := collect(t, a.RunTurn(context.Background(), history4()))

	if compactor.calls != 1 {
		t.Fatalf("Compactor called %d times, want 1", compactor.calls)
	}
	comp := lastEvent(events, agentapi.EventCompaction)
	if comp == nil || comp.Compaction.Summary != "SUMMARY" {
		t.Fatalf("compaction event = %+v, want Summary=SUMMARY", comp)
	}
	if len(p.calls) != 2 {
		t.Fatalf("provider called %d times, want 2", len(p.calls))
	}
	// The Stream call after compaction — triggered by the previous call's
	// reported usage, not by this call's own size — must see the
	// compacted history: the old exchange replaced by one summary message,
	// the still-open exchange kept verbatim.
	second := p.calls[1].Messages
	if len(second) != 5 {
		t.Fatalf("second request has %d messages, want 5 (system, summary, user, assistant+tool_calls, tool)", len(second))
	}
	if second[1].Role != agentapi.RoleSystem || second[1].Text() != "SUMMARY" {
		t.Errorf("second request[1] = %+v, want the summary system message", second[1])
	}
	end := lastEvent(events, agentapi.EventTurnEnd)
	if end == nil || end.FinishReason != agentapi.FinishStop {
		t.Fatalf("turn_end = %+v, want FinishStop", end)
	}
}

func TestRunTurn_OverflowCompactsAndRetries(t *testing.T) {
	compactor := &stubCompactor{summary: "SUMMARY2"}
	p := &fakeProvider{scripts: []script{
		{events: []agentapi.ChatEvent{failure(agentapi.ErrContextOverflow)}},
		{events: []agentapi.ChatEvent{delta("recovered"), done(agentapi.FinishStop)}},
	}}
	// MaxContextTokens: 0 deliberately — proactive compaction is off, so
	// this exercises only the reactive path: ErrContextOverflow must still
	// trigger a compact-and-retry with no window configured at all.
	a := agent.New(p, agent.WithCompactor(compactor),
		agent.WithLimits(agent.Limits{MaxIterations: 10, MaxContextTokens: 0, CompactThreshold: 0.8}))

	events := collect(t, a.RunTurn(context.Background(), history4()))

	if compactor.calls != 1 {
		t.Fatalf("Compactor called %d times, want 1", compactor.calls)
	}
	if len(p.calls) != 2 {
		t.Fatalf("provider called %d times, want 2 (the overflow attempt plus the retry)", len(p.calls))
	}
	retry := p.calls[1].Messages
	if len(retry) != 3 || retry[1].Text() != "SUMMARY2" {
		t.Fatalf("retry request = %+v, want [system, SUMMARY2, second question]", retry)
	}
	end := lastEvent(events, agentapi.EventTurnEnd)
	if end == nil || end.FinishReason != agentapi.FinishStop {
		t.Fatalf("turn_end = %+v, want FinishStop", end)
	}
}

func TestRunTurn_SecondOverflowWithNothingLeftToCompactIsFatal(t *testing.T) {
	compactor := &stubCompactor{summary: "SUMMARY3"}
	p := &fakeProvider{scripts: []script{
		{events: []agentapi.ChatEvent{failure(agentapi.ErrContextOverflow)}},
		{events: []agentapi.ChatEvent{failure(agentapi.ErrContextOverflow)}},
	}}
	a := agent.New(p, agent.WithCompactor(compactor),
		agent.WithLimits(agent.Limits{MaxIterations: 10, MaxContextTokens: 0, CompactThreshold: 0.8}))

	events := collect(t, a.RunTurn(context.Background(), history4()))

	// The first overflow found a real body (the finished first exchange) to
	// summarize and succeeded once. The second overflow hits a history
	// that is now just [system, summary, open question] — nothing left
	// between prefix and tail — so it must fail outright rather than loop.
	if compactor.calls != 1 {
		t.Fatalf("Compactor called %d times, want 1 (only the first compaction had anything to summarize)", compactor.calls)
	}
	if len(p.calls) != 2 {
		t.Fatalf("provider called %d times, want 2 (no third attempt after the fatal compaction)", len(p.calls))
	}
	if countEvents(events, agentapi.EventCompaction) != 1 {
		t.Errorf("want 1 EventCompaction (the first, successful one), got %d", countEvents(events, agentapi.EventCompaction))
	}
	end := lastEvent(events, agentapi.EventTurnEnd)
	if end == nil || end.FinishReason != agentapi.FinishError {
		t.Fatalf("turn_end = %+v, want FinishError", end)
	}
	errEv := lastEvent(events, agentapi.EventError)
	if errEv == nil || errEv.Err.Kind != agentapi.ErrContextOverflow {
		t.Fatalf("error = %+v, want ErrContextOverflow", errEv)
	}
}

// countingTool is a ClassRead tool that records how many times it ran, used
// by TestRunTurn_IterationLimit to prove the loop never runs a tool round
// beyond MaxIterations.
type countingTool struct{ count *int }

func (countingTool) Name() string                 { return "count" }
func (countingTool) Description() string          { return "Counts its own calls." }
func (countingTool) InputSchema() json.RawMessage { return json.RawMessage(`{"type":"object"}`) }
func (countingTool) Class() agentapi.ActionClass  { return agentapi.ClassRead }
func (t countingTool) Run(_ context.Context, callID string, _ json.RawMessage) (agentapi.ToolResult, error) {
	*t.count++
	return agentapi.TextResult(callID, "count", "ok"), nil
}
