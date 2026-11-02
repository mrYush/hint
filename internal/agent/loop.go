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

		if changed := a.refreshPrefix(ctx, &messages, out); changed {
			// The prefix the last usage figure was measured with is gone:
			// estimate the whole request afresh rather than trust it.
			lastUsage = agentapi.Usage{}
			usageBaseline = 0
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
				lastUsage = agentapi.Usage{}
				usageBaseline = 0
			}
		}

		// One outer iteration is one logical round. A retry after
		// ErrContextOverflow re-sends the same request against the
		// compacted history; it is not a new round, so it does not spend
		// the MaxIterations budget — but it is bounded by compaction
		// itself, which refuses once nothing but summaries is left.
		var res streamResult
		for {
			baseline := len(messages)
			var err error
			res, err = a.streamOnce(ctx, messages, out)
			if err != nil {
				if agentapi.KindOf(err) == agentapi.ErrCanceled {
					endTurn(out, total, agentapi.FinishCanceled)
					return
				}
				out <- agentapi.Event{Kind: agentapi.EventError, Err: toError(err)}
				out <- agentapi.Event{Kind: agentapi.EventTurnEnd, FinishReason: agentapi.FinishError}
				return
			}
			if !res.overflow {
				total = total.Add(res.usage)
				lastUsage = res.usage
				usageBaseline = baseline
				break
			}
			compacted, cerr := a.compact(ctx, messages, out)
			if cerr != nil {
				out <- agentapi.Event{Kind: agentapi.EventError, Err: toError(cerr)}
				out <- agentapi.Event{Kind: agentapi.EventTurnEnd, FinishReason: agentapi.FinishError}
				return
			}
			messages = compacted
			// The history the last usage figure was measured with is gone:
			// the next attempt must estimate from scratch, and a stale
			// baseline would index past the shorter history.
			lastUsage = agentapi.Usage{}
			usageBaseline = 0
		}

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
			// runTools has already routed a context-caused failure into the
			// canceled path, so what is left is a genuine machinery error;
			// KindOf keeps a classified *agentapi.Error's kind instead of
			// flattening everything to ErrUnknown.
			out <- agentapi.Event{Kind: agentapi.EventError, Err: agentapi.WrapError(
				agentapi.KindOf(abortErr), abortErr, "tool machinery failed")}
			out <- agentapi.Event{Kind: agentapi.EventTurnEnd, FinishReason: agentapi.FinishError}
			return
		}
		if canceled {
			endTurn(out, total, agentapi.FinishCanceled)
			return
		}
	}
}

// refreshPrefix asks the Preamble, when there is one, for the system
// prefix of the next request and swaps it in for the run of leading system
// messages that belong to it. The run stops at the first
// [agentapi.Message.Summary]: a compaction summary sits right after the
// prefix but is history, not prompt, so rebuilding the prompt must leave
// it in place. It reports whether the prefix changed. A Preamble that
// fails is reported as a non-terminal EventError and the request keeps
// the prefix it has: a rule that did not load is a worse prompt, not a
// failed turn.
func (a *Agent) refreshPrefix(ctx context.Context, messages *[]agentapi.Message, out chan<- agentapi.Event) bool {
	if a.preamble == nil {
		return false
	}
	prefix, err := a.preamble.Prefix(ctx, *messages)
	if err != nil {
		out <- agentapi.Event{Kind: agentapi.EventError, Err: agentapi.WrapError(
			agentapi.ErrUnknown, err, "rebuilding the system prompt")}
		return false
	}
	n := 0
	for n < len(*messages) && (*messages)[n].Role == agentapi.RoleSystem && !(*messages)[n].Summary {
		n++
	}
	if samePrefix((*messages)[:n], prefix) {
		return false
	}
	*messages = append(append([]agentapi.Message(nil), prefix...), (*messages)[n:]...)
	return true
}

// samePrefix reports whether two system prefixes carry the same text.
func samePrefix(a, b []agentapi.Message) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Role != b[i].Role || a[i].Text() != b[i].Text() {
			return false
		}
	}
	return true
}

// occupancy estimates how many tokens the next request would cost.
//
// When the last Stream call reported real usage, that InputTokens count is
// exact for the messages as they stood at that call, so only the messages
// appended since (baseline) need estimating; otherwise the whole history is
// estimated from scratch, e.g. before the first call of the turn. A
// baseline past the end of messages can only come from a stale figure —
// history shrank under it — so that case falls back to the whole-history
// estimate rather than slicing out of range.
func (a *Agent) occupancy(messages []agentapi.Message, lastUsage agentapi.Usage, baseline int) int64 {
	if lastUsage.InputTokens > 0 && baseline <= len(messages) {
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
