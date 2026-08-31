package agentapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/mrYush/hint/pkg/agentapi"
)

// Each test here pins a defect found in review of the initial WP0.1 contract.
// They are kept together so that the reason a rule exists stays attached to
// the rule.

// A tool that succeeds silently — write_file, todo, a quiet command — returns
// a result with no content. ToolResult.Validate accepted it while
// Message.Validate rejected the message built from it, so the failure surfaced
// one provider call later as a non-retryable invalid_request that aborted the
// whole turn.
func TestSilentToolResultStaysSendable(t *testing.T) {
	r := agentapi.ToolResult{CallID: "c1", Name: "write_file"}

	if err := r.Validate(); err != nil {
		t.Fatalf("a silent tool result must be valid: %v", err)
	}
	if err := r.Message().Validate(); err != nil {
		t.Fatalf("the message it converts to must be valid too: %v", err)
	}

	req := agentapi.ChatRequest{Messages: []agentapi.Message{
		agentapi.UserMessage("write the file"),
		{Role: agentapi.RoleAssistant, ToolCalls: []agentapi.ToolCall{
			{ID: "c1", Name: "write_file", Arguments: json.RawMessage(`{"path":"a.txt"}`)},
		}},
		r.Message(),
	}}
	if err := req.Validate(); err != nil {
		t.Errorf("the turn must remain sendable after a silent tool: %v", err)
	}

	// The rule is scoped to tool messages: an empty user or assistant message
	// carries nothing at all and is still rejected.
	for _, role := range []agentapi.Role{agentapi.RoleUser, agentapi.RoleAssistant, agentapi.RoleSystem} {
		if err := (agentapi.Message{Role: role}).Validate(); err == nil {
			t.Errorf("an empty %s message must still be rejected", role)
		}
	}
}

// A nil json.RawMessage marshals to the literal null and decodes back as the
// four bytes "null", which json.Valid accepts. Validate therefore rejected a
// call before it was written to a session file and accepted the same call
// after it was read back, and a tool unmarshalling null got zeroed arguments.
func TestNilRawMessageDoesNotBecomeNull(t *testing.T) {
	cases := []struct {
		name  string
		value any
	}{
		{"tool call", agentapi.ToolCall{ID: "c1", Name: "bash"}},
		{"tool schema", agentapi.ToolSchema{Name: "bash"}},
		{"chat request", agentapi.ChatRequest{Messages: []agentapi.Message{agentapi.UserMessage("hi")}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			data, err := json.Marshal(c.value)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			if bytes.Contains(data, []byte("null")) {
				t.Errorf("a nil raw field must be omitted, got %s", data)
			}
		})
	}

	// And the invalid-before/valid-after asymmetry is gone.
	call := agentapi.ToolCall{ID: "c1", Name: "bash"}
	before := call.Validate()
	data, err := json.Marshal(call)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var after agentapi.ToolCall
	if err := json.Unmarshal(data, &after); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if (before == nil) != (after.Validate() == nil) {
		t.Errorf("validity changed across a round trip: before=%v after=%v", before, after.Validate())
	}
}

// Arguments and input schemas must be JSON objects. json.Valid alone accepts
// null, scalars and arrays, which a tool then unmarshals into a zeroed struct
// and acts on — write_file against the empty path, for instance.
func TestArgumentsMustBeAnObject(t *testing.T) {
	for _, raw := range []string{`null`, `123`, `"oops"`, `[]`, `true`} {
		call := agentapi.ToolCall{ID: "c1", Name: "write_file", Arguments: json.RawMessage(raw)}
		if err := call.Validate(); err == nil {
			t.Errorf("arguments %s must be rejected", raw)
		}
		ev := agentapi.ChatEvent{Kind: agentapi.ChatToolCall, Call: &call}
		if err := ev.Validate(); err == nil {
			t.Errorf("a tool_call event carrying arguments %s must be rejected", raw)
		}
	}
}

// A permission prompt without a call id cannot be matched to the call it
// authorises. With two calls in one assistant message the client shows two
// indistinguishable prompts, and an approval meant for one can authorise the
// other.
func TestPermissionRequestNeedsCallID(t *testing.T) {
	r := agentapi.PermissionRequest{Tool: "bash", Class: agentapi.ClassExecute, Summary: `run "go test"`}
	if err := r.Validate(); err == nil {
		t.Fatal("a permission request without a call id must be rejected")
	}
	r.CallID = "c1"
	if err := r.Validate(); err != nil {
		t.Errorf("Validate() = %v, want nil once the call id is set", err)
	}
}

