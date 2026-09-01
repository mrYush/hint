package router

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mrYush/hint/pkg/agentapi"
)

// script is one Stream call of a fake provider.
type script struct {
	startErr error
	events   []agentapi.ChatEvent
}

// fakeProvider replays scripts, one per Stream call; the last script repeats
// when calls outnumber scripts (so retries see a stable failure).
type fakeProvider struct {
	name    string
	scripts []script
	calls   int
}

func (f *fakeProvider) Name() string { return f.name }

func (f *fakeProvider) Stream(ctx context.Context, req agentapi.ChatRequest) (<-chan agentapi.ChatEvent, error) {
	idx := f.calls
	if idx >= len(f.scripts) {
		idx = len(f.scripts) - 1
	}
	f.calls++
	s := f.scripts[idx]
	if s.startErr != nil {
		return nil, s.startErr
	}
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

// readyFake wraps a fakeProvider with a scripted Ready result.
type readyFake struct {
	*fakeProvider
	readyErr error
	checked  bool
}

func (r *readyFake) Ready(ctx context.Context) error {
	r.checked = true
	return r.readyErr
}

func noSleep(t *testing.T) Option {
	t.Helper()
	return withSleep(func(ctx context.Context, d time.Duration) error { return nil })
}

func request() agentapi.ChatRequest {
	return agentapi.ChatRequest{Messages: []agentapi.Message{agentapi.UserMessage("q")}}
}

func delta(text string) agentapi.ChatEvent {
	return agentapi.ChatEvent{Kind: agentapi.ChatTextDelta, Text: text}
}

func done() agentapi.ChatEvent {
	return agentapi.ChatEvent{Kind: agentapi.ChatDone, FinishReason: agentapi.FinishStop}
}

func failure(kind agentapi.ErrorKind) agentapi.ChatEvent {
	return agentapi.ChatEvent{Kind: agentapi.ChatError, Err: agentapi.NewError(kind, "scripted failure")}
}

func collect(t *testing.T, ch <-chan agentapi.ChatEvent) []agentapi.ChatEvent {
	t.Helper()
	var events []agentapi.ChatEvent
	timeout := time.After(10 * time.Second)
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				return events
			}
			events = append(events, ev)
		case <-timeout:
			t.Fatalf("router stream did not close; events so far: %+v", events)
		}
	}
}

func run(t *testing.T, r *Router) []agentapi.ChatEvent {
	t.Helper()
	ch, err := r.Stream(context.Background(), request())
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	return collect(t, ch)
}

// allKinds is every defined ErrorKind. agentapi's own exhaustiveness test
// guards the routing table; this list is checked against Valid so a kind
// added there without a row here fails loudly.
var allKinds = []agentapi.ErrorKind{
	agentapi.ErrUnknown, agentapi.ErrNetwork, agentapi.ErrTimeout,
	agentapi.ErrRateLimited, agentapi.ErrUnavailable, agentapi.ErrAuth,
	agentapi.ErrInvalidRequest, agentapi.ErrModelNotFound,
	agentapi.ErrContextOverflow, agentapi.ErrContentFiltered, agentapi.ErrCanceled,
}

func TestKindTableIsComplete(t *testing.T) {
	for _, k := range allKinds {
		if !k.Valid() {
			t.Errorf("kind %q is not valid", k)
		}
	}
}

// TestFailoverByKind exercises every ErrorKind arriving as the primary's
// terminal error before any delta, with a fallback configured.
func TestFailoverByKind(t *testing.T) {
	for _, kind := range allKinds {
		t.Run(string(kind), func(t *testing.T) {
			primary := &fakeProvider{name: "cloud", scripts: []script{{events: []agentapi.ChatEvent{failure(kind)}}}}
			fallback := &fakeProvider{name: "local", scripts: []script{{events: []agentapi.ChatEvent{delta("fb"), done()}}}}
			var notice bytes.Buffer
			r := New(primary, WithFallback(fallback), WithNotice(&notice), noSleep(t))

			events := run(t, r)
			term := events[len(events)-1]

			if kind.Fallbackable() {
				if term.Kind != agentapi.ChatDone {
					t.Fatalf("terminal = %+v, want fallback success", term)
				}
				if fallback.calls == 0 {
					t.Error("fallback was never called")
				}
				if !strings.Contains(notice.String(), `"cloud"`) || !strings.Contains(notice.String(), `"local"`) {
					t.Errorf("notice = %q", notice.String())
				}
				if !strings.Contains(notice.String(), string(kind)) {
					t.Errorf("notice %q does not name the failure kind %s", notice.String(), kind)
				}
			} else {
				if term.Kind != agentapi.ChatError || term.Err.Kind != kind {
					t.Fatalf("terminal = %+v, want forwarded %s", term, kind)
				}
				if fallback.calls != 0 {
					t.Errorf("fallback called on non-fallbackable %s", kind)
				}
				if notice.Len() != 0 {
					t.Errorf("notice written on non-fallbackable %s: %q", kind, notice.String())
				}
			}

			// Retryable failures burn the retry budget on the primary first.
			wantCalls := 1
			if kind.Retryable() {
				wantCalls = 1 + extraAttempts
			}
			if kind == agentapi.ErrCanceled {
				wantCalls = 1
			}
			if primary.calls != wantCalls {
				t.Errorf("primary.calls = %d, want %d", primary.calls, wantCalls)
			}
		})
	}
}

