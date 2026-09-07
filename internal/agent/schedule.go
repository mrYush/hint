package agent

import "github.com/mrYush/hint/pkg/agentapi"

// A schedule says which of a batch's tool calls may overlap. It is a
// series-parallel tree: a seq runs its children one after another, a par
// starts them together and waits for all of them, and a call is one index
// into the batch. The Agent builds it from the model's ToolCall list; the
// model never has to author one (docs/plan/phase-0-mvp-cli.md, WP0.11).
//
// node is a closed set: the unexported marker method keeps the three
// shapes below its only implementations, so the executor's type switch is
// complete by construction and a fourth shape is a compile-time change
// here, not a runtime surprise elsewhere.
type node interface{ isNode() }

// call is a leaf: the index of the ToolCall it runs.
type call int

// seq runs its children in order, each after the previous one finished.
type seq []node

// par starts its children together and joins before returning.
type par []node

func (call) isNode() {}
func (seq) isNode()  {}
func (par) isNode()  {}

// buildSchedule turns calls, in the model's request order, into a schedule.
//
// The rule is by action class, not by argument: consecutive calls to
// registered read-class tools share a par; every other call — a write, an
// execute, an unknown tool — is a step of its own in the enclosing seq.
// Reads cannot conflict with each other, and a read/write conflict is
// already ordered by the write being its own step, so nothing finer (paths,
// argument inspection) is needed to keep the batch as safe as the
// sequential chain it replaces. Gated classes never enter a par because
// the permission layer asks about one call at a time.
//
// A call's After hint tightens the heuristic in one direction only: naming
// a call in the par being assembled closes that par first, so the two run
// in request order. IDs of calls already past, of calls still to come, or
// of nothing in the batch are ignored — the conservative schedule wins.
//
// maxParallel <= 1 is the plain chain of WP0.4: every call its own step.
func buildSchedule(calls []agentapi.ToolCall, tools map[string]agentapi.Tool, maxParallel int) node {
	if maxParallel <= 1 {
		chain := make(seq, len(calls))
		for i := range calls {
			chain[i] = call(i)
		}
		return chain
	}

	var (
		steps   seq
		group   par
		inGroup = map[string]bool{} // IDs of the calls in group
	)
	flush := func() {
		switch len(group) {
		case 0:
		case 1:
			steps = append(steps, group[0])
		default:
			steps = append(steps, group)
		}
		group, inGroup = nil, map[string]bool{}
	}

	for i, c := range calls {
		tool, ok := tools[c.Name]
		if !ok || tool.Class() != agentapi.ClassRead {
			flush()
			steps = append(steps, call(i))
			continue
		}
		for _, id := range c.After {
			if inGroup[id] {
				flush()
				break
			}
		}
		group = append(group, call(i))
		inGroup[c.ID] = true
	}
	flush()
	return steps
}