// A provider that classifies a cancelled request as a transport failure — the
// natural thing to do, since that is what an HTTP client reports — must not
// make the router treat the user's Ctrl-C as a reason to fall back and retry.
func TestCancellationOutranksProviderClassification(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	// What a WP0.3 provider would produce for a cancelled in-flight request.
	wrapped := agentapi.WrapError(agentapi.ErrNetwork, fmt.Errorf("Post %q: %w", "https://api/v1", ctx.Err()), "streaming")

	if got := agentapi.KindOf(wrapped); got != agentapi.ErrCanceled {
		t.Errorf("KindOf() = %q, want %q — a cancelled turn must not look retryable", got, agentapi.ErrCanceled)
	}
	if agentapi.KindOf(wrapped).Retryable() || agentapi.KindOf(wrapped).Fallbackable() {
		t.Error("a cancelled turn must be neither retried nor failed over")
	}

	// A deadline behaves the same way, and an unrelated network failure is
	// still classified by the provider.
	deadline := agentapi.WrapError(agentapi.ErrNetwork, context.DeadlineExceeded, "streaming")
	if got := agentapi.KindOf(deadline); got != agentapi.ErrTimeout {
		t.Errorf("KindOf(deadline) = %q, want %q", got, agentapi.ErrTimeout)
	}
	plain := agentapi.NewError(agentapi.ErrNetwork, "connection refused")
	if got := agentapi.KindOf(plain); got != agentapi.ErrNetwork {
		t.Errorf("KindOf(plain) = %q, want %q", got, agentapi.ErrNetwork)
	}
}

// The package documents unknown Kind values as skippable rather than fatal.
// Without a sentinel, a decoder could not tell "written by a newer version"
// from "malformed" and would have to string-match the error message.
func TestUnknownKindIsDistinguishable(t *testing.T) {
	unknown := []struct {
		name string
		err  error
	}{
		{"content part", agentapi.ContentPart{Kind: "video"}.Validate()},
		{"chat event", agentapi.ChatEvent{Kind: "audio_delta"}.Validate()},
		{"event", agentapi.Event{Kind: "subagent_start"}.Validate()},
		{"finish reason", agentapi.ChatEvent{Kind: agentapi.ChatDone, FinishReason: "max_tokens"}.Validate()},
	}
	for _, c := range unknown {
		t.Run(c.name, func(t *testing.T) {
			if c.err == nil {
				t.Fatal("an unknown kind must still be reported")
			}
			if !errors.Is(c.err, agentapi.ErrUnknownKind) {
				t.Errorf("error %v must match ErrUnknownKind so a decoder can skip it", c.err)
			}
		})
	}

	// A malformed value of a known kind must not be mistaken for a skippable
	// one — that would silently drop real data.
	malformed := []struct {
		name string
		err  error
	}{
		{"image part with no payload", agentapi.ContentPart{Kind: agentapi.PartImage}.Validate()},
		{"done without a reason", agentapi.ChatEvent{Kind: agentapi.ChatDone}.Validate()},
		{"tool_end without a result", agentapi.Event{Kind: agentapi.EventToolEnd}.Validate()},
	}
	for _, c := range malformed {
		t.Run(c.name, func(t *testing.T) {
			if c.err == nil {
				t.Fatal("a malformed value must be reported")
			}
			if errors.Is(c.err, agentapi.ErrUnknownKind) {
				t.Error("a malformed value must not look skippable")
			}
		})
	}
}

// FinishError described a ChatError followed by a ChatDone — two terminal
// events, which ChatProvider forbids. A provider implementing the doc
// literally would block forever on the second send.
func TestFinishErrorIsAgentLevelOnly(t *testing.T) {
	if err := (agentapi.ChatEvent{Kind: agentapi.ChatDone, FinishReason: agentapi.FinishError}).Validate(); err == nil {
		t.Error("a provider stream must not end with done/error; ChatError is the terminal event")
	}
	if err := (agentapi.Event{Kind: agentapi.EventTurnEnd, FinishReason: agentapi.FinishError}).Validate(); err != nil {
		t.Errorf("a turn may end with the error reason: %v", err)
	}
}

// A streaming provider can close a text part empty. Keying the newline
// separator off "the buffer is still empty" swallowed the line break after
// one, so Text() silently disagreed with its own documentation.
func TestEmptyPartKeepsTheSeparator(t *testing.T) {
	msg := agentapi.Message{Role: agentapi.RoleAssistant, Content: []agentapi.ContentPart{
		agentapi.Text(""),
		agentapi.Text("line two"),
	}}
	if got, want := msg.Text(), "\nline two"; got != want {
		t.Errorf("Text() = %q, want %q", got, want)
	}

	only := agentapi.Message{Role: agentapi.RoleAssistant, Content: []agentapi.ContentPart{agentapi.Text("")}}
	if got := only.Text(); got != "" {
		t.Errorf("Text() of a single empty part = %q, want empty", got)
	}
}
