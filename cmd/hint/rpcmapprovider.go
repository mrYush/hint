package main

import (
	"context"
	"sync"

	"github.com/mrYush/hint/pkg/agentapi"
)

// inferenceProvider observes the complete request, including router fallback,
// without interpreting the provider name or assuming inference runs remotely.
type inferenceProvider struct {
	chat  agentapi.ChatProvider
	begin func() func()
}

func (e *rpcEngine) observeInference(chat agentapi.ChatProvider) agentapi.ChatProvider {
	if e.mapIndex == nil || chat == nil {
		return chat
	}
	if _, wrapped := chat.(*inferenceProvider); wrapped {
		return chat
	}
	return &inferenceProvider{chat: chat, begin: e.mapIndex.Inference}
}

func (p *inferenceProvider) Name() string { return p.chat.Name() }

func (p *inferenceProvider) Stream(ctx context.Context, req agentapi.ChatRequest) (<-chan agentapi.ChatEvent, error) {
	ctx, cancel := context.WithCancel(ctx)
	end := p.begin()
	var once sync.Once
	release := func() { once.Do(end) }
	events, err := p.chat.Stream(ctx, req)
	if err != nil {
		cancel()
		release()
		return nil, err
	}
	if events == nil {
		cancel()
		release()
		return nil, agentapi.NewError(agentapi.ErrUnknown, "provider returned no event stream")
	}
	// One buffered event lets cancellation publish its terminal event and
	// close promptly even when the former consumer has disconnected.
	out := make(chan agentapi.ChatEvent, 1)
	go func() {
		defer close(out)
		defer cancel()
		defer release()
		canceled := func() {
			select {
			case <-out:
			default:
			}
			out <- agentapi.ChatEvent{Kind: agentapi.ChatError,
				Err: agentapi.WrapError(agentapi.ErrCanceled, ctx.Err(), "inference canceled")}
		}
		for {
			select {
			case <-ctx.Done():
				canceled()
				return
			case event, ok := <-events:
				if !ok {
					return
				}
				if event.Terminal() {
					release()
				}
				select {
				case out <- event:
				case <-ctx.Done():
					canceled()
					return
				}
				if event.Terminal() {
					return
				}
			}
		}
	}()
	return out, nil
}
