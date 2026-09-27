package permission_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/mrYush/hint/internal/permission"
	"github.com/mrYush/hint/internal/tool"
	"github.com/mrYush/hint/pkg/agentapi"
)

// fakeTool has a fixed class and a fixed preview, and counts how often
// the preview was built, so a test can assert that no diff is computed
// for a call nobody will look at.
type fakeTool struct {
	class     agentapi.ActionClass
	desc      agentapi.PermissionRequest
	described *int
}

func (t fakeTool) Name() string {
	if t.desc.Tool != "" {
		return t.desc.Tool
	}
	return "fake"
}
func (fakeTool) Description() string           { return "fake" }
func (fakeTool) InputSchema() json.RawMessage  { return json.RawMessage(`{"type":"object"}`) }
func (t fakeTool) Class() agentapi.ActionClass { return t.class }
func (t fakeTool) Run(_ context.Context, id string, _ json.RawMessage) (agentapi.ToolResult, error) {
	return agentapi.TextResult(id, t.Name(), "ok"), nil
}
func (t fakeTool) Describe(context.Context, json.RawMessage) (tool.Description, error) {
	if t.described != nil {
		*t.described++
	}
	return tool.Description{Summary: t.desc.Summary, Detail: t.desc.Detail, Path: t.desc.Path}, nil
}

func reqCall(req agentapi.PermissionRequest) agentapi.ToolCall {
	return agentapi.ToolCall{ID: "c", Name: req.Tool, Arguments: json.RawMessage(`{}`)}
}

func TestGate_ReviewSkipsPreviewWhenModeIsSilent(t *testing.T) {
	n := 0
	tl := fakeTool{class: agentapi.ClassWrite, desc: writeReq("write_file", "a"), described: &n}
	g := permission.New(permission.ModeAutoEdit, nil)

	req, ask := g.Review(context.Background(), reqCall(tl.desc), tl)
	if ask {
		t.Fatal("auto-edit must not ask about a write")
	}
	if n != 0 {
		t.Fatalf("preview built %d times for a silent call", n)
	}
	if err := req.Validate(); err != nil {
		t.Fatalf("even the minimal request must validate: %v", err)
	}
}

func TestGate_ReviewBuildsPreviewWhenAsking(t *testing.T) {
	n := 0
	tl := fakeTool{class: agentapi.ClassExecute, desc: execReq("go vet"), described: &n}
	g := permission.New(permission.ModeAutoEdit, nil)

	req, ask := g.Review(context.Background(), reqCall(tl.desc), tl)
	if !ask || n != 1 {
		t.Fatalf("ask = %v, previews = %d; want ask with one preview", ask, n)
	}
	if req.Detail != "go vet" || req.Summary != "run go vet" {
		t.Fatalf("request = %+v", req)
	}
}

func TestGate_ReadNeverAsksInAnyMode(t *testing.T) {
	for _, m := range []permission.Mode{permission.ModeAsk, permission.ModeAutoEdit, permission.ModeYolo} {
		tl := fakeTool{class: agentapi.ClassRead, desc: agentapi.PermissionRequest{Tool: "read_file", Summary: "read"}}
		if _, ask := permission.New(m, nil).Review(context.Background(), reqCall(tl.desc), tl); ask {
			t.Errorf("%s asks about a read", m)
		}
	}
}

func TestGate_NilPrompterDenies(t *testing.T) {
	g := permission.New(permission.ModeAsk, nil)
	allowed, err := g.Authorize(context.Background(), execReq("rm -rf /"))
	if err != nil || allowed {
		t.Fatalf("Authorize with no prompter = %v, %v; want denied, no error", allowed, err)
	}
	if g.Mode() != permission.ModeAsk {
		t.Fatal("mode not kept")
	}
}

type errPrompter struct{ err error }

func (p errPrompter) Prompt(ctx context.Context, _ permission.Ask) (permission.Decision, error) {
	if p.err != nil {
		return permission.Deny, p.err
	}
	<-ctx.Done()
	return permission.Deny, ctx.Err()
}

func TestGate_PrompterErrorWithLiveContextDenies(t *testing.T) {
	g := permission.New(permission.ModeAsk, errPrompter{err: errors.New("tty gone")})
	allowed, err := g.Authorize(context.Background(), execReq("ls"))
	if err != nil || allowed {
		t.Fatalf("= %v, %v; a broken prompter is a denial, not an abort", allowed, err)
	}
}

func TestGate_CanceledPromptReturnsContextError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	g := permission.New(permission.ModeAsk, errPrompter{})
	allowed, err := g.Authorize(ctx, execReq("ls"))
	if allowed || !errors.Is(err, context.Canceled) {
		t.Fatalf("= %v, %v; want context.Canceled", allowed, err)
	}
}

func TestGate_AllowIsOneOff(t *testing.T) {
	p := &recordingPrompter{t: t, answers: []permission.Decision{permission.Allow}}
	g := permission.New(permission.ModeAsk, p)
	if allowed, _ := g.Authorize(context.Background(), execReq("go test")); !allowed {
		t.Fatal("allow refused")
	}
	tl := fakeTool{class: agentapi.ClassExecute, desc: execReq("go test")}
	if _, ask := g.Review(context.Background(), reqCall(tl.desc), tl); !ask {
		t.Fatal("a plain allow must not be remembered")
	}
}

func TestGate_DenyDoesNotRemember(t *testing.T) {
	p := &recordingPrompter{t: t, answers: []permission.Decision{permission.Deny, permission.Allow}}
	g := permission.New(permission.ModeAsk, p)
	if allowed, _ := g.Authorize(context.Background(), execReq("go test")); allowed {
		t.Fatal("deny allowed")
	}
	if allowed, _ := g.Authorize(context.Background(), execReq("go test")); !allowed {
		t.Fatal("a denial must not block a later, separately approved call")
	}
}
