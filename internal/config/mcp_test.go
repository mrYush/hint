package config

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const mcpProviderYAML = `
providers:
  - name: p
    kind: ollama
    model: m
default_provider: p
`

func loadMCP(t *testing.T, global, project string, env map[string]string, draft bool) (*Config, error) {
	t.Helper()
	opts := dirs(t)
	opts.LookupEnv = lookup(env)
	opts.Draft = draft
	if global != "" {
		writeFile(t, filepath.Join(opts.HomeDir, ".config", "hint", "config.yaml"), global)
	}
	if project != "" {
		writeFile(t, filepath.Join(opts.WorkingDir, ".hint", "config.yaml"), project)
	}
	return LoadFrom(opts)
}

func TestMCPServersLoad(t *testing.T) {
	global := `
mcp_servers:
  fs:
    transport: stdio
    command: npx
    args: ["-y", "pkg"]
    env:
      TOKEN: "${TOKEN_VALUE}"
    permission: read
    timeout: 5s
    tools:
      allow: ["read_file"]
    description_max: 200
  web:
    transport: http
    url: https://example.com/mcp
    headers:
      Authorization: "Bearer ${TOKEN_VALUE}"
`
	cfg, err := loadMCP(t, global, "", map[string]string{"TOKEN_VALUE": "abc123"}, true)
	if err != nil {
		t.Fatalf("LoadFrom: %v", err)
	}
	if len(cfg.MCPServers) != 2 {
		t.Fatalf("servers = %d, want 2", len(cfg.MCPServers))
	}
	if cfg.MCPServers[0].Name != "fs" || cfg.MCPServers[1].Name != "web" {
		t.Errorf("order = %q, %q; want fs, web", cfg.MCPServers[0].Name, cfg.MCPServers[1].Name)
	}
	fs := cfg.MCPServers[0]
	if fs.Transport != MCPTransportStdio || fs.Command != "npx" || len(fs.Args) != 2 || fs.Args[0] != "-y" {
		t.Errorf("fs = %+v", fs)
	}
	if fs.Env["TOKEN"] != "abc123" {
		t.Errorf("fs env TOKEN = %q, want expanded abc123", fs.Env["TOKEN"])
	}
	if fs.Permission != "read" || fs.Timeout != 5*time.Second || fs.DescriptionMax != 200 {
		t.Errorf("fs permission/timeout/description_max = %q/%s/%d", fs.Permission, fs.Timeout, fs.DescriptionMax)
	}
	if len(fs.Tools.Allow) != 1 || fs.Tools.Allow[0] != "read_file" {
		t.Errorf("fs allow = %v", fs.Tools.Allow)
	}
	web := cfg.MCPServers[1]
	if web.Permission != "execute" {
		t.Errorf("default permission = %q, want execute", web.Permission)
	}
	if got := web.Headers["Authorization"]; got != "Bearer abc123" {
		t.Errorf("web Authorization = %q, want expanded header", got)
	}
	secrets := cfg.Secrets()
	for _, want := range []string{"abc123", "Bearer abc123"} {
		if !containsString(secrets, want) {
			t.Errorf("Secrets() = %v; missing %q", secrets, want)
		}
	}
	if containsString(secrets, "Bearer ${TOKEN_VALUE}") {
		t.Errorf("Secrets() = %v; still holds the unexpanded template", secrets)
	}
}

func TestMCPServersProjectAddsAndCannotOverride(t *testing.T) {
	global := `
mcp_servers:
  fs:
    transport: stdio
    command: npx
    args: ["server"]
`
	project := `
mcp_servers:
  fs:
    transport: stdio
    command: evil
  extra:
    transport: stdio
    command: extra-cmd
`
	cfg, err := loadMCP(t, global, project, nil, true)
	if err != nil {
		t.Fatalf("LoadFrom: %v", err)
	}
	if len(cfg.MCPServers) != 2 {
		t.Fatalf("servers = %d, want 2", len(cfg.MCPServers))
	}
	if cfg.MCPServers[0].Command != "npx" {
		t.Errorf("global server was overridden: %+v", cfg.MCPServers[0])
	}
	extra := cfg.MCPServers[1]
	if extra.Name != "extra" || !extra.Project {
		t.Errorf("project server = %+v; want extra with Project", extra)
	}
	hasWarning(t, cfg, "may not override a global MCP server")
}

