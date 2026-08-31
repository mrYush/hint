package agentapi

import (
	"fmt"
	"strings"
)

// Role identifies who authored a [Message].
type Role string

// The roles a conversation is built from. They map onto the roles of the
// OpenAI chat-completions wire format, which every supported provider speaks.
const (
	// RoleSystem carries instructions to the model: the system prompt,
	// project context (HINT.md), and compaction summaries.
	RoleSystem Role = "system"
	// RoleUser carries input from the human.
	RoleUser Role = "user"
	// RoleAssistant carries the model's own output, including the tool calls
	// it decided to make.
	RoleAssistant Role = "assistant"
	// RoleTool carries the result of executing a tool call. A message with
	// this role must set ToolCallID.
	RoleTool Role = "tool"
)

// Valid reports whether r is one of the defined roles.
func (r Role) Valid() bool {
	switch r {
	case RoleSystem, RoleUser, RoleAssistant, RoleTool:
		return true
	default:
		return false
	}
}

// PartKind discriminates the payload of a [ContentPart].
type PartKind string

// The content kinds a message may carry. Only PartText and PartThinking are
// produced in Phase 0; the media kinds are part of the contract from day one
// so that multimodality (Phase 3) does not have to reshape [Message].
const (
	// PartText is plain text, held in ContentPart.Text.
	PartText PartKind = "text"
	// PartThinking is the model's reasoning trace, held in ContentPart.Text.
	// It is kept separate from PartText because it is displayed differently,
	// is dropped first during compaction, and some providers require it to be
	// replayed verbatim.
	PartThinking PartKind = "thinking"
	// PartImage is an image, held in ContentPart.Image.
	PartImage PartKind = "image"
	// PartAudio is an audio clip, held in ContentPart.Audio.
	PartAudio PartKind = "audio"
)

// ContentPart is one piece of a message's content.
//
// It is a tagged union: Kind selects which of the remaining fields is
// meaningful, and the others are zero. The alternative — a Go interface with
// one struct per kind — would need hand-written JSON marshalling, which this
// contract cannot afford: the same values are persisted in session files and
// shipped over RPC. Use [ContentPart.Validate] to reject a value whose Kind
// and payload disagree.
type ContentPart struct {
	// Kind selects the payload field. Always set.
	Kind PartKind `json:"kind"`
	// Text is the payload of PartText and PartThinking.
	Text string `json:"text,omitempty"`
	// Image is the payload of PartImage.
	Image *Image `json:"image,omitempty"`
	// Audio is the payload of PartAudio.
	Audio *Audio `json:"audio,omitempty"`
}

// Text returns a PartText content part.
func Text(s string) ContentPart {
	return ContentPart{Kind: PartText, Text: s}
}

// Thinking returns a PartThinking content part.
func Thinking(s string) ContentPart {
	return ContentPart{Kind: PartThinking, Text: s}
}

// ImagePart returns a PartImage content part.
func ImagePart(img Image) ContentPart {
	return ContentPart{Kind: PartImage, Image: &img}
}

// AudioPart returns a PartAudio content part.
func AudioPart(a Audio) ContentPart {
	return ContentPart{Kind: PartAudio, Audio: &a}
}

// Validate reports whether the part's payload matches its Kind. It returns an
// error for an unknown Kind, for a media kind with a nil payload, and for a
// part that carries a payload belonging to a different kind.
func (p ContentPart) Validate() error {
	switch p.Kind {
	case PartText, PartThinking:
		if p.Image != nil || p.Audio != nil {
			return fmt.Errorf("agentapi: %s part carries a media payload", p.Kind)
		}
	case PartImage:
		if p.Image == nil {
			return fmt.Errorf("agentapi: image part has no image")
		}
		if p.Audio != nil {
			return fmt.Errorf("agentapi: image part carries an audio payload")
		}
		if err := p.Image.Validate(); err != nil {
			return err
		}
	case PartAudio:
		if p.Audio == nil {
			return fmt.Errorf("agentapi: audio part has no audio")
		}
		if p.Image != nil {
			return fmt.Errorf("agentapi: audio part carries an image payload")
		}
		if err := p.Audio.Validate(); err != nil {
			return err
		}
	case "":
		return fmt.Errorf("agentapi: content part has no kind")
	default:
		return fmt.Errorf("agentapi: %w: content part kind %q", ErrUnknownKind, p.Kind)
	}
	return nil
}

// Image is an image referenced by a URL or carried inline.
//
// Exactly one of URL and Data is set. Inline data is base64-encoded by
// encoding/json automatically; MediaType is then required, because providers
// need it to build a data: URL or a typed upload.
type Image struct {
	// URL locates the image. Mutually exclusive with Data. Both remote URLs
	// and data: URLs are accepted.
	URL string `json:"url,omitempty"`
	// Data holds the image bytes inline. Mutually exclusive with URL.
	Data []byte `json:"data,omitempty"`
	// MediaType is the IANA media type, e.g. "image/png". Required with Data.
	MediaType string `json:"media_type,omitempty"`
	// Detail is an optional provider hint about the resolution to process the
	// image at ("low", "high", "auto"). Providers that do not support it
	// ignore it.
	Detail string `json:"detail,omitempty"`
}

