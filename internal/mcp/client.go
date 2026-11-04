package mcp

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"slices"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// ClientInfo identifies hint to a server. The MCP revision calls it the
// client implementation; it is advertised in _meta or the initialize
// handshake, whichever the SDK negotiates.
type ClientInfo struct {
	Name    string
	Version string
}

// DialFunc opens a session to one server. The default dials the real
// transport; tests inject an in-memory or scripted one.
type DialFunc func(ctx context.Context, cfg ServerConfig) (*mcp.ClientSession, error)

// connector builds the default transport for a server.
type connector struct {
	info    ClientInfo
	environ []string
	stderr  io.Writer
}

func (c *connector) dial(ctx context.Context, cfg ServerConfig) (*mcp.ClientSession, error) {
	name := c.info.Name
	if name == "" {
		name = "hint"
	}
	version := c.info.Version
	if version == "" {
		version = "dev"
	}
	client := mcp.NewClient(
		&mcp.Implementation{Name: name, Version: version},
		// WP2.1 implements no roots, sampling or elicitation. The SDK's
		// default advertises roots; an empty capability set says what is
		// true instead of inviting a request hint cannot answer.
		&mcp.ClientOptions{Capabilities: &mcp.ClientCapabilities{}},
	)
	var transport mcp.Transport
	switch cfg.Transport {
	case TransportStdio:
		cmd := exec.Command(cfg.Command, cfg.Args...)
		base := c.environ
		if cfg.Project {
			// A project server was chosen by whatever the repository
			// contains; it must not read the developer's variables even
			// when the config file did not declare them. Only the
			// variables a process needs to start are inherited.
			base = projectEnviron(base)
		}
		cmd.Env = mergeEnv(base, cfg.Env)
		cmd.Stderr = c.stderr
		transport = &mcp.CommandTransport{
			Command: cmd,
			// Shutdown closes stdin and waits for the child; after this
			// grace period the SDK terminates it.
			TerminateDuration: 5 * time.Second,
		}
	case TransportHTTP:
		transport = &mcp.StreamableClientTransport{
			Endpoint: cfg.URL,
			// The 2026-07-28 revision has no GET stream, and hint does
			// not consume notifications; not opening one keeps a modern
			// server from holding a connection per session.
			DisableStandaloneSSE: true,
			HTTPClient:           newHTTPClient(cfg),
		}
	default:
		return nil, fmt.Errorf("mcp: server %q: unknown transport %q", cfg.Name, cfg.Transport)
	}
	sess, err := client.Connect(ctx, transport, nil)
	if err != nil {
		return nil, fmt.Errorf("mcp: server %q: connect: %w", cfg.Name, err)
	}
	return sess, nil
}

// projectEnviron keeps only the variables a subprocess needs to start.
// It is what a project-added server inherits instead of the developer's
// whole environment.
func projectEnviron(base []string) []string {
	if base == nil {
		base = os.Environ()
	}
	keep := map[string]bool{
		"PATH": true, "HOME": true, "USERPROFILE": true, "TMPDIR": true,
		"TMP": true, "TEMP": true, "LANG": true, "LC_ALL": true, "LC_CTYPE": true,
		"TERM": true, "SYSTEMROOT": true, "COMSPEC": true, "PATHEXT": true,
		"XDG_RUNTIME_DIR": true,
	}
	out := make([]string, 0, len(keep))
	for _, kv := range base {
		name, _, _ := strings.Cut(kv, "=")
		upper := strings.ToUpper(name)
		if keep[upper] || strings.HasPrefix(upper, "LC_") {
			out = append(out, kv)
		}
	}
	return out
}

// mergeEnv returns base with extra overlaid, removing a base entry when
// extra declares the same variable. base is the caller's already-filtered
// environment: it carries PATH and HOME but not hint's API keys.
func mergeEnv(base []string, extra map[string]string) []string {
	if base == nil {
		base = os.Environ()
	}
	out := make([]string, 0, len(base)+len(extra))
	overridden := make(map[string]bool, len(extra))
	for name := range extra {
		overridden[strings.ToUpper(name)] = true
	}
	for _, kv := range base {
		name, _, _ := strings.Cut(kv, "=")
		if overridden[strings.ToUpper(name)] {
			continue
		}
		out = append(out, kv)
	}
	names := make([]string, 0, len(extra))
	for name := range extra {
		names = append(names, name)
	}
	slices.Sort(names)
	for _, name := range names {
		out = append(out, name+"="+extra[name])
	}
	return out
}

// newHTTPClient builds the HTTP client of one server. The transport adds
// the configured headers only on the configured origin, and the redirect
// policy refuses any other origin outright: a 302 must not carry a bearer
// token to a host the user did not configure.
func newHTTPClient(cfg ServerConfig) *http.Client {
	endpoint, err := url.Parse(cfg.URL)
	if err != nil {
		// Validate rejects this before a client is built; the zero value
		// only makes a later mistake fail closed.
		return &http.Client{Transport: originHeaders{headers: cfg.Headers}}
	}
	base, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		base = &http.Transport{}
	}
	return &http.Client{
		Transport: originHeaders{
			base:    base.Clone(),
			scheme:  endpoint.Scheme,
			host:    endpoint.Host,
			headers: cfg.Headers,
		},
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if req.URL.Scheme != endpoint.Scheme || req.URL.Host != endpoint.Host {
				return fmt.Errorf("mcp: refusing redirect from %s to %s: the configured credential stays on the configured origin", endpoint.Host, req.URL.Host)
			}
			return nil
		},
	}
}

// originHeaders injects a server's static headers only when the request
// goes to the configured scheme and host. Running after CheckRedirect is
// too late to rely on stripping alone, so the transport itself decides.
type originHeaders struct {
	base    *http.Transport
	scheme  string
	host    string
	headers map[string]string
}

func (t originHeaders) RoundTrip(req *http.Request) (*http.Response, error) {
	clone := req.Clone(req.Context())
	clone.Header = req.Header.Clone()
	same := req.URL.Scheme == t.scheme && req.URL.Host == t.host
	for name := range t.headers {
		clone.Header.Del(name)
	}
	if same {
		for name, value := range t.headers {
			clone.Header.Set(name, value)
		}
	}
	base := t.base
	if base == nil {
		base = &http.Transport{}
	}
	return base.RoundTrip(clone)
}
