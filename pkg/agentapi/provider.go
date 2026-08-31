package agentapi

import (
	"context"
	"io"
)

// ChatProvider produces a model response as a stream of [ChatEvent].
//
// It is the only modality implemented in Phase 0. An implementation must:
//
//   - return a non-nil error only for a failure to start the stream; once a
//     channel is returned, failures arrive as a [ChatError] event on it;
//   - close the channel after exactly one terminal event ([ChatEvent.Terminal]);
//   - stop and close the channel when ctx is cancelled, emitting a terminal
//     event of kind ChatDone with [FinishCanceled] or ChatError with
//     [ErrCanceled];
//   - assemble streamed tool-call fragments so that every [ChatToolCall]
//     event carries a complete, JSON-valid call.
//
// Implementations are expected to be safe for concurrent use: the router
// holds one instance per profile and may run turns in parallel.
type ChatProvider interface {
	// Name is the configured profile name, used in user-facing messages such
	// as the router's fallback notice.
	Name() string
	// Stream sends req and returns the response stream.
	Stream(ctx context.Context, req ChatRequest) (<-chan ChatEvent, error)
}

// ChatRequest is one call to a [ChatProvider].
type ChatRequest struct {
	// Model is the provider-specific model identifier. Empty means the
	// profile's configured default.
	Model string `json:"model,omitempty"`
	// Messages is the conversation so far, oldest first.
	Messages []Message `json:"messages"`
	// Tools are the tools the model may call. Empty disables tool calling.
	Tools []ToolSchema `json:"tools,omitempty"`
	// Temperature overrides the provider default when non-nil. It is a
	// pointer because 0 is a meaningful value: with a plain float64 there
	// would be no way to tell "deterministic" from "unset".
	Temperature *float64 `json:"temperature,omitempty"`
	// MaxTokens caps the response length. Zero means the provider default.
	MaxTokens int `json:"max_tokens,omitempty"`
}

// Validate reports whether the request is well-formed enough to send.
func (r ChatRequest) Validate() error {
	if len(r.Messages) == 0 {
		return NewError(ErrInvalidRequest, "chat request has no messages")
	}
	for i, m := range r.Messages {
		if err := m.Validate(); err != nil {
			return WrapError(ErrInvalidRequest, err, "messages[%d]", i)
		}
	}
	seen := make(map[string]struct{}, len(r.Tools))
	for i, t := range r.Tools {
		if err := t.Validate(); err != nil {
			return WrapError(ErrInvalidRequest, err, "tools[%d]", i)
		}
		if _, dup := seen[t.Name]; dup {
			return NewError(ErrInvalidRequest, "tools[%d]: duplicate tool name %q", i, t.Name)
		}
		seen[t.Name] = struct{}{}
	}
	return nil
}

// Usage is token accounting for one provider call.
type Usage struct {
	// InputTokens is the prompt size the provider billed for.
	InputTokens int64 `json:"input_tokens,omitempty"`
	// OutputTokens is the generated size.
	OutputTokens int64 `json:"output_tokens,omitempty"`
	// CachedInputTokens is the part of the input served from a prompt cache,
	// when the provider reports it. It is a subset of InputTokens.
	CachedInputTokens int64 `json:"cached_input_tokens,omitempty"`
	// ReasoningTokens is the part of the output spent on a reasoning trace,
	// when the provider reports it. It is a subset of OutputTokens.
	ReasoningTokens int64 `json:"reasoning_tokens,omitempty"`
}

// Total returns the tokens that count against the context window.
func (u Usage) Total() int64 { return u.InputTokens + u.OutputTokens }

// Add returns the sum of two usages, for accumulating across the calls of a
// turn.
func (u Usage) Add(o Usage) Usage {
	return Usage{
		InputTokens:       u.InputTokens + o.InputTokens,
		OutputTokens:      u.OutputTokens + o.OutputTokens,
		CachedInputTokens: u.CachedInputTokens + o.CachedInputTokens,
		ReasoningTokens:   u.ReasoningTokens + o.ReasoningTokens,
	}
}

// The interfaces below are declared as part of the contract but have no
// implementation in Phase 0. They are not placeholders to be filled with
// empty stubs: until the phase that implements one arrives, there is simply
// no constructor returning it. Declaring them now fixes the shape that
// internal/provider, the config profiles, and the RPC surface must fit.

// Embedder turns text into vectors. Implemented in Phase 2 for the memory
// index, against /v1/embeddings and Ollama.
type Embedder interface {
	// Embed returns one vector per input text, in the same order.
	Embed(ctx context.Context, texts []string) ([][]float32, error)
}

// Transcriber turns speech into text. Implemented in Phase 3, against
// /v1/audio/transcriptions and whisper.cpp.
type Transcriber interface {
	// Transcribe reads an audio stream and returns its transcript.
	Transcribe(ctx context.Context, audio io.Reader, opts STTOpts) (string, error)
}

// STTOpts are the options of a [Transcriber.Transcribe] call.
type STTOpts struct {
	// Model is the provider-specific model identifier. Empty means the
	// profile's default.
	Model string `json:"model,omitempty"`
	// Language is a BCP-47 tag hinting the spoken language. Empty means
	// auto-detect.
	Language string `json:"language,omitempty"`
	// Prompt biases recognition toward expected vocabulary.
	Prompt string `json:"prompt,omitempty"`
	// MediaType is the IANA media type of the audio stream, e.g. "audio/wav".
	MediaType string `json:"media_type,omitempty"`
}

// Speaker turns text into speech. Implemented in Phase 3, against
// /v1/audio/speech and piper.
type Speaker interface {
	// Speak synthesises text and returns the audio stream. The caller closes it.
	Speak(ctx context.Context, text string, opts TTSOpts) (io.ReadCloser, error)
}

// TTSOpts are the options of a [Speaker.Speak] call.
type TTSOpts struct {
	// Model is the provider-specific model identifier.
	Model string `json:"model,omitempty"`
	// Voice is the provider-specific voice identifier.
	Voice string `json:"voice,omitempty"`
	// Format is the requested container, e.g. "mp3", "wav", "pcm".
	Format string `json:"format,omitempty"`
	// Speed is a playback-rate multiplier; 0 means the provider default.
	Speed float64 `json:"speed,omitempty"`
}

// VisionProvider describes images. Implemented in Phase 3 for the camera
// scenarios, against chat models with image input and Ollama VLMs.
//
// It is separate from [ChatProvider] even though most implementations will
// route through the same endpoint: a client that only needs "what is in this
// picture" should not have to build a conversation.
type VisionProvider interface {
	// Describe answers prompt about the given images.
	Describe(ctx context.Context, images []Image, prompt string) (string, error)
}
