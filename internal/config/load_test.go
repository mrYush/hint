package config

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// writeFile creates a file with any missing parent directories.
func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// lookup builds a LookupEnv over a plain map, keeping every test isolated
// from the real process environment.
func lookup(env map[string]string) func(string) (string, bool) {
	return func(k string) (string, bool) {
		v, ok := env[k]
		return v, ok
	}
}

// dirs prepares an isolated home and working directory and returns Options
// pointing at them.
func dirs(t *testing.T) Options {
	t.Helper()
	return Options{
		HomeDir:    t.TempDir(),
		WorkingDir: t.TempDir(),
		LookupEnv:  lookup(nil),
	}
}

func hasWarning(t *testing.T, cfg *Config, substr string) {
	t.Helper()
	for _, w := range cfg.Warnings {
		if strings.Contains(w, substr) {
			return
		}
	}
	t.Errorf("expected a warning containing %q, got %v", substr, cfg.Warnings)
}

func mustDefault(t *testing.T, cfg *Config) Profile {
	t.Helper()
	p, err := cfg.Default()
	if err != nil {
		t.Fatalf("Default(): %v", err)
	}
	return p
}

const twoProfilesYAML = `
providers:
  - name: api-bar
    kind: openai
    base_url: https://api-bar.ru/route/openai
    api_key: file-key
    model: file-model
  - name: local
    kind: ollama
    model: qwen2.5:7b
default_provider: api-bar
fallback_provider: local
`

func TestSourcePriority(t *testing.T) {
	// One field (model) walked through every layer of the overlay chain:
	// flag > HINT_MODEL > project file > global file.
	tests := []struct {
		name      string
		global    string // model in the global file, "" = no file
		project   string // model in the project file, "" = no file
		env       map[string]string
		flags     Flags
		wantModel string
	}{
		{
			name:      "global only",
			global:    "g-model",
			wantModel: "g-model",
		},
		{
			name:      "project beats global",
			global:    "g-model",
			project:   "p-model",
			wantModel: "p-model",
		},
		{
			name:      "env beats project",
			global:    "g-model",
			project:   "p-model",
			env:       map[string]string{"HINT_MODEL": "e-model"},
			wantModel: "e-model",
		},
		{
			name:      "flag beats env",
			global:    "g-model",
			project:   "p-model",
			env:       map[string]string{"HINT_MODEL": "e-model"},
			flags:     Flags{Model: "f-model"},
			wantModel: "f-model",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts := dirs(t)
			opts.Flags = tt.flags
			env := map[string]string{}
			for k, v := range tt.env {
				env[k] = v
			}
			opts.LookupEnv = lookup(env)
			profile := `
providers:
  - name: p
    kind: openai
    api_key: k
    model: %s
`
			if tt.global != "" {
				writeFile(t, filepath.Join(opts.HomeDir, ".config", "hint", "config.yaml"),
					strings.ReplaceAll(profile, "%s", tt.global))
			}
			if tt.project != "" {
				writeFile(t, filepath.Join(opts.WorkingDir, ".hint", "config.yaml"),
					strings.ReplaceAll(profile, "%s", tt.project))
			}
			cfg, err := LoadFrom(opts)
			if err != nil {
				t.Fatalf("LoadFrom: %v", err)
			}
			if got := mustDefault(t, cfg).Model; got != tt.wantModel {
				t.Errorf("model = %q, want %q", got, tt.wantModel)
			}
		})
	}
}

func TestUnsetFlagDoesNotOverrideFile(t *testing.T) {
	// Regression for the pre-WP0.2 bug: --model had default "gpt-4", so the
	// file value never won. Empty Flags must leave the file value intact.
	opts := dirs(t)
	writeFile(t, filepath.Join(opts.WorkingDir, ".hint", "config.yaml"), `
providers:
  - name: p
    kind: openai
    api_key: k
    model: from-file
`)
	cfg, err := LoadFrom(opts)
	if err != nil {
		t.Fatalf("LoadFrom: %v", err)
	}
	if got := mustDefault(t, cfg).Model; got != "from-file" {
		t.Errorf("model = %q, want %q", got, "from-file")
	}
}

func TestEnvOverlayTouchesOnlySelectedProfile(t *testing.T) {
	opts := dirs(t)
	writeFile(t, filepath.Join(opts.WorkingDir, ".hint", "config.yaml"), twoProfilesYAML)
	opts.LookupEnv = lookup(map[string]string{"HINT_API_KEY": "env-key"})

	cfg, err := LoadFrom(opts)
	if err != nil {
		t.Fatalf("LoadFrom: %v", err)
	}
	if got := mustDefault(t, cfg).APIKey; got != "env-key" {
		t.Errorf("default profile key = %q, want %q", got, "env-key")
	}
	fb, ok := cfg.Fallback()
	if !ok {
		t.Fatal("Fallback() = _, false; want the local profile")
	}
	if fb.APIKey != "" {
		t.Errorf("fallback key = %q, want it untouched (empty)", fb.APIKey)
	}
}

