package agent

import (
	"context"
	"fmt"
	"sync"

	"github.com/mrYush/hint/pkg/agentapi"
)

// runTools executes one batch of calls under the schedule buildSchedule
// derives for it, reporting each call via EventToolStart/EventToolEnd as
// it runs. Results come back in the model's request order whatever order
// the calls finished in: a session file and the next request must look the
// same across runs of the same batch.
//
// Before WP0.11 this was a plain loop, one call after another, the way
// opencode runs its batches. Independent reads of one batch now overlap
// (see schedule.go); everything the permission layer may ask about still
// runs one at a time, in order, so the prompts keep their stable sequence.
//
// Outcomes per call, all from [agentapi.Tool]'s contract plus the
// permission layer's:
//   - unknown tool name, the user denying the call, or Run reporting
//     IsError: fed back to the model as an [agentapi.ToolResult]; the
//     batch continues and siblings in the same group are left alone. A
//     denied call is never announced with EventToolStart — it did not start.
//   - Run panics: recovered into an error ToolResult, same as above — a
//     tool's internal bug should not take the whole process down.
//   - Run returns a non-nil error with ctx still alive (the tool machinery
//     itself broke, not the action failing): the batch — and the turn —
//     aborts. Siblings still running in the same group are canceled; the
//     results already in hand are returned, the rest are not.
//   - ctx ends — between steps, inside a running tool, or while a prompt
//     waits for an answer: the batch stops and every call without a result
//     becomes a "canceled" ToolResult rather than simply being dropped, so
//     the model (if the turn is ever resumed) sees why those calls never
//     ran. A tool passing a caller's context.Canceled or DeadlineExceeded
//     through is classified by the context, not wrapped as a machinery
//     failure.
func (a *Agent) runTools(ctx context.Context, calls []agentapi.ToolCall, out chan<- agentapi.Event) (results []agentapi.ToolResult, abortErr error, canceled bool) {
	b := &batch{
		agent:   a,
		calls:   calls,
		results: make([]agentapi.ToolResult, len(calls)),
		done:    make([]bool, len(calls)),
		out:     out,
		limit:   max(a.limits.MaxParallelTools, 1),
	}
	err := b.run(ctx, buildSchedule(calls, a.tools, b.limit))
	return b.collect(ctx, err)
}

// batch is one schedule in flight: the calls, a result slot per call, and
// what every leaf needs to run. Leaves of a par fill disjoint slots from
// their own goroutines, so the slots need no lock; the only state a group
// shares is its first machinery error, and runPar owns that.
type batch struct {
	agent   *Agent
	calls   []agentapi.ToolCall
	results []agentapi.ToolResult
	done    []bool // results[i] is filled
	out     chan<- agentapi.Event
	limit   int // leaves of one par running at once
	// promptMu keeps permission prompts one at a time even if an Authorizer
	// ever asks about a read-class call inside a par. The contract says it
	// will not, and the schedule never groups gated classes, but two
	// questions racing for one terminal is not a failure mode to leave to
	// a contract.
	promptMu sync.Mutex
}

// run walks n. The error is the first machinery failure, or ctx's own
// error once it has ended; collect tells the two apart.
func (b *batch) run(ctx context.Context, n node) error {
	switch n := n.(type) {
	case call:
		return b.runCall(ctx, int(n))
	case seq:
		for _, child := range n {
			if err := b.run(ctx, child); err != nil {
				return err
			}
		}
		return nil
	case par:
		return b.runPar(ctx, n)
	default:
		// node is sealed; a new shape is added here, not discovered here.
		panic(fmt.Sprintf("agent: unknown schedule node %T", n))
	}
}

