package agentapi_test

import (
	"encoding/json"
	"testing"

	"github.com/mrYush/hint/pkg/agentapi"
)

// A tagged union lets Kind and payload disagree, which a sealed interface
// would have made unrepresentable. Validate is what buys that back, so it
// carries the same weight as the round-trip tests.

func TestContentPartValidate(t *testing.T) {
	cases := []struct {
		name    string
		part    agentapi.ContentPart
		wantErr bool
	}{
		{"text", agentapi.Text("hi"), false},
		{"empty text is allowed", agentapi.Text(""), false},
		{"thinking", agentapi.Thinking("hmm"), false},
		{"image by url", agentapi.ImagePart(agentapi.Image{URL: "https://e/a.png"}), false},
		{"inline image", agentapi.ImagePart(agentapi.Image{Data: []byte{1}, MediaType: "image/png"}), false},
		{"audio by url", agentapi.AudioPart(agentapi.Audio{URL: "file:///a.wav"}), false},

		{"no kind", agentapi.ContentPart{Text: "hi"}, true},
		{"unknown kind", agentapi.ContentPart{Kind: "video"}, true},
		{"image kind without image", agentapi.ContentPart{Kind: agentapi.PartImage}, true},
		{"audio kind without audio", agentapi.ContentPart{Kind: agentapi.PartAudio}, true},
		{"text kind with image", agentapi.ContentPart{Kind: agentapi.PartText, Image: &agentapi.Image{URL: "u"}}, true},
		{"image kind with audio", agentapi.ContentPart{Kind: agentapi.PartImage, Image: &agentapi.Image{URL: "u"}, Audio: &agentapi.Audio{URL: "u"}}, true},
		{"image with no source", agentapi.ImagePart(agentapi.Image{}), true},
		{"image with both sources", agentapi.ImagePart(agentapi.Image{URL: "u", Data: []byte{1}, MediaType: "image/png"}), true},
		{"inline image without media type", agentapi.ImagePart(agentapi.Image{Data: []byte{1}}), true},
		{"inline audio without media type", agentapi.AudioPart(agentapi.Audio{Data: []byte{1}}), true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := c.part.Validate()
			if (err != nil) != c.wantErr {
				t.Fatalf("Validate() error = %v, wantErr = %v", err, c.wantErr)
			}
		})
	}
}

func TestMessageValidate(t *testing.T) {
	call := agentapi.ToolCall{ID: "c1", Name: "glob", Arguments: json.RawMessage(`{}`)}

	cases := []struct {
		name    string
		msg     agentapi.Message
		wantErr bool
	}{
		{"user text", agentapi.UserMessage("hi"), false},
		{"assistant with calls", agentapi.Message{Role: agentapi.RoleAssistant, ToolCalls: []agentapi.ToolCall{call}}, false},
		{"tool result", agentapi.Message{Role: agentapi.RoleTool, Content: []agentapi.ContentPart{agentapi.Text("out")}, ToolCallID: "c1"}, false},

		{"zero value", agentapi.Message{}, true},
		{"unknown role", agentapi.Message{Role: "developer", Content: []agentapi.ContentPart{agentapi.Text("hi")}}, true},
		{"empty message", agentapi.Message{Role: agentapi.RoleUser}, true},
		{"bad part", agentapi.Message{Role: agentapi.RoleUser, Content: []agentapi.ContentPart{{Kind: agentapi.PartImage}}}, true},
		{"user with tool calls", agentapi.Message{Role: agentapi.RoleUser, Content: []agentapi.ContentPart{agentapi.Text("hi")}, ToolCalls: []agentapi.ToolCall{call}}, true},
		{"tool without call id", agentapi.Message{Role: agentapi.RoleTool, Content: []agentapi.ContentPart{agentapi.Text("out")}}, true},
		{"silent tool result", agentapi.Message{Role: agentapi.RoleTool, ToolCallID: "c1"}, false},
		{"user with call id", agentapi.Message{Role: agentapi.RoleUser, Content: []agentapi.ContentPart{agentapi.Text("hi")}, ToolCallID: "c1"}, true},
		{"assistant with malformed call", agentapi.Message{Role: agentapi.RoleAssistant, ToolCalls: []agentapi.ToolCall{{ID: "c1", Name: "glob", Arguments: json.RawMessage(`{`)}}}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := c.msg.Validate()
			if (err != nil) != c.wantErr {
				t.Fatalf("Validate() error = %v, wantErr = %v", err, c.wantErr)
			}
		})
	}
}

