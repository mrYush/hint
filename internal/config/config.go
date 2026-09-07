// Package config loads hint's configuration from CLI flags, HINT_*
// environment variables, and YAML files (project ./.hint/config.yaml and
// global ~/.config/hint/config.yaml), highest priority first.
//
// The result is a set of named provider profiles plus a default/fallback
// pair. Later packages (the provider router, WP0.3) consume Default() and
// Fallback() and know nothing about files or environment variables.
package config

import (
	"fmt"
)

// Kind identifies the API dialect of a provider profile.
type Kind string

const (
	// KindOpenAI is the OpenAI Chat Completions dialect (OpenAI, OpenRouter,
	// vLLM, api-bar /route/openai, Ollama /v1, ...). The default profile of
	// this kind must carry an API key.
	KindOpenAI Kind = "openai"
	// KindOllama is the same Chat Completions dialect served by a local
	// Ollama; no API key is required and WP0.3 adds a native client for
	// health checks and model listing.
	KindOllama Kind = "ollama"
)

// Built-in defaults applied to profiles after all overlays, when a field is
// still empty. The product default endpoint is the api-bar gateway, not
// api.openai.com; OpenAI remains an explicit opt-in profile.
const (
	defaultAPIBarBaseURL = "https://api-bar.ru/route/openai"
	defaultOpenAIBaseURL = "https://api.openai.com/v1"
	defaultOpenAIModel   = "gpt-4o"
	defaultOllamaBaseURL = "http://localhost:11434/v1"
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
}

// String implements fmt.Stringer with the API key masked, so a Profile
// printed via %v/%+v/%s can never leak the secret.
func (p Profile) String() string {
	return fmt.Sprintf("{Name:%s Kind:%s BaseURL:%s APIKey:%s Model:%s ContextWindow:%d}",
		p.Name, p.Kind, p.BaseURL, Mask(p.APIKey), p.Model, p.ContextWindow)
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
