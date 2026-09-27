package agent_test

import (
	"context"
	"encoding/json"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mrYush/hint/internal/agent"
	"github.com/mrYush/hint/pkg/agentapi"
)

// These tests pin WP0.11: which calls of a batch overlap, and what the
// loop promises regardless — request-order results, one prompt at a time,
// the three failure shapes. Overlap is proven with a rendezvous, never a
// timer: two calls that each wait for the other can only both return if
// they ran at the same time, so a schedule that serialized them would hang
// the test into collect's timeout instead of passing by luck.

// meetTool is a read-class tool whose Run reports its arrival on arrive and
// then waits for release (or ctx). A test that reads two arrivals has
// proven two calls were in flight together.
type meetTool struct {
	arrive  chan<- string
	release <-chan struct{}
	done    *atomic.Int32 // calls that returned, for a later step to check
}

func (meetTool) Name() string                 { return "meet" }
func (meetTool) Description() string          { return "Waits for its siblings." }
func (meetTool) InputSchema() json.RawMessage { return json.RawMessage(`{"type":"object"}`) }
func (meetTool) Class() agentapi.ActionClass  { return agentapi.ClassRead }
func (t meetTool) Run(ctx context.Context, callID string, _ json.RawMessage) (agentapi.ToolResult, error) {
	t.arrive <- callID
	select {
	case <-t.release:
	case <-ctx.Done():
		return agentapi.ToolResult{}, ctx.Err()
	}
	if t.done != nil {
		t.done.Add(1)
	}
	return agentapi.TextResult(callID, "meet", "met "+callID), nil
}

// inflightTool counts how many of its calls run at once. A schedule that
// serializes them can never observe two, so max == 1 is a deterministic
// check for "these never overlapped".
type inflightTool struct {
	name  string
	class agentapi.ActionClass
	cur   *atomic.Int32
	peak  *atomic.Int32
	order *[]string // call IDs in the order they ran (sequential use only)
	seen  *int32    // what another tool's counter read when this one ran
	watch *atomic.Int32
}

func (t inflightTool) Name() string                { return t.name }
func (inflightTool) Description() string           { return "Counts overlap." }
func (inflightTool) InputSchema() json.RawMessage  { return json.RawMessage(`{"type":"object"}`) }
func (t inflightTool) Class() agentapi.ActionClass { return t.class }
func (t inflightTool) Run(_ context.Context, callID string, _ json.RawMessage) (agentapi.ToolResult, error) {
	n := t.cur.Add(1)
	for {
		p := t.peak.Load()
		if n <= p || t.peak.CompareAndSwap(p, n) {
			break
		}
	}
	if t.order != nil {
		*t.order = append(*t.order, callID)
	}
	if t.seen != nil && t.watch != nil {
		*t.seen = t.watch.Load()
	}
	time.Sleep(time.Millisecond) // long enough for a sibling to overlap if it may
	t.cur.Add(-1)
	return agentapi.TextResult(callID, t.name, "ok"), nil
}

// panicTool is a read-class tool whose Run panics: the loop must turn that
// into an error result the model reads, not a crash.
type panicTool struct{}

func (panicTool) Name() string                 { return "panic" }
func (panicTool) Description() string          { return "Panics." }
func (panicTool) InputSchema() json.RawMessage { return json.RawMessage(`{"type":"object"}`) }
func (panicTool) Class() agentapi.ActionClass  { return agentapi.ClassRead }
func (panicTool) Run(context.Context, string, json.RawMessage) (agentapi.ToolResult, error) {
	panic("tool bug")
}

// toolCallAfter is toolCallEvent with an After hint.
func toolCallAfter(id, name string, after ...string) agentapi.ChatEvent {
	ev := toolCallEvent(id, name, `{}`)
	ev.Call.After = after
	return ev
}

// pump drains stream into a buffered channel from its own goroutine, so a
// test can block on a tool's rendezvous while the loop keeps emitting
// events: RunTurn's channel is unbuffered, and a loop nobody reads from
// never reaches the tool batch at all.
func pump(stream <-chan agentapi.Event) <-chan agentapi.Event {
	events := make(chan agentapi.Event, 256)
	go func() {
		defer close(events)
		for ev := range stream {
			events <- ev
		}
	}()
	return events
}

