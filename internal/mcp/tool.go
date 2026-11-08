package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mrYush/hint/internal/text"
	"github.com/mrYush/hint/internal/tool"
	"github.com/mrYush/hint/pkg/agentapi"
)

// toolAdapter is one server tool exposed as an [agentapi.Tool].
//
// The name is "<server>.<tool>" and never changes across a restart, so a
// call recorded in a session still names the same tool. The permission
// class comes from the server's config; the server's annotations are not
// consulted.
type toolAdapter struct {
	server      *server
	remote      string
	name        string
	description string
	schema      json.RawMessage
	class       agentapi.ActionClass
}

// newToolAdapter converts one SDK tool. It rejects a name the provider
// could not carry and a schema with an unsafe number instead of sending a
// silently altered tool.
func newToolAdapter(srv *server, remote *mcp.Tool) (*toolAdapter, error) {
	if remote == nil {
		return nil, fmt.Errorf("mcp: server %q: nil tool", srv.cfg.Name)
	}
	if !toolNamePattern.MatchString(remote.Name) {
		return nil, fmt.Errorf("mcp: server %q: tool name %q must match %s", srv.cfg.Name, remote.Name, toolNamePattern)
	}
	name := srv.cfg.Name + "." + remote.Name
	if len(name) > 128 {
		return nil, fmt.Errorf("mcp: server %q: tool name %q is longer than 128 bytes", srv.cfg.Name, name)
	}
	schema, err := convertSchema(remote.Name, remote.InputSchema)
	if err != nil {
		return nil, fmt.Errorf("mcp: server %q: tool %q: %w", srv.cfg.Name, remote.Name, err)
	}
	if bytes.Contains(schema, []byte(`"$ref"`)) || bytes.Contains(schema, []byte(`"$defs"`)) {
		if srv.mgr != nil {
			// Providers do not resolve references. The schema is still
			// usable when the reference is decorative; dereferencing is
			// a later enhancement, so say why a call may fail.
			srv.mgr.warnf("MCP server %q: tool %q uses $ref or $defs, which providers do not resolve", srv.cfg.Name, remote.Name)
		}
	}
	return &toolAdapter{
		server:      srv,
		remote:      remote.Name,
		name:        name,
		description: capDescription(remote.Description, srv.cfg.DescriptionMax),
		schema:      schema,
		class:       srv.cfg.Permission,
	}, nil
}

// Name implements agentapi.Tool.
func (t *toolAdapter) Name() string { return t.name }

// Description implements agentapi.Tool.
func (t *toolAdapter) Description() string { return t.description }

// InputSchema implements agentapi.Tool.
func (t *toolAdapter) InputSchema() json.RawMessage { return t.schema }

// Class implements agentapi.Tool.
func (t *toolAdapter) Class() agentapi.ActionClass { return t.class }

// Run implements agentapi.Tool. A server-level failure becomes an error
// result the model can read, except a canceled context, which aborts the
// turn the same way a built-in tool cancellation does.
func (t *toolAdapter) Run(ctx context.Context, callID string, args json.RawMessage) (agentapi.ToolResult, error) {
	res, err := t.server.call(ctx, t.remote, args, t.class)
	if err != nil {
		if ctx.Err() != nil {
			return agentapi.ToolResult{}, ctx.Err()
		}
		return agentapi.ErrorResult(callID, t.name, err.Error()), nil
	}
	return convertResult(callID, t.name, res), nil
}

// convertSchema marshals the SDK's decoded schema back to JSON. The SDK
// holds InputSchema as any, so numbers are already float64; a value at or
// above 2^53 would be re-encoded rounded, and the tool is rejected rather
// than sent with a wrong bound.
func convertSchema(name string, in any) (json.RawMessage, error) {
	if bad, found := unsafeNumber(in); found {
		return nil, fmt.Errorf("%w: %q uses the number %v", errUnsupportedNumber, name, bad)
	}
	if in == nil {
		return nil, fmt.Errorf("tool %q has no input schema", name)
	}
	b, err := json.Marshal(in)
	if err != nil {
		return nil, fmt.Errorf("encoding input schema: %w", err)
	}
	if !isJSONObject(b) {
		return nil, fmt.Errorf("input schema is not a JSON object")
	}
	return b, nil
}

