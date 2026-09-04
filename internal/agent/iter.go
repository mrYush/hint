package agent

import (
	"context"
	"strings"

	"github.com/mrYush/hint/pkg/agentapi"
)

// streamResult is one [agentapi.ChatProvider.Stream] call folded into the
// shape the loop needs: the assistant [agentapi.Message] to append, why the
// stream ended, and this call's [agentapi.Usage].
type streamResult struct {
	message agentapi.Message
	finish  agentapi.FinishReason
	usage   agentapi.Usage
	// overflow is set instead of returning an error: a context-overflow
	// ChatError is a normal, expected branch of the loop's state machine
	// (compact and retry), not a failure the caller should report as one.
	overflow bool
}

// streamOnce sends messages to the provider and folds the resulting
// [agentapi.ChatEvent] stream into a streamResult, forwarding text and
// thinking deltas to out as they arrive so a client can render the answer
// live instead of waiting for the whole turn.
//
// Every ChatError other than context overflow becomes a Go error; the
// caller turns that into a terminal EventError. A context-overflow error
// becomes streamResult.overflow instead, because the loop's reaction to it
// (compact, then retry the same request) is not a failure path.
func (a *Agent) streamOnce(ctx context.Context, messages []agentapi.Message, out chan<- agentapi.Event) (streamResult, error) {
	req := agentapi.ChatRequest{Messages: messages, Tools: a.schemas()}

	events, err := a.provider.Stream(ctx, req)
	if err != nil {
		return streamResult{}, err
	}

	var text, thinking strings.Builder
	var calls []agentapi.ToolCall
	var usage agentapi.Usage

	for ev := range events {
		switch ev.Kind {
		case agentapi.ChatTextDelta:
			text.WriteString(ev.Text)
			out <- agentapi.Event{Kind: agentapi.EventTextDelta, Text: ev.Text}
		case agentapi.ChatThinkingDelta:
			thinking.WriteString(ev.Text)
			out <- agentapi.Event{Kind: agentapi.EventThinkingDelta, Text: ev.Text}
		case agentapi.ChatToolCall:
			calls = append(calls, *ev.Call)
		case agentapi.ChatUsage:
			usage = *ev.Usage
		case agentapi.ChatDone:
			return buildStreamResult(text.String(), thinking.String(), calls, usage, ev.FinishReason), nil
		case agentapi.ChatError:
			if agentapi.KindOf(ev.Err) == agentapi.ErrContextOverflow {
				return streamResult{overflow: true, usage: usage}, nil
			}
			return streamResult{}, ev.Err
		}
	}

	// [agentapi.ChatProvider] promises exactly one terminal event before the
	// channel closes; a channel that closed without one is the provider's
	// own bug, not a state this loop should silently treat as "done".
	return streamResult{}, agentapi.NewError(agentapi.ErrUnknown, "provider closed the stream without a terminal event")
}

// buildStreamResult assembles the assistant [agentapi.Message] a completed
// stream produced. Thinking is kept as its own [agentapi.PartThinking] part
// (never merged into the visible text) so a later compaction pass can drop
// it independently, per the contract's promise on that part kind.
func buildStreamResult(text, thinking string, calls []agentapi.ToolCall, usage agentapi.Usage, finish agentapi.FinishReason) streamResult {
	var content []agentapi.ContentPart
	if thinking != "" {
		content = append(content, agentapi.Thinking(thinking))
	}
	if text != "" {
		content = append(content, agentapi.Text(text))
	}
	msg := agentapi.Message{Role: agentapi.RoleAssistant, Content: content, ToolCalls: calls}
	return streamResult{message: msg, finish: finish, usage: usage}
}