func TestProviderSelection(t *testing.T) {
	// --provider beats HINT_PROVIDER, and the field overlays land on the
	// profile selected AFTER that resolution, not on the file's default.
	opts := dirs(t)
	writeFile(t, filepath.Join(opts.WorkingDir, ".hint", "config.yaml"), twoProfilesYAML)
	opts.LookupEnv = lookup(map[string]string{
		"HINT_PROVIDER": "api-bar",
		"HINT_MODEL":    "env-model",
	})
	opts.Flags = Flags{Provider: "local"}

	cfg, err := LoadFrom(opts)
	if err != nil {
		t.Fatalf("LoadFrom: %v", err)
	}
	p := mustDefault(t, cfg)
	if p.Name != "local" {
		t.Fatalf("selected profile = %q, want %q", p.Name, "local")
	}
	if p.Model != "env-model" {
		t.Errorf("selected model = %q, want the HINT_MODEL overlay %q", p.Model, "env-model")
	}
	apiBar, err := cfg.Profile("api-bar")
	if err != nil {
		t.Fatal(err)
	}
	if apiBar.Model != "file-model" {
		t.Errorf("api-bar model = %q, want untouched %q", apiBar.Model, "file-model")
	}
	// The fallback now equals the selected default and must be cleared.
	if _, ok := cfg.Fallback(); ok {
		t.Error("Fallback() = _, true; want cleared when equal to default")
	}
	hasWarning(t, cfg, "fallback_provider")
}

func TestMergeByName(t *testing.T) {
	opts := dirs(t)
	writeFile(t, filepath.Join(opts.HomeDir, ".config", "hint", "config.yaml"), `
providers:
  - name: api-bar
    kind: openai
    base_url: https://global.example/v1
    api_key: global-key
    model: global-model
default_provider: api-bar
`)
	writeFile(t, filepath.Join(opts.WorkingDir, ".hint", "config.yaml"), `
providers:
  - name: api-bar
    model: project-model
  - name: extra
    kind: ollama
    model: llama3
`)
	cfg, err := LoadFrom(opts)
	if err != nil {
		t.Fatalf("LoadFrom: %v", err)
	}
	p := mustDefault(t, cfg)
	if p.Model != "project-model" {
		t.Errorf("model = %q, want project override %q", p.Model, "project-model")
	}
	if p.BaseURL != "https://global.example/v1" {
		t.Errorf("base_url = %q, want the global value kept", p.BaseURL)
	}
	if p.APIKey != "global-key" {
		t.Errorf("api_key = %q, want the global value kept", p.APIKey)
	}
	if _, err := cfg.Profile("extra"); err != nil {
		t.Errorf("project-only profile not merged in: %v", err)
	}
}

func TestEnvExpansionInFileValues(t *testing.T) {
	opts := dirs(t)
	writeFile(t, filepath.Join(opts.WorkingDir, ".hint", "config.yaml"), `
providers:
  - name: p
    kind: openai
    api_key: ${MY_KEY}
    model: $MY_MODEL
`)
	opts.LookupEnv = lookup(map[string]string{
		"MY_KEY":   "secret-value-123",
		"MY_MODEL": "gpt-4o-mini",
	})
	cfg, err := LoadFrom(opts)
	if err != nil {
		t.Fatalf("LoadFrom: %v", err)
	}
	p := mustDefault(t, cfg)
	if p.APIKey != "secret-value-123" {
		t.Errorf("api_key = %q, want expanded ${MY_KEY}", p.APIKey)
	}
	if p.Model != "gpt-4o-mini" {
		t.Errorf("model = %q, want expanded $MY_MODEL", p.Model)
	}
}

func TestUnsetVarExpandsToEmptyAndFailsValidation(t *testing.T) {
	opts := dirs(t)
	writeFile(t, filepath.Join(opts.WorkingDir, ".hint", "config.yaml"), `
providers:
  - name: p
    kind: openai
    api_key: ${UNSET_KEY}
`)
	_, err := LoadFrom(opts)
	if err == nil || !strings.Contains(err.Error(), "API key") {
		t.Errorf("want an API-key validation error for the unset variable, got %v", err)
	}
}

// TestDraftKeepsAProfileWithoutAKey: the profile setup is about to
// complete still comes back as configured, base_url and model included,
// while a file that does not parse is an error even for a draft.
func TestDraftKeepsAProfileWithoutAKey(t *testing.T) {
	opts := dirs(t)
	writeFile(t, filepath.Join(opts.HomeDir, ".config", "hint", "config.yaml"), `
default_provider: router
providers:
  - name: router
    kind: openai
    base_url: https://openrouter.ai/api/v1
    model: qwen/qwen3-coder
    api_key: ${UNSET_KEY}
`)
	if _, err := LoadFrom(opts); err == nil {
		t.Fatal("a profile without a key loaded; want the validation error")
	}
	opts.Draft = true
	cfg, err := LoadFrom(opts)
	if err != nil {
		t.Fatalf("draft: %v", err)
	}
	p := mustDefault(t, cfg)
	if p.BaseURL != "https://openrouter.ai/api/v1" || p.Model != "qwen/qwen3-coder" || p.APIKey != "" {
		t.Errorf("draft profile = %+v; want the file's base_url and model, no key", p)
	}

	writeFile(t, filepath.Join(opts.WorkingDir, ".hint", "config.yaml"), "providers: [\n")
	if _, err := LoadFrom(opts); err == nil {
		t.Error("a draft over a file that does not parse loaded")
	}
}

