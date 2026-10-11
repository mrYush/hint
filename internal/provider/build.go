// Package provider composes the concrete provider clients into the single
// agentapi.ChatProvider the CLI consumes. It is the composition root of the
// provider layer: cmd/hint calls Chat and never switches on a profile's Kind
// itself.
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
	primaryProfile, err := cfg.Default()
	if err != nil {
		return nil, err
	}
	primary := forProfile(primaryProfile, debugLog)

	opts := []router.Option{router.WithNotice(notice)}
	if fallbackProfile, ok := cfg.Fallback(); ok {
		opts = append(opts, router.WithFallback(forProfile(fallbackProfile, debugLog)))
	}
	return router.New(primary, opts...), nil
}

// forProfile builds the provider for one profile. Chat for every kind speaks
// the Chat Completions dialect; an ollama profile is additionally wrapped so
// the router can preflight the daemon over the native API before failing over
// to it.
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

// ollamaProvider decorates the Chat Completions client with the native
// readiness probe, satisfying the router's ReadyChecker interface.
type ollamaProvider struct {
	agentapi.ChatProvider
	native *ollama.Client
}

// Ready implements router.ReadyChecker.
func (o ollamaProvider) Ready(ctx context.Context) error {
	return o.native.Ready(ctx)
}
