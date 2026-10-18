package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestWriteProfileCreatesAFileOnlyTheOwnerReads(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hint", "config.yaml")
	err := WriteProfile(path, ProfileUpdate{Name: "api-bar", BaseURL: "https://example.test/v1", APIKey: "${API_BAR_KEY}", Model: "m"})
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v, want 0600", info.Mode().Perm())
	}
	// What was written loads back as the profile, with the default set.
	cfg, err := LoadFrom(Options{
		HomeDir:   filepath.Dir(filepath.Dir(path)),
		LookupEnv: lookup(map[string]string{"XDG_CONFIG_HOME": filepath.Dir(filepath.Dir(path)), "API_BAR_KEY": "sk-from-env"}),
	})
	if err != nil {
		t.Fatalf("written config does not load: %v", err)
	}
	p, err := cfg.Profile("api-bar")
	if err != nil || cfg.DefaultProvider != "api-bar" || p.APIKey != "sk-from-env" || p.Model != "m" || p.BaseURL != "https://example.test/v1" {
		t.Errorf("profile = %+v, default = %q, err = %v", p, cfg.DefaultProvider, err)
	}
}

func TestWriteProfileEditsInPlaceAndKeepsComments(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	before := `# my providers
providers:
  - name: local   # the offline one
    kind: ollama
    model: qwen
  - name: api-bar
    kind: openai
    api_key: old-key # rotate me
    context_window: 128000
default_provider: api-bar
`
	if err := os.WriteFile(path, []byte(before), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := WriteProfile(path, ProfileUpdate{Name: "api-bar", APIKey: "${NEW}"}); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	got := string(data)
	for _, want := range []string{"# my providers", "# the offline one", "# rotate me", "api_key: ${NEW}", "context_window: 128000", "model: qwen", "default_provider: api-bar"} {
		if !strings.Contains(got, want) {
			t.Errorf("result lacks %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "old-key") {
		t.Errorf("the old key is still there:\n%s", got)
	}
	if strings.Count(got, "name: api-bar") != 1 {
		t.Errorf("the profile was duplicated instead of edited:\n%s", got)
	}
}

func TestWriteProfileAddsAProfileWithoutChangingTheDefault(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("providers:\n  - name: a\n    kind: openai\ndefault_provider: a\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := WriteProfile(path, ProfileUpdate{Name: "b", APIKey: "k"}); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	got := string(data)
	if !strings.Contains(got, "name: b") || !strings.Contains(got, "default_provider: a") {
		t.Errorf("unexpected result:\n%s", got)
	}
}

func TestWriteProfileRefusesWhatItCannotEdit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("providers: nope\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := WriteProfile(path, ProfileUpdate{Name: "a"}); err == nil {
		t.Error("a providers value that is not a list was overwritten")
	}
	if err := WriteProfile(path, ProfileUpdate{}); err == nil {
		t.Error("a profile without a name was written")
	}
}

func TestLocate(t *testing.T) {
	home, wd := t.TempDir(), t.TempDir()
	loc := Locate(home, wd, lookup(nil))
	if want := filepath.Join(home, ".config", "hint", "config.yaml"); loc.Global != want || len(loc.Read) != 0 {
		t.Errorf("empty home: %+v, want global %s and nothing read", loc, want)
	}
	proj := filepath.Join(wd, ".hint", "config.yaml")
	if err := os.MkdirAll(filepath.Dir(proj), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(proj, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if loc := Locate(home, wd, lookup(nil)); len(loc.Read) != 1 || loc.Read[0] != proj {
		t.Errorf("project file not found: %+v", loc)
	}
}

// TestExampleConfigIsCurrent keeps config.example.yaml honest: it must
// load, and it must mention every key the loader reads, so that a new key
// cannot be added without being documented.
func TestExampleConfigIsCurrent(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "config.example.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	global := filepath.Join(home, ".config", "hint")
	if err := os.MkdirAll(global, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(global, "config.yaml"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadFrom(Options{HomeDir: home, WorkingDir: t.TempDir(), LookupEnv: lookup(map[string]string{"API_BAR_KEY": "k"})})
	if err != nil {
		t.Fatalf("the example does not load: %v", err)
	}
	if cfg.DefaultProvider != "api-bar" || cfg.FallbackProvider != "local" || len(cfg.Providers) != 2 {
		t.Errorf("the example loads as %+v", cfg)
	}

	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	documented := map[string]bool{}
	var walk func(n *yaml.Node)
	walk = func(n *yaml.Node) {
		for i, c := range n.Content {
			if n.Kind == yaml.MappingNode && i%2 == 0 {
				documented[c.Value] = true
			}
			walk(c)
		}
	}
	walk(&doc)
	for _, typ := range []reflect.Type{reflect.TypeOf(fileConfig{}), reflect.TypeOf(fileProfile{}), reflect.TypeOf(fileInstructions{}), reflect.TypeOf(fileOverview{})} {
		for i := 0; i < typ.NumField(); i++ {
			key := typ.Field(i).Tag.Get("yaml")
			if key == "" || key == "api_url" || (typ == reflect.TypeOf(fileConfig{}) && (key == "api_key" || key == "model")) {
				continue // the legacy flat schema is deliberately not documented
			}
			if !documented[key] {
				t.Errorf("config key %q (%s) is not in config.example.yaml", key, typ.Name())
			}
		}
	}
}
