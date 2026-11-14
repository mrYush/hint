package config

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

func TestMissingEnvironmentDiagnostics(t *testing.T) {
	for _, draft := range []bool{false, true} {
		for _, env := range []map[string]string{nil, {"MISSING_MODEL": ""}} {
			opts := dirs(t)
			opts.Draft = draft
			opts.LookupEnv = lookup(env)
			writeFile(t, filepath.Join(opts.HomeDir, ".config", "hint", "config.yaml"), `
providers:
  - name: local
    kind: ollama
    base_url: ${MISSING_URL}
    model: ${MISSING_MODEL}${MISSING_MODEL}
mcp_servers:
  shell:
    transport: stdio
    command: ${MISSING_COMMAND}
    args: ["$1 $$ $# $@ ${MISSING_ARG}"]
  web:
    transport: http
    url: ${MISSING_MCP_URL}
    headers: {Authorization: "Bearer ${MISSING_TOKEN}"}
    resources: ["file:///${MISSING_RESOURCE}"]
`)
			cfg, err := LoadFrom(opts)
			var diagnostics string
			if draft {
				if err != nil {
					t.Fatal(err)
				}
				diagnostics = strings.Join(cfg.Warnings, "\n")
			} else {
				if err == nil {
					t.Fatal("expected validation failure")
				}
				diagnostics = err.Error()
			}
			for _, field := range []struct{ key, variable string }{
				{"providers.local.base_url", "MISSING_URL"},
				{"providers.local.model", "MISSING_MODEL"},
				{"mcp_servers.shell.command", "MISSING_COMMAND"},
				{"mcp_servers.shell.args.0", "MISSING_ARG"},
				{"mcp_servers.web.url", "MISSING_MCP_URL"},
				{"mcp_servers.web.headers.Authorization", "MISSING_TOKEN"},
				{"mcp_servers.web.resources.0", "MISSING_RESOURCE"},
			} {
				want := field.key + ": environment variable " + field.variable + " is not set"
				if strings.Count(diagnostics, want) != 1 {
					t.Errorf("draft=%v: want exactly one %q in %s", draft, want, diagnostics)
				}
			}
			for _, parameter := range []string{"1", "$", "#", "@"} {
				if strings.Contains(diagnostics, "environment variable "+parameter+" is not set") {
					t.Errorf("shell parameter diagnosed as environment: %s", diagnostics)
				}
			}
		}
	}
}

func TestMissingEnvironmentSurvivesLimitFailure(t *testing.T) {
	opts := dirs(t)
	writeFile(t, filepath.Join(opts.WorkingDir, ".hint", "config.yaml"), `
providers:
  - name: p
    kind: ollama
    model: ${MISSING_MODEL}
overview: {depth: -1}
`)
	_, err := LoadFrom(opts)
	var ve *ValidationError
	if !errors.As(err, &ve) || ve.Key != "overview.depth" {
		t.Fatalf("want typed limit error, got %v", err)
	}
	if !strings.Contains(err.Error(), "MISSING_MODEL is not set") {
		t.Errorf("diagnostic lost before final validation: %v", err)
	}
}

func TestLoadValidationFields(t *testing.T) {
	base := "providers:\n  - name: p\n    kind: ollama\n    model: m\n"
	for _, tt := range []struct {
		name, yaml, key string
		cause           error
	}{
		{"MCP server", base + "mcp_servers: {broken: {transport: stdio}}\n", "mcp_servers.broken", nil},
		{"default reference", base + "default_provider: missing\n", "default_provider", ErrUnknownProfile},
		{"fallback reference", base + "fallback_provider: missing\n", "fallback_provider", ErrUnknownProfile},
		{"missing default", base + "  - {name: other, kind: ollama, model: m}\n", "default_provider", nil},
		{"kind", strings.Replace(base, "ollama", "unknown", 1), "providers.p.kind", nil},
		{"header timeout", base + "    header_timeout: -1s\n", "providers.p.header_timeout", nil},
		{"max tokens field", base + "    max_tokens_field: other\n", "providers.p.max_tokens_field", nil},
		{"context window", base + "    context_window: -1\n", "providers.p.context_window", nil},
		{"instruction budget", base + "instructions: {budget: -1}\n", "instructions.budget", nil},
		{"overview entries", base + "overview: {max_entries: -1}\n", "overview.max_entries", nil},
		{"completion mode", base + "completion: {mode: other}\n", "completion.mode", nil},
		{"completion idle", base + "completion: {idle_after: -1s}\n", "completion.idle_after", nil},
		{"completion warn", base + "completion: {warn_after: -1s}\n", "completion.warn_after", nil},
		{"secret store", base + "secrets: {store: other}\n", "secrets.store", nil},
	} {
		t.Run(tt.name, func(t *testing.T) {
			opts := dirs(t)
			writeFile(t, filepath.Join(opts.WorkingDir, ".hint", "config.yaml"), tt.yaml)
			_, err := LoadFrom(opts)
			var ve *ValidationError
			if !errors.As(err, &ve) || ve.Key != tt.key {
				t.Fatalf("want ValidationError.Key=%q, got %v", tt.key, err)
			}
			if tt.cause != nil && !errors.Is(err, tt.cause) {
				t.Errorf("want cause %v, got %v", tt.cause, err)
			}
			if !strings.HasPrefix(err.Error(), tt.key+": ") || strings.Contains(err.Error(), "config:") {
				t.Errorf("unexpected error context: %v", err)
			}
		})
	}
}

func TestCompletionWarningHasNoPackagePrefix(t *testing.T) {
	opts := dirs(t)
	writeFile(t, filepath.Join(opts.WorkingDir, ".hint", "config.yaml"), `
providers:
  - {name: local, kind: ollama, model: m}
  - {name: small, kind: openai, model: m}
default_provider: local
completion: {mode: tab, profile: small}
`)
	cfg, err := LoadFrom(opts)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Completion.Mode != CompletionOff || len(cfg.Warnings) != 1 {
		t.Fatalf("completion=%+v, warnings=%v", cfg.Completion, cfg.Warnings)
	}
	if strings.Contains(cfg.Warnings[0], "config:") || !strings.Contains(cfg.Warnings[0], "needs an API key") {
		t.Errorf("unexpected warning: %s", cfg.Warnings[0])
	}
}
