package openai

import (
	"encoding/json"
	"strings"

	"github.com/mrYush/hint/pkg/agentapi"
)

// Wire types for the OpenAI Chat Completions request. They are unexported:
// the dialect is an implementation detail of this package, and nothing
// outside it may depend on the exact JSON shape.

type wireRequest struct {
	Model         string             `json:"model"`
	Messages      []wireMessage      `json:"messages"`
	Tools         []wireTool         `json:"tools,omitempty"`
	Temperature   *float64           `json:"temperature,omitempty"`
	MaxTokens     int                `json:"max_tokens,omitempty"`
	Stream        bool               `json:"stream"`
	StreamOptions *wireStreamOptions `json:"stream_options,omitempty"`
}

type wireStreamOptions struct {
	IncludeUsage bool `json:"include_usage"`
}

type wireMessage struct {
	Role string `json:"role"`
	// Content is a plain string: every supported dialect accepts it, while
	// the array-of-parts form is rejected by some local servers. Phase 3
	// (images) is when the array form becomes necessary.
	Content    *string        `json:"content,omitempty"`
	ToolCalls  []wireToolCall `json:"tool_calls,omitempty"`
	ToolCallID string         `json:"tool_call_id,omitempty"`
}

type wireTool struct {
	Type     string           `json:"type"`
	Function wireToolFunction `json:"function"`
}

type wireToolFunction struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters"`
}

type wireToolCall struct {
	ID       string               `json:"id"`
	Type     string               `json:"type"`
	Function wireToolCallFunction `json:"function"`
}

type wireToolCallFunction struct {
	Name string `json:"name"`
	// Arguments is a JSON-encoded string on the wire, not a nested object.
	Arguments string `json:"arguments"`
}

// convertRequest maps an agentapi.ChatRequest onto the wire form. model is
// the already-resolved model identifier (request override or profile
// default). It returns *agentapi.Error with ErrInvalidRequest for content the
// dialect cannot carry yet (images, audio) — dropping such parts silently
// would have the model answer a question it never saw.
func convertRequest(model string, req agentapi.ChatRequest) (wireRequest, error) {
	msgs := make([]wireMessage, 0, len(req.Messages))
	for i, m := range req.Messages {
		wm, err := convertMessage(m)
		if err != nil {
			return wireRequest{}, agentapi.WrapError(agentapi.ErrInvalidRequest, err, "messages[%d]", i)
		}
		msgs = append(msgs, wm)
	}
	return wireRequest{
		Model:       model,
		Messages:    msgs,
		Tools:       convertTools(req.Tools),
		Temperature: req.Temperature,
		MaxTokens:   req.MaxTokens,
		Stream:      true,
		// Aggregators that do not implement stream_options simply omit the
		// usage chunk; the ones that reject it are handled by the caller's
		// error mapping.
		StreamOptions: &wireStreamOptions{IncludeUsage: true},
	}, nil
}

func convertMessage(m agentapi.Message) (wireMessage, error) {
	// PartThinking is deliberately not replayed: most /v1 dialects either
	// reject an unknown field or bill for tokens the model ignores. Media
	// parts fail loudly until Phase 3 wires them.
	for _, p := range m.Content {
		if p.Kind == agentapi.PartImage || p.Kind == agentapi.PartAudio {
			return wireMessage{}, agentapi.NewError(agentapi.ErrInvalidRequest,
				"%s content is not supported by the chat completions client yet", p.Kind)
		}
	}
	wm := wireMessage{
		Role:       string(m.Role),
		ToolCallID: m.ToolCallID,
	}
	text := m.Text()
	switch {
	case m.Role == agentapi.RoleTool:
		// A tool message must always carry content on the wire, even when
		// the tool succeeded silently: several dialects reject a tool
		// message with a null content field.
		wm.Content = &text
	case text != "" || len(m.ToolCalls) == 0:
		wm.Content = &text
	default:
		// An assistant message that only calls tools sends no content field.
	}
	for _, c := range m.ToolCalls {
		args := string(c.Arguments)
		if strings.TrimSpace(args) == "" {
			args = "{}"
		}
		wm.ToolCalls = append(wm.ToolCalls, wireToolCall{
			ID:   c.ID,
			Type: "function",
			Function: wireToolCallFunction{
				Name:      c.Name,
				Arguments: args,
			},
		})
	}
	return wm, nil
}

func convertTools(tools []agentapi.ToolSchema) []wireTool {
	if len(tools) == 0 {
		return nil
	}
	out := make([]wireTool, 0, len(tools))
	for _, t := range tools {
		out = append(out, wireTool{
			Type: "function",
			Function: wireToolFunction{
				Name:        t.Name,
				Description: t.Description,
				Parameters:  t.InputSchema,
			},
		})
	}
	return out
}
