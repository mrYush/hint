package tool

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/mrYush/hint/pkg/agentapi"
)

// Limits bound one tool call.
type Limits struct {
	// Timeout caps a single Run. Zero disables the cap, for a tool that
	// enforces its own (bash takes a per-call timeout argument).
	Timeout time.Duration
	// MaxOutput caps the bytes of every text part in the result, applying
	// [Truncate] beyond it. Zero disables truncation.
	MaxOutput int
}

// DefaultLimits is what a tool gets unless the caller says otherwise: 30s is
// generous for anything that only touches the local disk, and 50 kB
// (~12k tokens) leaves room for several tool results in one turn of a
// 128k-token window without any single one dominating it.
func DefaultLimits() Limits {
	return Limits{Timeout: 30 * time.Second, MaxOutput: 50_000}
}

// WithLimits wraps t so that every Run honours l. The wrapper is itself an
// [agentapi.Tool]: name, description, schema and class pass through
// untouched, only Run changes.
//
// This is where timeout and truncation are enforced for every tool at once —
// the plan's "each tool: timeout, large-output truncation" — instead of each
// implementation remembering to do both. A tool built outside this package,
// or adapted from an MCP server later, gets the same guarantees by being
// wrapped.
func WithLimits(t agentapi.Tool, l Limits) agentapi.Tool {
	return &limited{Tool: t, limits: l}
}

// limited is the decorator returned by WithLimits. Embedding the interface
// promotes the four descriptive methods; only Run is overridden.
type limited struct {
	agentapi.Tool
	limits Limits
}

// Unwrap returns the decorated tool, for a caller that needs the concrete
// implementation (the same convention errors.Unwrap follows).
func (l *limited) Unwrap() agentapi.Tool { return l.Tool }

// Run implements agentapi.Tool.
func (l *limited) Run(ctx context.Context, callID string, args json.RawMessage) (agentapi.ToolResult, error) {
	if l.limits.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, l.limits.Timeout)
		defer cancel()
	}

	res, err := l.Tool.Run(ctx, callID, args)
	if err != nil {
		// Hitting the cap is a failure of the action, not of the tool
		// machinery: the model should see it and try something smaller,
		// not have the whole turn aborted. A cancel coming from above
		// (Ctrl-C) is left alone — the loop already knows what it did.
		if errors.Is(err, context.DeadlineExceeded) && errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return agentapi.ErrorResult(callID, l.Name(),
				fmt.Sprintf("%s timed out after %s", l.Name(), l.limits.Timeout)), nil
		}
		return res, err
	}

	for i := range res.Content {
		if res.Content[i].Kind == agentapi.PartText {
			res.Content[i].Text = Truncate(res.Content[i].Text, l.limits.MaxOutput)
		}
	}
	return res, nil
}