func TestMessageTextAndThinking(t *testing.T) {
	msg := agentapi.Message{
		Role: agentapi.RoleAssistant,
		Content: []agentapi.ContentPart{
			agentapi.Thinking("first thought"),
			agentapi.Text("line one"),
			agentapi.ImagePart(agentapi.Image{URL: "https://e/a.png"}),
			agentapi.Thinking("second thought"),
			agentapi.Text("line two"),
		},
	}
	if got, want := msg.Text(), "line one\nline two"; got != want {
		t.Errorf("Text() = %q, want %q", got, want)
	}
	if got, want := msg.Thinking(), "first thought\nsecond thought"; got != want {
		t.Errorf("Thinking() = %q, want %q", got, want)
	}
	if got := (agentapi.Message{}).Text(); got != "" {
		t.Errorf("Text() of zero message = %q, want empty", got)
	}
}

func TestToolCallValidate(t *testing.T) {
	cases := []struct {
		name    string
		call    agentapi.ToolCall
		wantErr bool
	}{
		{"ok", agentapi.ToolCall{ID: "c1", Name: "glob", Arguments: json.RawMessage(`{"pattern":"*"}`)}, false},
		{"empty object", agentapi.ToolCall{ID: "c1", Name: "glob", Arguments: json.RawMessage(`{}`)}, false},
		{"no id", agentapi.ToolCall{Name: "glob", Arguments: json.RawMessage(`{}`)}, true},
		{"no name", agentapi.ToolCall{ID: "c1", Arguments: json.RawMessage(`{}`)}, true},
		{"no arguments", agentapi.ToolCall{ID: "c1", Name: "glob"}, true},
		{"truncated arguments", agentapi.ToolCall{ID: "c1", Name: "glob", Arguments: json.RawMessage(`{"pattern":`)}, true},
		{"null arguments", agentapi.ToolCall{ID: "c1", Name: "glob", Arguments: json.RawMessage(`null`)}, true},
		{"scalar arguments", agentapi.ToolCall{ID: "c1", Name: "glob", Arguments: json.RawMessage(`123`)}, true},
		{"array arguments", agentapi.ToolCall{ID: "c1", Name: "glob", Arguments: json.RawMessage(`[]`)}, true},
		{"string arguments", agentapi.ToolCall{ID: "c1", Name: "glob", Arguments: json.RawMessage(`"oops"`)}, true},
		{"leading whitespace is fine", agentapi.ToolCall{ID: "c1", Name: "glob", Arguments: json.RawMessage("  \n{}")}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := c.call.Validate()
			if (err != nil) != c.wantErr {
				t.Fatalf("Validate() error = %v, wantErr = %v", err, c.wantErr)
			}
		})
	}
}

func TestToolSchemaValidate(t *testing.T) {
	cases := []struct {
		name    string
		schema  agentapi.ToolSchema
		wantErr bool
	}{
		{"ok", agentapi.ToolSchema{Name: "glob", InputSchema: json.RawMessage(`{"type":"object"}`)}, false},
		{"no name", agentapi.ToolSchema{InputSchema: json.RawMessage(`{}`)}, true},
		{"no schema", agentapi.ToolSchema{Name: "glob"}, true},
		{"malformed schema", agentapi.ToolSchema{Name: "glob", InputSchema: json.RawMessage(`{`)}, true},
		{"schema is not an object", agentapi.ToolSchema{Name: "glob", InputSchema: json.RawMessage(`"not a schema"`)}, true},
		{"null schema", agentapi.ToolSchema{Name: "glob", InputSchema: json.RawMessage(`null`)}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := c.schema.Validate()
			if (err != nil) != c.wantErr {
				t.Fatalf("Validate() error = %v, wantErr = %v", err, c.wantErr)
			}
		})
	}
}

func TestToolResultMessage(t *testing.T) {
	r := agentapi.TextResult("c1", "read_file", "package main")

	msg := r.Message()
	if msg.Role != agentapi.RoleTool {
		t.Errorf("Role = %q, want %q", msg.Role, agentapi.RoleTool)
	}
	if msg.ToolCallID != "c1" {
		t.Errorf("ToolCallID = %q, want %q", msg.ToolCallID, "c1")
	}
	if err := msg.Validate(); err != nil {
		t.Errorf("the message a result converts to must be valid: %v", err)
	}
	if got, want := r.Text(), "package main"; got != want {
		t.Errorf("Text() = %q, want %q", got, want)
	}

	e := agentapi.ErrorResult("c1", "bash", "exit status 1")
	if !e.IsError {
		t.Error("ErrorResult must set IsError")
	}
	if err := e.Message().Validate(); err != nil {
		t.Errorf("an error result must still convert to a valid message: %v", err)
	}
}

