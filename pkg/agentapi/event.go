package agentapi

import "fmt"

// FinishReason says why a model stopped generating.
type FinishReason string

const (
	// FinishStop is a natural end of the response.
	FinishStop FinishReason = "stop"
	// FinishToolCalls means the model stopped in order to call tools. The
	// agent loop executes them and continues the turn.
	FinishToolCalls FinishReason = "tool_calls"
	// FinishLength means the output hit the token limit and is truncated.
	FinishLength FinishReason = "length"
	// FinishContentFilter means a safety filter withheld the response.
	FinishContentFilter FinishReason = "content_filter"
	// FinishCanceled means the caller cancelled the context.
	FinishCanceled FinishReason = "canceled"
	// FinishError means a turn ended because of a failure. It appears on
	// [EventTurnEnd], where the failure was already reported by a separate,
	// non-terminal [EventError].
	//
	// It never appears on [ChatDone]: at the provider level a failed stream
	// ends with the single terminal event [ChatError], and emitting a
	// ChatDone after it would be a second terminal event, which
	// [ChatProvider] forbids.
	FinishError FinishReason = "error"
)

// Valid reports whether r is one of the defined reasons.
//
// Providers must map their own vocabulary onto these: OpenAI-compatible
// endpoints spell truncation "length" or "max_tokens" and tool use
// "tool_calls" or "function_call" depending on the dialect. Forwarding a raw
// upstream value would make the agent loop's switch fall through to its
// default and report a truncated answer as a normal stop.
func (r FinishReason) Valid() bool {
	switch r {
	case FinishStop, FinishToolCalls, FinishLength, FinishContentFilter,
		FinishCanceled, FinishError:
		return true
	default:
		return false
	}
}

// ChatEventKind discriminates the payload of a [ChatEvent].
type ChatEventKind string

const (
	// ChatTextDelta carries a fragment of the assistant's visible text in
	// ChatEvent.Text.
	ChatTextDelta ChatEventKind = "text_delta"
	// ChatThinkingDelta carries a fragment of the model's reasoning trace in
	// ChatEvent.Text.
	ChatThinkingDelta ChatEventKind = "thinking_delta"
	// ChatToolCall carries one complete tool call in ChatEvent.Call. The
	// provider has already assembled it from however many stream fragments
	// the wire format used, so it is never partial.
	ChatToolCall ChatEventKind = "tool_call"
	// ChatUsage carries token accounting in ChatEvent.Usage. Emitted at most
	// once per stream, before ChatDone; some providers never send it.
	ChatUsage ChatEventKind = "usage"
	// ChatDone ends the stream successfully and carries FinishReason.
	ChatDone ChatEventKind = "done"
	// ChatError ends the stream with a failure and carries ChatEvent.Err.
	ChatError ChatEventKind = "error"
)

// ChatEvent is one item of a [ChatProvider] stream: the provider-facing,
// low-level view of a model response.
//
// A stream is a sequence of deltas and tool calls terminated by exactly one
// ChatDone or ChatError, after which the channel is closed. A consumer that
// does not recognise a Kind must skip the event rather than fail — new kinds
// are additive.
type ChatEvent struct {
	// Kind selects the payload. Always set.
	Kind ChatEventKind `json:"kind"`
	// Text is the payload of ChatTextDelta and ChatThinkingDelta.
	Text string `json:"text,omitempty"`
	// Call is the payload of ChatToolCall.
	Call *ToolCall `json:"call,omitempty"`
	// Usage is the payload of ChatUsage.
	Usage *Usage `json:"usage,omitempty"`
	// FinishReason is set on ChatDone.
	FinishReason FinishReason `json:"finish_reason,omitempty"`
	// Err is the payload of ChatError.
	Err *Error `json:"error,omitempty"`
}

// Terminal reports whether the event ends the stream.
func (e ChatEvent) Terminal() bool {
	return e.Kind == ChatDone || e.Kind == ChatError
}

