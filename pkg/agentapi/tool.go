package agentapi

import (
	"context"
	"encoding/json"
	"fmt"
)

// ActionClass classifies what a tool does to the world. The permission layer
// decides whether to ask the user based on this class and the current run
// mode; it lives here, not in internal/permission, so that a third-party tool
// can name its own class.
type ActionClass string

const (
	// ClassRead observes without changing anything: reading a file, listing a
	// directory, searching. Never prompts.
	ClassRead ActionClass = "read"
	// ClassWrite modifies state the user owns: writing or editing a file.
	// Prompts with a diff unless the mode suppresses it.
	ClassWrite ActionClass = "write"
	// ClassExecute runs arbitrary code: shell commands, subprocesses. Prompts
	// with the command shown unless the mode suppresses it.
	ClassExecute ActionClass = "execute"
)

// Valid reports whether c is one of the defined classes.
func (c ActionClass) Valid() bool {
	switch c {
	case ClassRead, ClassWrite, ClassExecute:
		return true
	default:
		return false
	}
}

// ToolSchema describes a tool to the model. It is the subset of a [Tool] that
// travels to the provider; field names match the MCP tool descriptor so that
// an MCP-provided tool (Phase 2) converts field for field.
type ToolSchema struct {
	// Name is the identifier the model uses to call the tool. Unique within
	// a request.
	Name string `json:"name"`
	// Description tells the model when to use the tool. It is prompt text and
	// carries most of the tool's usability.
	Description string `json:"description,omitempty"`
	// InputSchema is a JSON Schema object describing the tool's arguments.
	// Held raw so that a tool can ship a hand-written schema and so that an
	// MCP server's schema passes through untouched.
	InputSchema json.RawMessage `json:"input_schema,omitempty"`
}

// Validate reports whether the schema can be sent to a provider.
func (s ToolSchema) Validate() error {
	if s.Name == "" {
		return fmt.Errorf("agentapi: tool schema has no name")
	}
	if len(s.InputSchema) == 0 {
		return fmt.Errorf("agentapi: tool %q has no input schema", s.Name)
	}
	if !isJSONObject(s.InputSchema) {
		return fmt.Errorf("agentapi: tool %q has a malformed input schema", s.Name)
	}
	return nil
}

// ToolCall is the model's request to run a tool.
//
// Arguments is raw JSON, not a string: providers that stream tool calls in
// fragments assemble and validate them before emitting a ToolCall, so every
// consumer sees a complete, syntactically valid call. A tool never has to
// parse a half-built argument object.
type ToolCall struct {
	// ID correlates the call with its [ToolResult]. Assigned by the provider.
	ID string `json:"id"`
	// Name is the tool to run, matching a [ToolSchema.Name] sent in the
	// request.
	Name string `json:"name"`
	// Arguments is the call's argument object, as JSON.
	Arguments json.RawMessage `json:"arguments,omitempty"`
	// After lists IDs of calls in the same batch that must finish before
	// this one starts. It is an optional hint for the agent's tool schedule
	// (WP0.11), which already runs every write and command after whatever
	// the model asked for before it and only overlaps reads; After lets a
	// producer that knows more tighten that — "read this file only after
	// that other call has listed it". IDs the batch does not contain are
	// ignored. Providers leave it empty; a client or a wrapper may set it.
	After []string `json:"after,omitempty"`
}

// Validate reports whether the call is complete and its arguments parse.
func (c ToolCall) Validate() error {
	if c.ID == "" {
		return fmt.Errorf("agentapi: tool call has no id")
	}
	if c.Name == "" {
		return fmt.Errorf("agentapi: tool call %q has no name", c.ID)
	}
	if len(c.Arguments) == 0 {
		return fmt.Errorf("agentapi: tool call %q has no arguments", c.ID)
	}
	if !isJSONObject(c.Arguments) {
		return fmt.Errorf("agentapi: tool call %q has malformed arguments", c.ID)
	}
	return nil
}