func TestFlatConfigMigration(t *testing.T) {
	opts := dirs(t)
	writeFile(t, filepath.Join(opts.WorkingDir, ".hint", "config.yaml"), `
api_url: https://api.openai.com/v1
api_key: flat-key
model: gpt-4
`)
	cfg, err := LoadFrom(opts)
	if err != nil {
		t.Fatalf("LoadFrom: %v", err)
	}
	p := mustDefault(t, cfg)
	if p.Name != "default" || p.Kind != KindOpenAI {
		t.Errorf("migrated profile = %v, want name=default kind=openai", p)
	}
	if p.BaseURL != "https://api.openai.com/v1" || p.APIKey != "flat-key" || p.Model != "gpt-4" {
		t.Errorf("migrated fields = %v, want the flat values mapped", p)
	}
	hasWarning(t, cfg, "deprecated")
}

func TestFlatKeysIgnoredWhenProvidersPresent(t *testing.T) {
	opts := dirs(t)
	writeFile(t, filepath.Join(opts.WorkingDir, ".hint", "config.yaml"), `
api_key: flat-key
providers:
  - name: p
    kind: openai
    api_key: real-key
    model: m
`)
	cfg, err := LoadFrom(opts)
	if err != nil {
		t.Fatalf("LoadFrom: %v", err)
	}
	if got := mustDefault(t, cfg).APIKey; got != "real-key" {
		t.Errorf("api_key = %q, want the providers value %q", got, "real-key")
	}
	hasWarning(t, cfg, "ignored")
}

func TestLegacyPaths(t *testing.T) {
	t.Run("legacy global read with warning", func(t *testing.T) {
		opts := dirs(t)
		writeFile(t, filepath.Join(opts.HomeDir, ".config", "hint.yaml"), `
providers:
  - name: p
    kind: openai
    api_key: k
    model: m
`)
		cfg, err := LoadFrom(opts)
		if err != nil {
			t.Fatalf("LoadFrom: %v", err)
		}
		if _, err := cfg.Profile("p"); err != nil {
			t.Errorf("legacy global file not read: %v", err)
		}
		hasWarning(t, cfg, "deprecated")
	})
	t.Run("canonical wins silently over legacy", func(t *testing.T) {
		opts := dirs(t)
		writeFile(t, filepath.Join(opts.HomeDir, ".config", "hint", "config.yaml"), `
providers:
  - name: canon
    kind: openai
    api_key: k
    model: m
`)
		writeFile(t, filepath.Join(opts.HomeDir, ".config", "hint.yaml"), `
providers:
  - name: legacy
    kind: openai
    api_key: k
    model: m
`)
		cfg, err := LoadFrom(opts)
		if err != nil {
			t.Fatalf("LoadFrom: %v", err)
		}
		if _, err := cfg.Profile("canon"); err != nil {
			t.Errorf("canonical file not read: %v", err)
		}
		if _, err := cfg.Profile("legacy"); err == nil {
			t.Error("legacy file read although the canonical one exists")
		}
		if len(cfg.Warnings) != 0 {
			t.Errorf("unexpected warnings: %v", cfg.Warnings)
		}
	})
	t.Run("legacy project path", func(t *testing.T) {
		opts := dirs(t)
		writeFile(t, filepath.Join(opts.WorkingDir, "hint.yaml"), `
providers:
  - name: p
    kind: openai
    api_key: k
    model: m
`)
		cfg, err := LoadFrom(opts)
		if err != nil {
			t.Fatalf("LoadFrom: %v", err)
		}
		if _, err := cfg.Profile("p"); err != nil {
			t.Errorf("legacy project file not read: %v", err)
		}
		hasWarning(t, cfg, "deprecated")
	})
}

func TestXDGConfigHome(t *testing.T) {
	opts := dirs(t)
	xdg := t.TempDir()
	opts.LookupEnv = lookup(map[string]string{"XDG_CONFIG_HOME": xdg})
	writeFile(t, filepath.Join(xdg, "hint", "config.yaml"), `
providers:
  - name: p
    kind: openai
    api_key: k
    model: m
`)
	cfg, err := LoadFrom(opts)
	if err != nil {
		t.Fatalf("LoadFrom: %v", err)
	}
	if _, err := cfg.Profile("p"); err != nil {
		t.Errorf("XDG_CONFIG_HOME config not read: %v", err)
	}
}