// Validate reports whether the event's payload matches its Kind.
func (e ChatEvent) Validate() error {
	switch e.Kind {
	case ChatTextDelta, ChatThinkingDelta:
		if e.Call != nil || e.Err != nil {
			return fmt.Errorf("agentapi: %s event carries a non-text payload", e.Kind)
		}
	case ChatToolCall:
		if e.Call == nil {
			return fmt.Errorf("agentapi: tool_call event has no call")
		}
		return e.Call.Validate()
	case ChatUsage:
		if e.Usage == nil {
			return fmt.Errorf("agentapi: usage event has no usage")
		}
	case ChatDone:
		if e.FinishReason == "" {
			return fmt.Errorf("agentapi: done event has no finish reason")
		}
		if !e.FinishReason.Valid() {
			return fmt.Errorf("agentapi: %w: finish reason %q", ErrUnknownKind, e.FinishReason)
		}
		if e.FinishReason == FinishError {
			return fmt.Errorf("agentapi: a failed stream ends with an error event, not done/error")
		}
	case ChatError:
		if e.Err == nil {
			return fmt.Errorf("agentapi: error event has no error")
		}
	case "":
		return fmt.Errorf("agentapi: chat event has no kind")
	default:
		return fmt.Errorf("agentapi: %w: chat event kind %q", ErrUnknownKind, e.Kind)
	}
	return nil
}

// EventKind discriminates the payload of an [Event].
type EventKind string

const (
	// EventTurnStart opens a turn, before the first provider call.
	EventTurnStart EventKind = "turn_start"
	// EventTextDelta carries a fragment of assistant text in Event.Text,
	// forwarded from the provider stream for live rendering.
	EventTextDelta EventKind = "text_delta"
	// EventThinkingDelta carries a fragment of the reasoning trace in
	// Event.Text.
	EventThinkingDelta EventKind = "thinking_delta"
	// EventMessage carries a complete message that was appended to the
	// conversation, in Event.Message. A client that ignores the delta kinds
	// can render a whole turn from these alone.
	EventMessage EventKind = "message"
	// EventPermission asks the client to confirm an action, in
	// Event.Permission. The client answers out of band; the turn is blocked
	// until it does.
	EventPermission EventKind = "permission"
	// EventToolStart announces that a tool is about to run, in Event.Call.
	EventToolStart EventKind = "tool_start"
	// EventToolEnd reports a finished tool, in Event.Result.
	EventToolEnd EventKind = "tool_end"
	// EventCompaction reports that the context was compacted, in
	// Event.Compaction.
	EventCompaction EventKind = "compaction"
	// EventUsage reports cumulative token accounting for the turn, in
	// Event.Usage.
	EventUsage EventKind = "usage"
	// EventTurnEnd closes a turn and carries FinishReason.
	EventTurnEnd EventKind = "turn_end"
	// EventError reports a failure, in Event.Err. It does not necessarily end
	// the turn — a recovered provider fallback is reported this way too.
	EventError EventKind = "error"
)

// Event is one item of the agent's observable stream: the client-facing,
// high-level view of a turn.
//
// It is what a CLI renders today and what the Phase 1 core-as-a-service ships
// over RPC to a TUI, a desktop widget, or a mobile shell. It is a separate
// type from [ChatEvent] on purpose: a client must not have to know that a
// turn involved several provider calls, a fallback, and a compaction.
type Event struct {
	// Kind selects the payload. Always set.
	Kind EventKind `json:"kind"`
	// Text is the payload of EventTextDelta and EventThinkingDelta.
	Text string `json:"text,omitempty"`
	// Message is the payload of EventMessage.
	Message *Message `json:"message,omitempty"`
	// Call is the payload of EventToolStart.
	Call *ToolCall `json:"call,omitempty"`
	// Result is the payload of EventToolEnd.
	Result *ToolResult `json:"result,omitempty"`
	// Permission is the payload of EventPermission.
	Permission *PermissionRequest `json:"permission,omitempty"`
	// Compaction is the payload of EventCompaction.
	Compaction *Compaction `json:"compaction,omitempty"`
	// Usage is the payload of EventUsage.
	Usage *Usage `json:"usage,omitempty"`
	// FinishReason is set on EventTurnEnd.
	FinishReason FinishReason `json:"finish_reason,omitempty"`
	// Err is the payload of EventError.
	Err *Error `json:"error,omitempty"`
}

