package main

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mrYush/hint/pkg/agentapi"
)

type inferenceTestProvider struct {
	stream func(context.Context, agentapi.ChatRequest) (<-chan agentapi.ChatEvent, error)
}

func (inferenceTestProvider) Name() string { return "local-test" }
func (p inferenceTestProvider) Stream(ctx context.Context, req agentapi.ChatRequest) (<-chan agentapi.ChatEvent, error) {
	return p.stream(ctx, req)
}

func TestInferenceProviderLifetime(t *testing.T) {
	for _, tc := range []struct {
		name  string
		event agentapi.ChatEvent
	}{
		{name: "done", event: agentapi.ChatEvent{Kind: agentapi.ChatDone}},
		{name: "error", event: agentapi.ChatEvent{Kind: agentapi.ChatError}},
		{name: "closed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var active, releases atomic.Int32
			events := make(chan agentapi.ChatEvent, 2)
			p := &inferenceProvider{
				begin: func() func() {
					active.Add(1)
					return func() { active.Add(-1); releases.Add(1) }
				},
				chat: inferenceTestProvider{stream: func(_ context.Context, _ agentapi.ChatRequest) (<-chan agentapi.ChatEvent, error) {
					if active.Load() != 1 {
						t.Fatal("request was not admitted before provider startup")
					}
					return events, nil
				}},
			}
			ctx, cancel := context.WithTimeout(t.Context(), time.Second)
			defer cancel()
			out, err := p.Stream(ctx, agentapi.ChatRequest{})
			if err != nil || p.Name() != "local-test" {
				t.Fatalf("start: %v", err)
			}
			events <- agentapi.ChatEvent{Kind: agentapi.ChatTextDelta, Text: "hello"}
			if event := <-out; event.Text != "hello" || active.Load() != 1 {
				t.Fatalf("streaming activity: event=%+v active=%d", event, active.Load())
			}
			if tc.event.Kind != "" {
				events <- tc.event
			}
			close(events)
			for range out {
			}
			if active.Load() != 0 || releases.Load() != 1 {
				t.Fatalf("release: active=%d releases=%d", active.Load(), releases.Load())
			}
		})
	}
}

func TestInferenceProviderStartupFailure(t *testing.T) {
	for _, fails := range []bool{false, true} {
		var active atomic.Int32
		failure := errors.New("start failed")
		p := &inferenceProvider{
			begin: func() func() { active.Add(1); return func() { active.Add(-1) } },
			chat: inferenceTestProvider{stream: func(context.Context, agentapi.ChatRequest) (<-chan agentapi.ChatEvent, error) {
				if fails {
					return nil, failure
				}
				return nil, nil
			}},
		}
		out, err := p.Stream(t.Context(), agentapi.ChatRequest{})
		if err == nil || out != nil || active.Load() != 0 {
			t.Fatalf("invalid startup: out=%v err=%v active=%d", out, err, active.Load())
		}
		if fails && !errors.Is(err, failure) {
			t.Fatalf("lost startup error: %v", err)
		}
	}
}

func TestInferenceProviderCancellationWhileConsumerBlocked(t *testing.T) {
	var active, releases atomic.Int32
	providerCanceled := make(chan struct{})
	events := make(chan agentapi.ChatEvent, 1)
	events <- agentapi.ChatEvent{Kind: agentapi.ChatTextDelta, Text: "pending"}
	p := &inferenceProvider{
		begin: func() func() {
			active.Add(1)
			return func() { active.Add(-1); releases.Add(1) }
		},
		chat: inferenceTestProvider{stream: func(ctx context.Context, _ agentapi.ChatRequest) (<-chan agentapi.ChatEvent, error) {
			go func() { <-ctx.Done(); close(providerCanceled) }()
			return events, nil
		}},
	}
	ctx, cancel := context.WithCancel(t.Context())
	out, err := p.Stream(ctx, agentapi.ChatRequest{})
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	select {
	case <-providerCanceled:
	case <-time.After(time.Second):
		t.Fatal("provider did not receive cancellation")
	}
	terminal := 0
	for event := range out {
		if event.Terminal() {
			terminal++
			if event.Err == nil || event.Err.Kind != agentapi.ErrCanceled {
				t.Fatalf("cancellation terminal: %+v", event)
			}
		}
	}
	if terminal != 1 {
		t.Fatalf("cancellation emitted %d terminal events", terminal)
	}
	if active.Load() != 0 || releases.Load() != 1 {
		t.Fatalf("cancel release: active=%d releases=%d", active.Load(), releases.Load())
	}
}