func TestZeroConfig(t *testing.T) {
	tests := []struct {
		name        string
		env         map[string]string
		flags       Flags
		wantName    string
		wantBaseURL string
		wantModel   string
		wantKey     string
		wantWarning string
	}{
		{
			name:        "APIBAR_KEY makes api-bar the default",
			env:         map[string]string{"APIBAR_KEY": "bar-key"},
			wantName:    "api-bar",
			wantBaseURL: "https://api-bar.ru/route/openai",
			wantModel:   "gpt-4o",
			wantKey:     "bar-key",
		},
		{
			name:        "API_BAR_KEY, the old spelling, still works",
			env:         map[string]string{"API_BAR_KEY": "bar-key"},
			wantName:    "api-bar",
			wantBaseURL: "https://api-bar.ru/route/openai",
			wantModel:   "gpt-4o",
			wantKey:     "bar-key",
			wantWarning: "API_BAR_KEY is the old name",
		},
		{
			name:        "APIBAR_TOKEN works too",
			env:         map[string]string{"APIBAR_TOKEN": "bar-token"},
			wantName:    "api-bar",
			wantBaseURL: "https://api-bar.ru/route/openai",
			wantModel:   "gpt-4o",
			wantKey:     "bar-token",
		},
		{
			name:        "OPENAI_API_KEY alone gives the legacy openai profile",
			env:         map[string]string{"OPENAI_API_KEY": "sk-x"},
			wantName:    "openai",
			wantBaseURL: "https://api.openai.com/v1",
			wantModel:   "gpt-4o",
			wantKey:     "sk-x",
		},
		{
			name: "api-bar wins over openai when both keys are set",
			env: map[string]string{
				"APIBAR_KEY":     "bar-key",
				"OPENAI_API_KEY": "sk-x",
			},
			wantName:    "api-bar",
			wantBaseURL: "https://api-bar.ru/route/openai",
			wantModel:   "gpt-4o",
			wantKey:     "bar-key",
		},
		{
			name:        "flags alone synthesize a profile on the kind default, a local server",
			flags:       Flags{APIKey: "flag-key", Model: "flag-model"},
			wantName:    "default",
			wantBaseURL: "http://localhost:8000/v1",
			wantModel:   "flag-model",
			wantKey:     "flag-key",
		},
		{
			name:        "HINT_API_KEY and HINT_MODEL synthesize on the kind default",
			env:         map[string]string{"HINT_API_KEY": "hint-key", "HINT_MODEL": "env-model"},
			wantName:    "default",
			wantBaseURL: "http://localhost:8000/v1",
			wantModel:   "env-model",
			wantKey:     "hint-key",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts := dirs(t)
			opts.LookupEnv = lookup(tt.env)
			opts.Flags = tt.flags
			cfg, err := LoadFrom(opts)
			if err != nil {
				t.Fatalf("LoadFrom: %v", err)
			}
			p := mustDefault(t, cfg)
			if p.Name != tt.wantName || p.BaseURL != tt.wantBaseURL || p.Model != tt.wantModel || p.APIKey != tt.wantKey {
				t.Errorf("profile = %#v, want name=%q base_url=%q model=%q key=%q",
					p, tt.wantName, tt.wantBaseURL, tt.wantModel, tt.wantKey)
			}
			warned := strings.Contains(strings.Join(cfg.Warnings, "\n"), tt.wantWarning)
			if tt.wantWarning != "" && !warned {
				t.Errorf("warnings = %v, want one about %q", cfg.Warnings, tt.wantWarning)
			}
			if tt.wantWarning == "" && len(cfg.Warnings) != 0 {
				t.Errorf("unexpected warnings %v", cfg.Warnings)
			}
		})
	}

	t.Run("a key without a model is an error that names the model", func(t *testing.T) {
		opts := dirs(t)
		opts.LookupEnv = lookup(map[string]string{"HINT_API_KEY": "hint-key"})
		_, err := LoadFrom(opts)
		var noModel *NoModelError
		if !errors.As(err, &noModel) || !strings.Contains(err.Error(), "needs a model") {
			t.Errorf("want a no-model error, got %v", err)
		}
	})

	t.Run("nothing configured is an error", func(t *testing.T) {
		opts := dirs(t)
		_, err := LoadFrom(opts)
		if err == nil || !strings.Contains(err.Error(), "no provider configured") {
			t.Errorf("want a no-provider error, got %v", err)
		}
	})
}

func TestOpenAIKindDefaultsToALocalServerNotOpenAICom(t *testing.T) {
	opts := dirs(t)
	writeFile(t, filepath.Join(opts.WorkingDir, ".hint", "config.yaml"), `
providers:
  - name: p
    kind: openai
    api_key: k
    model: m
`)
	cfg, err := LoadFrom(opts)
	if err != nil {
		t.Fatalf("LoadFrom: %v", err)
	}
	p := mustDefault(t, cfg)
	if p.BaseURL != "http://localhost:8000/v1" {
		t.Errorf("base_url = %q, want the api-bar gateway", p.BaseURL)
	}
	if strings.Contains(p.BaseURL, "api.openai.com") {
		t.Errorf("empty base_url must not resolve to api.openai.com, got %q", p.BaseURL)
	}
}