// Validate reports whether the event's payload matches its Kind.
func (e Event) Validate() error {
	switch e.Kind {
	case EventTurnStart:
	case EventTextDelta, EventThinkingDelta:
	case EventMessage:
		if e.Message == nil {
			return fmt.Errorf("agentapi: message event has no message")
		}
		return e.Message.Validate()
	case EventPermission:
		if e.Permission == nil {
			return fmt.Errorf("agentapi: permission event has no request")
		}
		return e.Permission.Validate()
	case EventToolStart:
		if e.Call == nil {
			return fmt.Errorf("agentapi: tool_start event has no call")
		}
		return e.Call.Validate()
	case EventToolEnd:
		if e.Result == nil {
			return fmt.Errorf("agentapi: tool_end event has no result")
		}
		return e.Result.Validate()
	case EventCompaction:
		if e.Compaction == nil {
			return fmt.Errorf("agentapi: compaction event has no details")
		}
	case EventUsage:
		if e.Usage == nil {
			return fmt.Errorf("agentapi: usage event has no usage")
		}
	case EventTurnEnd:
		if e.FinishReason == "" {
			return fmt.Errorf("agentapi: turn_end event has no finish reason")
		}
		if !e.FinishReason.Valid() {
			return fmt.Errorf("agentapi: %w: finish reason %q", ErrUnknownKind, e.FinishReason)
		}
	case EventError:
		if e.Err == nil {
			return fmt.Errorf("agentapi: error event has no error")
		}
	case "":
		return fmt.Errorf("agentapi: event has no kind")
	default:
		return fmt.Errorf("agentapi: %w: event kind %q", ErrUnknownKind, e.Kind)
	}
	return nil
}

// PermissionRequest asks the user to approve an action before it runs. It is
// carried by [EventPermission] so that the confirmation UI lives entirely in
// the client, whatever the client happens to be.
type PermissionRequest struct {
	// CallID is the [ToolCall.ID] the request is about.
	CallID string `json:"call_id"`
	// Tool is the tool that wants to act.
	Tool string `json:"tool"`
	// Class is the action class being requested.
	Class ActionClass `json:"class"`
	// Summary is a one-line description for a prompt, e.g. `run "go test ./..."`.
	Summary string `json:"summary"`
	// Detail is the full evidence: a unified diff for a write, the exact
	// command for an execute. May be long; the client decides how to show it.
	Detail string `json:"detail,omitempty"`
	// Path is the file the action targets, when there is one.
	Path string `json:"path,omitempty"`
}

// Validate reports whether the request is well-formed.
func (r *PermissionRequest) Validate() error {
	// Without a CallID a client cannot tell two pending prompts of the same
	// turn apart, and an approval meant for one call could authorise another.
	// Every other correlation key in the package is enforced the same way.
	if r.CallID == "" {
		return fmt.Errorf("agentapi: permission request has no call id")
	}
	if r.Tool == "" {
		return fmt.Errorf("agentapi: permission request has no tool")
	}
	if !r.Class.Valid() {
		return fmt.Errorf("agentapi: permission request has unknown class %q", r.Class)
	}
	if r.Summary == "" {
		return fmt.Errorf("agentapi: permission request has no summary")
	}
	return nil
}

// Compaction reports that older turns were summarised to fit the context
// window. It exists so a client can show the user why earlier messages
// stopped influencing the conversation.
type Compaction struct {
	// MessagesReplaced is how many messages the summary stands in for.
	MessagesReplaced int `json:"messages_replaced"`
	// TokensBefore and TokensAfter are the estimated context sizes around
	// the compaction.
	TokensBefore int `json:"tokens_before,omitempty"`
	TokensAfter  int `json:"tokens_after,omitempty"`
	// Summary is the text that replaced them.
	Summary string `json:"summary,omitempty"`
}
