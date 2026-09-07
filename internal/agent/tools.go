package agent

import (
	"context"
	"fmt"

	"github.com/mrYush/hint/pkg/agentapi"
)

// runTools executes calls in the order the model requested them, reporting
// each via EventToolStart/EventToolEnd as it runs.
//
// Sequential, not a parallel errgroup.Group: opencode's loop runs calls one
// at a time, and it keeps a stable order for the confirmation prompts,
// which ask about one call at a time. A parallel batch would trade that
// order — and the per-call gating — for latency this MVP does not need.
//
// Outcomes per call, all from [agentapi.Tool]'s contract plus the
// permission layer's:
//   - unknown tool name, the user denying the call, or Run reporting
//     IsError: fed back to the model as an [agentapi.ToolResult], the
//     batch continues. A denied call is never announced with
//     EventToolStart — it did not start.
//   - Run panics: recovered into an error ToolResult, same as above — a
//     tool's internal bug should not take the whole process down.
//   - Run returns a non-nil error with ctx still alive (the tool machinery
//     itself broke, not the action failing): the batch — and the turn —
//     aborts. No result is fed back for the call that broke it.
//   - ctx ends — between calls, inside a running tool, or while a prompt
//     waits for an answer: the batch stops and the interrupted call plus
//     every remaining one become a "canceled" ToolResult rather than
//     simply being dropped, so the model (if the turn is ever resumed)
//     sees why those calls never ran. A tool passing a caller's
//     context.Canceled or DeadlineExceeded through is classified by the
//     context, not wrapped as a machinery failure.
func (a *Agent) runTools(ctx context.Context, calls []agentapi.ToolCall, out chan<- agentapi.Event) (results []agentapi.ToolResult, abortErr error, canceled bool) {
	for i, call := range calls {
		if ctx.Err() != nil {
			return cancelFrom(results, calls, i), nil, true //nolint:nilerr // the canceled flag carries it; the loop reports the context, not an error
		}

		tool, ok := a.tools[call.Name]
		if !ok {
			out <- agentapi.Event{Kind: agentapi.EventToolStart, Call: &call}
			res := agentapi.ErrorResult(call.ID, call.Name, fmt.Sprintf("unknown tool %q", call.Name))
			out <- agentapi.Event{Kind: agentapi.EventToolEnd, Result: &res}
			results = append(results, res)
			continue
		}

		if a.authorizer != nil {
			req, ask := a.authorizer.Review(ctx, call, tool)
			if ask {
				// The event goes out before the blocking Authorize so a
				// client that renders prompts from the stream (Phase 1)
				// sees the question while it is being asked.
				out <- agentapi.Event{Kind: agentapi.EventPermission, Permission: &req}
				allowed, err := a.authorizer.Authorize(ctx, req)
				if err != nil {
					return a.abort(ctx, results, calls, i, fmt.Errorf("authorizing %s: %w", call.Name, err))
				}
				if !allowed {
					res := agentapi.ErrorResult(call.ID, call.Name, deniedMessage(req))
					out <- agentapi.Event{Kind: agentapi.EventToolEnd, Result: &res}
					results = append(results, res)
					continue
				}
			}
		}

		out <- agentapi.Event{Kind: agentapi.EventToolStart, Call: &call}
		res, err := a.runOneTool(ctx, tool, call)
		if err != nil {
			return a.abort(ctx, results, calls, i, err)
		}
		out <- agentapi.Event{Kind: agentapi.EventToolEnd, Result: &res}
		results = append(results, res)
	}
	return results, nil, false
}

// abort classifies the error that stopped the batch at calls[i]: if the
// turn's context has ended, the batch is canceled — the error is the
// context's own, passed through — and the unfinished calls get "canceled"
// results; otherwise it is a machinery failure that aborts the turn.
func (a *Agent) abort(ctx context.Context, results []agentapi.ToolResult, calls []agentapi.ToolCall, i int, err error) ([]agentapi.ToolResult, error, bool) {
	if ctx.Err() != nil {
		return cancelFrom(results, calls, i), nil, true //nolint:nilerr // same: canceled is a state of the batch, not a failure of the turn
	}
	return results, err, false
}

// cancelFrom appends a "canceled" result for every call from calls[i] on.
func cancelFrom(results []agentapi.ToolResult, calls []agentapi.ToolCall, i int) []agentapi.ToolResult {
	for _, c := range calls[i:] {
		results = append(results, agentapi.ErrorResult(c.ID, c.Name, "canceled"))
	}
	return results
}

// deniedMessage is what the model reads when the user refuses a call. It
// says what not to do next: a model that simply retries the same call
// would put the same prompt back in front of the user.
func deniedMessage(req agentapi.PermissionRequest) string {
	return fmt.Sprintf("permission denied: the user did not approve %q. "+
		"Do not retry the same action unchanged; explain what you intended or propose a different approach.", req.Summary)
}

// runOneTool runs a single call, converting a panic into an error
// [agentapi.ToolResult] instead of letting it crash the process.
func (a *Agent) runOneTool(ctx context.Context, tool agentapi.Tool, call agentapi.ToolCall) (res agentapi.ToolResult, err error) {
	// A tool's own bug must not take the agent process down with it: recover
	// converts a panic into the same shape as a returned IsError result. The
	// named return values are what let the deferred recover hand back a
	// value instead of just re-panicking — res and err below are the
	// function's actual return slots, not local shadows.
	defer func() {
		if r := recover(); r != nil {
			res = agentapi.ErrorResult(call.ID, call.Name, fmt.Sprintf("tool panicked: %v", r))
			err = nil
		}
	}()

	res, err = tool.Run(ctx, call.ID, call.Arguments)
	if err != nil {
		return agentapi.ToolResult{}, err
	}
	res.CallID = call.ID
	if res.Name == "" {
		res.Name = call.Name
	}
	return res, nil
}
