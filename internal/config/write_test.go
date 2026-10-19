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
	err := WriteProfile(path, "api-bar", map[string]string{"base_url": "https://example.test/v1", "api_key": "${APIBAR_KEY}", "model": "m"})
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
		LookupEnv: lookup(map[string]string{"XDG_CONFIG_HOME": filepath.Dir(filepath.Dir(path)), "APIBAR_KEY": "sk-from-env"}),
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
	if err := WriteProfile(path, "api-bar", map[string]string{"api_key": "${NEW}"}); err != nil {
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
	if err := WriteProfile(path, "b", map[string]string{"api_key": "k"}); err != nil {
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
	if err := WriteProfile(path, "a", nil); err == nil {
		t.Error("a providers value that is not a list was overwritten")
	}
	if err := WriteProfile(path, "", nil); err == nil {
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
	cfg, err := LoadFrom(Options{HomeDir: home, WorkingDir: t.TempDir(), LookupEnv: lookup(map[string]string{"VLLM_API_KEY": "k"})})
	if err != nil {
		t.Fatalf("the example does not load: %v", err)
	}
	if cfg.DefaultProvider != "vllm" || cfg.FallbackProvider != "local" || len(cfg.Providers) != 2 {
		t.Errorf("the example loads as %+v", cfg)
	}

	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	documented := map[string]bool{}
	var walk func(n *yaml.Node, prefix string)
	walk = func(n *yaml.Node, prefix string) {
		switch n.Kind {
		case yaml.DocumentNode:
			walk(n.Content[0], prefix)
		case yaml.SequenceNode:
			for _, c := range n.Content {
				walk(c, prefix+"*.")
			}
		case yaml.MappingNode:
			for i := 0; i+1 < len(n.Content); i += 2 {
				documented[prefix+n.Content[i].Value] = true
				walk(n.Content[i+1], prefix+n.Content[i].Value+".")
			}
		}
	}
	walk(&doc, "")
	for _, k := range Keys() {
		if !documented[k.Path] {
			t.Errorf("config key %q is in the schema but not in config.example.yaml", k.Path)
		}
	}
}

// TestSchemaCoversTheLoader keeps the key table and the loader's structs
// in step: every key the loader reads is in the table, and the other way
// round, so that setup and /config can neither miss a key nor offer one
// that is ignored.
func TestSchemaCoversTheLoader(t *testing.T) {
	read := map[string]bool{}
	collect := func(typ reflect.Type, prefix string) {
		for i := 0; i < typ.NumField(); i++ {
			if tag := typ.Field(i).Tag.Get("yaml"); tag != "" {
				read[prefix+tag] = true
			}
		}
	}
	collect(reflect.TypeOf(fileProfile{}), "providers.*.")
	collect(reflect.TypeOf(fileInstructions{}), "instructions.")
	collect(reflect.TypeOf(fileOverview{}), "overview.")
	collect(reflect.TypeOf(fileCompletion{}), "completion.")
	collect(reflect.TypeOf(fileSecrets{}), "secrets.")
	for _, top := range []string{"default_provider", "fallback_provider"} {
		read[top] = true
	}
	delete(read, "providers.*.name") // the profile's identity, not a settable field
	inTable := map[string]bool{}
	for _, k := range Keys() {
		inTable[k.Path] = true
		if !read[k.Path] {
			t.Errorf("schema key %q is not read by the loader", k.Path)
		}
	}
	for path := range read {
		if !inTable[path] {
			t.Errorf("the loader reads %q but the schema does not list it", path)
		}
	}
}

func TestNormalize(t *testing.T) {
	cases := []struct {
		path, in, want string
		bad            bool
	}{
		{"overview.depth", " 3 ", "3", false},
		{"overview.depth", "-1", "", true},
		{"instructions.budget", "big", "", true},
		{"instructions.summarize", "TRUE", "true", false},
		{"providers.x.header_timeout", "15m", "15m", false},
		{"providers.x.header_timeout", "15", "", true},
		{"providers.x.kind", "ollama", "ollama", false},
		{"providers.x.kind", "anthropic", "", true},
		{"providers.x.model", "", "", true},
	}
	for _, c := range cases {
		k, _, err := LookupKey(c.path)
		if err != nil {
			t.Fatalf("LookupKey(%q): %v", c.path, err)
		}
		got, err := k.Normalize(c.in)
		if (err != nil) != c.bad || got != c.want {
			t.Errorf("%s = %q: got %q, %v", c.path, c.in, got, err)
		}
	}
	for _, bad := range []string{"nope", "providers.x", "providers.x.colour", "providers..model", "overview"} {
		if _, _, err := LookupKey(bad); err == nil {
			t.Errorf("LookupKey(%q) accepted", bad)
		}
	}
}

func TestEditSetsAndUnsetsInOneWrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("# keep me\noverview:\n  depth: 2 # levels\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := Edit(path, func(d *Document) error {
		for _, kv := range [][2]string{{"overview.max_entries", "50"}, {"instructions.summarize", "yes"}, {"providers.local.model", "8"}, {"providers.local.kind", "ollama"}} {
			if err := d.Set(kv[0], kv[1]); err != nil {
				return err
			}
		}
		return d.Unset("overview.depth")
	})
	if err == nil {
		t.Fatal("\"yes\" was accepted as a boolean")
	}
	// Nothing was written by the failed edit.
	if data, _ := os.ReadFile(path); !strings.Contains(string(data), "depth: 2") {
		t.Fatalf("a failed edit changed the file:\n%s", data)
	}

	err = Edit(path, func(d *Document) error {
		for _, kv := range [][2]string{{"overview.max_entries", "50"}, {"instructions.summarize", "true"}, {"providers.local.model", "8"}, {"providers.local.kind", "ollama"}} {
			if err := d.Set(kv[0], kv[1]); err != nil {
				return err
			}
		}
		if err := d.Unset("overview.depth"); err != nil {
			return err
		}
		return d.Unset("fallback_provider") // not there: fine
	})
	if err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	got := string(data)
	for _, want := range []string{"# keep me", "max_entries: 50", "summarize: true", "name: local", "kind: ollama"} {
		if !strings.Contains(got, want) {
			t.Errorf("result lacks %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "depth:") {
		t.Errorf("unset left the key:\n%s", got)
	}
	if strings.Contains(got, "default_provider") {
		t.Errorf("setting a profile field made the profile the default:\n%s", got)
	}
	// The model "8" must come back as a string, which the loader requires.
	var fc fileConfig
	if err := yaml.Unmarshal(data, &fc); err != nil || fc.Providers[0].Model != "8" {
		t.Errorf("model read back as %+v, %v", fc.Providers, err)
	}
}