// awaitArrivals reads n call IDs from arrive, failing the test if they do
// not all show up: a schedule that ran the calls one at a time never
// delivers the second while the first is still waiting.
func awaitArrivals(t *testing.T, arrive <-chan string, n int) []string {
	t.Helper()
	var ids []string
	deadline := time.After(5 * time.Second)
	for len(ids) < n {
		select {
		case id := <-arrive:
			ids = append(ids, id)
		case <-deadline:
			t.Fatalf("only %d of %d calls arrived: they did not overlap", len(ids), n)
		}
	}
	return ids
}

// toolResultIDs lists the ToolCallID of every tool message in messages, in
// order — the order the next request, and the session, will carry.
func toolResultIDs(messages []agentapi.Message) []string {
	var ids []string
	for _, m := range messages {
		if m.Role == agentapi.RoleTool {
			ids = append(ids, m.ToolCallID)
		}
	}
	return ids
}

func TestRunTurn_IndependentReadsOverlapAndKeepRequestOrder(t *testing.T) {
	arrive := make(chan string, 2)
	release := make(chan struct{})
	p := &fakeProvider{scripts: []script{
		toolRound(toolCallEvent("c1", "meet", `{}`), toolCallEvent("c2", "meet", `{}`)),
		{events: []agentapi.ChatEvent{delta("ok"), done(agentapi.FinishStop)}},
	}}
	a := agent.New(p, agent.WithTools(meetTool{arrive: arrive, release: release}))

	events := pump(a.RunTurn(context.Background(), question("q")))
	awaitArrivals(t, arrive, 2)
	close(release)
	got := collect(t, events)

	if end := lastEvent(got, agentapi.EventTurnEnd); end == nil || end.FinishReason != agentapi.FinishStop {
		t.Fatalf("turn_end = %+v, want FinishStop", end)
	}
	if len(p.calls) != 2 {
		t.Fatalf("provider called %d times, want 2", len(p.calls))
	}
	// Whatever order the two finished in, the model — and the session —
	// see the results in request order.
	ids := toolResultIDs(p.calls[1].Messages)
	if len(ids) != 2 || ids[0] != "c1" || ids[1] != "c2" {
		t.Fatalf("tool results in request = %v, want [c1 c2]", ids)
	}
	if n := countEvents(got, agentapi.EventToolStart); n != 2 {
		t.Errorf("tool_start events = %d, want 2", n)
	}
}

func TestRunTurn_GatedCallsRunOneAtATimeInOrder(t *testing.T) {
	var cur, peak atomic.Int32
	var order []string
	w := inflightTool{name: "write", class: agentapi.ClassWrite, cur: &cur, peak: &peak, order: &order}
	auth := &fakeAuthorizer{ask: map[string]bool{"write": true}, allow: true}
	p := &fakeProvider{scripts: []script{
		toolRound(toolCallEvent("c1", "write", `{}`), toolCallEvent("c2", "write", `{}`), toolCallEvent("c3", "write", `{}`)),
		{events: []agentapi.ChatEvent{delta("ok"), done(agentapi.FinishStop)}},
	}}
	a := agent.New(p, agent.WithTools(w), agent.WithAuthorizer(auth))

	events := collect(t, a.RunTurn(context.Background(), question("q")))

	if end := lastEvent(events, agentapi.EventTurnEnd); end == nil || end.FinishReason != agentapi.FinishStop {
		t.Fatalf("turn_end = %+v, want FinishStop", end)
	}
	if peak.Load() != 1 {
		t.Fatalf("gated calls overlapped: %d in flight at once", peak.Load())
	}
	if len(order) != 3 || order[0] != "c1" || order[1] != "c2" || order[2] != "c3" {
		t.Errorf("run order = %v, want [c1 c2 c3]", order)
	}
	// The prompts came one at a time, in the same order.
	if len(auth.prompts) != 3 {
		t.Fatalf("prompts = %d, want 3", len(auth.prompts))
	}
	for i, want := range []string{"c1", "c2", "c3"} {
		if auth.prompts[i].CallID != want {
			t.Errorf("prompt %d asked about %s, want %s", i, auth.prompts[i].CallID, want)
		}
	}
}

