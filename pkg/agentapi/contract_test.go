package agentapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/mrYush/hint/pkg/agentapi"
)

// This file exercises the package as an SDK consumer would: it implements the
// contract's interfaces from outside and checks that the documented protocol
// can actually be followed. A contract package that compiles but cannot be
// implemented is not a contract.

// echoTool is a minimal third-party tool, written against nothing but the
// exported API — the same position an external contributor is in.
type echoTool struct{ failNext bool }

func (echoTool) Name() string        { return "echo" }
func (echoTool) Description() string { return "Echo the given text back." }

func (echoTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"text":{"type":"string"}},"required":["text"]}`)
}

func (echoTool) Class() agentapi.ActionClass { return agentapi.ClassRead }

func (e echoTool) Run(ctx context.Context, callID string, args json.RawMessage) (agentapi.ToolResult, error) {
	if err := ctx.Err(); err != nil {
		return agentapi.ToolResult{}, err
	}
	var in struct {
		Text string `json:"text"`
	}
	// A model can and will send arguments that do not match the schema; the
	// contract says decoding them is the tool's job, and that a bad input is
	// a result the model should see, not an aborted turn.
	if err := json.Unmarshal(args, &in); err != nil {
		return agentapi.ErrorResult(callID, "echo", "invalid arguments: "+err.Error()), nil
	}
	if e.failNext {
		return agentapi.ErrorResult(callID, "echo", "echo is unavailable"), nil
	}
	return agentapi.TextResult(callID, "echo", in.Text), nil
}

// Compile-time assertion that the interface is satisfiable from outside the
// package. This is the idiomatic Go way to state "type X implements Y" — it
// costs nothing at run time and fails the build the moment the interface and
// the implementation drift apart.
var _ agentapi.Tool = echoTool{}

func TestToolIsImplementable(t *testing.T) {
	tool := echoTool{}

	schema := agentapi.SchemaOf(tool)
	if err := schema.Validate(); err != nil {
		t.Fatalf("SchemaOf produced an unsendable schema: %v", err)
	}
	if schema.Name != tool.Name() || schema.Description != tool.Description() {
		t.Errorf("SchemaOf = %+v, want it to mirror the tool's methods", schema)
	}
	if !json.Valid(schema.InputSchema) {
		t.Error("SchemaOf must carry the tool's schema through unchanged")
	}

	t.Run("success feeds back a valid message", func(t *testing.T) {
		res, err := tool.Run(context.Background(), "c1", json.RawMessage(`{"text":"hi"}`))
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
		if res.IsError {
			t.Error("a well-formed call must not produce an error result")
		}
		if got := res.Text(); got != "hi" {
			t.Errorf("Text() = %q, want %q", got, "hi")
		}
		if err := res.Validate(); err != nil {
			t.Errorf("result must be valid: %v", err)
		}
		if err := res.Message().Validate(); err != nil {
			t.Errorf("the message a result converts to must be valid: %v", err)
		}
	})

	t.Run("malformed arguments become a result, not an error", func(t *testing.T) {
		res, err := tool.Run(context.Background(), "c1", json.RawMessage(`{"text":`))
		if err != nil {
			t.Fatalf("a bad model argument must not abort the turn, got err = %v", err)
		}
		if !res.IsError {
			t.Error("a bad model argument must produce an error result the model can read")
		}
		if err := res.Message().Validate(); err != nil {
			t.Errorf("even an error result must convert to a valid message: %v", err)
		}
	})

	t.Run("honours context cancellation", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if _, err := tool.Run(ctx, "c1", json.RawMessage(`{"text":"hi"}`)); !errors.Is(err, context.Canceled) {
			t.Errorf("Run on a cancelled context = %v, want context.Canceled", err)
		}
	})
}

// scriptedProvider replays a fixed event script. It is the shape the WP0.4
// agent-loop tests will need, built here to prove the streaming contract can
// be satisfied.
type scriptedProvider struct {
	name   string
	script []agentapi.ChatEvent
}

func (p scriptedProvider) Name() string { return p.name }

func (p scriptedProvider) Stream(ctx context.Context, req agentapi.ChatRequest) (<-chan agentapi.ChatEvent, error) {
	if err := req.Validate(); err != nil {
		return nil, err
	}
	ch := make(chan agentapi.ChatEvent)
	go func() {
		defer close(ch)
		for _, ev := range p.script {
			select {
			case <-ctx.Done():
				// The contract requires exactly one terminal event even on
				// cancellation, so the consumer never blocks waiting for one.
				ch <- agentapi.ChatEvent{Kind: agentapi.ChatDone, FinishReason: agentapi.FinishCanceled}
				return
			case ch <- ev:
			}
		}
	}()
	return ch, nil
}

var _ agentapi.ChatProvider = scriptedProvider{}

func TestChatProviderIsImplementable(t *testing.T) {
	call := agentapi.ToolCall{ID: "c1", Name: "echo", Arguments: json.RawMessage(`{"text":"hi"}`)}
	provider := scriptedProvider{name: "scripted", script: []agentapi.ChatEvent{
		{Kind: agentapi.ChatThinkingDelta, Text: "the user wants an echo"},
		{Kind: agentapi.ChatTextDelta, Text: "Calling "},
		{Kind: agentapi.ChatTextDelta, Text: "echo."},
		{Kind: agentapi.ChatToolCall, Call: &call},
		{Kind: agentapi.ChatUsage, Usage: &agentapi.Usage{InputTokens: 12, OutputTokens: 4}},
		{Kind: agentapi.ChatDone, FinishReason: agentapi.FinishToolCalls},
	}}

	req := agentapi.ChatRequest{
		Messages: []agentapi.Message{agentapi.UserMessage("echo hi")},
		Tools:    []agentapi.ToolSchema{agentapi.SchemaOf(echoTool{})},
	}
	stream, err := provider.Stream(context.Background(), req)
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}

	// Consume the way the WP0.4 loop will: accumulate deltas, collect calls,
	// stop at the single terminal event.
	var text, thinking string
	var calls []agentapi.ToolCall
	var usage agentapi.Usage
	var terminal *agentapi.ChatEvent

	for ev := range stream {
		if err := ev.Validate(); err != nil {
			t.Fatalf("provider emitted an invalid event %+v: %v", ev, err)
		}
		if terminal != nil {
			t.Fatalf("event %+v arrived after the terminal event", ev)
		}
		switch ev.Kind {
		case agentapi.ChatTextDelta:
			text += ev.Text
		case agentapi.ChatThinkingDelta:
			thinking += ev.Text
		case agentapi.ChatToolCall:
			calls = append(calls, *ev.Call)
		case agentapi.ChatUsage:
			usage = usage.Add(*ev.Usage)
		case agentapi.ChatDone, agentapi.ChatError:
			ev := ev
			terminal = &ev
		}
	}

	if terminal == nil {
		t.Fatal("the stream closed without a terminal event")
	}
	if terminal.FinishReason != agentapi.FinishToolCalls {
		t.Errorf("FinishReason = %q, want %q", terminal.FinishReason, agentapi.FinishToolCalls)
	}
	if text != "Calling echo." {
		t.Errorf("accumulated text = %q, want %q", text, "Calling echo.")
	}
	if thinking != "the user wants an echo" {
		t.Errorf("accumulated thinking = %q, want %q", thinking, "the user wants an echo")
	}
	if len(calls) != 1 || calls[0].ID != "c1" {
		t.Fatalf("calls = %+v, want exactly the scripted one", calls)
	}
	if usage.Total() != 16 {
		t.Errorf("Usage.Total() = %d, want 16", usage.Total())
	}

	// The call the provider emitted must be runnable as-is: that is the whole
	// point of having the provider assemble streamed fragments.
	res, err := echoTool{}.Run(context.Background(), calls[0].ID, calls[0].Arguments)
	if err != nil {
		t.Fatalf("running the emitted call: %v", err)
	}
	if res.Text() != "hi" {
		t.Errorf("tool output = %q, want %q", res.Text(), "hi")
	}
}

func TestChatProviderRejectsInvalidRequest(t *testing.T) {
	// The contract says a non-nil error is only for failing to start the
	// stream — an unsendable request is exactly that case.
	provider := scriptedProvider{name: "scripted"}
	stream, err := provider.Stream(context.Background(), agentapi.ChatRequest{})
	if err == nil {
		t.Fatal("Stream must reject a request with no messages")
	}
	if stream != nil {
		t.Error("Stream must not return a channel alongside an error")
	}
	if got := agentapi.KindOf(err); got != agentapi.ErrInvalidRequest {
		t.Errorf("KindOf() = %q, want %q", got, agentapi.ErrInvalidRequest)
	}
}

// TestTurnRoundTripsThroughSession simulates what WP0.7 will do: build a
// conversation the way the agent loop does, write it out as JSONL, read it
// back, and check the result is still a sendable request. This is the
// end-to-end reason the wire types exist.
func TestTurnRoundTripsThroughSession(t *testing.T) {
	call := agentapi.ToolCall{ID: "c1", Name: "echo", Arguments: json.RawMessage(`{"text":"hi"}`)}
	result, err := echoTool{}.Run(context.Background(), call.ID, call.Arguments)
	if err != nil {
		t.Fatalf("tool: %v", err)
	}

	original := []agentapi.Message{
		agentapi.SystemMessage("you are hint"),
		agentapi.UserMessage("echo hi"),
		{Role: agentapi.RoleAssistant, ToolCalls: []agentapi.ToolCall{call}},
		result.Message(),
		agentapi.AssistantMessage("hi"),
	}

	var jsonl []byte
	for _, m := range original {
		line, err := json.Marshal(m)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		jsonl = append(jsonl, line...)
		jsonl = append(jsonl, '\n')
	}

	var restored []agentapi.Message
	dec := json.NewDecoder(bytes.NewReader(jsonl))
	for dec.More() {
		var m agentapi.Message
		if err := dec.Decode(&m); err != nil {
			t.Fatalf("decode: %v", err)
		}
		restored = append(restored, m)
	}

	if len(restored) != len(original) {
		t.Fatalf("restored %d messages, wrote %d", len(restored), len(original))
	}
	for i, m := range restored {
		if err := m.Validate(); err != nil {
			t.Errorf("restored[%d] is invalid: %v", i, err)
		}
	}
	if err := (agentapi.ChatRequest{Messages: restored}).Validate(); err != nil {
		t.Errorf("a restored conversation must still be sendable: %v", err)
	}
	if got := restored[3].ToolCallID; got != call.ID {
		t.Errorf("the tool result lost its link to the call: %q", got)
	}
}
