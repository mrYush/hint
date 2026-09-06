package tool_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/mrYush/hint/internal/tool"
	"github.com/mrYush/hint/pkg/agentapi"
)

// previewTool is an execute-class tool with a Describer, optionally
// failing its preview.
type previewTool struct {
	fail bool
}

func (previewTool) Name() string                 { return "sh" }
func (previewTool) Description() string          { return "Runs things." }
func (previewTool) InputSchema() json.RawMessage { return json.RawMessage(`{"type":"object"}`) }
func (previewTool) Class() agentapi.ActionClass  { return agentapi.ClassExecute }
func (previewTool) Run(_ context.Context, id string, _ json.RawMessage) (agentapi.ToolResult, error) {
	return agentapi.TextResult(id, "sh", "ran"), nil
}
func (t previewTool) Describe(_ context.Context, args json.RawMessage) (tool.Description, error) {
	if t.fail {
		return tool.Description{}, errors.New("cannot preview")
	}
	var in struct {
		Command string `json:"command"`
	}
	_ = json.Unmarshal(args, &in)
	return tool.Description{Summary: "run " + in.Command, Detail: in.Command}, nil
}

// plainTool has no Describer.
type plainTool struct{}

func (plainTool) Name() string                 { return "plain" }
func (plainTool) Description() string          { return "No preview." }
func (plainTool) InputSchema() json.RawMessage { return json.RawMessage(`{"type":"object"}`) }
func (plainTool) Class() agentapi.ActionClass  { return agentapi.ClassWrite }
func (plainTool) Run(_ context.Context, id string, _ json.RawMessage) (agentapi.ToolResult, error) {
	return agentapi.TextResult(id, "plain", "ok"), nil
}

func call(name, args string) agentapi.ToolCall {
	return agentapi.ToolCall{ID: "c1", Name: name, Arguments: json.RawMessage(args)}
}

func TestDescribe_UsesDescriberThroughDecorator(t *testing.T) {
	// The limits decorator hides the concrete tool; Describe must find the
	// Describer through Unwrap, or every built-in would lose its preview.
	tl := tool.WithLimits(previewTool{}, tool.DefaultLimits())
	if _, ok := tl.(tool.Describer); ok {
		t.Fatal("test premise broken: the decorator itself is a Describer")
	}
	req := tool.Describe(context.Background(), call("sh", `{"command":"go test ./..."}`), tl)
	if err := req.Validate(); err != nil {
		t.Fatal(err)
	}
	if req.Summary != "run go test ./..." || req.Detail != "go test ./..." {
		t.Fatalf("request = %+v", req)
	}
	if req.CallID != "c1" || req.Tool != "sh" || req.Class != agentapi.ClassExecute {
		t.Fatalf("identity fields wrong: %+v", req)
	}
}

func TestDescribe_FallsBackToRawArgs(t *testing.T) {
	req := tool.Describe(context.Background(), call("plain", `{ "path": "a.txt",   "n": 1 }`), plainTool{})
	if err := req.Validate(); err != nil {
		t.Fatal(err)
	}
	if req.Summary != `plain {"path":"a.txt","n":1}` {
		t.Fatalf("summary = %q", req.Summary)
	}
	if !strings.Contains(req.Detail, "\n  \"path\": \"a.txt\"") {
		t.Fatalf("detail should be indented JSON, got %q", req.Detail)
	}
	if req.Class != agentapi.ClassWrite {
		t.Fatalf("class = %s", req.Class)
	}
}

func TestDescribe_PreviewErrorStillGates(t *testing.T) {
	req := tool.Describe(context.Background(), call("sh", `{"command":"x"}`), previewTool{fail: true})
	if err := req.Validate(); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(req.Summary, "sh ") {
		t.Fatalf("summary = %q, want the raw fallback", req.Summary)
	}
	if !strings.Contains(req.Detail, "preview unavailable: cannot preview") {
		t.Fatalf("detail = %q", req.Detail)
	}
}

func TestDescribe_LongArgsAreCut(t *testing.T) {
	long := strings.Repeat("x", 200)
	req := tool.Describe(context.Background(), call("plain", `{"text":"`+long+`"}`), plainTool{})
	if len(req.Summary) > 100 || !strings.HasSuffix(req.Summary, "...") {
		t.Fatalf("summary not cut: %d bytes", len(req.Summary))
	}
}

func TestUnwrap(t *testing.T) {
	inner := plainTool{}
	if tool.Unwrap(inner) != nil {
		t.Fatal("a plain tool unwraps to nil")
	}
	if got := tool.Unwrap(tool.WithLimits(inner, tool.DefaultLimits())); got != inner {
		t.Fatalf("Unwrap(decorated) = %v, want the inner tool", got)
	}
}