func TestMCPServersProjectCredentialsRejected(t *testing.T) {
	project := `
mcp_servers:
  leaky:
    transport: http
    url: https://example.com/mcp
    headers:
      Authorization: "Bearer secret"
`
	cfg, err := loadMCP(t, "", project, nil, true)
	if err != nil {
		t.Fatalf("LoadFrom: %v", err)
	}
	if len(cfg.MCPServers) != 0 {
		t.Errorf("servers = %+v; a project server with headers must be dropped", cfg.MCPServers)
	}
	hasWarning(t, cfg, "may not set env or headers")
}

func TestMCPServersProjectValuesNotExpanded(t *testing.T) {
	project := `
mcp_servers:
  p:
    transport: stdio
    command: "${BIN}"
`
	cfg, err := loadMCP(t, "", project, map[string]string{"BIN": "/tmp/evil"}, true)
	if err != nil {
		t.Fatalf("LoadFrom: %v", err)
	}
	if len(cfg.MCPServers) != 1 {
		t.Fatalf("servers = %+v", cfg.MCPServers)
	}
	if got := cfg.MCPServers[0].Command; got != "${BIN}" {
		t.Errorf("project command = %q; want the literal reference", got)
	}
	if containsString(cfg.Secrets(), "/tmp/evil") {
		t.Errorf("Secrets() = %v; project expansion leaked the value", cfg.Secrets())
	}
	hasWarning(t, cfg, "not expanded")
}

func TestMCPServersMissingVariableWarns(t *testing.T) {
	global := `
mcp_servers:
  web:
    transport: http
    url: https://example.com/mcp
    headers:
      Authorization: "Bearer ${MISSING}"
`
	cfg, err := loadMCP(t, global, "", nil, true)
	if err != nil {
		t.Fatalf("LoadFrom: %v", err)
	}
	hasWarning(t, cfg, "MISSING expands to nothing")
	if got := cfg.MCPServers[0].Headers["Authorization"]; got != "Bearer " {
		t.Errorf("Authorization = %q; want the empty expansion", got)
	}
}

func TestMCPServersValidation(t *testing.T) {
	tests := []struct {
		name string
		yaml string
		want string
	}{
		{
			name: "stdio without command",
			yaml: "mcp_servers:\n  s:\n    transport: stdio\n",
			want: "needs command",
		},
		{
			name: "http without url",
			yaml: "mcp_servers:\n  s:\n    transport: http\n",
			want: "needs url",
		},
		{
			name: "bad server name",
			yaml: "mcp_servers:\n  bad.name:\n    transport: stdio\n    command: c\n",
			want: "server name must match",
		},
		{
			name: "unknown transport",
			yaml: "mcp_servers:\n  s:\n    transport: car\n",
			want: "transport must be",
		},
		{
			name: "allow and deny",
			yaml: "mcp_servers:\n  s:\n    transport: stdio\n    command: c\n    tools:\n      allow: [a]\n      deny: [b]\n",
			want: "cannot both be set",
		},
		{
			name: "unknown permission",
			yaml: "mcp_servers:\n  s:\n    transport: stdio\n    command: c\n    permission: sudo\n",
			want: "permission must be",
		},
		{
			name: "negative timeout",
			yaml: "mcp_servers:\n  s:\n    transport: stdio\n    command: c\n    timeout: -1s\n",
			want: "timeout must not be negative",
		},
		{
			name: "stdio with headers",
			yaml: "mcp_servers:\n  s:\n    transport: stdio\n    command: c\n    headers:\n      X: y\n",
			want: "cannot set url or headers",
		},
		{
			name: "http with userinfo",
			yaml: "mcp_servers:\n  s:\n    transport: http\n    url: https://user:pass@example.com/mcp\n",
			want: "must not carry a user or password",
		},
		{
			name: "http with env",
			yaml: "mcp_servers:\n  s:\n    transport: http\n    url: https://example.com/mcp\n    env:\n      X: y\n",
			want: "cannot set command, args or env",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := loadMCP(t, mcpProviderYAML+tt.yaml, "", nil, false)
			if err == nil {
				t.Fatalf("LoadFrom accepted the config")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("error = %v; want %q", err, tt.want)
			}
		})
	}
}

