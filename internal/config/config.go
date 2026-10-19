// Package config loads hint's configuration from CLI flags, HINT_*
// environment variables, and YAML files (project ./.hint/config.yaml and
// global ~/.config/hint/config.yaml), highest priority first.
//
// The result is a set of named provider profiles plus a default/fallback
// pair. Later packages (the provider router, WP0.3) consume Default() and
// Fallback() and know nothing about files or environment variables.
package config

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// Kind identifies the API dialect of a provider profile.
type Kind string

const (
	// KindOpenAI is the OpenAI Chat Completions dialect (vLLM, llama-server,
	// OpenAI, OpenRouter, api-bar /route/openai, Ollama /v1, ...). The
	// default profile of this kind must carry an API key.
	KindOpenAI Kind = "openai"
	// KindOllama is the same Chat Completions dialect served by a local
	// Ollama; no API key is required and WP0.3 adds a native client for
	// health checks and model listing.
	KindOllama Kind = "ollama"
)

// Built-in defaults applied to profiles after all overlays, when a field is
// still empty. An openai profile that names no endpoint is taken to be a
// model served on this machine the way vLLM serves one (decided
// 2026-10-18; until then the compiled-in default was the maintainer's
// api-bar gateway). The two hosted endpoints are used only by the
// zero-config profiles their key variables synthesize.
const (
	defaultVLLMBaseURL   = "http://localhost:8000/v1"
	defaultAPIBarBaseURL = "https://api-bar.ru/route/openai"
	defaultOpenAIBaseURL = "https://api.openai.com/v1"
	defaultOllamaBaseURL = "http://localhost:11434/v1"
	// hostedDefaultModel is the model of the two zero-config profiles,
	// whose endpoints are known to serve it. A profile from a file names
	// its model: a server serves whatever was loaded into it, so there is
	// no default worth guessing (decided 2026-10-18).
	hostedDefaultModel = "gpt-4o"
)

// Profile is one configured provider endpoint.
type Profile struct {
	Name    string
	Kind    Kind
	BaseURL string
	// APIKey holds the key after ${VAR} expansion. It must never reach a log
	// unmasked; String() and the Mask/Redact helpers exist for that.
	APIKey string
	Model  string
	// ContextWindow is the model's context size in tokens, when the
	// profile states it (`context_window`, HINT_CONTEXT_WINDOW,
	// --context-window). Zero means unknown: the agent loop then keeps
	// its built-in window for proactive compaction.
	ContextWindow int
	// HeaderTimeout bounds the wait for the response headers of a chat
	// request (`header_timeout`, e.g. "15m"). A local server such as
	// llama-server sends them only after the whole prompt is processed,
	// which on a small board takes minutes. Zero keeps the client's
	// built-in default.
	HeaderTimeout time.Duration
	// KeyFrom says where APIKey came from: KeyFromFile for a literal in
	// a config file, KeyFromEnv for a ${VAR} reference, KeyFromKeyring
	// for a keyring:<account> reference, KeyFromOverlay for HINT_API_KEY
	// or --api-key, or "" for no key. Setup uses it to say where a key
	// is and whether it could be moved into the secret store.
	KeyFrom string
	// KeyRef is the unexpanded api_key value when it was a reference
	// (${VAR} or keyring:<account>); empty for a literal. KeyError is
	// why a keyring reference yielded nothing.
	KeyRef   string
	KeyError error
}

// The sources of a profile's key, for Profile.KeyFrom.
const (
	KeyFromFile    = "file"
	KeyFromEnv     = "env"
	KeyFromKeyring = "keyring"
	KeyFromOverlay = "overlay"
)

// String implements fmt.Stringer with the API key masked, so a Profile
// printed via %v/%+v/%s can never leak the secret.
func (p Profile) String() string {
	return fmt.Sprintf("{Name:%s Kind:%s BaseURL:%s APIKey:%s Model:%s ContextWindow:%d HeaderTimeout:%s KeyFrom:%s}",
		p.Name, p.Kind, p.BaseURL, Mask(p.APIKey), p.Model, p.ContextWindow, p.HeaderTimeout, p.KeyFrom)
}

// Built-in defaults of the project-context limits, applied by the loader
// when no source sets them. They mirror internal/project's constants —
// asserted equal by a test there — because config is loaded before the
// project package is touched and must not depend on it.
const (
	// DefaultInstructionBudget is the byte budget shared by every
	// instruction file of a run.
	DefaultInstructionBudget = 32 << 10
	// DefaultOverviewDepth is how many directory levels the overview lists.
	DefaultOverviewDepth = 2
	// DefaultOverviewEntries caps the overview's entries.
	DefaultOverviewEntries = 100
)

