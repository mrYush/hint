package mcp

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/mrYush/hint/pkg/agentapi"
)

// Transport is how a server is reached.
type Transport string

const (
	// TransportStdio is a subprocess that speaks JSON-RPC on stdin and
	// stdout.
	TransportStdio Transport = "stdio"
	// TransportHTTP is the MCP Streamable HTTP transport.
	TransportHTTP Transport = "http"
)

// DefaultTimeout bounds one tool call when the server does not set
// `timeout`. It matches [tool.DefaultLimits], which the adapter still
// applies; the two are the same value so a caller sees one number.
const DefaultTimeout = 30 * time.Second

// DefaultConnectTimeout bounds one connection attempt. A server that
// never answers the handshake costs this much at startup and no more.
const DefaultConnectTimeout = 30 * time.Second

// DefaultListTimeout bounds one tools/list exchange, pagination included.
// A server that completes the handshake and then hangs on tools/list must
// not hold the run at startup.
const DefaultListTimeout = 30 * time.Second

// serverNamePattern is the part before the dot in every tool name. A dot
// is excluded so "<server>.<tool>" always splits at the first dot; the
// character set matches the MCP tool-name rules.
var serverNamePattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// toolNamePattern is the MCP tool-name grammar, including dots.
var toolNamePattern = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,128}$`)

// ToolFilter narrows the tools a server contributes. Names are the
// server's own, without the "<server>." prefix.
type ToolFilter struct {
	Allow []string
	Deny  []string
}

// ServerConfig is one configured MCP server, independent of the config
// file that produced it.
type ServerConfig struct {
	// Name prefixes the server's tools: "<server>.<tool>". Stable across
	// restarts.
	Name string
	// Transport is TransportStdio or TransportHTTP.
	Transport Transport
	// Command and Args start a stdio server, which receives Env in
	// addition to the safe inherited environment.
	Command string
	Args    []string
	Env     map[string]string
	// URL and Headers reach an HTTP server. Headers are static
	// credentials for WP2.1; OAuth is a later package.
	URL     string
	Headers map[string]string
	// Permission is the action class every tool of this server gets.
	// Empty means execute; a server annotation never changes it.
	Permission agentapi.ActionClass
	// Timeout bounds one call. Zero means DefaultTimeout.
	Timeout time.Duration
	// Tools is the optional allow or deny list.
	Tools ToolFilter
	// Resources names resource URIs loaded as instruction documents.
	Resources []string
	// DescriptionMax, when positive, caps a tool description in bytes.
	DescriptionMax int
	// Project marks a server that came from the project file. Such a
	// server must be approved before it starts.
	Project bool
}

// Validate reports whether the configuration can be used. The config
// loader performs its own structural checks; this method is the boundary
// for a manager built without the loader (tests and future callers).
func (c ServerConfig) Validate() error {
	if !serverNamePattern.MatchString(c.Name) {
		return fmt.Errorf("mcp: server name %q must match %s", c.Name, serverNamePattern)
	}
	switch c.Transport {
	case TransportStdio:
		if c.Command == "" {
			return fmt.Errorf("mcp: server %q: stdio needs a command", c.Name)
		}
		if c.URL != "" || len(c.Headers) > 0 {
			return fmt.Errorf("mcp: server %q: stdio cannot set url or headers", c.Name)
		}
	case TransportHTTP:
		if c.URL == "" {
			return fmt.Errorf("mcp: server %q: http needs a url", c.Name)
		}
		u, err := url.Parse(c.URL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return fmt.Errorf("mcp: server %q: url must be an absolute http or https URL", c.Name)
		}
		if u.User != nil {
			return fmt.Errorf("mcp: server %q: url must not carry a user or password; use headers", c.Name)
		}
		if c.Command != "" || len(c.Args) > 0 || len(c.Env) > 0 {
			return fmt.Errorf("mcp: server %q: http cannot set command, args or env", c.Name)
		}
	default:
		return fmt.Errorf("mcp: server %q: unknown transport %q", c.Name, c.Transport)
	}
	switch c.Permission {
	case "", agentapi.ClassRead, agentapi.ClassWrite, agentapi.ClassExecute:
	default:
		return fmt.Errorf("mcp: server %q: unknown permission %q", c.Name, c.Permission)
	}
	if c.Timeout < 0 {
		return fmt.Errorf("mcp: server %q: timeout is negative", c.Name)
	}
	if c.DescriptionMax < 0 {
		return fmt.Errorf("mcp: server %q: description_max is negative", c.Name)
	}
	if len(c.Tools.Allow) > 0 && len(c.Tools.Deny) > 0 {
		return fmt.Errorf("mcp: server %q: allow and deny cannot both be set", c.Name)
	}
	for _, name := range append(append([]string{}, c.Tools.Allow...), c.Tools.Deny...) {
		if name == "" {
			return fmt.Errorf("mcp: server %q: tool filter names cannot be empty", c.Name)
		}
	}
	seenResources := map[string]bool{}
	for _, uri := range c.Resources {
		u, err := url.Parse(uri)
		if err != nil || u.Scheme == "" || strings.TrimSpace(uri) != uri {
			return fmt.Errorf("mcp: server %q: resource URI %q must be absolute", c.Name, uri)
		}
		if seenResources[uri] {
			return fmt.Errorf("mcp: server %q: resource URI %q is listed more than once", c.Name, uri)
		}
		seenResources[uri] = true
	}
	for name := range c.Env {
		if name == "" || strings.ContainsAny(name, "=\x00") {
			return fmt.Errorf("mcp: server %q: invalid env name %q", c.Name, name)
		}
	}
	for name := range c.Headers {
		if name == "" || strings.ContainsAny(name, ": \t\r\n\x00") {
			return fmt.Errorf("mcp: server %q: invalid header name %q", c.Name, name)
		}
	}
	if c.Project && (len(c.Env) > 0 || len(c.Headers) > 0) {
		return fmt.Errorf("mcp: server %q: a project server may not set env or headers", c.Name)
	}
	return nil
}

// normalize fills the defaults a zero value leaves open. It does not
// validate; NewManager calls Validate first.
func (c *ServerConfig) normalize() {
	if c.Permission == "" {
		c.Permission = agentapi.ClassExecute
	}
	if c.Timeout == 0 {
		c.Timeout = DefaultTimeout
	}
}

// allows reports whether the server's tool name passes the configured
// allow or deny list.
func (c ServerConfig) allows(name string) bool {
	if len(c.Tools.Allow) > 0 {
		for _, allowed := range c.Tools.Allow {
			if allowed == name {
				return true
			}
		}
		return false
	}
	for _, denied := range c.Tools.Deny {
		if denied == name {
			return false
		}
	}
	return true
}

// fingerprint is the SHA-256 of the canonical JSON form of the fields a
// project server's consent is keyed by. It is a method on the value after
// defaults are applied, so a permission that defaults to execute has one
// identity.
func (c ServerConfig) fingerprint() string {
	type canonical struct {
		Transport  Transport `json:"transport"`
		Command    string    `json:"command"`
		Args       []string  `json:"args"`
		URL        string    `json:"url"`
		Permission string    `json:"permission"`
		Resources  []string  `json:"resources,omitempty"`
	}
	b, err := json.Marshal(canonical{
		Transport:  c.Transport,
		Command:    c.Command,
		Args:       c.Args,
		URL:        c.URL,
		Permission: string(c.Permission),
		Resources:  c.Resources,
	})
	if err != nil {
		// The fields are strings and a slice of strings; Marshal cannot
		// fail on them. An empty fingerprint still disables the server.
		return ""
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// errUnsupportedNumber is what a tool with an unsafe numeric literal is
// rejected with.
var errUnsupportedNumber = errors.New("unsupported numeric precision")

// unsafeNumber reports the first number in v that cannot be re-encoded
// without changing it. The SDK decodes an input schema into float64
// values, so an integer at or above 2^53 has already lost precision by
// the time the adapter sees it; sending the rounded schema would invite a
// tool call with the wrong argument.
func unsafeNumber(v any) (float64, bool) {
	switch v := v.(type) {
	case float64:
		if math.Abs(v) >= float64(1<<53) {
			return v, true
		}
	case float32:
		if math.Abs(float64(v)) >= float64(1<<53) {
			return float64(v), true
		}
	case json.Number:
		f, err := v.Float64()
		if err == nil && math.Abs(f) >= float64(1<<53) {
			return f, true
		}
	case map[string]any:
		for _, child := range v {
			if f, ok := unsafeNumber(child); ok {
				return f, true
			}
		}
	case []any:
		for _, child := range v {
			if f, ok := unsafeNumber(child); ok {
				return f, true
			}
		}
	}
	return 0, false
}
