package openai

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/mrYush/hint/pkg/agentapi"
)

func TestConvertRequestRoundTrip(t *testing.T) {
	temp := 0.2
	req := agentapi.ChatRequest{
		Messages: []agentapi.Message{
			agentapi.SystemMessage("be terse"),
			agentapi.UserMessage("list files"),
			{
				Role:    agentapi.RoleAssistant,
				Content: []agentapi.ContentPart{agentapi.Thinking("hmm"), agentapi.Text("looking")},
				ToolCalls: []agentapi.ToolCall{
					{ID: "call_1", Name: "list_dir", Arguments: json.RawMessage(`{"path":"."}`)},
				},
			},
			agentapi.TextResult("call_1", "list_dir", "main.go").Message(),
		},
		Tools: []agentapi.ToolSchema{
			{Name: "list_dir", Description: "List a directory", InputSchema: json.RawMessage(`{"type":"object"}`)},
		},
		Temperature: &temp,
		MaxTokens:   512,
	}

	wire, err := convertRequest("m1", req)
	if err != nil {
		t.Fatalf("convertRequest: %v", err)
	}
	if wire.Model != "m1" || !wire.Stream || wire.StreamOptions == nil || !wire.StreamOptions.IncludeUsage {
		t.Errorf("envelope = %+v", wire)
	}
	if wire.Temperature == nil || *wire.Temperature != 0.2 || wire.MaxTokens != 512 {
		t.Errorf("sampling params lost: temp=%v max=%d", wire.Temperature, wire.MaxTokens)
	}
	if len(wire.Messages) != 4 {
		t.Fatalf("got %d messages", len(wire.Messages))
	}

	sys := wire.Messages[0]
	if sys.Role != "system" || sys.Content == nil || *sys.Content != "be terse" {
		t.Errorf("system = %+v", sys)
	}
	asst := wire.Messages[2]
	if asst.Content == nil || *asst.Content != "looking" {
		t.Errorf("assistant content = %v, want thinking dropped and text kept", asst.Content)
	}
	if len(asst.ToolCalls) != 1 || asst.ToolCalls[0].Function.Arguments != `{"path":"."}` {
		t.Errorf("assistant tool calls = %+v", asst.ToolCalls)
	}
	tool := wire.Messages[3]
	if tool.Role != "tool" || tool.ToolCallID != "call_1" || tool.Content == nil || *tool.Content != "main.go" {
		t.Errorf("tool message = %+v", tool)
	}
	if len(wire.Tools) != 1 || wire.Tools[0].Type != "function" || wire.Tools[0].Function.Name != "list_dir" {
		t.Errorf("tools = %+v", wire.Tools)
	}
}

func TestConvertEmptyToolResultStillSendable(t *testing.T) {
	// WP0.1 silent-tool rule: a tool that succeeds silently produces an
	// empty message, and the wire form must carry content anyway.
	msg := agentapi.ToolResult{CallID: "call_9", Name: "write_file"}.Message()
	req := agentapi.ChatRequest{Messages: []agentapi.Message{
		agentapi.UserMessage("x"),
		{
			Role:      agentapi.RoleAssistant,
			ToolCalls: []agentapi.ToolCall{{ID: "call_9", Name: "write_file", Arguments: json.RawMessage(`{}`)}},
		},
		msg,
	}}
	wire, err := convertRequest("m", req)
	if err != nil {
		t.Fatalf("convertRequest: %v", err)
	}
	tool := wire.Messages[2]
	if tool.Content == nil {
		t.Fatal("empty tool result serialised with null content")
	}
	if *tool.Content != "" {
		t.Errorf("content = %q, want empty string", *tool.Content)
	}

	asst := wire.Messages[1]
	if asst.Content != nil {
		t.Errorf("tool-only assistant message content = %q, want omitted", *asst.Content)
	}

	raw, err := json.Marshal(wire)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(raw), `"content":""`) {
		t.Errorf("wire JSON lost the empty tool content: %s", raw)
	}
}

func TestConvertRejectsMediaParts(t *testing.T) {
	req := agentapi.ChatRequest{Messages: []agentapi.Message{
		{
			Role:    agentapi.RoleUser,
			Content: []agentapi.ContentPart{agentapi.ImagePart(agentapi.Image{URL: "https://x/y.png"})},
		},
	}}
	_, err := convertRequest("m", req)
	if err == nil {
		t.Fatal("image content silently accepted")
	}
	if kind := agentapi.KindOf(err); kind != agentapi.ErrInvalidRequest {
		t.Errorf("kind = %s, want invalid_request", kind)
	}
}
