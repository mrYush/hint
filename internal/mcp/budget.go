package mcp

import (
	"encoding/json"
	"fmt"

	"github.com/mrYush/hint/pkg/agentapi"
)

// Budget bounds the MCP tool descriptions that travel to the provider.
// The numbers are bytes of the compact {name, description, input_schema}
// JSON, the same shape [agentapi.ToolSchema] sends, not bytes of the full
// MCP descriptor: the adapter has already dropped title, outputSchema and
// annotations by the time a schema is counted.
type Budget struct {
	// Total is the whole MCP share of the prompt. Zero selects the
	// default.
	Total int
	// PerServer hides one server whose tools alone exceed it. Zero
	// selects the default; a negative value disables the per-server cap.
	PerServer int
}

// The spike measured 44.6 KiB for the ten reachable servers and 37.4 KiB
// for the seven reference servers, so 48 KiB fits the measured catalog
// without trimming. A single server over 20 KiB has to be narrowed with
// its tools allow/deny list.
const (
	DefaultTotalBudget     = 48 << 10
	DefaultPerServerBudget = 20 << 10
)

// DefaultBudget is the policy from the Q6 spike.
func DefaultBudget() Budget {
	return Budget{Total: DefaultTotalBudget, PerServer: DefaultPerServerBudget}
}

// normalize fills a zero budget with the defaults; a negative PerServer
// stays negative and disables that cap.
func (b Budget) normalize() Budget {
	if b.Total <= 0 {
		b.Total = DefaultTotalBudget
	}
	if b.PerServer == 0 {
		b.PerServer = DefaultPerServerBudget
	}
	return b
}

// schemaSize is the byte cost of one tool in the provider request.
func schemaSize(t agentapi.Tool) int {
	b, err := json.Marshal(agentapi.SchemaOf(t))
	if err != nil {
		// The registry validates the schema before it is sent; a tool
		// that cannot marshal here has no place in the request.
		return 0
	}
	return len(b)
}

func hideReasonServer(name string, size, cap int) string {
	return fmt.Sprintf("MCP server %q is hidden: its tools take %d bytes, over the %d-byte per-server budget", name, size, cap)
}

func hideReasonTotal(name string, size, total, cap int) string {
	return fmt.Sprintf("MCP server %q is hidden: %d more bytes would put the MCP tools at %d, over the %d-byte budget", name, size, total+size, cap)
}
