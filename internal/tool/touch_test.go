package tool_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/mrYush/hint/internal/tool"
	"github.com/mrYush/hint/pkg/agentapi"
)

// touching is a tool that names one path per call; mute is one that
// does not implement Toucher at all.
type touching struct{ path string }

func (touching) Name() string                 { return "touching" }
func (touching) Description() string          { return "" }
func (touching) InputSchema() json.RawMessage { return json.RawMessage(`{"type":"object"}`) }
func (touching) Class() agentapi.ActionClass  { return agentapi.ClassRead }
func (t touching) Run(_ context.Context, id string, _ json.RawMessage) (agentapi.ToolResult, error) {
	return agentapi.TextResult(id, "touching", "ok"), nil
}
func (t touching) Touches(json.RawMessage) []string { return []string{t.path} }

type mute struct{ touching }

func (mute) Touches(json.RawMessage) []string { return nil }

func TestTouches(t *testing.T) {
	call := agentapi.ToolCall{ID: "c1", Name: "touching", Arguments: json.RawMessage(`{}`)}
	// Through a decorator, the inner tool answers.
	wrapped := tool.WithLimits(touching{path: "/p/a.go"}, tool.DefaultLimits())
	if got := tool.Touches(call, wrapped); len(got) != 1 || got[0] != "/p/a.go" {
		t.Errorf("Touches through WithLimits = %v", got)
	}
	// A tool without the method touches nothing.
	plain := tool.WithLimits(echoOnly{}, tool.DefaultLimits())
	if got := tool.Touches(call, plain); got != nil {
		t.Errorf("Touches of a non-Toucher = %v, want nil", got)
	}
	if got := tool.Touches(call, mute{}); got != nil {
		t.Errorf("Touches of a mute Toucher = %v, want nil", got)
	}
}

type echoOnly struct{}

func (echoOnly) Name() string                 { return "echo" }
func (echoOnly) Description() string          { return "" }
func (echoOnly) InputSchema() json.RawMessage { return json.RawMessage(`{"type":"object"}`) }
func (echoOnly) Class() agentapi.ActionClass  { return agentapi.ClassRead }
func (echoOnly) Run(_ context.Context, id string, _ json.RawMessage) (agentapi.ToolResult, error) {
	return agentapi.TextResult(id, "echo", "ok"), nil
}