func TestRunTurn_GroupThenWrite(t *testing.T) {
	// Seq(Par(c1, c2), c3): the write starts only after both reads
	// returned — it reads their completion counter at the moment it runs.
	arrive := make(chan string, 2)
	release := make(chan struct{})
	var finishedReads atomic.Int32
	var cur, peak atomic.Int32
	var seen int32
	w := inflightTool{name: "write", class: agentapi.ClassWrite, cur: &cur, peak: &peak, seen: &seen, watch: &finishedReads}
	p := &fakeProvider{scripts: []script{
		toolRound(toolCallEvent("c1", "meet", `{}`), toolCallEvent("c2", "meet", `{}`), toolCallEvent("c3", "write", `{}`)),
		{events: []agentapi.ChatEvent{delta("ok"), done(agentapi.FinishStop)}},
	}}
	a := agent.New(p, agent.WithTools(meetTool{arrive: arrive, release: release, done: &finishedReads}, w))

	events := pump(a.RunTurn(context.Background(), question("q")))
	awaitArrivals(t, arrive, 2)
	close(release)
	got := collect(t, events)

	if end := lastEvent(got, agentapi.EventTurnEnd); end == nil || end.FinishReason != agentapi.FinishStop {
		t.Fatalf("turn_end = %+v, want FinishStop", end)
	}
	if seen != 2 {
		t.Fatalf("the write ran with %d of 2 reads finished", seen)
	}
	if ids := toolResultIDs(p.calls[1].Messages); len(ids) != 3 || ids[2] != "c3" {
		t.Errorf("tool results in request = %v, want [c1 c2 c3]", ids)
	}
}

func TestRunTurn_ParallelLimitOfOneIsThePlainChain(t *testing.T) {
	var cur, peak atomic.Int32
	var order []string
	r := inflightTool{name: "count", class: agentapi.ClassRead, cur: &cur, peak: &peak, order: &order}
	p := &fakeProvider{scripts: []script{
		toolRound(toolCallEvent("c1", "count", `{}`), toolCallEvent("c2", "count", `{}`), toolCallEvent("c3", "count", `{}`)),
		{events: []agentapi.ChatEvent{delta("ok"), done(agentapi.FinishStop)}},
	}}
	a := agent.New(p, agent.WithTools(r), agent.WithLimits(sequential()))

	events := collect(t, a.RunTurn(context.Background(), question("q")))

	if end := lastEvent(events, agentapi.EventTurnEnd); end == nil || end.FinishReason != agentapi.FinishStop {
		t.Fatalf("turn_end = %+v, want FinishStop", end)
	}
	if peak.Load() != 1 {
		t.Fatalf("reads overlapped under a limit of one: %d in flight", peak.Load())
	}
	if len(order) != 3 || order[0] != "c1" || order[1] != "c2" || order[2] != "c3" {
		t.Errorf("run order = %v, want request order", order)
	}
}

func TestRunTurn_AfterHintSerializesReads(t *testing.T) {
	var cur, peak atomic.Int32
	var order []string
	r := inflightTool{name: "count", class: agentapi.ClassRead, cur: &cur, peak: &peak, order: &order}
	p := &fakeProvider{scripts: []script{
		toolRound(toolCallEvent("c1", "count", `{}`), toolCallAfter("c2", "count", "c1")),
		{events: []agentapi.ChatEvent{delta("ok"), done(agentapi.FinishStop)}},
	}}
	a := agent.New(p, agent.WithTools(r))

	events := collect(t, a.RunTurn(context.Background(), question("q")))

	if end := lastEvent(events, agentapi.EventTurnEnd); end == nil || end.FinishReason != agentapi.FinishStop {
		t.Fatalf("turn_end = %+v, want FinishStop", end)
	}
	if peak.Load() != 1 {
		t.Fatalf("c2 overlapped the call its After hint names: %d in flight", peak.Load())
	}
	if len(order) != 2 || order[0] != "c1" || order[1] != "c2" {
		t.Errorf("run order = %v, want [c1 c2]", order)
	}
}

