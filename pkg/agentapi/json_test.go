package agentapi_test

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/mrYush/hint/pkg/agentapi"
)

// roundTrip marshals want, unmarshals the result into a fresh value of the
// same type, and reports whether the two are equal.
//
// It is generic so that one helper covers every wire type without reflection
// tricks in the test bodies: T is inferred from the argument.
func roundTrip[T any](t *testing.T, want T) {
	t.Helper()

	data, err := json.Marshal(want)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var got T
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal %s: %v", data, err)
	}

	if !reflect.DeepEqual(want, got) {
		t.Errorf("round trip changed the value\n json: %s\n want: %#v\n  got: %#v", data, want, got)
	}
}

func TestContentPartRoundTrip(t *testing.T) {
	cases := map[string]agentapi.ContentPart{
		"text":         agentapi.Text("hello"),
		"empty text":   agentapi.Text(""),
		"thinking":     agentapi.Thinking("let me check the file first"),
		"image url":    agentapi.ImagePart(agentapi.Image{URL: "https://example.com/a.png", Detail: "high"}),
		"image inline": agentapi.ImagePart(agentapi.Image{Data: []byte{0x89, 'P', 'N', 'G'}, MediaType: "image/png"}),
		"audio url":    agentapi.AudioPart(agentapi.Audio{URL: "file:///tmp/a.wav"}),
		"audio inline": agentapi.AudioPart(agentapi.Audio{Data: []byte{'R', 'I', 'F', 'F'}, MediaType: "audio/wav"}),
	}
	for name, part := range cases {
		t.Run(name, func(t *testing.T) { roundTrip(t, part) })
	}
}

func TestMessageRoundTrip(t *testing.T) {
	cases := map[string]agentapi.Message{
		"system":         agentapi.SystemMessage("you are a helpful assistant"),
		"user":           agentapi.UserMessage("what does main.go do?"),
		"assistant text": agentapi.AssistantMessage("It wires up the CLI."),
		"assistant with thinking": {
			Role: agentapi.RoleAssistant,
			Content: []agentapi.ContentPart{
				agentapi.Thinking("the user wants a summary"),
				agentapi.Text("It wires up the CLI."),
			},
		},
		"assistant tool calls only": {
			Role: agentapi.RoleAssistant,
			ToolCalls: []agentapi.ToolCall{
				{ID: "call_1", Name: "read_file", Arguments: json.RawMessage(`{"path":"main.go"}`)},
				{ID: "call_2", Name: "list_dir", Arguments: json.RawMessage(`{"path":"."}`)},
			},
		},
		"tool result": {
			Role:       agentapi.RoleTool,
			Content:    []agentapi.ContentPart{agentapi.Text("package main")},
			ToolCallID: "call_1",
		},
		"multimodal user": {
			Role: agentapi.RoleUser,
			Content: []agentapi.ContentPart{
				agentapi.Text("what is on this screen?"),
				agentapi.ImagePart(agentapi.Image{Data: []byte{1, 2, 3}, MediaType: "image/jpeg"}),
			},
		},
	}
	for name, msg := range cases {
		t.Run(name, func(t *testing.T) { roundTrip(t, msg) })
	}
}