func TestChatEventValidate(t *testing.T) {
	usage := agentapi.Usage{InputTokens: 1}
	cases := []struct {
		name    string
		event   agentapi.ChatEvent
		wantErr bool
	}{
		{"text delta", agentapi.ChatEvent{Kind: agentapi.ChatTextDelta, Text: "a"}, false},
		{"tool call", agentapi.ChatEvent{Kind: agentapi.ChatToolCall, Call: &agentapi.ToolCall{ID: "c1", Name: "n", Arguments: json.RawMessage(`{}`)}}, false},
		{"usage", agentapi.ChatEvent{Kind: agentapi.ChatUsage, Usage: &usage}, false},
		{"done", agentapi.ChatEvent{Kind: agentapi.ChatDone, FinishReason: agentapi.FinishStop}, false},
		{"done with an upstream finish reason", agentapi.ChatEvent{Kind: agentapi.ChatDone, FinishReason: "max_tokens"}, true},
		{"done with error reason is a second terminal event", agentapi.ChatEvent{Kind: agentapi.ChatDone, FinishReason: agentapi.FinishError}, true},
		{"error", agentapi.ChatEvent{Kind: agentapi.ChatError, Err: agentapi.NewError(agentapi.ErrNetwork, "boom")}, false},

		{"no kind", agentapi.ChatEvent{}, true},
		{"unknown kind", agentapi.ChatEvent{Kind: "audio_delta"}, true},
		{"tool call without call", agentapi.ChatEvent{Kind: agentapi.ChatToolCall}, true},
		{"tool call with partial arguments", agentapi.ChatEvent{Kind: agentapi.ChatToolCall, Call: &agentapi.ToolCall{ID: "c1", Name: "n", Arguments: json.RawMessage(`{"p":`)}}, true},
		{"usage without usage", agentapi.ChatEvent{Kind: agentapi.ChatUsage}, true},
		{"done without reason", agentapi.ChatEvent{Kind: agentapi.ChatDone}, true},
		{"error without error", agentapi.ChatEvent{Kind: agentapi.ChatError}, true},
		{"text delta carrying a call", agentapi.ChatEvent{Kind: agentapi.ChatTextDelta, Call: &agentapi.ToolCall{ID: "c1"}}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := c.event.Validate()
			if (err != nil) != c.wantErr {
				t.Fatalf("Validate() error = %v, wantErr = %v", err, c.wantErr)
			}
		})
	}
}

func TestChatEventTerminal(t *testing.T) {
	terminal := []agentapi.ChatEventKind{agentapi.ChatDone, agentapi.ChatError}
	for _, k := range terminal {
		if !(agentapi.ChatEvent{Kind: k}).Terminal() {
			t.Errorf("%s must be terminal", k)
		}
	}
	nonTerminal := []agentapi.ChatEventKind{agentapi.ChatTextDelta, agentapi.ChatThinkingDelta, agentapi.ChatToolCall, agentapi.ChatUsage}
	for _, k := range nonTerminal {
		if (agentapi.ChatEvent{Kind: k}).Terminal() {
			t.Errorf("%s must not be terminal", k)
		}
	}
}

func TestEventValidate(t *testing.T) {
	msg := agentapi.UserMessage("hi")
	bad := agentapi.Message{Role: "nope"}
	usage := agentapi.Usage{}

	cases := []struct {
		name    string
		event   agentapi.Event
		wantErr bool
	}{
		{"turn start", agentapi.Event{Kind: agentapi.EventTurnStart}, false},
		{"text delta", agentapi.Event{Kind: agentapi.EventTextDelta, Text: "a"}, false},
		{"message", agentapi.Event{Kind: agentapi.EventMessage, Message: &msg}, false},
		{"permission", agentapi.Event{Kind: agentapi.EventPermission, Permission: &agentapi.PermissionRequest{
			CallID: "c1", Tool: "bash", Class: agentapi.ClassExecute, Summary: "run go test",
		}}, false},
		{"tool end", agentapi.Event{Kind: agentapi.EventToolEnd, Result: &agentapi.ToolResult{CallID: "c1"}}, false},
		{"compaction", agentapi.Event{Kind: agentapi.EventCompaction, Compaction: &agentapi.Compaction{MessagesReplaced: 3}}, false},
		{"usage", agentapi.Event{Kind: agentapi.EventUsage, Usage: &usage}, false},
		{"turn end", agentapi.Event{Kind: agentapi.EventTurnEnd, FinishReason: agentapi.FinishStop}, false},

		{"no kind", agentapi.Event{}, true},
		{"unknown kind", agentapi.Event{Kind: "subagent_start"}, true},
		{"message without message", agentapi.Event{Kind: agentapi.EventMessage}, true},
		{"message with invalid message", agentapi.Event{Kind: agentapi.EventMessage, Message: &bad}, true},
		{"permission without request", agentapi.Event{Kind: agentapi.EventPermission}, true},
		{"permission with unknown class", agentapi.Event{Kind: agentapi.EventPermission, Permission: &agentapi.PermissionRequest{
			CallID: "c1", Tool: "bash", Class: "sudo", Summary: "run go test",
		}}, true},
		{"permission without call id", agentapi.Event{Kind: agentapi.EventPermission, Permission: &agentapi.PermissionRequest{
			Tool: "bash", Class: agentapi.ClassExecute, Summary: "run go test",
		}}, true},
		{"turn end with an upstream finish reason", agentapi.Event{
			Kind: agentapi.EventTurnEnd, FinishReason: "max_tokens",
		}, true},
		{"tool start without call", agentapi.Event{Kind: agentapi.EventToolStart}, true},
		{"tool end without result", agentapi.Event{Kind: agentapi.EventToolEnd}, true},
		{"compaction without details", agentapi.Event{Kind: agentapi.EventCompaction}, true},
		{"turn end without reason", agentapi.Event{Kind: agentapi.EventTurnEnd}, true},
		{"error without error", agentapi.Event{Kind: agentapi.EventError}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := c.event.Validate()
			if (err != nil) != c.wantErr {
				t.Fatalf("Validate() error = %v, wantErr = %v", err, c.wantErr)
			}
		})
	}
}

