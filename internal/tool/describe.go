package tool

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/mrYush/hint/pkg/agentapi"
)

// Describer is implemented by a tool that can preview its action without
// performing it: the diff a write would produce, the exact command a shell
// call would run. The permission layer (WP0.6) shows the preview to the
// user before letting the tool run.
//
// It is optional — a tool that does not implement it is described by its
// name and raw arguments — so that an MCP-provided tool (Phase 2) works
// behind the same prompt without a preview of its own.
type Describer interface {
	// Describe previews the action args would perform. It must not change
	// anything. An error means the preview could not be built (the file is
	// unreadable, the arguments are malformed); the action is still gated,
	// only with the raw arguments as evidence.
	Describe(ctx context.Context, args json.RawMessage) (Description, error)
}

// Description is a Describer's preview of one call.
type Description struct {
	// Summary is a one-line account of the action, e.g. `edit main.go`.
	Summary string
	// Detail is the evidence: a unified diff for a write, the exact command
	// for an execute. For an execute-class tool it must be the command
	// alone, because the permission layer derives its allow-list key from
	// it.
	Detail string
	// Path is the root-relative file the action targets, if any.
	Path string
}

// Unwrap returns the tool t decorates, or nil when t is not a decorator.
// It follows the same convention as errors.Unwrap: a decorator such as
// [WithLimits] exposes its inner tool through an Unwrap method.
func Unwrap(t agentapi.Tool) agentapi.Tool {
	u, ok := t.(interface{ Unwrap() agentapi.Tool })
	if !ok {
		return nil
	}
	return u.Unwrap()
}

// Describe builds the [agentapi.PermissionRequest] for call, asking the
// tool for a preview when it — or any tool it decorates — is a
// [Describer]. It never fails: without a usable preview the request
// carries the tool name and the raw arguments, which is still enough for
// a user to decide.
func Describe(ctx context.Context, call agentapi.ToolCall, t agentapi.Tool) agentapi.PermissionRequest {
	req := agentapi.PermissionRequest{
		CallID: call.ID,
		Tool:   call.Name,
		Class:  t.Class(),
	}

	var previewErr error
	for x := t; x != nil; x = Unwrap(x) {
		d, ok := x.(Describer)
		if !ok {
			continue
		}
		desc, err := d.Describe(ctx, call.Arguments)
		if err != nil {
			previewErr = err
			break
		}
		req.Summary, req.Detail, req.Path = desc.Summary, desc.Detail, desc.Path
		break
	}

	if req.Summary == "" {
		req.Summary = strings.TrimSpace(call.Name + " " + compactArgs(call.Arguments, 80))
		req.Detail = indentArgs(call.Arguments)
	}
	if previewErr != nil {
		req.Detail = strings.TrimRight(req.Detail, "\n") + fmt.Sprintf("\n(preview unavailable: %v)", previewErr)
	}
	return req
}

// compactArgs renders raw JSON arguments on one line, cut at max bytes.
func compactArgs(raw json.RawMessage, max int) string {
	var buf bytes.Buffer
	if err := json.Compact(&buf, raw); err != nil {
		buf.Reset()
		buf.Write(raw)
	}
	s := strings.Join(strings.Fields(buf.String()), " ")
	if len(s) > max {
		s = s[:max] + "..."
	}
	return s
}

// indentArgs pretty-prints raw JSON arguments, or returns them as-is when
// they do not parse.
func indentArgs(raw json.RawMessage) string {
	var buf bytes.Buffer
	if err := json.Indent(&buf, raw, "", "  "); err != nil {
		return string(raw)
	}
	return buf.String()
}
