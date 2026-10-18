package config

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"
)

// KeyType is the kind of value a config key holds; it decides how an
// answer typed by the user is checked and how it is written.
type KeyType int

// The key types.
const (
	// TypeString is free text.
	TypeString KeyType = iota
	// TypeInt is a whole number, at least Key.Min.
	TypeInt
	// TypeBool is true or false.
	TypeBool
	// TypeDuration is a Go duration with units: 90s, 15m.
	TypeDuration
	// TypeEnum is one of Key.Enum.
	TypeEnum
	// TypeSecret is a string that is never echoed: an API key.
	TypeSecret
)

// Key describes one key of the config file. The table of them in [Keys]
// is the one place that says which keys exist: the interactive setup,
// `hint config` and the TUI's /config build on it, and a test checks it
// against the loader's structs and against config.example.yaml.
type Key struct {
	// Path is the key as written on the command line: "overview.depth",
	// "default_provider", or "providers.*.model" for a profile field,
	// where * is the profile name.
	Path string
	Type KeyType
	// Enum are the allowed values of a TypeEnum key.
	Enum []string
	// Min is the smallest allowed value of a TypeInt key.
	Min int
	// Default is the value that applies when no layer sets the key, as
	// the user would write it; empty when there is none.
	Default string
	// Env and Flag are the overlays that beat the files, when there are.
	Env, Flag string
	Help      string
}

// profilePrefix starts the path of every profile field.
const profilePrefix = "providers.*."

// Profile reports whether the key is a field of a provider profile, and
// returns the field name.
func (k Key) Profile() (field string, ok bool) {
	return strings.CutPrefix(k.Path, profilePrefix)
}

// keys is the table. Order is the order of config.example.yaml and of
// the setup form.
var keys = []Key{
	{Path: "providers.*.kind", Type: TypeEnum, Enum: []string{string(KindOpenAI), string(KindOllama)}, Default: string(KindOpenAI),
		Help: "API dialect: openai (Chat Completions) or ollama (no key needed)"},
	{Path: "providers.*.base_url", Type: TypeString, Default: defaultAPIBarBaseURL, Env: "HINT_API_URL", Flag: "--api-url",
		Help: "endpoint; hint appends /chat/completions. Default for ollama: " + defaultOllamaBaseURL},
	{Path: "providers.*.api_key", Type: TypeSecret, Env: "HINT_API_KEY", Flag: "--api-key",
		Help: "the provider's key, or ${VAR} to read it from the environment; required for openai"},
	{Path: "providers.*.model", Type: TypeString, Default: defaultOpenAIModel, Env: "HINT_MODEL", Flag: "--model",
		Help: "model name; ollama has no default"},
	{Path: "providers.*.context_window", Type: TypeInt, Min: 1, Env: "HINT_CONTEXT_WINDOW", Flag: "--context-window",
		Help: "the model's context size in tokens; sizes the compaction"},
	{Path: "providers.*.header_timeout", Type: TypeDuration, Default: "60s",
		Help: "how long to wait for response headers, with units (90s, 15m); raise it for a slow local server"},
	{Path: "default_provider", Type: TypeString, Env: "HINT_PROVIDER", Flag: "--provider",
		Help: "the profile a run uses; optional with a single profile"},
	{Path: "fallback_provider", Type: TypeString, Env: "HINT_FALLBACK_PROVIDER",
		Help: "the profile tried when the default one is unreachable"},
	{Path: "instructions.budget", Type: TypeInt, Min: 1, Default: strconv.Itoa(DefaultInstructionBudget), Env: "HINT_INSTRUCTION_BUDGET", Flag: "--instruction-budget",
		Help: "bytes all HINT.md / AGENTS.md / CLAUDE.md files may take together"},
	{Path: "instructions.summarize", Type: TypeBool, Default: "false", Env: "HINT_INSTRUCTIONS_SUMMARIZE", Flag: "--summarize-instructions",
		Help: "let the model summarize instruction files that do not fit even as outlines"},
	{Path: "overview.depth", Type: TypeInt, Min: 0, Default: strconv.Itoa(DefaultOverviewDepth), Env: "HINT_OVERVIEW_DEPTH", Flag: "--overview-depth",
		Help: "directory levels listed in the system prompt; 0 for none"},
	{Path: "overview.max_entries", Type: TypeInt, Min: 1, Default: strconv.Itoa(DefaultOverviewEntries), Env: "HINT_OVERVIEW_ENTRIES", Flag: "--overview-entries",
		Help: "cap on the entries the overview lists"},
}

// Keys returns the table of config keys. The slice is a copy.
func Keys() []Key { return slices.Clone(keys) }

// ProfileKeys returns the profile fields, in table order.
func ProfileKeys() []Key {
	var out []Key
	for _, k := range keys {
		if _, ok := k.Profile(); ok {
			out = append(out, k)
		}
	}
	return out
}

// LookupKey resolves a path as typed by the user. A profile field is
// written providers.<name>.<field>; profile is then the name.
func LookupKey(path string) (k Key, profile string, err error) {
	if rest, ok := strings.CutPrefix(path, "providers."); ok {
		name, field, ok := strings.Cut(rest, ".")
		if !ok || name == "" || strings.Contains(field, ".") {
			return Key{}, "", fmt.Errorf("config: a profile field is providers.<profile>.<field>, not %q", path)
		}
		for _, k := range keys {
			if f, ok := k.Profile(); ok && f == field {
				return k, name, nil
			}
		}
		return Key{}, "", fmt.Errorf("config: profiles have no field %q (hint config keys lists them)", field)
	}
	for _, k := range keys {
		if k.Path == path {
			return k, "", nil
		}
	}
	return Key{}, "", fmt.Errorf("config: unknown key %q (hint config keys lists them)", path)
}

// Normalize checks value against the key's type and returns it in the
// form it is written. An empty value is an error; to remove a key, unset
// it.
func (k Key) Normalize(value string) (string, error) {
	v := strings.TrimSpace(value)
	if v == "" {
		return "", fmt.Errorf("config: %s needs a value", k.Path)
	}
	switch k.Type {
	case TypeInt:
		n, err := strconv.Atoi(v)
		if err != nil {
			return "", fmt.Errorf("config: %s must be a whole number, not %q", k.Path, v)
		}
		if n < k.Min {
			return "", fmt.Errorf("config: %s must be at least %d", k.Path, k.Min)
		}
		return strconv.Itoa(n), nil
	case TypeBool:
		b, err := strconv.ParseBool(v)
		if err != nil {
			return "", fmt.Errorf("config: %s must be true or false, not %q", k.Path, v)
		}
		return strconv.FormatBool(b), nil
	case TypeDuration:
		d, err := time.ParseDuration(v)
		if err != nil || d <= 0 {
			return "", fmt.Errorf("config: %s must be a positive duration with units (90s, 15m), not %q", k.Path, v)
		}
		return v, nil
	case TypeEnum:
		if !slices.Contains(k.Enum, v) {
			return "", fmt.Errorf("config: %s must be one of %s, not %q", k.Path, strings.Join(k.Enum, ", "), v)
		}
		return v, nil
	case TypeString, TypeSecret:
	}
	return v, nil
}
