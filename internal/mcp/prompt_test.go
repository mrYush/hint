package mcp

import (
	"context"
	"errors"
	"strings"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

func newPromptManager(t *testing.T) *Manager {
	t.Helper()
	server := sdk.NewServer(&sdk.Implementation{Name: "fake", Version: "v0"}, &sdk.ServerOptions{
		CompletionHandler: func(_ context.Context, req *sdk.CompleteRequest) (*sdk.CompleteResult, error) {
			if req.Params.Ref.Type != "ref/prompt" || req.Params.Ref.Name != "review" {
				return &sdk.CompleteResult{}, nil
			}
			switch req.Params.Argument.Name {
			case "path":
				return &sdk.CompleteResult{Completion: sdk.CompletionResultDetails{Values: []string{"main.go", "model.go"}, HasMore: true}}, nil
			case "focus":
				if req.Params.Context != nil && req.Params.Context.Arguments["path"] == "main.go" {
					return &sdk.CompleteResult{Completion: sdk.CompletionResultDetails{Values: []string{"tests"}}}, nil
				}
			}
			return &sdk.CompleteResult{}, nil
		},
	})
	server.AddPrompt(&sdk.Prompt{
		Name: "review", Description: "Review a file.",
		Arguments: []*sdk.PromptArgument{{Name: "path", Description: "File to review.", Required: true}, {Name: "focus", Description: "Optional focus."}},
	}, func(_ context.Context, req *sdk.GetPromptRequest) (*sdk.GetPromptResult, error) {
		path := req.Params.Arguments["path"]
		return &sdk.GetPromptResult{Messages: []*sdk.PromptMessage{
			{Role: sdk.Role("user"), Content: &sdk.TextContent{Text: "Review " + path}},
			{Role: sdk.Role("assistant"), Content: &sdk.EmbeddedResource{Resource: &sdk.ResourceContents{URI: "test://rules", MIMEType: "text/plain", Text: "Follow the review rules."}}},
		}}, nil
	})
	m, err := NewManager([]ServerConfig{{Name: "fake", Transport: TransportStdio, Command: "unused"}},
		Options{Dial: newMemoryDial(server).dial})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.Close() })
	return m
}

func TestManagerPromptsExpandAndComplete(t *testing.T) {
	m := newPromptManager(t)
	ctx := context.Background()
	prompts, err := m.Prompts(ctx)
	if err != nil {
		t.Fatalf("Prompts: %v", err)
	}
	if len(prompts) != 1 || prompts[0].Server != "fake" || prompts[0].Name != "review" {
		t.Fatalf("prompts = %+v", prompts)
	}
	if len(prompts[0].Arguments) != 2 || !prompts[0].Arguments[0].Required || prompts[0].Arguments[1].Required {
		t.Errorf("arguments = %+v", prompts[0].Arguments)
	}

	text, err := m.ExpandPrompt(ctx, "fake", "review", []string{"main.go"})
	if err != nil {
		t.Fatalf("ExpandPrompt: %v", err)
	}
	if text != "Review main.go\n\nFollow the review rules." {
		t.Errorf("expansion = %q", text)
	}
	if _, err := m.ExpandPrompt(ctx, "fake", "review", nil); err == nil || !strings.Contains(err.Error(), "required") {
		t.Errorf("missing required argument: %v", err)
	}
	if _, err := m.ExpandPrompt(ctx, "fake", "review", []string{"a", "b", "c"}); err == nil || !strings.Contains(err.Error(), "too many") {
		t.Errorf("extra argument: %v", err)
	}

	values, hasMore, err := m.CompletePrompt(ctx, "fake", "review", "path", "ma", nil)
	if err != nil {
		t.Fatalf("CompletePrompt: %v", err)
	}
	if err != nil || !hasMore || len(values) != 2 || values[0] != "main.go" || values[1] != "model.go" {
		t.Errorf("completion = %v hasMore=%v err=%v", values, hasMore, err)
	}
	values, _, err = m.CompletePrompt(ctx, "fake", "review", "focus", "", map[string]string{"path": "main.go"})
	if err != nil || len(values) != 1 || values[0] != "tests" {
		t.Errorf("completion with context = %v, %v", values, err)
	}
	if _, err := m.ExpandPrompt(ctx, "fake", "missing", nil); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown prompt error = %v", err)
	}
	if _, _, err := m.CompletePrompt(ctx, "missing", "review", "path", "", nil); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown server error = %v", err)
	}
}

func TestExpandPromptRejectsOversizedText(t *testing.T) {
	server := sdk.NewServer(&sdk.Implementation{Name: "fake", Version: "v0"}, nil)
	server.AddPrompt(&sdk.Prompt{Name: "huge"}, func(context.Context, *sdk.GetPromptRequest) (*sdk.GetPromptResult, error) {
		return &sdk.GetPromptResult{Messages: []*sdk.PromptMessage{{
			Role: sdk.Role("user"), Content: &sdk.TextContent{Text: strings.Repeat("x", MaxPromptText+1)},
		}}}, nil
	})
	m, err := NewManager([]ServerConfig{{Name: "fake", Transport: TransportStdio, Command: "unused"}}, Options{Dial: newMemoryDial(server).dial})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.Close() })
	if _, err := m.ExpandPrompt(context.Background(), "fake", "huge", nil); err == nil || !strings.Contains(err.Error(), "byte limit") {
		t.Errorf("ExpandPrompt = %v", err)
	}
}

func TestManagerServerWithoutPromptsIsEmpty(t *testing.T) {
	m, _ := testManager(t, nil, ServerConfig{}, Options{})
	prompts, err := m.Prompts(context.Background())
	if err != nil || len(prompts) != 0 {
		t.Errorf("prompts = %+v, %v", prompts, err)
	}
}

func TestCompletePromptWithoutCompletionCapability(t *testing.T) {
	server := sdk.NewServer(&sdk.Implementation{Name: "fake", Version: "v0"}, nil)
	server.AddPrompt(&sdk.Prompt{Name: "review"}, func(context.Context, *sdk.GetPromptRequest) (*sdk.GetPromptResult, error) {
		return &sdk.GetPromptResult{}, nil
	})
	m, err := NewManager([]ServerConfig{{Name: "fake", Transport: TransportStdio, Command: "unused"}}, Options{Dial: newMemoryDial(server).dial})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.Close() })
	values, _, err := m.CompletePrompt(context.Background(), "fake", "review", "path", "m", nil)
	if err != nil || len(values) != 0 {
		t.Errorf("CompletePrompt = %v, %v", values, err)
	}
}

func TestPromptsSkipNamesThatCannotBeCommands(t *testing.T) {
	server := sdk.NewServer(&sdk.Implementation{Name: "fake", Version: "v0"}, nil)
	handler := func(context.Context, *sdk.GetPromptRequest) (*sdk.GetPromptResult, error) {
		return &sdk.GetPromptResult{}, nil
	}
	for _, name := range []string{"bad:name", "bad name", "good"} {
		server.AddPrompt(&sdk.Prompt{Name: name}, handler)
	}
	m, err := NewManager([]ServerConfig{{Name: "fake", Transport: TransportStdio, Command: "unused"}}, Options{Dial: newMemoryDial(server).dial})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.Close() })
	prompts, err := m.Prompts(context.Background())
	if err != nil || len(prompts) != 1 || prompts[0].Name != "good" {
		t.Errorf("Prompts = %+v, %v", prompts, err)
	}
	if !hasWarning(m, "cannot be used") {
		t.Errorf("warnings = %v", m.Warnings())
	}
}