// TestNoFailoverAfterOutput: once a delta reached the consumer, a failure is
// forwarded as-is regardless of kind — no retry, no fallback.
func TestNoFailoverAfterOutput(t *testing.T) {
	for _, kind := range allKinds {
		t.Run(string(kind), func(t *testing.T) {
			primary := &fakeProvider{name: "cloud", scripts: []script{
				{events: []agentapi.ChatEvent{delta("half an ans"), failure(kind)}},
			}}
			fallback := &fakeProvider{name: "local", scripts: []script{{events: []agentapi.ChatEvent{done()}}}}
			var notice bytes.Buffer
			r := New(primary, WithFallback(fallback), WithNotice(&notice), noSleep(t))

			events := run(t, r)
			if len(events) != 2 {
				t.Fatalf("events = %+v", events)
			}
			if events[0].Kind != agentapi.ChatTextDelta {
				t.Errorf("delta not forwarded: %+v", events[0])
			}
			term := events[1]
			if term.Kind != agentapi.ChatError || term.Err.Kind != kind {
				t.Errorf("terminal = %+v, want forwarded %s", term, kind)
			}
			if primary.calls != 1 || fallback.calls != 0 {
				t.Errorf("calls: primary=%d fallback=%d, want 1/0", primary.calls, fallback.calls)
			}
			if notice.Len() != 0 {
				t.Errorf("notice = %q, want empty", notice.String())
			}
		})
	}
}

func TestNoFallbackConfiguredForwardsError(t *testing.T) {
	primary := &fakeProvider{name: "cloud", scripts: []script{{events: []agentapi.ChatEvent{failure(agentapi.ErrNetwork)}}}}
	r := New(primary, noSleep(t))

	events := run(t, r)
	term := events[len(events)-1]
	if term.Kind != agentapi.ChatError || term.Err.Kind != agentapi.ErrNetwork {
		t.Fatalf("terminal = %+v", term)
	}
	if term.Err.Provider != "cloud" {
		t.Errorf("provider = %q, want attribution to cloud", term.Err.Provider)
	}
}

func TestRetrySucceedsWithoutFallback(t *testing.T) {
	primary := &fakeProvider{name: "cloud", scripts: []script{
		{events: []agentapi.ChatEvent{failure(agentapi.ErrUnavailable)}},
		{events: []agentapi.ChatEvent{delta("ok"), done()}},
	}}
	fallback := &fakeProvider{name: "local", scripts: []script{{events: []agentapi.ChatEvent{done()}}}}
	var slept []time.Duration
	r := New(primary, WithFallback(fallback), withSleep(func(ctx context.Context, d time.Duration) error {
		slept = append(slept, d)
		return nil
	}))

	events := run(t, r)
	term := events[len(events)-1]
	if term.Kind != agentapi.ChatDone {
		t.Fatalf("terminal = %+v", term)
	}
	if primary.calls != 2 || fallback.calls != 0 {
		t.Errorf("calls: primary=%d fallback=%d, want 2/0", primary.calls, fallback.calls)
	}
	if len(slept) != 1 {
		t.Errorf("slept %v, want one backoff", slept)
	}
}

func TestRetryHonoursRetryAfter(t *testing.T) {
	err429 := agentapi.NewError(agentapi.ErrRateLimited, "slow down")
	err429.RetryAfterSeconds = 3
	primary := &fakeProvider{name: "cloud", scripts: []script{
		{events: []agentapi.ChatEvent{{Kind: agentapi.ChatError, Err: err429}}},
		{events: []agentapi.ChatEvent{done()}},
	}}
	var slept []time.Duration
	r := New(primary, withSleep(func(ctx context.Context, d time.Duration) error {
		slept = append(slept, d)
		return nil
	}))

	run(t, r)
	if len(slept) != 1 || slept[0] != 3*time.Second {
		t.Errorf("slept %v, want [3s]", slept)
	}
}

func TestStartErrorAlsoFailsOver(t *testing.T) {
	primary := &fakeProvider{name: "cloud", scripts: []script{
		{startErr: agentapi.NewError(agentapi.ErrAuth, "bad key")},
	}}
	fallback := &fakeProvider{name: "local", scripts: []script{{events: []agentapi.ChatEvent{delta("fb"), done()}}}}
	var notice bytes.Buffer
	r := New(primary, WithFallback(fallback), WithNotice(&notice), noSleep(t))

	events := run(t, r)
	if events[len(events)-1].Kind != agentapi.ChatDone {
		t.Fatalf("events = %+v", events)
	}
	if !strings.Contains(notice.String(), "auth") {
		t.Errorf("notice = %q", notice.String())
	}
}