func isJSONObject(raw []byte) bool {
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

// capDescription applies the optional per-server cap. The ellipsis is
// included in the budget, so the result is never longer than max.
func capDescription(desc string, max int) string {
	if max <= 0 || len(desc) <= max {
		return desc
	}
	if max <= len("…") {
		return text.Cut(desc, max)
	}
	return text.Cut(desc, max-len("…")) + "…"
}

// convertResult turns the protocol content into the parts the model sees.
// Images and audio are a Phase 3 capability; an explicit error is better
// than a silent empty result that looks like success.
func convertResult(callID, name string, res *mcp.CallToolResult) agentapi.ToolResult {
	if res == nil {
		return agentapi.ErrorResult(callID, name, "the MCP server returned no result")
	}
	parts := make([]agentapi.ContentPart, 0, len(res.Content)+1)
	for _, content := range res.Content {
		switch content := content.(type) {
		case *mcp.TextContent:
			parts = append(parts, agentapi.Text(content.Text))
		case *mcp.EmbeddedResource:
			parts = append(parts, agentapi.Text(embeddedText(content)))
		case *mcp.ResourceLink:
			parts = append(parts, agentapi.Text(resourceLinkText(content)))
		case *mcp.ImageContent:
			return agentapi.ErrorResult(callID, name,
				fmt.Sprintf("the MCP server returned an image (%s), which hint cannot show yet", content.MIMEType))
		case *mcp.AudioContent:
			return agentapi.ErrorResult(callID, name,
				fmt.Sprintf("the MCP server returned audio (%s), which hint cannot play yet", content.MIMEType))
		default:
			return agentapi.ErrorResult(callID, name,
				fmt.Sprintf("the MCP server returned unsupported content %T", content))
		}
	}
	if res.StructuredContent != nil {
		b, err := json.Marshal(res.StructuredContent)
		if err != nil {
			return agentapi.ErrorResult(callID, name, fmt.Sprintf("encoding structured result: %v", err))
		}
		// A response may carry both a text explanation and structured
		// data; the model needs both, not the explanation alone.
		parts = append(parts, agentapi.Text(string(b)))
	}
	if len(parts) == 0 {
		if res.IsError {
			parts = append(parts, agentapi.Text("the MCP server reported an error without a message"))
		} else {
			parts = append(parts, agentapi.Text(""))
		}
	}
	return agentapi.ToolResult{CallID: callID, Name: name, Content: parts, IsError: res.IsError}
}

func embeddedText(c *mcp.EmbeddedResource) string {
	if c == nil || c.Resource == nil {
		return "the MCP server returned an empty embedded resource"
	}
	r := c.Resource
	if r.Text != "" {
		return r.Text
	}
	mime := r.MIMEType
	if mime == "" {
		mime = "unknown type"
	}
	if len(r.Blob) > 0 {
		return fmt.Sprintf("embedded resource %s (%s, %d bytes)", r.URI, mime, len(r.Blob))
	}
	return fmt.Sprintf("embedded resource %s (%s, empty)", r.URI, mime)
}

func resourceLinkText(c *mcp.ResourceLink) string {
	if c == nil {
		return "the MCP server returned an empty resource link"
	}
	if c.Name != "" {
		return fmt.Sprintf("resource %s (%s)", c.URI, c.Name)
	}
	return "resource " + c.URI
}

// limitedTool wraps a tool with the per-server timeout and the built-in
// output cap. It is the same wrapper every built-in gets, so an MCP result
// is truncated by the same rule and a timeout is an error result the model
// can read.
func limitedTool(t agentapi.Tool, cfg ServerConfig) agentapi.Tool {
	limits := tool.DefaultLimits()
	if cfg.Timeout > 0 {
		limits.Timeout = cfg.Timeout
	}
	return tool.WithLimits(t, limits)
}
