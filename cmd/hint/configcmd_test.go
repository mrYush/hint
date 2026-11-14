package main

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestConfigCommandTypedFlagsAndWarnings(t *testing.T) {
	env := environment{home: t.TempDir(), cwd: t.TempDir()}
	if err := os.MkdirAll(filepath.Join(env.cwd, ".hint"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(env.cwd, ".hint", "config.yaml"), []byte(`
providers:
  - {name: local, kind: ollama, model: m, context_window: 8192}
  - {name: spare, kind: ollama, model: "${MISSING_MODEL}"}
default_provider: local
overview: {depth: 3, max_entries: 7}
instructions: {budget: 1000, summarize: true}
`), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		name string
		args []string
		want []string
	}{
		{"omitted", nil, []string{"overview.depth = 3", "instructions.summarize = true", "instructions.budget = 1000", "overview.max_entries = 7", "providers.local.context_window = 8192"}},
		{"explicit zero and false", []string{"--overview-depth=0", "--summarize-instructions=false", "--context-window=0"}, []string{"overview.depth = 0", "instructions.summarize = false", "providers.local.context_window = (unset)"}},
		{"explicit positive values", []string{"--instruction-budget=2000", "--overview-entries=15", "--context-window=16384", "--summarize-instructions"}, []string{"instructions.budget = 2000", "overview.max_entries = 15", "providers.local.context_window = 16384", "instructions.summarize = true"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			root := newRootCommandWithEnvironment(env)
			var out, stderr bytes.Buffer
			root.SetOut(&out)
			root.SetErr(&stderr)
			root.SetArgs(append(tt.args, "config"))
			if err := root.Execute(); err != nil {
				t.Fatal(err)
			}
			wantWarning := "hint: providers.spare.model: environment variable MISSING_MODEL is not set\n"
			if stderr.String() != wantWarning {
				t.Errorf("stderr = %q, want %q", stderr.String(), wantWarning)
			}
			if strings.Contains(out.String(), "environment variable") || strings.Contains(out.String(), "warning:") {
				t.Errorf("diagnostics leaked into stdout: %s", &out)
			}
			for _, want := range tt.want {
				if !strings.Contains(out.String(), want+"\n") {
					t.Errorf("want %q in output:\n%s", want, &out)
				}
			}
		})
	}
}

func TestConfigCLIErrorCategories(t *testing.T) {
	for _, tt := range []struct {
		name string
		args []string
		code int
		text string
	}{
		{"integer syntax", []string{"--instruction-budget=abc", "config"}, 2, "invalid argument"},
		{"depth syntax", []string{"--overview-depth=deep", "config"}, 2, "invalid argument"},
		{"boolean syntax", []string{"--summarize-instructions=maybe", "config"}, 2, "invalid argument"},
		{"integer range", []string{"--instruction-budget=0", "config"}, 1, "instructions.budget: must be at least 1"},
		{"no provider", []string{"-p", "hello"}, 1, "loading configuration: no provider configured"},
		{"unknown key", []string{"config", "set", "nope.key", "x"}, 1, "unknown key"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			home := t.TempDir()
			cmd := cliProcess(t, []string{"HOME=" + home, "USERPROFILE=" + home}, tt.args...)
			out, err := cmd.CombinedOutput()
			var exit *exec.ExitError
			if !errors.As(err, &exit) || exit.ExitCode() != tt.code {
				t.Fatalf("want exit %d, got %v: %s", tt.code, err, out)
			}
			if !strings.Contains(string(out), tt.text) || strings.Contains(string(out), "config:") {
				t.Errorf("unexpected diagnostic: %s", out)
			}
		})
	}
}
