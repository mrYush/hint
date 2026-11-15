package config

import (
	"net/url"
	"regexp"
	"strings"
	"time"
)

// mcpServerNamePattern is the part before the dot in every MCP tool
// name. A dot is excluded so "<server>.<tool>" always splits at the first
// dot.
var mcpServerNamePattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// MCP transport names. A server is either a subprocess spoken to over its
// standard input and output, or an HTTP endpoint using the MCP Streamable
// HTTP transport.
const (
	MCPTransportStdio = "stdio"
	MCPTransportHTTP  = "http"
)

// MCPServer is one `mcp_servers:` entry after merging the config files.
// ${VAR} references in a global entry are expanded; a project entry keeps
// them literal, so a cloned repository cannot pull the developer's
// environment into a command it chose.
//
// It is a neutral copy: internal/mcp turns it into its own ServerConfig, so
// this package does not depend on the MCP SDK.
type MCPServer struct {
	// Name is the map key in the YAML file; it prefixes every tool the
	// server contributes as "<server>.<tool>".
	Name string
	// Transport is MCPTransportStdio or MCPTransportHTTP.
	Transport string
	// Command and Args start a stdio server. Env is its declared
	// environment; the manager adds the safe inherited variables.
	Command string
	Args    []string
	Env     map[string]string
	// URL and Headers reach an HTTP server. Headers carry the static
	// credential WP2.1 supports; OAuth is a later package.
	URL     string
	Headers map[string]string
	// Permission is the action class every tool of the server gets:
	// read, write or execute. Empty means execute.
	Permission string
	// Timeout bounds one tool call. Zero means the built-in 30s.
	Timeout time.Duration
	// Tools narrows what the server may contribute. At most one of Allow
	// and Deny is set.
	Tools MCPToolFilter
	// Resources names resource URIs loaded as instruction documents.
	Resources []string
	// DescriptionMax, when positive, caps a tool description in bytes.
	// It is an explicit opt-in for a server worth keeping under budget.
	DescriptionMax int
	// Project marks a server added by .hint/config.yaml rather than the
	// global file. Such a server starts disabled until consent is
	// recorded; see internal/mcp.
	Project bool
}

// MCPToolFilter is the `tools:` block of an mcp_servers entry. Names are
// the server's own tool names, without the "<server>." prefix.
type MCPToolFilter struct {
	Allow []string
	Deny  []string
}

// validateMCPServer checks the structural rules the loader can decide
// before internal/mcp sees the value. The MCP package checks them again,
// because it is also called directly by tests and future callers.
// Failures identify the server with ValidationError.Key = mcp_servers.<name>.
func validateMCPServer(s MCPServer) error {
	where := "mcp_servers." + s.Name
	if !mcpServerNamePattern.MatchString(s.Name) {
		return validationErrorf(where, "server name must match %s", mcpServerNamePattern)
	}
	switch s.Transport {
	case MCPTransportStdio:
		if s.Command == "" {
			return validationErrorf(where, "a stdio server needs command")
		}
		if s.URL != "" || len(s.Headers) > 0 {
			return validationErrorf(where, "a stdio server cannot set url or headers")
		}
	case MCPTransportHTTP:
		if s.URL == "" {
			return validationErrorf(where, "an http server needs url")
		}
		u, err := url.Parse(s.URL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return validationErrorf(where, "url must be an absolute http or https URL")
		}
		if u.User != nil {
			return validationErrorf(where, "url must not carry a user or password; use headers")
		}
		if s.Command != "" || len(s.Args) > 0 || len(s.Env) > 0 {
			return validationErrorf(where, "an http server cannot set command, args or env")
		}
	default:
		return validationErrorf(where, "transport must be %q or %q, not %q", MCPTransportStdio, MCPTransportHTTP, s.Transport)
	}
	switch s.Permission {
	case "", "read", "write", "execute":
	default:
		return validationErrorf(where, "permission must be read, write or execute, not %q", s.Permission)
	}
	if s.Timeout < 0 {
		return validationErrorf(where, "timeout must not be negative")
	}
	if s.DescriptionMax < 0 {
		return validationErrorf(where, "description_max must not be negative")
	}
	if len(s.Tools.Allow) > 0 && len(s.Tools.Deny) > 0 {
		return validationErrorf(where, "tools.allow and tools.deny cannot both be set")
	}
	for _, name := range append(append([]string{}, s.Tools.Allow...), s.Tools.Deny...) {
		if name == "" {
			return validationErrorf(where, "tools lists an empty tool name")
		}
		if strings.ContainsAny(name, " \t\r\n") {
			return validationErrorf(where, "tool name %q contains whitespace", name)
		}
	}
	seenResources := map[string]bool{}
	for _, uri := range s.Resources {
		u, err := url.Parse(uri)
		if err != nil || u.Scheme == "" || strings.TrimSpace(uri) != uri {
			return validationErrorf(where, "resource URI %q must be absolute", uri)
		}
		if seenResources[uri] {
			return validationErrorf(where, "resource URI %q is listed more than once", uri)
		}
		seenResources[uri] = true
	}
	for name := range s.Env {
		if name == "" || strings.ContainsAny(name, "=\x00") {
			return validationErrorf(where, "env has an invalid variable name %q", name)
		}
	}
	for name := range s.Headers {
		if name == "" || strings.ContainsAny(name, ": \t\r\n\x00") {
			return validationErrorf(where, "headers has an invalid name %q", name)
		}
	}
	if s.Project && (len(s.Env) > 0 || len(s.Headers) > 0) {
		return validationErrorf(where, "a project server may not set env or headers")
	}
	return nil
}
