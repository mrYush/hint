package mcp

import (
	"context"
	"strings"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

func newPromptManager(t *testing.T) *Manager {
	t.Helper()
	server := sdk.NewServer(&sdk.Implementation{Name: "fake", Version: "v0"}, &sdk.ServerOptions{
		CompletionHandler: func(_ context.Context, req *sdk.CompleteRequest) (*sdk.CompleteResult, error) {
			if req.Params.Ref.Type != "ref/prompt" || req.Params.Ref.Name != "review" || req.Params.Argument.Name != "path" {
				return &sdk.CompleteResult{}, nil
			}
			return &sdk.CompleteResult{Completion: sdk.CompletionResultDetails{Values: []string{"main.go", "model.go"}}}, nil
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
	if len(prompts) != 1 || prompts[0].Command() != "/fake:review" {
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

	values, err := m.CompletePrompt(ctx, "fake", "review", "path", "ma")
	if err != nil {
		t.Fatalf("CompletePrompt: %v", err)
	}
	if len(values) != 2 || values[0] != "main.go" || values[1] != "model.go" {
		t.Errorf("completion = %v", values)
	}
}

func TestManagerServerWithoutPromptsIsEmpty(t *testing.T) {
	m, _ := testManager(t, nil, ServerConfig{}, Options{})
	prompts, err := m.Prompts(context.Background())
	if err != nil || len(prompts) != 0 {
		t.Errorf("prompts = %+v, %v", prompts, err)
	}
}
