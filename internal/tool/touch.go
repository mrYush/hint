package tool

import (
	"encoding/json"

	"github.com/mrYush/hint/pkg/agentapi"
)

// Toucher is implemented by a tool that can name the files a call would
// read or write, before it runs. The split rules of WP0.12 load into the
// system prompt once a turn touches a path they apply to, and this is how
// the loop learns which paths a turn touched: the tool says so, in the
// same way a [Describer] says what a call would do. A tool that does not
// implement it touches nothing as far as rules are concerned — bash does
// not parse its command for paths, and an MCP-adapted tool (Phase 2) will
// decide for itself.
type Toucher interface {
	// Touches returns the absolute paths args would read or write, or nil
	// when they cannot be told (malformed arguments, a path outside the
	// root). It must not change anything.
	Touches(args json.RawMessage) []string
}

// Touches returns the paths call would read or write, asking the tool —
// or any tool it decorates — when it is a [Toucher].
func Touches(call agentapi.ToolCall, t agentapi.Tool) []string {
	for x := t; x != nil; x = Unwrap(x) {
		if tc, ok := x.(Toucher); ok {
			return tc.Touches(call.Arguments)
		}
	}
	return nil
}
