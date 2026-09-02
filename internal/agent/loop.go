package agent

import (
	"context"
	"errors"

	"github.com/mrYush/hint/pkg/agentapi"
)

// RunTurn executes one user turn: it streams the provider, runs any tool
// calls the model requests, feeds the results back, and repeats until the
// model produces a final answer or a limit is hit.
//
// history must already contain the new user message (and a system prompt,
// if any). RunTurn copies the slice and never mutates the caller's.
//
// RunTurn never returns a synchronous error — the same shape as
// [internal/provider/router.Router.Stream] (WP0.3), so a client handles a
// turn's failure the same way regardless of which layer produced it. A
// failure arrives as EventError followed by a terminal EventTurnEnd on the
// returned channel; the caller reconstructs the resulting history, if it
// needs one, purely from the EventMessage/EventCompaction events observed
// on the stream. A stateful Agent that kept its own history across turns
// would be the start of a session — that belongs to WP0.7, not here.
func (a *Agent) RunTurn(ctx context.Context, history []agentapi.Message) <-chan agentapi.Event {
	out := make(chan agentapi.Event)
	go a.runTurn(ctx, history, out)
	return out
}

func (a *Agent) runTurn(ctx context.Context, history []agentapi.Message, out chan<- agentapi.Event) {
	defer close(out)

	messages := append([]agentapi.Message(nil), history...)
	out <- agentapi.Event{Kind: agentapi.EventTurnStart}

	var total agentapi.Usage
	var lastUsage agentapi.Usage
	usageBaseline := 0

	for iter := 0; ; iter++ {
		select {
		case <-ctx.Done():
			endTurn(out, total, agentapi.FinishCanceled)
			return
		default:
		}

		if iter >= a.limits.MaxIterations {
			out <- agentapi.Event{Kind: agentapi.EventError, Err: agentapi.NewError(
				agentapi.ErrTurnLimit, "reached %d iterations without a final answer", a.limits.MaxIterations)}
			out <- agentapi.Event{Kind: agentapi.EventTurnEnd, FinishReason: agentapi.FinishError}
			return
		}

		if a.limits.MaxContextTokens > 0 && a.overflowing(a.occupancy(messages, lastUsage, usageBaseline)) {
			compacted, err := a.compact(ctx, messages, out)
			if err != nil {
				// Proactive compaction is a courtesy, not a requirement: if
				// it fails, report it and try the request as-is — the
				// provider's own ErrContextOverflow, handled below, is the
				// fallback that actually protects the turn.
				out <- agentapi.Event{Kind: agentapi.EventError, Err: agentapi.WrapError(
					agentapi.ErrUnknown, err, "proactive compaction")}
			} else {
				messages = compacted
			}
		}

		baseline := len(messages)
		res, err := a.streamOnce(ctx, messages, out)
		if err != nil {
			if agentapi.KindOf(err) == agentapi.ErrCanceled {
				endTurn(out, total, agentapi.FinishCanceled)
				return
			}
			out <- agentapi.Event{Kind: agentapi.EventError, Err: toError(err)}
			out <- agentapi.Event{Kind: agentapi.EventTurnEnd, FinishReason: agentapi.FinishError}
			return
		}

		if res.overflow {
			compacted, cerr := a.compact(ctx, messages, out)
			if cerr != nil {
				out <- agentapi.Event{Kind: agentapi.EventError, Err: toError(cerr)}
				out <- agentapi.Event{Kind: agentapi.EventTurnEnd, FinishReason: agentapi.FinishError}
				return
			}
			messages = compacted
			// Retry the same iteration's Stream call against the
			// compacted history without counting it as a tool-calling
			// round: iter is the outer for-loop's own counter, and `continue`
			// here skips straight back to the top without the implicit
			// iter++ a plain loop-around would apply, since this is a retry
			// of the current round, not the start of a new one.
			iter--
			continue
		}

		total = total.Add(res.usage)
		lastUsage = res.usage
		usageBaseline = baseline

		assistant := res.message
		messages = append(messages, assistant)
		out <- agentapi.Event{Kind: agentapi.EventMessage, Message: &assistant}

		if res.finish != agentapi.FinishToolCalls {
			endTurn(out, total, res.finish)
			return
		}

		results, abortErr, canceled := a.runTools(ctx, assistant.ToolCalls, out)
		for _, r := range results {
			m := r.Message()
			messages = append(messages, m)
			out <- agentapi.Event{Kind: agentapi.EventMessage, Message: &m}
		}

		if abortErr != nil {
			out <- agentapi.Event{Kind: agentapi.EventError, Err: agentapi.WrapError(
				agentapi.ErrUnknown, abortErr, "tool machinery failed")}
			out <- agentapi.Event{Kind: agentapi.EventTurnEnd, FinishReason: agentapi.FinishError}
			return
		}
		if canceled {
			endTurn(out, total, agentapi.FinishCanceled)
			return
		}
	}
}

// occupancy estimates how many tokens the next request would cost.
//
// When the last Stream call reported real usage, that InputTokens count is
// exact for the messages as they stood at that call, so only the messages
// appended since (baseline) need estimating; otherwise the whole history is
// estimated from scratch, e.g. before the first call of the turn.
func (a *Agent) occupancy(messages []agentapi.Message, lastUsage agentapi.Usage, baseline int) int64 {
	if lastUsage.InputTokens > 0 {
		return lastUsage.InputTokens + a.estimator.Estimate(messages[baseline:])
	}
	return a.estimator.Estimate(messages)
}

// endTurn emits the turn's final accumulated usage followed by its
// terminal event — the pair every non-error exit path of the loop ends
// with.
func endTurn(out chan<- agentapi.Event, total agentapi.Usage, finish agentapi.FinishReason) {
	out <- agentapi.Event{Kind: agentapi.EventUsage, Usage: &total}
	out <- agentapi.Event{Kind: agentapi.EventTurnEnd, FinishReason: finish}
}

// toError adapts a plain Go error into the *agentapi.Error that EventError
// carries. streamOnce already returns *agentapi.Error for everything that
// comes from the provider itself; errors.As (not a plain type assertion —
// this project's errorlint gate requires it) also reaches one wrapped by a
// ChatProvider implementation that does not return the contract's type
// directly.
func toError(err error) *agentapi.Error {
	var e *agentapi.Error
	if errors.As(err, &e) {
		return e
	}
	return agentapi.WrapError(agentapi.KindOf(err), err, "agent loop")
}
