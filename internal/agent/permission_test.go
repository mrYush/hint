package agent_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mrYush/hint/internal/agent"
	"github.com/mrYush/hint/pkg/agentapi"
)

// writeTool is a write-class tool that records whether it ran, so a test
// can tell a denied call (never ran) from a failed one.
type writeTool struct{ runs *int }

func (writeTool) Name() string                 { return "write" }
func (writeTool) Description() string          { return "Pretends to write." }
func (writeTool) InputSchema() json.RawMessage { return json.RawMessage(`{"type":"object"}`) }
func (writeTool) Class() agentapi.ActionClass  { return agentapi.ClassWrite }
func (t writeTool) Run(_ context.Context, callID string, _ json.RawMessage) (agentapi.ToolResult, error) {
	*t.runs++
	return agentapi.TextResult(callID, "write", "written"), nil
}

// passThroughTool cancels the turn from inside Run and returns the
// context's own error, the way a tool wrapped by tool.WithLimits forwards
// a caller's cancel or deadline untouched.
type passThroughTool struct{ cancel context.CancelFunc }

func (passThroughTool) Name() string                 { return "passthrough" }
func (passThroughTool) Description() string          { return "Returns ctx.Err()." }
func (passThroughTool) InputSchema() json.RawMessage { return json.RawMessage(`{"type":"object"}`) }
func (passThroughTool) Class() agentapi.ActionClass  { return agentapi.ClassRead }
func (t passThroughTool) Run(ctx context.Context, _ string, _ json.RawMessage) (agentapi.ToolResult, error) {
	if t.cancel != nil {
		t.cancel()
	}
	<-ctx.Done()
	return agentapi.ToolResult{}, ctx.Err()
}

// kindTool fails its machinery with a classified error.
type kindTool struct{ kind agentapi.ErrorKind }

func (kindTool) Name() string                 { return "kind" }
func (kindTool) Description() string          { return "Fails with a kind." }
func (kindTool) InputSchema() json.RawMessage { return json.RawMessage(`{"type":"object"}`) }
func (kindTool) Class() agentapi.ActionClass  { return agentapi.ClassRead }
func (t kindTool) Run(context.Context, string, json.RawMessage) (agentapi.ToolResult, error) {
	return agentapi.ToolResult{}, agentapi.NewError(t.kind, "classified failure")
}

// fakeAuthorizer asks about the tools named in ask and answers with
// allow; it records what it was asked so a test can assert that the loop
// prompts exactly when it should.
type fakeAuthorizer struct {
	ask     map[string]bool
	allow   bool
	err     error
	block   bool // Authorize waits for ctx to end
	prompts []agentapi.PermissionRequest
}

func (f *fakeAuthorizer) Review(_ context.Context, call agentapi.ToolCall, tool agentapi.Tool) (agentapi.PermissionRequest, bool) {
	req := agentapi.PermissionRequest{CallID: call.ID, Tool: call.Name, Class: tool.Class(), Summary: "do " + call.Name}
	return req, f.ask[call.Name]
}

func (f *fakeAuthorizer) Authorize(ctx context.Context, req agentapi.PermissionRequest) (bool, error) {
	f.prompts = append(f.prompts, req)
	if f.block {
		<-ctx.Done()
		return false, ctx.Err()
	}
	if f.err != nil {
		return false, f.err
	}
	return f.allow, nil
}

func toolRound(calls ...agentapi.ChatEvent) script {
	return script{events: append(calls, done(agentapi.FinishToolCalls))}
}