func TestChatRequestValidate(t *testing.T) {
	msgs := []agentapi.Message{agentapi.UserMessage("hi")}
	schema := json.RawMessage(`{"type":"object"}`)

	cases := []struct {
		name    string
		req     agentapi.ChatRequest
		wantErr bool
	}{
		{"ok", agentapi.ChatRequest{Messages: msgs}, false},
		{"with tools", agentapi.ChatRequest{Messages: msgs, Tools: []agentapi.ToolSchema{
			{Name: "a", InputSchema: schema}, {Name: "b", InputSchema: schema},
		}}, false},

		{"no messages", agentapi.ChatRequest{}, true},
		{"invalid message", agentapi.ChatRequest{Messages: []agentapi.Message{{Role: agentapi.RoleUser}}}, true},
		{"invalid tool", agentapi.ChatRequest{Messages: msgs, Tools: []agentapi.ToolSchema{{Name: "a"}}}, true},
		{"duplicate tool names", agentapi.ChatRequest{Messages: msgs, Tools: []agentapi.ToolSchema{
			{Name: "a", InputSchema: schema}, {Name: "a", InputSchema: schema},
		}}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := c.req.Validate()
			if (err != nil) != c.wantErr {
				t.Fatalf("Validate() error = %v, wantErr = %v", err, c.wantErr)
			}
			if err != nil && agentapi.KindOf(err) != agentapi.ErrInvalidRequest {
				t.Errorf("KindOf(%v) = %q, want %q", err, agentapi.KindOf(err), agentapi.ErrInvalidRequest)
			}
		})
	}
}

func TestRoleAndClassValid(t *testing.T) {
	for _, r := range []agentapi.Role{agentapi.RoleSystem, agentapi.RoleUser, agentapi.RoleAssistant, agentapi.RoleTool} {
		if !r.Valid() {
			t.Errorf("%q must be a valid role", r)
		}
	}
	for _, r := range []agentapi.Role{"", "developer", "function"} {
		if r.Valid() {
			t.Errorf("%q must not be a valid role", r)
		}
	}
	for _, c := range []agentapi.ActionClass{agentapi.ClassRead, agentapi.ClassWrite, agentapi.ClassExecute} {
		if !c.Valid() {
			t.Errorf("%q must be a valid class", c)
		}
	}
	for _, c := range []agentapi.ActionClass{"", "network", "admin"} {
		if c.Valid() {
			t.Errorf("%q must not be a valid class", c)
		}
	}
}

func TestUsageArithmetic(t *testing.T) {
	a := agentapi.Usage{InputTokens: 10, OutputTokens: 3, CachedInputTokens: 8, ReasoningTokens: 1}
	b := agentapi.Usage{InputTokens: 5, OutputTokens: 2, CachedInputTokens: 0, ReasoningTokens: 2}

	if got, want := a.Total(), int64(13); got != want {
		t.Errorf("Total() = %d, want %d", got, want)
	}
	sum := a.Add(b)
	want := agentapi.Usage{InputTokens: 15, OutputTokens: 5, CachedInputTokens: 8, ReasoningTokens: 3}
	if sum != want {
		t.Errorf("Add() = %#v, want %#v", sum, want)
	}
	if a.InputTokens != 10 {
		t.Error("Add must not mutate the receiver")
	}
}
