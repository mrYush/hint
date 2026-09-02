// Package ollama is a minimal client for Ollama's native API. Chat traffic
// does not go through here — Ollama speaks the Chat Completions dialect on
// /v1 and is served by internal/provider/openai. This package covers the two
// endpoints that dialect does not expose: readiness (GET /api/version) and
// installed-model listing (GET /api/tags).
package ollama

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/mrYush/hint/pkg/agentapi"
)

// requestTimeout bounds the native calls. Unlike chat they are tiny local
// requests; a second of silence already means the daemon is not there.
const requestTimeout = 5 * time.Second

// Client talks to a local Ollama daemon.
type Client struct {
	baseURL string
	http    *http.Client
}

// Option configures a Client.
type Option func(*Client)

// WithHTTPClient replaces the default HTTP client; tests inject an
// httptest-backed one.
func WithHTTPClient(h *http.Client) Option {
	return func(c *Client) {
		if h != nil {
			c.http = h
		}
	}
}

// New returns a Client for the daemon behind baseURL. The profile's base URL
// names the OpenAI-compatible surface (…:11434/v1); the native API lives at
// the root, so a trailing /v1 is stripped.
func New(baseURL string, opts ...Option) *Client {
	c := &Client{
		baseURL: nativeBaseURL(baseURL),
		http:    &http.Client{Timeout: requestTimeout},
	}
	for _, o := range opts {
		o(c)
	}
	return c
}

func nativeBaseURL(base string) string {
	base = strings.TrimRight(base, "/")
	base = strings.TrimSuffix(base, "/v1")
	return strings.TrimRight(base, "/")
}

// Ready reports whether the daemon answers. A failure is an
// *agentapi.Error of kind ErrUnavailable (or ErrCanceled/ErrTimeout when the
// context ended first), so the router can show one clear message instead of
// a second opaque transport error.
func (c *Client) Ready(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/api/version", nil)
	if err != nil {
		return agentapi.WrapError(agentapi.ErrUnavailable, err, "ollama: build version request")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		kind := agentapi.KindOf(err)
		if kind == agentapi.ErrUnknown || kind == agentapi.ErrNetwork {
			kind = agentapi.ErrUnavailable
		}
		return agentapi.WrapError(kind, err, "ollama is not reachable at %s", c.baseURL)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return agentapi.NewError(agentapi.ErrUnavailable,
			"ollama at %s answered version check with HTTP %d", c.baseURL, resp.StatusCode)
	}
	return nil
}

// Model is one locally installed model as reported by /api/tags.
type Model struct {
	// Name is the model tag usable in a profile, e.g. "qwen2.5:7b".
	Name string `json:"name"`
	// SizeBytes is the on-disk size.
	SizeBytes int64 `json:"size"`
	// ModifiedAt is when the model was last pulled or created.
	ModifiedAt time.Time `json:"modified_at"`
}

// List returns the locally installed models. No CLI command calls it yet
// (that surface is WP0.9's `hint models`); it ships with the client so the
// native API is covered in one place.
func (c *Client) List(ctx context.Context) ([]Model, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/api/tags", nil)
	if err != nil {
		return nil, agentapi.WrapError(agentapi.ErrUnavailable, err, "ollama: build tags request")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		kind := agentapi.KindOf(err)
		if kind == agentapi.ErrUnknown || kind == agentapi.ErrNetwork {
			kind = agentapi.ErrUnavailable
		}
		return nil, agentapi.WrapError(kind, err, "ollama is not reachable at %s", c.baseURL)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, agentapi.NewError(agentapi.ErrUnavailable,
			"ollama at %s answered tags with HTTP %d", c.baseURL, resp.StatusCode)
	}
	var payload struct {
		Models []Model `json:"models"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, agentapi.WrapError(agentapi.ErrUnavailable, err, "ollama: decode tags response")
	}
	return payload.Models, nil
}

// String aids debug output.
func (m Model) String() string {
	return fmt.Sprintf("%s (%d MB)", m.Name, m.SizeBytes/(1024*1024))
}