// runPar starts every child of group together, at most b.limit at a time,
// and waits for all of them. A machinery failure in one child cancels the
// group's context so its siblings stop early; their results, if any, are
// discarded with the turn. Anything less than that — an error result, a
// panic, a denial — is one child's outcome and leaves its siblings alone.
func (b *batch) runPar(ctx context.Context, group par) error {
	gctx, cancel := context.WithCancel(ctx)
	defer cancel()

	var (
		wg    sync.WaitGroup
		mu    sync.Mutex
		first error
		slots = make(chan struct{}, b.limit)
	)
	for _, child := range group {
		wg.Add(1)
		go func(child node) {
			defer wg.Done()
			select {
			case slots <- struct{}{}:
				defer func() { <-slots }()
			case <-gctx.Done():
				// Never started: the slot stays empty and collect names
				// it canceled, or the turn aborts on the sibling's error.
				return
			}
			if err := b.run(gctx, child); err != nil {
				mu.Lock()
				if first == nil {
					first = err
					cancel()
				}
				mu.Unlock()
			}
		}(child)
	}
	wg.Wait()
	return first
}

// runCall runs calls[i] and fills its slot. It returns nil for every
// outcome the model gets to read about, ctx's error if ctx ended before the
// call could start, and whatever the machinery failed with otherwise.
func (b *batch) runCall(ctx context.Context, i int) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	c := b.calls[i]
	a := b.agent

	tool, ok := a.tools[c.Name]
	if !ok {
		b.out <- agentapi.Event{Kind: agentapi.EventToolStart, Call: &c}
		b.finish(i, agentapi.ErrorResult(c.ID, c.Name, fmt.Sprintf("unknown tool %q", c.Name)))
		return nil
	}

	if a.authorizer != nil {
		req, ask := a.authorizer.Review(ctx, c, tool)
		if ask {
			allowed, err := b.prompt(ctx, req)
			if err != nil {
				return fmt.Errorf("authorizing %s: %w", c.Name, err)
			}
			if !allowed {
				b.finish(i, agentapi.ErrorResult(c.ID, c.Name, deniedMessage(req)))
				return nil
			}
		}
	}

	b.out <- agentapi.Event{Kind: agentapi.EventToolStart, Call: &c}
	res, err := a.runOneTool(ctx, tool, c)
	if err != nil {
		return err
	}
	b.finish(i, res)
	return nil
}

// prompt asks the authorizer about req, one question at a time.
func (b *batch) prompt(ctx context.Context, req agentapi.PermissionRequest) (bool, error) {
	b.promptMu.Lock()
	defer b.promptMu.Unlock()
	// The event goes out before the blocking Authorize so a client that
	// renders prompts from the stream (Phase 1) sees the question while it
	// is being asked.
	b.out <- agentapi.Event{Kind: agentapi.EventPermission, Permission: &req}
	return b.agent.authorizer.Authorize(ctx, req)
}

// finish records res for calls[i] and announces it.
func (b *batch) finish(i int, res agentapi.ToolResult) {
	b.results[i], b.done[i] = res, true
	b.out <- agentapi.Event{Kind: agentapi.EventToolEnd, Result: &b.results[i]}
}

// collect turns the batch's slots into runTools' three-way answer.
//
// ctx having ended is a cancel whatever error the walk returned — a tool
// passing ctx.Err() through, a prompt interrupted, a sibling stopped by
// its group — and every call without a result gets a "canceled" one. With
// ctx alive, a non-nil error is a machinery failure: the results in hand
// go back in request order and the turn aborts.
func (b *batch) collect(ctx context.Context, err error) ([]agentapi.ToolResult, error, bool) {
	if ctx.Err() != nil {
		for i, c := range b.calls {
			if !b.done[i] {
				b.results[i] = agentapi.ErrorResult(c.ID, c.Name, "canceled")
			}
		}
		return b.results, nil, true
	}
	if err != nil {
		var got []agentapi.ToolResult
		for i := range b.calls {
			if b.done[i] {
				got = append(got, b.results[i])
			}
		}
		return got, err, false
	}
	return b.results, nil, false
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