// InstructionSettings bounds the project instruction files (HINT.md and
// its compatible names) read into the system prompt.
type InstructionSettings struct {
	// Budget is the byte budget shared by every instruction file, the
	// global one included. Always positive after loading.
	Budget int
	// BudgetExplicit reports that a file, HINT_INSTRUCTION_BUDGET or
	// --instruction-budget set Budget. When false, Budget is the built-in
	// default and the project package scales it down to the model's
	// context window (WP0.12); an explicit budget is taken as given.
	BudgetExplicit bool
	// Summarize lets a model summarize instruction files that do not fit
	// the budget even as outlines (instructions.summarize,
	// HINT_INSTRUCTIONS_SUMMARIZE, --summarize-instructions). Off by
	// default: a paraphrase of the user's own words must be their choice.
	Summarize bool
}

// OverviewSettings shapes the directory listing placed in the system
// prompt.
type OverviewSettings struct {
	// Depth is how many directory levels are listed; 0 disables the
	// overview altogether.
	Depth int
	// MaxEntries caps the number of entries listed. Always positive after
	// loading.
	MaxEntries int
}

// SecretStore says where setup puts an API key (WP1.7).
type SecretStore string

// The secret stores. Auto is the OS secret store when it answers and the
// 0600 config file otherwise, with a warning; the other two insist.
const (
	StoreAuto    SecretStore = "auto"
	StoreKeyring SecretStore = "keyring"
	StoreFile    SecretStore = "file"
)

// SecretsSettings is the secrets: block of the config files.
type SecretsSettings struct {
	Store SecretStore
}

// wellKnownKeyEnv are the variables the zero-config path reads a key
// from; a tool subprocess never sees them.
var wellKnownKeyEnv = []string{"APIBAR_KEY", "APIBAR_TOKEN", "API_BAR_KEY", "OPENAI_API_KEY", "HINT_API_KEY"}

// CompletionMode says when the shell asks a model to continue what the
// user is typing (WP1.6).
type CompletionMode string

// The completion modes. Off is the default: a continuation is a request
// to a provider on the user's behalf, and that is opted into.
const (
	CompletionOff CompletionMode = "off"
	// CompletionTab asks only when the user presses Tab on a question.
	CompletionTab CompletionMode = "tab"
	// CompletionIdle asks after a pause in typing (CompletionSettings.IdleAfter).
	CompletionIdle CompletionMode = "idle"
	// CompletionBoth is tab and idle together.
	CompletionBoth CompletionMode = "both"
)

// OnTab says whether the mode asks when Tab is pressed.
func (m CompletionMode) OnTab() bool { return m == CompletionTab || m == CompletionBoth }

// OnIdle says whether the mode asks after a pause in typing.
func (m CompletionMode) OnIdle() bool { return m == CompletionIdle || m == CompletionBoth }

// Built-in defaults of the completion block.
const (
	DefaultCompletionIdleAfter = 600 * time.Millisecond
	DefaultCompletionWarnAfter = 2 * time.Second
)

// CompletionSettings is the completion: block of the config files, with
// the usual layering (the project file overrides the global one key by
// key) and the defaults applied.
type CompletionSettings struct {
	Mode CompletionMode
	// Profile is the provider profile that answers completions; empty
	// means the session's own. Model overrides that profile's model.
	Profile string
	Model   string
	// IdleAfter is the pause in typing that triggers a request in the
	// idle mode; WarnAfter is how long a request may take before the
	// shell says the model is slow.
	IdleAfter time.Duration
	WarnAfter time.Duration
}

// Config is the fully resolved configuration.
type Config struct {
	Providers        []Profile
	DefaultProvider  string
	FallbackProvider string
	// Debug is the --debug flag or HINT_DEBUG: write the run's redacted
	// request/response trace to a log file.
	Debug bool
	// Instructions and Overview are the project-context limits as resolved
	// from flags, HINT_* variables and the instructions:/overview: blocks
	// of the config files, with the built-in defaults applied.
	Instructions InstructionSettings
	Overview     OverviewSettings
	// Completion is the completion: block (WP1.6).
	Completion CompletionSettings
	// SecretStore is the secrets: block (WP1.7).
	SecretStore SecretsSettings
	// keyEnv are the variables the files' api_key values referred to
	// (${VAR}); SecretEnv adds the well-known ones.
	keyEnv []string
	// Warnings collects non-fatal findings (legacy paths, migrated flat
	// keys, ignored values). The CLI prints them to stderr once per run.
	Warnings []string
}

// Profile returns the profile with the given name.
func (c *Config) Profile(name string) (Profile, error) {
	for _, p := range c.Providers {
		if p.Name == name {
			return p, nil
		}
	}
	return Profile{}, fmt.Errorf("config: unknown provider profile %q", name)
}

// Default returns the profile selected as default_provider.
func (c *Config) Default() (Profile, error) {
	if c.DefaultProvider == "" {
		return Profile{}, fmt.Errorf("config: no default provider configured")
	}
	return c.Profile(c.DefaultProvider)
}