func TestReadyFailureSkipsFallbackChat(t *testing.T) {
	primary := &fakeProvider{name: "cloud", scripts: []script{{events: []agentapi.ChatEvent{failure(agentapi.ErrNetwork)}}}}
	fb := &readyFake{
		fakeProvider: &fakeProvider{name: "local", scripts: []script{{events: []agentapi.ChatEvent{done()}}}},
		readyErr:     agentapi.NewError(agentapi.ErrUnavailable, "ollama is not reachable"),
	}
	r := New(primary, WithFallback(fb), noSleep(t))

	events := run(t, r)
	term := events[len(events)-1]
	if term.Kind != agentapi.ChatError || term.Err.Kind != agentapi.ErrUnavailable {
		t.Fatalf("terminal = %+v", term)
	}
	if !fb.checked {
		t.Error("Ready was never called")
	}
	if fb.calls != 0 {
		t.Error("chat attempted against a daemon that failed Ready")
	}
	if term.Err.Provider != "local" {
		t.Errorf("provider = %q, want local", term.Err.Provider)
	}
}

func TestReadySuccessProceedsToChat(t *testing.T) {
	primary := &fakeProvider{name: "cloud", scripts: []script{{events: []agentapi.ChatEvent{failure(agentapi.ErrNetwork)}}}}
	fb := &readyFake{
		fakeProvider: &fakeProvider{name: "local", scripts: []script{{events: []agentapi.ChatEvent{delta("fb"), done()}}}},
	}
	r := New(primary, WithFallback(fb), noSleep(t))

	events := run(t, r)
	if events[len(events)-1].Kind != agentapi.ChatDone {
		t.Fatalf("events = %+v", events)
	}
	if !fb.checked || fb.calls != 1 {
		t.Errorf("ready=%v chat calls=%d", fb.checked, fb.calls)
	}
}

func TestUsageWithheldUntilDone(t *testing.T) {
	usage := agentapi.ChatEvent{Kind: agentapi.ChatUsage, Usage: &agentapi.Usage{InputTokens: 1}}
	primary := &fakeProvider{name: "cloud", scripts: []script{
		{events: []agentapi.ChatEvent{delta("a"), usage, done()}},
	}}
	r := New(primary)

	events := run(t, r)
	if len(events) != 3 {
		t.Fatalf("events = %+v", events)
	}
	if events[1].Kind != agentapi.ChatUsage || events[2].Kind != agentapi.ChatDone {
		t.Errorf("order = %v %v, want usage before done", events[1].Kind, events[2].Kind)
	}
}

func TestContractViolationClosedWithoutTerminal(t *testing.T) {
	primary := &fakeProvider{name: "cloud", scripts: []script{{events: []agentapi.ChatEvent{delta("a")}}}}
	r := New(primary)

	events := run(t, r)
	term := events[len(events)-1]
	if term.Kind != agentapi.ChatError || term.Err.Kind != agentapi.ErrUnknown {
		t.Fatalf("terminal = %+v", term)
	}
}

func TestNameIsPrimaryStatic(t *testing.T) {
	primary := &fakeProvider{name: "cloud", scripts: []script{{events: []agentapi.ChatEvent{failure(agentapi.ErrNetwork)}}}}
	fallback := &fakeProvider{name: "local", scripts: []script{{events: []agentapi.ChatEvent{done()}}}}
	r := New(primary, WithFallback(fallback), noSleep(t))

	run(t, r)
	if r.Name() != "cloud" {
		t.Errorf("Name() = %q, want the primary's static name", r.Name())
	}
}

func TestSleepInterruptedByCancelStopsRetrying(t *testing.T) {
	primary := &fakeProvider{name: "cloud", scripts: []script{{events: []agentapi.ChatEvent{failure(agentapi.ErrUnavailable)}}}}
	r := New(primary, withSleep(func(ctx context.Context, d time.Duration) error {
		return context.Canceled
	}))

	events := run(t, r)
	term := events[len(events)-1]
	if term.Kind != agentapi.ChatError {
		t.Fatalf("terminal = %+v", term)
	}
	if primary.calls != 1 {
		t.Errorf("primary.calls = %d, want 1 (sleep aborted)", primary.calls)
	}
}

func TestRetryDelayCapped(t *testing.T) {
	long := agentapi.NewError(agentapi.ErrRateLimited, "come back tomorrow")
	long.RetryAfterSeconds = 3600
	if d := retryDelay(0, long); d != maxRetryDelay {
		t.Errorf("delay = %v, want capped at %v", d, maxRetryDelay)
	}
	if d := retryDelay(0, agentapi.NewError(agentapi.ErrNetwork, "x")); d <= 0 || d > backoffBase {
		t.Errorf("first backoff = %v, want (0, %v]", d, backoffBase)
	}
}

func TestNonAgentErrorIsClassified(t *testing.T) {
	primary := &fakeProvider{name: "cloud", scripts: []script{{startErr: errors.New("plain failure")}}}
	r := New(primary)

	events := run(t, r)
	term := events[len(events)-1]
	if term.Kind != agentapi.ChatError || term.Err.Kind != agentapi.ErrUnknown {
		t.Fatalf("terminal = %+v", term)
	}
	if term.Err.Provider != "cloud" {
		t.Errorf("provider = %q", term.Err.Provider)
	}
}
