package tool

import (
	"context"
	"encoding/json"

	"github.com/mrYush/hint/internal/config"
	"github.com/mrYush/hint/pkg/agentapi"
)

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
			res.Content[i].Text = config.Redact(res.Content[i].Text, secrets)
		}
	}
	return res, nil
}
