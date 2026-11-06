package tool

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"

	"github.com/mrYush/hint/internal/config"
	"github.com/mrYush/hint/pkg/agentapi"
)

// StructuredOutput opts a tool into JSON string-value redaction. Protocol keys
// must be fixed, trusted schema names rather than user-controlled content.
type StructuredOutput interface {
	StructuredOutput() bool
}

// Redacting wraps t so that every result it produces has the configured
// secrets replaced with "***" before anyone else sees it (WP1.7). The
// agent loop is the only reader of a tool's result, and it hands the
// result to the model, to the event stream and to the session file; one
// decorator at the registry covers all three. secrets is a function: the
// set changes when the configuration is reloaded.
func Redacting(t agentapi.Tool, secrets func() []string) agentapi.Tool {
	return &redacting{Tool: t, secrets: secrets}
}

type redacting struct {
	agentapi.Tool
	secrets func() []string
}

// Unwrap returns the decorated tool.
func (r *redacting) Unwrap() agentapi.Tool { return r.Tool }

// Run implements agentapi.Tool.
func (r *redacting) Run(ctx context.Context, callID string, args json.RawMessage) (agentapi.ToolResult, error) {
	res, err := r.Tool.Run(ctx, callID, args)
	if err != nil {
		return res, err
	}
	if r.secrets == nil {
		return res, nil
	}
	secrets := r.secrets()
	if len(secrets) == 0 {
		return res, nil
	}
	for i := range res.Content {
		if res.Content[i].Text != "" {
			text := res.Content[i].Text
			if structuredOutput(r.Tool) {
				if redacted, ok := redactJSON(text, secrets); ok {
					res.Content[i].Text = redacted
					continue
				}
			}
			res.Content[i].Text = config.Redact(text, secrets)
		}
	}
	return res, nil
}

func structuredOutput(t agentapi.Tool) bool {
	// A finite decorator chain prevents a broken third-party Unwrap from
	// making the redaction boundary hang indefinitely.
	for range 32 {
		if marker, ok := t.(StructuredOutput); ok {
			return marker.StructuredOutput()
		}
		wrapper, ok := t.(interface{ Unwrap() agentapi.Tool })
		if !ok {
			return false
		}
		t = wrapper.Unwrap()
	}
	return false
}

func redactJSON(text string, secrets []string) (string, bool) {
	d := json.NewDecoder(bytes.NewBufferString(text))
	d.UseNumber()
	var value any
	if err := d.Decode(&value); err != nil {
		return "", false
	}
	if err := d.Decode(new(any)); !errors.Is(err, io.EOF) {
		return "", false
	}
	data, err := json.Marshal(redactStrings(value, secrets))
	return string(data), err == nil
}

func redactStrings(value any, secrets []string) any {
	switch v := value.(type) {
	case string:
		return config.Redact(v, secrets)
	case []any:
		for i := range v {
			v[i] = redactStrings(v[i], secrets)
		}
	case map[string]any:
		for key, child := range v {
			v[key] = redactStrings(child, secrets)
		}
	}
	return value
}