func TestRunTurn_DeniedCallContinuesTurnWithoutRunning(t *testing.T) {
	runs := 0
	auth := &fakeAuthorizer{ask: map[string]bool{"write": true}, allow: false}
	p := &fakeProvider{scripts: []script{
		toolRound(toolCallEvent("c1", "write", `{}`), toolCallEvent("c2", "echo", `{"text":"hi"}`)),
		{events: []agentapi.ChatEvent{delta("ok"), done(agentapi.FinishStop)}},
	}}
	a := agent.New(p, agent.WithTools(writeTool{runs: &runs}, echoTool{}), agent.WithAuthorizer(auth))

	events := collect(t, a.RunTurn(context.Background(), question("q")))

	if runs != 0 {
		t.Fatalf("denied tool ran %d times", runs)
	}
	if end := lastEvent(events, agentapi.EventTurnEnd); end == nil || end.FinishReason != agentapi.FinishStop {
		t.Fatalf("turn_end = %+v, want FinishStop (batch continues after a denial)", end)
	}
	if n := countEvents(events, agentapi.EventPermission); n != 1 {
		t.Fatalf("permission events = %d, want 1", n)
	}
	// The denied call never started; the echo did.
	starts := 0
	for _, e := range events {
		if e.Kind == agentapi.EventToolStart {
			starts++
			if e.Call.Name == "write" {
				t.Error("denied call was announced with tool_start")
			}
		}
	}
	if starts != 1 {
		t.Errorf("tool_start events = %d, want 1 (echo only)", starts)
	}
	// Both calls got a result, the denied one an error the model reads.
	var denied *agentapi.ToolResult
	for _, e := range events {
		if e.Kind == agentapi.EventToolEnd && e.Result.CallID == "c1" {
			denied = e.Result
		}
	}
	if denied == nil || !denied.IsError || !strings.Contains(denied.Text(), "permission denied") {
		t.Fatalf("denied result = %+v", denied)
	}
	if len(p.calls) != 2 || len(p.calls[1].Messages) != 5 {
		t.Fatalf("second request has %d messages, want 5 (system, user, assistant, 2 tool results)", len(p.calls[1].Messages))
	}
}

func TestRunTurn_AllowedCallRunsAfterPrompt(t *testing.T) {
	runs := 0
	auth := &fakeAuthorizer{ask: map[string]bool{"write": true}, allow: true}
	p := &fakeProvider{scripts: []script{
		toolRound(toolCallEvent("c1", "write", `{}`)),
		{events: []agentapi.ChatEvent{delta("ok"), done(agentapi.FinishStop)}},
	}}
	a := agent.New(p, agent.WithTools(writeTool{runs: &runs}), agent.WithAuthorizer(auth))

	events := collect(t, a.RunTurn(context.Background(), question("q")))

	if runs != 1 {
		t.Fatalf("allowed tool ran %d times, want 1", runs)
	}
	want := []agentapi.EventKind{agentapi.EventPermission, agentapi.EventToolStart, agentapi.EventToolEnd}
	var got []agentapi.EventKind
	for _, e := range events {
		switch e.Kind {
		case agentapi.EventPermission, agentapi.EventToolStart, agentapi.EventToolEnd:
			got = append(got, e.Kind)
		}
	}
	if strings.Join(kindsToStrings(got), ",") != strings.Join(kindsToStrings(want), ",") {
		t.Fatalf("event order = %v, want %v", got, want)
	}
	if len(auth.prompts) != 1 || auth.prompts[0].CallID != "c1" {
		t.Fatalf("prompts = %+v", auth.prompts)
	}
}

func kindsToStrings(k []agentapi.EventKind) []string {
	out := make([]string, len(k))
	for i, x := range k {
		out[i] = string(x)
	}
	return out
}

func TestRunTurn_NoPromptMeansNoPermissionEvent(t *testing.T) {
	runs := 0
	auth := &fakeAuthorizer{ask: map[string]bool{}, allow: false}
	p := &fakeProvider{scripts: []script{
		toolRound(toolCallEvent("c1", "write", `{}`)),
		{events: []agentapi.ChatEvent{delta("ok"), done(agentapi.FinishStop)}},
	}}
	a := agent.New(p, agent.WithTools(writeTool{runs: &runs}), agent.WithAuthorizer(auth))

	events := collect(t, a.RunTurn(context.Background(), question("q")))

	if runs != 1 {
		t.Fatalf("tool ran %d times, want 1 (Review said no prompt)", runs)
	}
	if n := countEvents(events, agentapi.EventPermission); n != 0 {
		t.Fatalf("permission events = %d, want 0", n)
	}
	if len(auth.prompts) != 0 {
		t.Fatalf("Authorize was called %d times", len(auth.prompts))
	}
}

