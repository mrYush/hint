package main

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mrYush/hint/internal/config"
)

func TestConfigShowKeyring(t *testing.T) {
	t.Parallel()
	const secret = "sk-keyring-test-secret-123"
	const reference = "keyring:hint-test"
	env := environment{home: t.TempDir(), cwd: t.TempDir()}
	dir := filepath.Join(env.cwd, ".hint")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(`
providers:
  - {name: hub, kind: openai, base_url: "https://example.invalid/v1", model: m, api_key: `+reference+`}
`), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		name string
		ring config.Keyring
		want string
	}{
		{"hit", config.MapKeyring{"hint-test": secret}, "sk-key…123"},
		{"miss", config.MapKeyring{}, reference + " (no key came back)"},
		{"nil keyring", nil, reference + " (no key came back)"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			info := newSetupInfoWithEnvironment(nil, config.Flags{}, env)
			info.keyring = tt.ring
			out, warnings, err := info.showWithWarnings()
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(out, secret) || strings.Contains(strings.Join(warnings, "\n"), secret) {
				t.Fatal("config show exposed the API key")
			}
			if !strings.Contains(out+"\n", "providers.hub.api_key = "+tt.want+"\n") {
				t.Errorf("missing API key display %q in:\n%s", tt.want, out)
			}
		})
	}
}

func TestConfigCommandTypedFlagsAndWarnings(t *testing.T) {
	t.Parallel()
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

func TestConfigCLIDisplaysUnreadyConfiguration(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name, yaml, want, warning string
	}{
		{"empty", "", "default_provider = (unset)", ""},
		{"DeepSeek default", "providers: [{name: ds, kind: openai, base_url: 'https://api.deepseek.com', model: m}]\n", "providers.ds.thinking = enabled (default)", ""},
		{"DeepSeek explicit", "providers: [{name: ds, kind: openai, base_url: 'https://api.deepseek.com', model: m, thinking: disabled}]\n", "providers.ds.thinking = disabled", ""},
		{"invalid profile", "providers: [{name: hub, kind: bogus}]\ndefault_provider: nope\n", "providers.hub.kind = bogus", ""},
		{"duplicate fallback", "providers: [{name: local, kind: ollama, model: m}]\ndefault_provider: local\nfallback_provider: local\n", "fallback_provider = (unset)", "hint: fallback_provider \"local\" equals default_provider and was ignored\n"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			home := t.TempDir()
			cmd := cliProcess(t, []string{"HOME=" + home, "USERPROFILE=" + home}, "config")
			// Keep coverage-runtime diagnostics out of the CLI's stderr.
			cmd.Env = append(cmd.Env, "GOCOVERDIR="+t.TempDir())
			dir := filepath.Join(cmd.Dir, ".hint")
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(tt.yaml), 0o600); err != nil {
				t.Fatal(err)
			}
			var stderr bytes.Buffer
			cmd.Stderr = &stderr
			out, err := cmd.Output()
			if err != nil {
				t.Fatalf("config failed: %v, stderr: %s", err, &stderr)
			}
			if !strings.Contains(string(out), tt.want+"\n") || stderr.String() != tt.warning {
				t.Errorf("stdout = %s, stderr = %s", out, &stderr)
			}
		})
	}
}

func TestConfigCLIErrorCategories(t *testing.T) {
	t.Parallel()
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