// Usable returns the profile named name after checking that a run can
// use it: every profile needs a model, an openai profile a key as well.
// It is the check the loader applies to the default and fallback
// profiles, offered for a profile picked later (session.profile, WP1.5).
// role names the profile's part in the error ("default", "selected").
func (c *Config) Usable(name, role string) (Profile, error) {
	p, err := c.Profile(name)
	if err != nil {
		return Profile{}, err
	}
	if p.Kind == KindOpenAI && p.APIKey == "" {
		return Profile{}, &NoKeyError{Role: role, Profile: p.Name, Ref: p.KeyRef, Cause: p.KeyError}
	}
	if p.Model == "" {
		return Profile{}, &NoModelError{Role: role, Profile: p.Name}
	}
	return p, nil
}

// Fallback returns the fallback profile, or false when none is configured.
func (c *Config) Fallback() (Profile, bool) {
	if c.FallbackProvider == "" {
		return Profile{}, false
	}
	p, err := c.Profile(c.FallbackProvider)
	if err != nil {
		return Profile{}, false
	}
	return p, true
}

// Secrets returns every distinct non-empty API key in the configuration —
// the input for Redact when scrubbing log output.
func (c *Config) Secrets() []string {
	seen := make(map[string]struct{}, len(c.Providers))
	var out []string
	for _, p := range c.Providers {
		if p.APIKey == "" {
			continue
		}
		if _, ok := seen[p.APIKey]; ok {
			continue
		}
		seen[p.APIKey] = struct{}{}
		out = append(out, p.APIKey)
	}
	return out
}

// SecretEnv returns the names of the environment variables that hold, or
// may hold, an API key: every ${VAR} an api_key referred to, and the
// well-known ones the zero-config path reads. A tool subprocess runs
// without them (see FilterEnviron).
func (c *Config) SecretEnv() []string {
	seen := map[string]bool{}
	var out []string
	for _, name := range append(append([]string{}, c.keyEnv...), wellKnownKeyEnv...) {
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		out = append(out, name)
	}
	return out
}

// FilterEnviron returns environ (KEY=value lines, as os.Environ gives
// them) without the variables named in names and without any variable
// whose value is one of values: a key under its own name, and a copy of
// it under another.
func FilterEnviron(environ, names, values []string) []string {
	drop := make(map[string]bool, len(names))
	for _, n := range names {
		drop[n] = true
	}
	secret := make(map[string]bool, len(values))
	for _, v := range values {
		if v != "" {
			secret[v] = true
		}
	}
	out := make([]string, 0, len(environ))
	for _, kv := range environ {
		name, value, _ := strings.Cut(kv, "=")
		if drop[name] || secret[value] {
			continue
		}
		out = append(out, kv)
	}
	return out
}

// ErrNoProvider is what loading returns when nothing says which provider
// to use: no file, no key in the environment, no flag. A shell that can
// ask questions answers it with the interactive setup.
var ErrNoProvider = errors.New("config: no provider configured: run `hint setup`, set APIBAR_KEY (or OPENAI_API_KEY), pass --api-key, or create ~/.config/hint/config.yaml (see config.example.yaml)")

// NoKeyError is a profile the run needs that has no API key — none in
// the file, a ${VAR} that expands to nothing, or a keyring:<account>
// the secret store has no entry for (Ref and Cause say which).
type NoKeyError struct {
	// Role is "default", "fallback" or "selected".
	Role    string
	Profile string
	// Ref is the api_key reference that yielded nothing, if any.
	Ref string
	// Cause is why a keyring reference yielded nothing.
	Cause error
}

// NoModelError is a profile the run needs that names no model. A server
// serves whatever was loaded into it, so the loader never guesses one.
type NoModelError struct {
	// Role is "default", "fallback" or "selected".
	Role    string
	Profile string
}

func (e *NoModelError) Error() string {
	return fmt.Sprintf("config: %s profile %q needs a model: set model (hint models lists what the server serves), HINT_MODEL, or --model", e.Role, e.Profile)
}

func (e *NoKeyError) Error() string {
	switch {
	case e.Cause != nil:
		return fmt.Sprintf("config: %s profile %q refers to the secret store (%s) but no key came back: %v; run `hint setup` to store one", e.Role, e.Profile, e.Ref, e.Cause)
	case e.Ref != "":
		return fmt.Sprintf("config: %s profile %q needs an API key: %s expands to nothing; export it, or run `hint setup`", e.Role, e.Profile, e.Ref)
	}
	return fmt.Sprintf("config: %s profile %q needs an API key: run `hint setup`, or set api_key (e.g. ${VLLM_API_KEY}), HINT_API_KEY, or --api-key", e.Role, e.Profile)
}

// Unwrap exposes the secret store's error.
func (e *NoKeyError) Unwrap() error { return e.Cause }