// Validate reports whether exactly one source is set and, for inline data,
// that a media type is present.
func (i *Image) Validate() error {
	return validateMediaSource("image", i.URL, i.Data, i.MediaType)
}

// Audio is an audio clip referenced by a URL or carried inline. The rules
// mirror [Image].
type Audio struct {
	// URL locates the clip. Mutually exclusive with Data.
	URL string `json:"url,omitempty"`
	// Data holds the audio bytes inline. Mutually exclusive with URL.
	Data []byte `json:"data,omitempty"`
	// MediaType is the IANA media type, e.g. "audio/wav". Required with Data.
	MediaType string `json:"media_type,omitempty"`
}

// Validate reports whether exactly one source is set and, for inline data,
// that a media type is present.
func (a *Audio) Validate() error {
	return validateMediaSource("audio", a.URL, a.Data, a.MediaType)
}

// validateMediaSource holds the url/data/media-type rule shared by [Image] and
// [Audio]. The rule is one paragraph of the contract, so it lives in one place
// rather than being copied per media kind as Phase 3 adds more of them.
func validateMediaSource(noun, url string, data []byte, mediaType string) error {
	switch {
	case url == "" && len(data) == 0:
		return fmt.Errorf("agentapi: %s has neither url nor data", noun)
	case url != "" && len(data) > 0:
		return fmt.Errorf("agentapi: %s has both url and data", noun)
	case len(data) > 0 && mediaType == "":
		return fmt.Errorf("agentapi: inline %s has no media type", noun)
	}
	return nil
}

// Message is one entry of a conversation, in the form a provider is given it.
//
// It deliberately carries no identifier and no timestamp: those belong to the
// session record that wraps a message, not to the value sent upstream.
type Message struct {
	// Role identifies the author. Always set.
	Role Role `json:"role"`
	// Content is the message body. It may be empty on an assistant message
	// that only makes tool calls.
	Content []ContentPart `json:"content,omitempty"`
	// ToolCalls are the calls an assistant message requests. Only meaningful
	// on RoleAssistant.
	ToolCalls []ToolCall `json:"tool_calls,omitempty"`
	// ToolCallID links a RoleTool message to the ToolCall it answers.
	// Required on RoleTool, empty otherwise.
	ToolCallID string `json:"tool_call_id,omitempty"`
}

// SystemMessage returns a text message with RoleSystem.
func SystemMessage(text string) Message {
	return Message{Role: RoleSystem, Content: []ContentPart{Text(text)}}
}

// UserMessage returns a text message with RoleUser.
func UserMessage(text string) Message {
	return Message{Role: RoleUser, Content: []ContentPart{Text(text)}}
}

// AssistantMessage returns a text message with RoleAssistant.
func AssistantMessage(text string) Message {
	return Message{Role: RoleAssistant, Content: []ContentPart{Text(text)}}
}

// Text returns the message's PartText parts joined by newlines. Thinking and
// media parts are skipped, so the result is what a plain-text renderer or a
// log line should show.
func (m Message) Text() string { return m.joinParts(PartText) }

// Thinking returns the message's PartThinking parts joined by newlines.
func (m Message) Thinking() string { return m.joinParts(PartThinking) }

// joinParts concatenates the text of every part of the given kind, separated
// by newlines.
//
// It counts the parts it has written rather than testing the buffer length:
// a streaming provider can close a text part empty, and keying the separator
// off "the buffer is still empty" would silently drop the line break after
// one, making Text() disagree with its own documentation.
func (m Message) joinParts(kind PartKind) string {
	var b strings.Builder
	written := 0
	for _, p := range m.Content {
		if p.Kind != kind {
			continue
		}
		if written > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(p.Text)
		written++
	}
	return b.String()
}

// Validate reports whether the message is well-formed: a known role, valid
// parts, and role-specific fields used only where they belong.
func (m Message) Validate() error {
	if !m.Role.Valid() {
		return fmt.Errorf("agentapi: unknown role %q", m.Role)
	}
	for i, p := range m.Content {
		if err := p.Validate(); err != nil {
			return fmt.Errorf("agentapi: content[%d]: %w", i, err)
		}
	}
	if m.Role != RoleAssistant && len(m.ToolCalls) > 0 {
		return fmt.Errorf("agentapi: %s message carries tool calls", m.Role)
	}
	for i, c := range m.ToolCalls {
		if err := c.Validate(); err != nil {
			return fmt.Errorf("agentapi: tool_calls[%d]: %w", i, err)
		}
	}
	if m.Role == RoleTool && m.ToolCallID == "" {
		return fmt.Errorf("agentapi: tool message has no tool_call_id")
	}
	if m.Role != RoleTool && m.ToolCallID != "" {
		return fmt.Errorf("agentapi: %s message has a tool_call_id", m.Role)
	}
	// A tool message may legitimately be empty: a tool that succeeds silently
	// (write_file, todo, a quiet command) produces no output, and the message
	// is still identified by its ToolCallID. Requiring content here would make
	// ToolResult.Message() build a message that ToolResult.Validate accepts
	// but Message.Validate rejects.
	if m.Role != RoleTool && len(m.Content) == 0 && len(m.ToolCalls) == 0 {
		return fmt.Errorf("agentapi: %s message is empty", m.Role)
	}
	return nil
}