func TestToolTypesRoundTrip(t *testing.T) {
	t.Run("schema", func(t *testing.T) {
		roundTrip(t, agentapi.ToolSchema{
			Name:        "read_file",
			Description: "Read a file from the working directory.",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}},"required":["path"]}`),
		})
	})
	t.Run("call", func(t *testing.T) {
		roundTrip(t, agentapi.ToolCall{
			ID:        "call_1",
			Name:      "bash",
			Arguments: json.RawMessage(`{"command":"go test ./...","timeout":120}`),
		})
	})
	t.Run("result", func(t *testing.T) {
		roundTrip(t, agentapi.TextResult("call_1", "bash", "ok\n"))
	})
	t.Run("error result", func(t *testing.T) {
		roundTrip(t, agentapi.ErrorResult("call_1", "bash", "exit status 1"))
	})
}

func TestChatEventRoundTrip(t *testing.T) {
	usage := agentapi.Usage{InputTokens: 1200, OutputTokens: 64, CachedInputTokens: 1024}
	cases := map[string]agentapi.ChatEvent{
		"text delta":     {Kind: agentapi.ChatTextDelta, Text: "Hel"},
		"thinking delta": {Kind: agentapi.ChatThinkingDelta, Text: "hmm"},
		"tool call": {Kind: agentapi.ChatToolCall, Call: &agentapi.ToolCall{
			ID: "call_1", Name: "glob", Arguments: json.RawMessage(`{"pattern":"**/*.go"}`),
		}},
		"usage": {Kind: agentapi.ChatUsage, Usage: &usage},
		"done":  {Kind: agentapi.ChatDone, FinishReason: agentapi.FinishToolCalls},
		"error": {Kind: agentapi.ChatError, Err: &agentapi.Error{
			Kind: agentapi.ErrRateLimited, Message: "slow down", Provider: "api-bar",
			StatusCode: 429, RetryAfterSeconds: 20,
		}},
	}
	for name, ev := range cases {
		t.Run(name, func(t *testing.T) { roundTrip(t, ev) })
	}
}

func TestEventRoundTrip(t *testing.T) {
	msg := agentapi.AssistantMessage("done")
	call := agentapi.ToolCall{ID: "call_1", Name: "edit_file", Arguments: json.RawMessage(`{"path":"main.go"}`)}
	result := agentapi.TextResult("call_1", "edit_file", "1 replacement")
	usage := agentapi.Usage{InputTokens: 10, OutputTokens: 2}

	cases := map[string]agentapi.Event{
		"turn start":     {Kind: agentapi.EventTurnStart},
		"text delta":     {Kind: agentapi.EventTextDelta, Text: "Hel"},
		"thinking delta": {Kind: agentapi.EventThinkingDelta, Text: "hmm"},
		"message":        {Kind: agentapi.EventMessage, Message: &msg},
		"permission": {Kind: agentapi.EventPermission, Permission: &agentapi.PermissionRequest{
			CallID: "call_1", Tool: "edit_file", Class: agentapi.ClassWrite,
			Summary: "edit main.go", Detail: "@@ -1 +1 @@\n-a\n+b", Path: "main.go",
		}},
		"tool start": {Kind: agentapi.EventToolStart, Call: &call},
		"tool end":   {Kind: agentapi.EventToolEnd, Result: &result},
		"compaction": {Kind: agentapi.EventCompaction, Compaction: &agentapi.Compaction{
			MessagesReplaced: 12, TokensBefore: 100_000, TokensAfter: 8_000, Summary: "earlier: refactored config",
			History: []agentapi.Message{
				agentapi.SystemMessage("you are hint"),
				agentapi.SystemMessage("earlier: refactored config"),
				agentapi.UserMessage("now add tests"),
			},
		}},
		"usage":    {Kind: agentapi.EventUsage, Usage: &usage},
		"turn end": {Kind: agentapi.EventTurnEnd, FinishReason: agentapi.FinishStop},
		"error": {Kind: agentapi.EventError, Err: &agentapi.Error{
			Kind: agentapi.ErrNetwork, Message: "dial tcp: connection refused", Provider: "api-bar",
		}},
	}
	for name, ev := range cases {
		t.Run(name, func(t *testing.T) { roundTrip(t, ev) })
	}
}

func TestChatRequestRoundTrip(t *testing.T) {
	temp := 0.0 // zero, not unset: the pointer is what carries the difference
	roundTrip(t, agentapi.ChatRequest{
		Model:    "gpt-4o",
		Messages: []agentapi.Message{agentapi.UserMessage("hi")},
		Tools: []agentapi.ToolSchema{{
			Name:        "list_dir",
			InputSchema: json.RawMessage(`{"type":"object"}`),
		}},
		Temperature: &temp,
		MaxTokens:   2048,
	})
}

func TestOptionRoundTrip(t *testing.T) {
	t.Run("stt", func(t *testing.T) {
		roundTrip(t, agentapi.STTOpts{Model: "whisper-1", Language: "ru", Prompt: "hint", MediaType: "audio/wav"})
	})
	t.Run("tts", func(t *testing.T) {
		roundTrip(t, agentapi.TTSOpts{Model: "tts-1", Voice: "alloy", Format: "mp3", Speed: 1.25})
	})
	t.Run("usage", func(t *testing.T) {
		roundTrip(t, agentapi.Usage{InputTokens: 1, OutputTokens: 2, CachedInputTokens: 3, ReasoningTokens: 4})
	})
}

// TestZeroValuesRoundTrip guards the omitempty tags: a zero value must
// survive the trip too, which is what a decoder of an old session file sees.
func TestZeroValuesRoundTrip(t *testing.T) {
	roundTrip(t, agentapi.ContentPart{})
	roundTrip(t, agentapi.Message{})
	roundTrip(t, agentapi.ToolResult{})
	roundTrip(t, agentapi.ChatEvent{})
	roundTrip(t, agentapi.Event{})
	roundTrip(t, agentapi.Usage{})
	roundTrip(t, agentapi.Compaction{})
	roundTrip(t, agentapi.PermissionRequest{})
}

// TestJSONFieldNames pins the wire names of the most-used types. Renaming a
// field is a breaking change to sessions on disk and to RPC clients, so it
// must fail a test rather than pass review unnoticed.
func TestJSONFieldNames(t *testing.T) {
	cases := []struct {
		name  string
		value any
		want  string
	}{
		{
			name:  "message",
			value: agentapi.Message{Role: agentapi.RoleTool, Content: []agentapi.ContentPart{agentapi.Text("out")}, ToolCallID: "c1"},
			want:  `{"role":"tool","content":[{"kind":"text","text":"out"}],"tool_call_id":"c1"}`,
		},
		{
			name:  "tool call",
			value: agentapi.ToolCall{ID: "c1", Name: "glob", Arguments: json.RawMessage(`{"pattern":"*.go"}`)},
			want:  `{"id":"c1","name":"glob","arguments":{"pattern":"*.go"}}`,
		},
		{
			name:  "tool schema",
			value: agentapi.ToolSchema{Name: "glob", Description: "find files", InputSchema: json.RawMessage(`{"type":"object"}`)},
			want:  `{"name":"glob","description":"find files","input_schema":{"type":"object"}}`,
		},
		{
			name:  "chat event done",
			value: agentapi.ChatEvent{Kind: agentapi.ChatDone, FinishReason: agentapi.FinishStop},
			want:  `{"kind":"done","finish_reason":"stop"}`,
		},
		{
			name:  "error",
			value: agentapi.Error{Kind: agentapi.ErrTimeout, Message: "deadline exceeded", Provider: "local"},
			want:  `{"kind":"timeout","message":"deadline exceeded","provider":"local"}`,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := json.Marshal(c.value)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			if string(got) != c.want {
				t.Errorf("wire format changed\n want: %s\n  got: %s", c.want, got)
			}
		})
	}
}

// TestUnknownFieldsIgnored documents forward compatibility: a value written
// by a newer version, carrying fields this version does not know, still
// decodes. Adding an optional field is therefore not a breaking change.
func TestUnknownFieldsIgnored(t *testing.T) {
	const future = `{"role":"user","content":[{"kind":"text","text":"hi","annotations":["x"]}],"trace_id":"abc"}`

	var msg agentapi.Message
	if err := json.Unmarshal([]byte(future), &msg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if msg.Text() != "hi" {
		t.Errorf("Text() = %q, want %q", msg.Text(), "hi")
	}
}
