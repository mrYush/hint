package tool_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mrYush/hint/internal/tool"
	"github.com/mrYush/hint/pkg/agentapi"
)

// stubTool is the smallest agentapi.Tool: it returns whatever run says.
type stubTool struct {
	name string
	run  func(ctx context.Context, callID string) (agentapi.ToolResult, error)
}

func (s stubTool) Name() string                 { return s.name }
func (s stubTool) Description() string          { return "stub" }
func (s stubTool) InputSchema() json.RawMessage { return json.RawMessage(`{"type":"object"}`) }
func (s stubTool) Class() agentapi.ActionClass  { return agentapi.ClassRead }
func (s stubTool) Run(ctx context.Context, callID string, _ json.RawMessage) (agentapi.ToolResult, error) {
	return s.run(ctx, callID)
}

func TestWithLimits_PassesDescriptorsThrough(t *testing.T) {
	inner := stubTool{name: "stub"}
	wrapped := tool.WithLimits(inner, tool.DefaultLimits())
	if wrapped.Name() != "stub" || wrapped.Description() != "stub" || wrapped.Class() != agentapi.ClassRead {
		t.Fatalf("descriptors changed by the decorator")
	}
	u, ok := wrapped.(interface{ Unwrap() agentapi.Tool })
	if !ok || u.Unwrap().Name() != "stub" {
		t.Fatalf("Unwrap does not return the inner tool")
	}
}

func TestWithLimits_TimeoutBecomesErrorResult(t *testing.T) {
	slow := stubTool{name: "slow", run: func(ctx context.Context, callID string) (agentapi.ToolResult, error) {
		<-ctx.Done()
		return agentapi.ToolResult{}, ctx.Err()
	}}
	wrapped := tool.WithLimits(slow, tool.Limits{Timeout: 20 * time.Millisecond})

	res, err := wrapped.Run(context.Background(), "c1", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("a timeout must be a result, not a turn-aborting error: %v", err)
	}
	if !res.IsError || !strings.Contains(res.Text(), "timed out after 20ms") {
		t.Fatalf("unexpected result: %+v", res)
	}
	if res.CallID != "c1" || res.Name != "slow" {
		t.Fatalf("result not attributed: %+v", res)
	}
}

func TestWithLimits_ParentCancelIsNotRelabelled(t *testing.T) {
	slow := stubTool{name: "slow", run: func(ctx context.Context, callID string) (agentapi.ToolResult, error) {
		<-ctx.Done()
		return agentapi.ToolResult{}, ctx.Err()
	}}
	wrapped := tool.WithLimits(slow, tool.Limits{Timeout: time.Minute})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := wrapped.Run(ctx, "c1", json.RawMessage(`{}`))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want the caller's cancellation passed through", err)
	}
}

func TestWithLimits_TruncatesTextParts(t *testing.T) {
	big := strings.Repeat("line of output\n", 1000)
	chatty := stubTool{name: "chatty", run: func(ctx context.Context, callID string) (agentapi.ToolResult, error) {
		return agentapi.TextResult(callID, "chatty", big), nil
	}}
	wrapped := tool.WithLimits(chatty, tool.Limits{MaxOutput: 2000})

	res, err := wrapped.Run(context.Background(), "c1", json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Text()) > 2000 || !strings.Contains(res.Text(), "output truncated") {
		t.Fatalf("output not truncated: %d bytes", len(res.Text()))
	}
}

func TestWithLimits_MachineryErrorPassesThrough(t *testing.T) {
	boom := errors.New("machinery broke")
	broken := stubTool{name: "broken", run: func(ctx context.Context, callID string) (agentapi.ToolResult, error) {
		return agentapi.ToolResult{}, boom
	}}
	wrapped := tool.WithLimits(broken, tool.DefaultLimits())
	if _, err := wrapped.Run(context.Background(), "c1", json.RawMessage(`{}`)); !errors.Is(err, boom) {
		t.Fatalf("err = %v, want %v", err, boom)
	}
}