// isJSONObject reports whether raw is a syntactically valid JSON object.
//
// json.Valid alone is not enough here: it accepts any JSON value, so the
// literal null — which is what a nil json.RawMessage decodes back into after
// a round trip — and scalars like 123 would pass as an "argument object". A
// tool unmarshalling null into its argument struct gets a silent no-op and
// runs with every field zeroed, so the check belongs at this boundary rather
// than inside each tool.
func isJSONObject(raw json.RawMessage) bool {
	if !json.Valid(raw) {
		return false
	}
	for _, b := range raw {
		switch b {
		case ' ', '\t', '\r', '\n':
			continue
		default:
			return b == '{'
		}
	}
	return false
}

// ToolResult is the outcome of running a tool.
//
// Content is a part list rather than a string so that a tool can return an
// image (a screenshot, a rendered chart) once vision lands in Phase 3,
// without changing this type.
type ToolResult struct {
	// CallID is the [ToolCall.ID] this result answers.
	CallID string `json:"call_id"`
	// Name is the tool that produced the result. Redundant with the call, but
	// it keeps a session file readable on its own.
	Name string `json:"name,omitempty"`
	// Content is what the model sees. Already truncated by the tool if the
	// raw output was too large.
	Content []ContentPart `json:"content,omitempty"`
	// IsError marks a failure. The result is still fed back to the model —
	// an error the model can read and react to beats a turn that aborts.
	IsError bool `json:"is_error,omitempty"`
}

// ErrorResult returns a failed ToolResult carrying msg as its text.
func ErrorResult(callID, name, msg string) ToolResult {
	return ToolResult{
		CallID:  callID,
		Name:    name,
		Content: []ContentPart{Text(msg)},
		IsError: true,
	}
}

// TextResult returns a successful ToolResult carrying out as its text.
func TextResult(callID, name, out string) ToolResult {
	return ToolResult{
		CallID:  callID,
		Name:    name,
		Content: []ContentPart{Text(out)},
	}
}

// Message converts the result into the [Message] that is appended to the
// conversation and sent back to the provider.
func (r ToolResult) Message() Message {
	return Message{
		Role:       RoleTool,
		Content:    r.Content,
		ToolCallID: r.CallID,
	}
}

// Text returns the result's text parts joined by newlines.
func (r ToolResult) Text() string {
	return Message{Role: RoleTool, Content: r.Content}.Text()
}

// Validate reports whether the result is well-formed.
func (r ToolResult) Validate() error {
	if r.CallID == "" {
		return fmt.Errorf("agentapi: tool result has no call id")
	}
	for i, p := range r.Content {
		if err := p.Validate(); err != nil {
			return fmt.Errorf("agentapi: content[%d]: %w", i, err)
		}
	}
	return nil
}

// Tool is a capability the agent can invoke on the model's behalf.
//
// Implementations live in internal/tool/builtin (Phase 0) and are adapted
// from MCP servers (Phase 2). The interface is declared here so that a tool
// can be written outside this module.
//
// Run receives the raw argument JSON from [ToolCall.Arguments] and is
// responsible for decoding and validating it against its own schema — a
// model can and will produce arguments that do not match. Run must honour ctx
// cancellation, and must return a ToolResult with IsError set for a failure
// the model should see; it returns a non-nil error only for a failure of the
// tool machinery itself, which aborts the turn.
//
// Run of a read-class tool may be called concurrently: the agent overlaps
// the independent reads of one batch (WP0.11), so such a tool must be safe
// for use from several goroutines at once. A write- or execute-class tool
// never runs at the same time as any other call.
type Tool interface {
	// Name is the identifier the model calls, matching ToolSchema.Name.
	Name() string
	// Description is the prompt text describing when to use the tool.
	Description() string
	// InputSchema is the JSON Schema of the tool's arguments.
	InputSchema() json.RawMessage
	// Class is the permission class the tool's action falls into.
	Class() ActionClass
	// Run executes the tool.
	Run(ctx context.Context, callID string, args json.RawMessage) (ToolResult, error)
}

// SchemaOf builds the [ToolSchema] describing t. Provider implementations use
// it instead of reading the four methods themselves.
func SchemaOf(t Tool) ToolSchema {
	return ToolSchema{
		Name:        t.Name(),
		Description: t.Description(),
		InputSchema: t.InputSchema(),
	}
}
