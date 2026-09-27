package permission

import (
	"context"
	"fmt"

	"github.com/mrYush/hint/internal/tool"
	"github.com/mrYush/hint/pkg/agentapi"
)

// Gate is the permission decision for one run: a [Mode], the grants the
// user has made so far, and the [Prompter] that asks for new ones. It
// satisfies the agent loop's Authorizer interface.
type Gate struct {
	mode     Mode
	prompter Prompter
	grants   allowList
}

// New returns a Gate in mode that asks through p. A nil p is allowed and
// means every prompt is denied — the fail-closed answer for a run with
// nobody to ask.
func New(mode Mode, p Prompter) *Gate {
	return &Gate{mode: mode, prompter: p}
}

// Mode returns the gate's run mode.
func (g *Gate) Mode() Mode { return g.mode }

// Review decides whether call needs the user's confirmation and, when it
// does, builds the request to show. Read-class calls and calls the mode
// or an earlier "always" covers come back with ask == false and a
// minimal request; the preview — a diff, a command — is only built when
// the mode says the class asks. The allow-list is consulted after the
// preview because it needs the command text the preview carries; only
// execute-class calls can be granted, and their preview is the command
// itself, so nothing expensive is ever built for a call that a grant
// then silences.
func (g *Gate) Review(ctx context.Context, call agentapi.ToolCall, t agentapi.Tool) (agentapi.PermissionRequest, bool) {
	if !g.mode.Asks(t.Class()) {
		return agentapi.PermissionRequest{CallID: call.ID, Tool: call.Name, Class: t.Class(), Summary: call.Name}, false
	}
	req := tool.Describe(ctx, call, t)
	if g.grants.allows(req) {
		return req, false
	}
	return req, true
}

// Authorize asks the prompter about req and records an "always" answer.
// It returns whether the call may run; the error is non-nil only when ctx
// ended before an answer arrived, in which case the call is not allowed.
func (g *Gate) Authorize(ctx context.Context, req agentapi.PermissionRequest) (bool, error) {
	if g.prompter == nil {
		return false, nil
	}
	decision, err := g.prompter.Prompt(ctx, Ask{Request: req, Always: g.alwaysLabel(req)})
	if err != nil {
		if ctx.Err() != nil {
			return false, ctx.Err()
		}
		// The prompter itself failed with the context still alive: there
		// is no answer, and no answer is a denial.
		return false, nil
	}
	switch decision {
	case Allow:
		return true, nil
	case AllowAlways:
		g.grants.grant(req)
		return true, nil
	case Deny:
		return false, nil
	default:
		return false, nil
	}
}

// alwaysLabel describes what "always" would remember for req, or "" when
// the option is not offered — every write, and a command without a
// scope (see [CommandScope]).
func (g *Gate) alwaysLabel(req agentapi.PermissionRequest) string {
	if req.Class != agentapi.ClassExecute {
		return ""
	}
	scope := CommandScope(req.Detail)
	if scope == "" {
		return ""
	}
	return fmt.Sprintf("commands starting with %q", scope)
}