func TestUsabilityValidation(t *testing.T) {
	tests := []struct {
		name    string
		yaml    string
		wantErr string // "" = must load
	}{
		{
			name: "ollama default without key is fine",
			yaml: `
providers:
  - name: local
    kind: ollama
    model: llama3
`,
		},
		{
			name: "openai default without key fails",
			yaml: `
providers:
  - name: p
    kind: openai
`,
			wantErr: "API key",
		},
		{
			name: "spare openai profile without key does not block loading",
			yaml: `
providers:
  - name: p
    kind: openai
    api_key: k
    model: m
  - name: spare
    kind: openai
default_provider: p
`,
		},
		{
			name: "openai fallback without key fails",
			yaml: `
providers:
  - name: p
    kind: openai
    api_key: k
    model: m
  - name: fb
    kind: openai
default_provider: p
fallback_provider: fb
`,
			wantErr: "fallback",
		},
		{
			name: "ollama default without model fails",
			yaml: `
providers:
  - name: local
    kind: ollama
`,
			wantErr: "model",
		},
		{
			name: "unknown kind fails even on a spare profile",
			yaml: `
providers:
  - name: p
    kind: openai
    api_key: k
    model: m
  - name: bad
    kind: anthropic
default_provider: p
`,
			wantErr: "unknown kind",
		},
		{
			name: "unknown default_provider fails",
			yaml: `
providers:
  - name: p
    kind: openai
    api_key: k
    model: m
default_provider: nope
`,
			wantErr: "default_provider",
		},
		{
			name: "duplicate names in one file fail",
			yaml: `
providers:
  - name: p
    kind: openai
    api_key: k
    model: m
  - name: p
    kind: ollama
    model: llama3
`,
			wantErr: "duplicate",
		},
		{
			name: "two profiles without default_provider fail",
			yaml: `
providers:
  - name: a
    kind: openai
    api_key: k
    model: m
  - name: b
    kind: ollama
    model: llama3
`,
			wantErr: "default_provider",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts := dirs(t)
			writeFile(t, filepath.Join(opts.WorkingDir, ".hint", "config.yaml"), tt.yaml)
			_, err := LoadFrom(opts)
			if tt.wantErr == "" {
				if err != nil {
					t.Errorf("LoadFrom: %v, want success", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("LoadFrom error = %v, want it to contain %q", err, tt.wantErr)
			}
		})
	}
}

func TestSingleProfileIsImplicitDefault(t *testing.T) {
	opts := dirs(t)
	writeFile(t, filepath.Join(opts.WorkingDir, ".hint", "config.yaml"), `
providers:
  - name: only
    kind: openai
    api_key: k
    model: m
`)
	cfg, err := LoadFrom(opts)
	if err != nil {
		t.Fatalf("LoadFrom: %v", err)
	}
	if cfg.DefaultProvider != "only" {
		t.Errorf("DefaultProvider = %q, want the single profile %q", cfg.DefaultProvider, "only")
	}
}

func TestHintDebug(t *testing.T) {
	opts := dirs(t)
	writeFile(t, filepath.Join(opts.WorkingDir, ".hint", "config.yaml"), `
providers:
  - name: p
    kind: openai
    api_key: k
    model: m
`)
	opts.LookupEnv = lookup(map[string]string{"HINT_DEBUG": "true"})
	cfg, err := LoadFrom(opts)
	if err != nil {
		t.Fatalf("LoadFrom: %v", err)
	}
	if !cfg.Debug {
		t.Error("Debug = false, want true for HINT_DEBUG=true")
	}

	opts.LookupEnv = lookup(map[string]string{"HINT_DEBUG": "banana"})
	cfg, err = LoadFrom(opts)
	if err != nil {
		t.Fatalf("LoadFrom: %v", err)
	}
	if cfg.Debug {
		t.Error("Debug = true for a non-boolean HINT_DEBUG")
	}
	hasWarning(t, cfg, "HINT_DEBUG")
}

func TestMalformedYAMLErrorNamesThePath(t *testing.T) {
	opts := dirs(t)
	path := filepath.Join(opts.WorkingDir, ".hint", "config.yaml")
	writeFile(t, path, "providers: [not: valid: yaml")
	_, err := LoadFrom(opts)
	if err == nil || !strings.Contains(err.Error(), path) {
		t.Errorf("want a parse error naming %s, got %v", path, err)
	}
}

func TestSecrets(t *testing.T) {
	cfg := &Config{Providers: []Profile{
		{Name: "a", APIKey: "key-one"},
		{Name: "b", APIKey: "key-two"},
		{Name: "c", APIKey: "key-one"}, // duplicate
		{Name: "d"},                    // empty
	}}
	got := cfg.Secrets()
	if len(got) != 2 {
		t.Fatalf("Secrets() = %v, want two distinct keys", got)
	}
}

func TestLimits(t *testing.T) {
	base := `
providers:
  - name: p
    kind: openai
    api_key: k
    model: m
`
	t.Run("defaults", func(t *testing.T) {
		opts := dirs(t)
		writeFile(t, filepath.Join(opts.WorkingDir, ".hint", "config.yaml"), base)
		cfg, err := LoadFrom(opts)
		if err != nil {
			t.Fatalf("LoadFrom: %v", err)
		}
		if cfg.Instructions.Budget != DefaultInstructionBudget || cfg.Overview.Depth != DefaultOverviewDepth || cfg.Overview.MaxEntries != DefaultOverviewEntries {
			t.Errorf("limits = %+v / %+v, want defaults", cfg.Instructions, cfg.Overview)
		}
		if mustDefault(t, cfg).ContextWindow != 0 {
			t.Errorf("ContextWindow = %d, want 0 (unknown)", mustDefault(t, cfg).ContextWindow)
		}
	})

	t.Run("files merge and an explicit zero survives", func(t *testing.T) {
		opts := dirs(t)
		writeFile(t, filepath.Join(opts.HomeDir, ".config", "hint", "config.yaml"), base+`
instructions:
  budget: 1000
overview:
  depth: 3
  max_entries: 7
`)
		// The project file switches the overview off and leaves the rest
		// to the global file; the profile states its window.
		writeFile(t, filepath.Join(opts.WorkingDir, ".hint", "config.yaml"), `
providers:
  - name: p
    context_window: 8000
overview:
  depth: 0
`)
		cfg, err := LoadFrom(opts)
		if err != nil {
			t.Fatalf("LoadFrom: %v", err)
		}
		if cfg.Instructions.Budget != 1000 || cfg.Overview.Depth != 0 || cfg.Overview.MaxEntries != 7 {
			t.Errorf("limits = %+v / %+v, want budget 1000, depth 0, entries 7", cfg.Instructions, cfg.Overview)
		}
		if got := mustDefault(t, cfg).ContextWindow; got != 8000 {
			t.Errorf("ContextWindow = %d, want 8000", got)
		}
	})

	t.Run("flag beats env beats file", func(t *testing.T) {
		opts := dirs(t)
		writeFile(t, filepath.Join(opts.WorkingDir, ".hint", "config.yaml"), base+`
instructions:
  budget: 1000
overview:
  depth: 3
`)
		opts.LookupEnv = lookup(map[string]string{
			"HINT_INSTRUCTION_BUDGET": "2000",
			"HINT_OVERVIEW_DEPTH":     "4",
			"HINT_OVERVIEW_ENTRIES":   "50",
			"HINT_CONTEXT_WINDOW":     "16000",
		})
		opts.Flags = Flags{InstructionBudget: "3000", ContextWindow: "32000"}
		cfg, err := LoadFrom(opts)
		if err != nil {
			t.Fatalf("LoadFrom: %v", err)
		}
		if cfg.Instructions.Budget != 3000 || cfg.Overview.Depth != 4 || cfg.Overview.MaxEntries != 50 {
			t.Errorf("limits = %+v / %+v, want budget 3000 (flag), depth 4 (env), entries 50 (env)", cfg.Instructions, cfg.Overview)
		}
		if got := mustDefault(t, cfg).ContextWindow; got != 32000 {
			t.Errorf("ContextWindow = %d, want 32000 (flag)", got)
		}
	})

	t.Run("bad env is a warning, bad flag an error", func(t *testing.T) {
		opts := dirs(t)
		writeFile(t, filepath.Join(opts.WorkingDir, ".hint", "config.yaml"), base)
		opts.LookupEnv = lookup(map[string]string{"HINT_OVERVIEW_DEPTH": "deep", "HINT_INSTRUCTION_BUDGET": "-5"})
		cfg, err := LoadFrom(opts)
		if err != nil {
			t.Fatalf("LoadFrom: %v", err)
		}
		hasWarning(t, cfg, "HINT_OVERVIEW_DEPTH")
		hasWarning(t, cfg, "HINT_INSTRUCTION_BUDGET")
		if cfg.Overview.Depth != DefaultOverviewDepth || cfg.Instructions.Budget != DefaultInstructionBudget {
			t.Errorf("bad env changed the limits: %+v / %+v", cfg.Instructions, cfg.Overview)
		}

		for _, flags := range []Flags{{OverviewDepth: "deep"}, {OverviewDepth: "-1"}, {InstructionBudget: "0"}, {ContextWindow: "-1"}} {
			opts.LookupEnv = lookup(nil)
			opts.Flags = flags
			if _, err := LoadFrom(opts); err == nil {
				t.Errorf("flags %+v: want an error", flags)
			}
		}
	})

	t.Run("negative file value is an error naming the key", func(t *testing.T) {
		opts := dirs(t)
		writeFile(t, filepath.Join(opts.WorkingDir, ".hint", "config.yaml"), base+`
overview:
  max_entries: -3
`)
		_, err := LoadFrom(opts)
		if err == nil || !strings.Contains(err.Error(), "overview.max_entries") {
			t.Errorf("want an error naming overview.max_entries, got %v", err)
		}
	})
}

func TestGlobalDir(t *testing.T) {
	if got := GlobalDir("/home/u", lookup(nil)); got != filepath.Join("/home/u", ".config", "hint") {
		t.Errorf("GlobalDir = %q", got)
	}
	if got := GlobalDir("/home/u", lookup(map[string]string{"XDG_CONFIG_HOME": "/xdg"})); got != filepath.Join("/xdg", "hint") {
		t.Errorf("GlobalDir with XDG = %q", got)
	}
	if got := GlobalDir("", lookup(map[string]string{"XDG_CONFIG_HOME": ""})); got != "" {
		t.Errorf("GlobalDir without home = %q, want empty", got)
	}
}

func TestHeaderTimeout(t *testing.T) {
	t.Run("project file overrides the global profile", func(t *testing.T) {
		opts := dirs(t)
		writeFile(t, filepath.Join(opts.HomeDir, ".config", "hint", "config.yaml"), `
providers:
  - name: local
    kind: openai
    base_url: http://127.0.0.1:8080/v1
    api_key: local
    model: m
    header_timeout: 90s
`)
		writeFile(t, filepath.Join(opts.WorkingDir, ".hint", "config.yaml"), `
providers:
  - name: local
    header_timeout: 15m
`)
		cfg, err := LoadFrom(opts)
		if err != nil {
			t.Fatalf("LoadFrom: %v", err)
		}
		if got := mustDefault(t, cfg).HeaderTimeout; got != 15*time.Minute {
			t.Errorf("HeaderTimeout = %s, want 15m", got)
		}
	})

	t.Run("unset is zero", func(t *testing.T) {
		opts := dirs(t)
		writeFile(t, filepath.Join(opts.WorkingDir, ".hint", "config.yaml"), `
providers:
  - name: p
    kind: openai
    api_key: k
    model: m
`)
		cfg, err := LoadFrom(opts)
		if err != nil {
			t.Fatalf("LoadFrom: %v", err)
		}
		if got := mustDefault(t, cfg).HeaderTimeout; got != 0 {
			t.Errorf("HeaderTimeout = %s, want 0 (client default)", got)
		}
	})

	for name, value := range map[string]string{
		"negative":       "-1s",
		"bare number":    "600",
		"not a duration": "soon",
	} {
		t.Run(name+" is refused", func(t *testing.T) {
			opts := dirs(t)
			writeFile(t, filepath.Join(opts.WorkingDir, ".hint", "config.yaml"), `
providers:
  - name: p
    kind: openai
    api_key: k
    model: m
    header_timeout: `+value+`
`)
			if _, err := LoadFrom(opts); err == nil {
				t.Errorf("header_timeout: %s loaded without an error", value)
			}
		})
	}
}

func TestCompletionBlockLayersAndGuards(t *testing.T) {
	global := `
providers:
  - name: big
    kind: openai
    api_key: k
    model: m
  - name: small
    kind: ollama
    model: tiny
default_provider: big
completion:
  mode: idle
  profile: small
  idle_after: 900ms
`
	t.Run("files merge key by key with the defaults behind", func(t *testing.T) {
		opts := dirs(t)
		writeFile(t, filepath.Join(opts.HomeDir, ".config", "hint", "config.yaml"), global)
		writeFile(t, filepath.Join(opts.WorkingDir, ".hint", "config.yaml"), "completion:\n  mode: \"off\"\n")
		cfg, err := LoadFrom(opts)
		if err != nil {
			t.Fatal(err)
		}
		c := cfg.Completion
		if c.Mode != CompletionOff || c.Profile != "small" || c.IdleAfter != 900*time.Millisecond || c.WarnAfter != DefaultCompletionWarnAfter {
			t.Errorf("completion = %+v; want the project's off over the global idle, the global profile and pause, the default warn", c)
		}
	})
	t.Run("env and flag beat the files", func(t *testing.T) {
		opts := dirs(t)
		writeFile(t, filepath.Join(opts.HomeDir, ".config", "hint", "config.yaml"), global)
		opts.LookupEnv = lookup(map[string]string{"HINT_COMPLETION_MODE": "tab"})
		cfg, err := LoadFrom(opts)
		if err != nil {
			t.Fatalf("env: %v", err)
		}
		if cfg.Completion.Mode != CompletionTab {
			t.Errorf("env: mode = %v", cfg.Completion.Mode)
		}
		opts.Flags.Completion = "both"
		cfg, err = LoadFrom(opts)
		if err != nil {
			t.Fatalf("flag: %v", err)
		}
		if cfg.Completion.Mode != CompletionBoth || !cfg.Completion.Mode.OnTab() || !cfg.Completion.Mode.OnIdle() {
			t.Errorf("flag: mode = %v", cfg.Completion.Mode)
		}
		opts.Flags.Completion = "sometimes"
		if _, err := LoadFrom(opts); err == nil || !strings.Contains(err.Error(), "--completion") {
			t.Errorf("a bad flag value was accepted: %v", err)
		}
	})
	t.Run("an unusable completion profile switches the block off with a warning", func(t *testing.T) {
		opts := dirs(t)
		writeFile(t, filepath.Join(opts.HomeDir, ".config", "hint", "config.yaml"), strings.Replace(global, "profile: small", "profile: gone", 1))
		cfg, err := LoadFrom(opts)
		if err != nil {
			t.Fatalf("a missing completion profile stopped the run: %v", err)
		}
		if cfg.Completion.Mode != CompletionOff || len(cfg.Warnings) != 1 || !strings.Contains(cfg.Warnings[0], "completion is off") {
			t.Errorf("completion = %+v, warnings %v", cfg.Completion, cfg.Warnings)
		}
	})
	t.Run("defaults when the block is absent", func(t *testing.T) {
		opts := dirs(t)
		writeFile(t, filepath.Join(opts.HomeDir, ".config", "hint", "config.yaml"), strings.SplitN(global, "completion:", 2)[0])
		cfg, err := LoadFrom(opts)
		if err != nil {
			t.Fatal(err)
		}
		if c := cfg.Completion; c.Mode != CompletionOff || c.IdleAfter != DefaultCompletionIdleAfter || c.WarnAfter != DefaultCompletionWarnAfter {
			t.Errorf("completion = %+v", c)
		}
	})
}

func TestKeyringReferencesAndKeySources(t *testing.T) {
	file := `
providers:
  - name: stored
    kind: openai
    api_key: "keyring:" # the account defaults to the profile's name
    model: m
  - name: named
    kind: openai
    api_key: "keyring: team-key"
    model: m
  - name: fromenv
    kind: openai
    api_key: ${MY_KEY}
    model: m
  - name: literal
    kind: openai
    api_key: sk-literal-1234567890
    model: m
  - name: missing
    kind: openai
    api_key: keyring:nope
    model: m
default_provider: stored
`
	opts := dirs(t)
	writeFile(t, filepath.Join(opts.HomeDir, ".config", "hint", "config.yaml"), file)
	opts.LookupEnv = lookup(map[string]string{"MY_KEY": "sk-from-env-1234567890"})
	opts.Keyring = MapKeyring{"stored": "sk-stored-1234567890", "team-key": "sk-team-1234567890"}
	cfg, err := LoadFrom(opts)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string][3]string{
		"stored":  {"sk-stored-1234567890", KeyFromKeyring, "keyring:stored"},
		"named":   {"sk-team-1234567890", KeyFromKeyring, "keyring:team-key"},
		"fromenv": {"sk-from-env-1234567890", KeyFromEnv, "${MY_KEY}"},
		"literal": {"sk-literal-1234567890", KeyFromFile, ""},
		"missing": {"", KeyFromKeyring, "keyring:nope"},
	}
	for name, w := range want {
		p, err := cfg.Profile(name)
		if err != nil {
			t.Fatal(err)
		}
		if p.APIKey != w[0] || p.KeyFrom != w[1] || p.KeyRef != w[2] {
			t.Errorf("%s = key %q from %q ref %q; want %v", name, p.APIKey, p.KeyFrom, p.KeyRef, w)
		}
	}
	// A missing entry is reported with the reference and the cause.
	_, err = cfg.Usable("missing", "selected")
	var noKey *NoKeyError
	if !errors.As(err, &noKey) || noKey.Ref != "keyring:nope" || !errors.Is(err, ErrKeyringNotFound) || !strings.Contains(err.Error(), "secret store") {
		t.Errorf("missing entry = %v", err)
	}
	// The variables api_key referred to are what a subprocess must not
	// see, with the well-known ones; the secrets are every key loaded.
	env := cfg.SecretEnv()
	for _, name := range []string{"MY_KEY", "APIBAR_KEY", "OPENAI_API_KEY", "HINT_API_KEY"} {
		if !slices.Contains(env, name) {
			t.Errorf("SecretEnv lacks %s: %v", name, env)
		}
	}
	if secrets := cfg.Secrets(); len(secrets) != 4 {
		t.Errorf("Secrets = %d values, want the four keys", len(secrets))
	}

	// An overlay replaces the key and says so.
	opts.LookupEnv = lookup(map[string]string{"MY_KEY": "x", "HINT_API_KEY": "sk-overlay-1234567890"})
	cfg, err = LoadFrom(opts)
	if err != nil {
		t.Fatal(err)
	}
	if p, _ := cfg.Profile("stored"); p.APIKey != "sk-overlay-1234567890" || p.KeyFrom != KeyFromOverlay || p.KeyRef != "" {
		t.Errorf("overlay: %+v", p)
	}
	// No store in the process: a reference yields nothing, and says why.
	opts.Keyring = nil
	cfg, err = LoadFrom(opts)
	if err != nil {
		t.Fatal(err)
	}
	if p, _ := cfg.Profile("named"); p.APIKey != "" || p.KeyError == nil || !strings.Contains(p.KeyError.Error(), "no secret store") {
		t.Errorf("without a keyring: %+v, %v", p, p.KeyError)
	}
}

