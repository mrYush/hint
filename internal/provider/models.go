package provider

import (
	"context"
	"time"

	"github.com/mrYush/hint/internal/config"
	"github.com/mrYush/hint/internal/provider/ollama"
	"github.com/mrYush/hint/internal/provider/openai"
)

// Model is one model a profile's endpoint serves, in the shape `hint
// models` prints. The two clients report different things — Ollama knows
// sizes and pull dates, the Chat Completions dialect an owner and a
// creation date — so this is their union with zero values for what a
// source does not know.
type Model struct {
	// Name is usable as the profile's model.
	Name string
	// SizeBytes is the on-disk size (Ollama), or 0.
	SizeBytes int64
	// Modified is when the model was pulled (Ollama) or created (dialect),
	// or zero.
	Modified time.Time
	// Owner is the dialect's owned_by label, or "".
	Owner string
}

// Models lists what the profile's endpoint serves. An ollama profile is
// asked over the native API, which knows sizes; every other kind over
// GET /models of the dialect.
func Models(ctx context.Context, p config.Profile) ([]Model, error) {
	if p.Kind == config.KindOllama {
		list, err := ollama.New(p.BaseURL).List(ctx)
		if err != nil {
			return nil, err
		}
		out := make([]Model, 0, len(list))
		for _, m := range list {
			out = append(out, Model{Name: m.Name, SizeBytes: m.SizeBytes, Modified: m.ModifiedAt})
		}
		return out, nil
	}
	list, err := openai.New(p).Models(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Model, 0, len(list))
	for _, m := range list {
		out = append(out, Model{Name: m.ID, Modified: m.Created, Owner: m.OwnedBy})
	}
	return out, nil
}
