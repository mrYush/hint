// Package openai implements agentapi.ChatProvider over the OpenAI Chat
// Completions dialect: streaming SSE responses and tool calls, spoken by
// OpenAI itself, Azure's /openai/v1 surface, OpenRouter, vLLM, LM Studio,
// aggregator gateways, and Ollama's /v1 endpoint.
//
// The client is hand-rolled over net/http rather than an SDK on purpose:
// fixture tests replay recorded byte streams, error mapping is explicit, and
// the dialect quirks (tool-call fragments, usage chunks, gateway error
// events) stay visible in this package instead of inside a dependency.
//
// The streaming loop and the index-keyed tool-call accumulator follow the
// shape of Ollama's runner client (llm/llama_server.go, MIT) and the message
// conversion follows opencode's provider (internal/llm/provider/openai.go,
// MIT), both rewritten against the agentapi types.
package openai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/mrYush/hint/internal/config"
	"github.com/mrYush/hint/pkg/agentapi"
)

const (
	// completionsPath is appended to the profile's base URL.
	completionsPath = "/chat/completions"
	// responseHeaderTimeout bounds the wait for the first response byte
	// (time-to-first-token on a cold model can be tens of seconds). The
	// overall request deliberately has no deadline: a healthy stream may
	// run for minutes.
	responseHeaderTimeout = 60 * time.Second
)

// Client is an agentapi.ChatProvider speaking the Chat Completions dialect.
// It holds no per-request state, so one instance is safe for concurrent use.
type Client struct {
	profile     config.Profile
	url         string
	http        *http.Client
	log         func(string)
	idleTimeout time.Duration
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

// WithLogger installs a debug sink for one-line request/response notes. The
// client masks the API key itself; the caller is still responsible for
// redacting anything it appends.
func WithLogger(log func(string)) Option {
	return func(c *Client) { c.log = log }
}

// WithIdleTimeout overrides how long the stream may stay silent between
// chunks before it is treated as stalled. Zero keeps the default.
func WithIdleTimeout(d time.Duration) Option {
	return func(c *Client) { c.idleTimeout = d }
}

// New returns a Client for the given profile.
func New(profile config.Profile, opts ...Option) *Client {
	c := &Client{
		profile: profile,
		url:     completionsURL(profile.BaseURL),
		http:    defaultHTTPClient(),
	}
	for _, o := range opts {
		o(c)
	}
	return c
}

// completionsURL joins the base URL with the completions path, tolerating a
// base that already names it (gateways and copy-pasted configs).
func completionsURL(base string) string {
	base = strings.TrimRight(base, "/")
	if strings.HasSuffix(base, completionsPath) {
		return base
	}
	return base + completionsPath
}

// defaultHTTPClient builds the client used when the caller injects none. The
// zero http.Client would work but has no header timeout, so a provider that
// accepts the connection and never answers would hang the run forever.
func defaultHTTPClient() *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.ResponseHeaderTimeout = responseHeaderTimeout
	return &http.Client{Transport: transport}
}

// Name implements agentapi.ChatProvider.
func (c *Client) Name() string { return c.profile.Name }

// Stream implements agentapi.ChatProvider. It validates and sends the
// request; a non-nil error means the stream never started (bad request,
// transport failure, HTTP error status). Once a channel is returned, all
// further failures arrive on it as a terminal ChatError.
func (c *Client) Stream(ctx context.Context, req agentapi.ChatRequest) (<-chan agentapi.ChatEvent, error) {
	if err := req.Validate(); err != nil {
		return nil, c.attributed(agentapi.WrapError(agentapi.ErrInvalidRequest, err, "invalid chat request"))
	}
	model := req.Model
	if model == "" {
		model = c.profile.Model
	}
	wire, err := convertRequest(model, req)
	if err != nil {
		return nil, c.attributed(err)
	}
	body, err := json.Marshal(wire)
	if err != nil {
		return nil, c.attributed(agentapi.WrapError(agentapi.ErrInvalidRequest, err, "encode request"))
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url, bytes.NewReader(body))
	if err != nil {
		return nil, c.attributed(agentapi.WrapError(agentapi.ErrInvalidRequest, err, "build request"))
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "text/event-stream")
	if c.profile.APIKey != "" {
		// A keyless profile (local Ollama, LM Studio) must not send an empty
		// Bearer header — some servers reject it.
		httpReq.Header.Set("Authorization", "Bearer "+c.profile.APIKey)
	}
	c.debugf("POST %s model=%s key=%s body=%s", c.url, model, config.Mask(c.profile.APIKey), body)

	resp, err := c.http.Do(httpReq)
	if err != nil {
		return nil, c.attributed(transportError(err))
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		defer func() { _ = resp.Body.Close() }()
		e := c.attributed(statusError(resp))
		c.debugf("response %d from %s: %s", resp.StatusCode, c.profile.Name, e.Message)
		return nil, e
	}
	c.debugf("response %d %s", resp.StatusCode, resp.Header.Get("Content-Type"))

	events := make(chan agentapi.ChatEvent)
	go c.readStream(ctx, resp, events)
	return events, nil
}

// attributed stamps the profile name onto an error, whatever shape it
// arrived in.
func (c *Client) attributed(err error) *agentapi.Error {
	var e *agentapi.Error
	if !asAgentError(err, &e) {
		e = agentapi.WrapError(agentapi.KindOf(err), err, "provider failure")
	}
	return e.WithProvider(c.profile.Name)
}

func (c *Client) debugf(format string, args ...any) {
	if c.log != nil {
		c.log(fmt.Sprintf(format, args...))
	}
}
