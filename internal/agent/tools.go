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
// at a time, and it keeps a stable order for WP0.6's confirmation prompts,
// which ask about one call at a time. A parallel batch would trade that
// order — and WP0.6's per-call gating — for latency this MVP does not need.
//
// Three distinct outcomes per call, all from [agentapi.Tool]'s contract:
//   - unknown tool name, or Run reporting IsError: fed back to the model as
//     an [agentapi.ToolResult], the batch continues.
//   - Run panics: recovered into an error ToolResult, same as above — a
//     tool's internal bug should not take the whole process down.
//   - Run returns a non-nil error (the tool machinery itself broke, not the
//     action failing): the batch — and the turn — aborts. No result is fed
//     back for the call that broke it.
//
// ctx cancellation between calls stops the batch and turns every remaining
// call into a "canceled" ToolResult rather than simply dropping it, so the
// model (if the turn is ever resumed) sees why those calls never ran.
func (a *Agent) runTools(ctx context.Context, calls []agentapi.ToolCall, out chan<- agentapi.Event) (results []agentapi.ToolResult, abortErr error, canceled bool) {
	for i, call := range calls {
		select {
		case <-ctx.Done():
			for _, c := range calls[i:] {
				results = append(results, agentapi.ErrorResult(c.ID, c.Name, "canceled"))
			}
			return results, nil, true
		default:
		}

		out <- agentapi.Event{Kind: agentapi.EventToolStart, Call: &call}

		res, err := a.runOneTool(ctx, call)
		if err != nil {
			return results, err, false
		}

		out <- agentapi.Event{Kind: agentapi.EventToolEnd, Result: &res}
		results = append(results, res)
	}
	return results, nil, false
}

// runOneTool runs a single call, converting an unknown tool name or a panic
// into an error [agentapi.ToolResult] instead of letting either abort the
// turn or crash the process.
func (a *Agent) runOneTool(ctx context.Context, call agentapi.ToolCall) (res agentapi.ToolResult, err error) {
	tool, ok := a.tools[call.Name]
	if !ok {
		return agentapi.ErrorResult(call.ID, call.Name, fmt.Sprintf("unknown tool %q", call.Name)), nil
	}

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