func TestFilterEnviron(t *testing.T) {
	env := []string{"PATH=/bin", "APIBAR_KEY=sk-1", "MY_KEY=sk-2", "COPY=sk-2", "EMPTY=", "PLAIN=hello", "NOEQ"}
	got := FilterEnviron(env, []string{"APIBAR_KEY", "MY_KEY"}, []string{"sk-2", ""})
	want := []string{"PATH=/bin", "EMPTY=", "PLAIN=hello", "NOEQ"}
	if !slices.Equal(got, want) {
		t.Errorf("FilterEnviron = %v, want %v", got, want)
	}
}

func TestSecretsStoreAndKeyringAvailable(t *testing.T) {
	opts := dirs(t)
	writeFile(t, filepath.Join(opts.HomeDir, ".config", "hint", "config.yaml"), "providers:\n  - name: p\n    kind: openai\n    api_key: k-1234567890\n    model: m\nsecrets:\n  store: file\n")
	cfg, err := LoadFrom(opts)
	if err != nil || cfg.SecretStore.Store != StoreFile {
		t.Errorf("store = %v, %v", cfg.SecretStore.Store, err)
	}
	writeFile(t, filepath.Join(opts.WorkingDir, ".hint", "config.yaml"), "secrets:\n  store: vault\n")
	if _, err := LoadFrom(opts); err == nil || !strings.Contains(err.Error(), "secrets.store") {
		t.Errorf("a bad store was accepted: %v", err)
	}
	if !KeyringAvailable(MapKeyring{}) || KeyringAvailable(nil) || KeyringAvailable(failingKeyring{}) {
		t.Error("KeyringAvailable misjudges a store")
	}
	m := MapKeyring{}
	if err := m.Set("a", "s"); err != nil {
		t.Fatal(err)
	}
	if v, err := m.Get("a"); err != nil || v != "s" {
		t.Errorf("MapKeyring round trip: %q, %v", v, err)
	}
	if err := m.Delete("a"); err != nil || m.Delete("a") == nil {
		t.Error("MapKeyring delete")
	}
}

// failingKeyring is a store that does not answer: no Secret Service.
type failingKeyring struct{}

func (failingKeyring) Get(string) (string, error) {
	return "", errors.New("config: secret store: no D-Bus session")
}
func (failingKeyring) Set(string, string) error {
	return errors.New("config: secret store: no D-Bus session")
}
func (failingKeyring) Delete(string) error {
	return errors.New("config: secret store: no D-Bus session")
}