func TestRunTurn_PanicInGroupLeavesSiblingAlone(t *testing.T) {
	arrive := make(chan string, 1)
	release := make(chan struct{})
	p := &fakeProvider{scripts: []script{
		toolRound(toolCallEvent("c1", "panic", `{}`), toolCallEvent("c2", "meet", `{}`)),
		{events: []agentapi.ChatEvent{delta("ok"), done(agentapi.FinishStop)}},
	}}
	a := agent.New(p, agent.WithTools(panicTool{}, meetTool{arrive: arrive, release: release}))

	// Hold c2 until c1's failure has been reported: if a failed sibling
	// canceled the group, c2 would now see a dead context.
	var events []agentapi.Event
	stream := pump(a.RunTurn(context.Background(), question("q")))
	awaitArrivals(t, arrive, 1)
	for ev := range stream {
		events = append(events, ev)
		if ev.Kind == agentapi.EventToolEnd && ev.Result.CallID == "c1" {
			break
		}
	}
	close(release)
	events = append(events, collect(t, stream)...)

	if end := lastEvent(events, agentapi.EventTurnEnd); end == nil || end.FinishReason != agentapi.FinishStop {
		t.Fatalf("turn_end = %+v, want FinishStop", end)
	}
	var c1, c2 *agentapi.ToolResult
	for _, e := range events {
		if e.Kind != agentapi.EventToolEnd {
			continue
		}
		switch e.Result.CallID {
		case "c1":
			c1 = e.Result
		case "c2":
			c2 = e.Result
		}
	}
	if c1 == nil || !c1.IsError {
		t.Fatalf("panicking call's result = %+v, want an error result", c1)
	}
	if c2 == nil || c2.IsError {
		t.Fatalf("sibling's result = %+v, want a normal result", c2)
	}
}

func TestRunTurn_MachineryErrorInGroupCancelsSiblingsAndAbortsTurn(t *testing.T) {
	// c2 blocks until its context ends. Without the group cancel it would
	// wait forever and collect's deadline would fail the test.
	p := &fakeProvider{scripts: []script{
		toolRound(toolCallEvent("c1", "broken", `{}`), toolCallEvent("c2", "passthrough", `{}`)),
	}}
	a := agent.New(p, agent.WithTools(brokenTool{}, passThroughTool{}))

	events := collect(t, a.RunTurn(context.Background(), question("q")))

	if end := lastEvent(events, agentapi.EventTurnEnd); end == nil || end.FinishReason != agentapi.FinishError {
		t.Fatalf("turn_end = %+v, want FinishError", end)
	}
	if e := lastEvent(events, agentapi.EventError); e == nil || e.Err.Kind == agentapi.ErrCanceled {
		t.Fatalf("error event = %+v, want the machinery error, not the group's cancel", e)
	}
	if len(p.calls) != 1 {
		t.Errorf("provider called %d times, want 1 (the turn aborted)", len(p.calls))
	}
}

func TestRunTurn_CancelDuringGroupMarksUnfinishedCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	p := &fakeProvider{scripts: []script{
		toolRound(toolCallEvent("c1", "passthrough", `{}`), toolCallEvent("c2", "passthrough", `{}`)),
	}}
	a := agent.New(p, agent.WithTools(passThroughTool{}))

	var events []agentapi.Event
	starts := 0
	for ev := range a.RunTurn(ctx, question("q")) {
		events = append(events, ev)
		if ev.Kind == agentapi.EventToolStart {
			if starts++; starts == 2 {
				cancel() // Ctrl-C with both reads in flight
			}
		}
	}

	if end := lastEvent(events, agentapi.EventTurnEnd); end == nil || end.FinishReason != agentapi.FinishCanceled {
		t.Fatalf("turn_end = %+v, want FinishCanceled", end)
	}
	canceled := 0
	for _, e := range events {
		if e.Kind == agentapi.EventMessage && e.Message.Role == agentapi.RoleTool && e.Message.Text() == "canceled" {
			canceled++
		}
	}
	if canceled != 2 {
		t.Fatalf("canceled tool results = %d, want 2, in request order", canceled)
	}
	if n := countEvents(events, agentapi.EventError); n != 0 {
		t.Errorf("error events = %d, want 0", n)
	}
}