func TestRunTurn_CancelAtPromptEndsCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runs := 0
	auth := &fakeAuthorizer{ask: map[string]bool{"write": true}, block: true}
	p := &fakeProvider{scripts: []script{
		toolRound(toolCallEvent("c1", "write", `{}`), toolCallEvent("c2", "echo", `{"text":"never"}`)),
	}}
	a := agent.New(p, agent.WithTools(writeTool{runs: &runs}, echoTool{}), agent.WithAuthorizer(auth))

	var events []agentapi.Event
	for ev := range a.RunTurn(ctx, question("q")) {
		events = append(events, ev)
		if ev.Kind == agentapi.EventPermission {
			cancel() // Ctrl-C while the prompt waits
		}
	}

	if end := lastEvent(events, agentapi.EventTurnEnd); end == nil || end.FinishReason != agentapi.FinishCanceled {
		t.Fatalf("turn_end = %+v, want FinishCanceled", end)
	}
	if runs != 0 || countEvents(events, agentapi.EventToolStart) != 0 {
		t.Fatal("a call ran after the prompt was interrupted")
	}
	// Both the interrupted call and the one after it are recorded as
	// canceled results, the same as a cancel between calls.
	canceled := 0
	for _, e := range events {
		if e.Kind == agentapi.EventMessage && e.Message.Role == agentapi.RoleTool && e.Message.Text() == "canceled" {
			canceled++
		}
	}
	if canceled != 2 {
		t.Fatalf("canceled tool results = %d, want 2", canceled)
	}
	if len(p.calls) != 1 {
		t.Fatalf("provider called %d times, want 1", len(p.calls))
	}
}

func TestRunTurn_AuthorizerFailureAbortsTurn(t *testing.T) {
	auth := &fakeAuthorizer{ask: map[string]bool{"write": true}, err: errors.New("prompt channel broke")}
	runs := 0
	p := &fakeProvider{scripts: []script{toolRound(toolCallEvent("c1", "write", `{}`))}}
	a := agent.New(p, agent.WithTools(writeTool{runs: &runs}), agent.WithAuthorizer(auth))

	events := collect(t, a.RunTurn(context.Background(), question("q")))

	if end := lastEvent(events, agentapi.EventTurnEnd); end == nil || end.FinishReason != agentapi.FinishError {
		t.Fatalf("turn_end = %+v, want FinishError", end)
	}
	if runs != 0 {
		t.Fatal("tool ran without an answer")
	}
	if e := lastEvent(events, agentapi.EventError); e == nil || !strings.Contains(e.Err.Message, "prompt channel broke") {
		t.Fatalf("error event = %+v", e)
	}
}

func TestRunTurn_ToolPassingThroughCancelEndsCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	p := &fakeProvider{scripts: []script{
		toolRound(toolCallEvent("c1", "passthrough", `{}`), toolCallEvent("c2", "echo", `{"text":"never"}`)),
	}}
	// A chain, so the echo is the call after the interrupted one rather
	// than its sibling in a group.
	a := agent.New(p, agent.WithTools(passThroughTool{cancel: cancel}, echoTool{}), agent.WithLimits(sequential()))

	events := collect(t, a.RunTurn(ctx, question("q")))

	if end := lastEvent(events, agentapi.EventTurnEnd); end == nil || end.FinishReason != agentapi.FinishCanceled {
		t.Fatalf("turn_end = %+v, want FinishCanceled, not a machinery error", end)
	}
	if n := countEvents(events, agentapi.EventError); n != 0 {
		t.Fatalf("error events = %d, want 0", n)
	}
	if n := countEvents(events, agentapi.EventToolEnd); n != 0 {
		t.Fatalf("tool_end events = %d, want 0 (the interrupted call has no real result)", n)
	}
}

const millisecond = time.Millisecond

func TestRunTurn_ToolPassingThroughDeadlineEndsCanceled(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*millisecond)
	defer cancel()
	p := &fakeProvider{scripts: []script{toolRound(toolCallEvent("c1", "passthrough", `{}`))}}
	a := agent.New(p, agent.WithTools(passThroughTool{}))

	events := collect(t, a.RunTurn(ctx, question("q")))

	if end := lastEvent(events, agentapi.EventTurnEnd); end == nil || end.FinishReason != agentapi.FinishCanceled {
		t.Fatalf("turn_end = %+v, want FinishCanceled for an inherited deadline", end)
	}
}

func TestRunTurn_MachineryErrorKeepsItsKind(t *testing.T) {
	p := &fakeProvider{scripts: []script{toolRound(toolCallEvent("c1", "kind", `{}`))}}
	a := agent.New(p, agent.WithTools(kindTool{kind: agentapi.ErrUnavailable}))

	events := collect(t, a.RunTurn(context.Background(), question("q")))

	if end := lastEvent(events, agentapi.EventTurnEnd); end == nil || end.FinishReason != agentapi.FinishError {
		t.Fatalf("turn_end = %+v, want FinishError", end)
	}
	e := lastEvent(events, agentapi.EventError)
	if e == nil || e.Err.Kind != agentapi.ErrUnavailable {
		t.Fatalf("error event = %+v, want kind unavailable preserved", e)
	}
}
