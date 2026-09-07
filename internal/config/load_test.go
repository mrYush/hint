package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
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
`)
		writeFile(t, filepath.Join(opts.HomeDir, ".config", "hint.yaml"), `
providers:
  - name: legacy
    kind: openai
    api_key: k
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
	}{
		{
			name:        "API_BAR_KEY makes api-bar the default",
			env:         map[string]string{"API_BAR_KEY": "bar-key"},
			wantName:    "api-bar",
			wantBaseURL: "https://api-bar.ru/route/openai",
			wantModel:   "gpt-4o",
			wantKey:     "bar-key",
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
				"API_BAR_KEY":    "bar-key",
				"OPENAI_API_KEY": "sk-x",
			},
			wantName:    "api-bar",
			wantBaseURL: "https://api-bar.ru/route/openai",
			wantModel:   "gpt-4o",
			wantKey:     "bar-key",
		},
		{
			name:        "flags alone synthesize a profile on the product default",
			flags:       Flags{APIKey: "flag-key"},
			wantName:    "default",
			wantBaseURL: "https://api-bar.ru/route/openai",
			wantModel:   "gpt-4o",
			wantKey:     "flag-key",
		},
		{
			name:        "HINT_API_KEY alone synthesizes on the product default",
			env:         map[string]string{"HINT_API_KEY": "hint-key"},
			wantName:    "default",
			wantBaseURL: "https://api-bar.ru/route/openai",
			wantModel:   "gpt-4o",
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
		})
	}

	t.Run("nothing configured is an error", func(t *testing.T) {
		opts := dirs(t)
		_, err := LoadFrom(opts)
		if err == nil || !strings.Contains(err.Error(), "no provider configured") {
			t.Errorf("want a no-provider error, got %v", err)
		}
	})
}

func TestOpenAIKindDefaultsToAPIBarNotOpenAICom(t *testing.T) {
	opts := dirs(t)
	writeFile(t, filepath.Join(opts.WorkingDir, ".hint", "config.yaml"), `
providers:
  - name: p
    kind: openai
    api_key: k
`)
	cfg, err := LoadFrom(opts)
	if err != nil {
		t.Fatalf("LoadFrom: %v", err)
	}
	p := mustDefault(t, cfg)
	if p.BaseURL != "https://api-bar.ru/route/openai" {
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