func TestMCPServersDuplicateRejected(t *testing.T) {
	global := `
mcp_servers:
  s:
    transport: stdio
    command: a
  s:
    transport: stdio
    command: b
`
	if _, err := loadMCP(t, global, "", nil, true); err == nil {
		t.Fatal("LoadFrom accepted a duplicate MCP server")
	}
}

func containsString(values []string, want string) bool {
	for _, v := range values {
		if v == want {
			return true
		}
	}
	return false
}

func TestMCPServersEmptySection(t *testing.T) {
	cfg, err := loadMCP(t, "mcp_servers:\n", "", nil, true)
	if err != nil {
		t.Fatalf("LoadFrom: %v", err)
	}
	if len(cfg.MCPServers) != 0 {
		t.Errorf("servers = %+v", cfg.MCPServers)
	}
}

func TestMCPServersLiteralHeaderIsSecret(t *testing.T) {
	global := `
mcp_servers:
  web:
    transport: http
    url: https://example.com/mcp
    headers:
      X-Api-Key: "literal-secret-value"
`
	cfg, err := loadMCP(t, global, "", nil, true)
	if err != nil {
		t.Fatalf("LoadFrom: %v", err)
	}
	if !containsString(cfg.Secrets(), "literal-secret-value") {
		t.Errorf("Secrets() = %v; a literal header was not collected", cfg.Secrets())
	}
	if len(cfg.Warnings) != 0 {
		t.Errorf("warnings = %v; want none", cfg.Warnings)
	}
}

func TestMCPServersBareTokenRedacted(t *testing.T) {
	global := `
mcp_servers:
  web:
    transport: http
    url: https://example.com/mcp
    headers:
      Authorization: "Bearer ${TOKEN}"
`
	cfg, err := loadMCP(t, global, "", map[string]string{"TOKEN": "tok_1234567890"}, true)
	if err != nil {
		t.Fatalf("LoadFrom: %v", err)
	}
	secrets := cfg.Secrets()
	if !containsString(secrets, "tok_1234567890") {
		t.Fatalf("Secrets() = %v; the bare token is missing", secrets)
	}
	for _, text := range []string{"the token is tok_1234567890", "header Bearer tok_1234567890"} {
		if got := Redact(text, secrets); strings.Contains(got, "tok_1234567890") {
			t.Errorf("Redact(%q) = %q; the token survived", text, got)
		}
	}
}

func TestMCPServersSecretValueFiltersEnviron(t *testing.T) {
	global := `
mcp_servers:
  web:
    transport: http
    url: https://example.com/mcp
    headers:
      Authorization: "Bearer ${TOKEN}"
`
	cfg, err := loadMCP(t, global, "", map[string]string{"TOKEN": "tok_1234567890"}, true)
	if err != nil {
		t.Fatalf("LoadFrom: %v", err)
	}
	got := FilterEnviron([]string{"TOKEN=tok_1234567890", "OTHER=visible"}, nil, cfg.Secrets())
	if containsString(got, "TOKEN=tok_1234567890") {
		t.Errorf("FilterEnviron kept the token: %v", got)
	}
	if !containsString(got, "OTHER=visible") {
		t.Errorf("FilterEnviron dropped an unrelated value: %v", got)
	}
}

func TestMCPServersURLSecret(t *testing.T) {
	global := `
mcp_servers:
  web:
    transport: http
    url: "https://example.com/mcp?token=${TOKEN}"
`
	cfg, err := loadMCP(t, global, "", map[string]string{"TOKEN": "tok_1234567890"}, true)
	if err != nil {
		t.Fatalf("LoadFrom: %v", err)
	}
	if !containsString(cfg.Secrets(), "tok_1234567890") {
		t.Errorf("Secrets() = %v; a URL reference was not collected", cfg.Secrets())
	}
}
