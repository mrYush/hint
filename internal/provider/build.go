// Package provider composes the concrete provider clients into the single
// agentapi.ChatProvider the CLI consumes. It is the composition root of the
// provider layer: cmd/hint calls Chat or ForProfile and never switches on a
// profile's Kind itself.
package provider

import (
	"context"
	"io"

	"github.com/mrYush/hint/internal/config"
	"github.com/mrYush/hint/internal/provider/ollama"
	"github.com/mrYush/hint/internal/provider/openai"
	"github.com/mrYush/hint/internal/provider/router"
	"github.com/mrYush/hint/pkg/agentapi"
)

// Chat builds the ChatProvider for the resolved configuration: the default
// profile, wrapped in a failover router when a fallback profile is
// configured. notice is where the router announces a failover (the CLI
// passes os.Stderr; nil discards). debugLog, when non-nil, receives one-line
// redacted request/response notes from the underlying clients.
func Chat(cfg *config.Config, notice io.Writer, debugLog func(string)) (agentapi.ChatProvider, error) {
	chat, _, err := ForProfile(cfg, cfg.DefaultProvider, "", notice, debugLog)
	return chat, err
}

// ForProfile builds the provider for the profile named name, with model
// in place of the profile's own when it is not empty. The configured
// fallback profile stands behind it unless it is the same profile. The
// profile actually used is returned with the provider, so the caller can
// read its context window. An unknown or unusable profile (no key, no
// model) is an error from config.
func ForProfile(cfg *config.Config, name, model string, notice io.Writer, debugLog func(string)) (agentapi.ChatProvider, config.Profile, error) {
	role := "default"
	if name != cfg.DefaultProvider {
		role = "selected"
	}
	primaryProfile, err := cfg.Usable(name, role)
	if err != nil {
		return nil, config.Profile{}, err
	}
	if model != "" {
		primaryProfile.Model = model
	}
	primary := forProfile(primaryProfile, debugLog)

	opts := []router.Option{router.WithNotice(notice)}
	if fallbackProfile, ok := cfg.Fallback(); ok && fallbackProfile.Name != primaryProfile.Name {
		opts = append(opts, router.WithFallback(forProfile(fallbackProfile, debugLog)))
	}
	return router.New(primary, opts...), primaryProfile, nil
}

func forProfile(p config.Profile, debugLog func(string)) agentapi.ChatProvider {
	var opts []openai.Option
	if debugLog != nil {
		opts = append(opts, openai.WithLogger(debugLog))
	}
	chat := openai.New(p, opts...)
	if p.Kind == config.KindOllama {
		return ollamaProvider{
			ChatProvider: chat,
			native:       ollama.New(p.BaseURL),
		}
	}
	return chat
}

type ollamaProvider struct {
	agentapi.ChatProvider
	native *ollama.Client
}

func (o ollamaProvider) Ready(ctx context.Context) error {
	return o.native.Ready(ctx)
}
