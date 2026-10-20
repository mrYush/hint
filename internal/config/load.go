package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Flags carries the values of the CLI configuration flags. An empty string
// means "flag not passed" — flag defaults must stay empty so a value from a
// config file is not silently overridden (the old --model=gpt-4 bug).
//
// The numeric knobs are strings for the same reason: a cobra int flag
// cannot tell "not passed" from an explicit 0, and 0 is a legal value for
// --overview-depth. The loader parses them and reports a bad number as an
// error naming the flag.
type Flags struct {
	Provider string // --provider: profile to use as default for this run
	APIURL   string // --api-url: overrides base_url of the selected profile
	APIKey   string // --api-key: overrides api_key of the selected profile
	Model    string // --model: overrides model of the selected profile

	ContextWindow     string // --context-window: overrides context_window of the selected profile
	InstructionBudget string // --instruction-budget: bytes for all instruction files
	OverviewDepth     string // --overview-depth: directory levels in the overview, 0 for none
	OverviewEntries   string // --overview-entries: cap on overview entries

	// SummarizeInstructions is --summarize-instructions: "true" or "false",
	// a string so that an unset flag stays distinct from an explicit false.
	SummarizeInstructions string
	// Completion is --completion: a completion mode for this run.
	Completion string
}

// Options are the external inputs of LoadFrom. Everything the loader touches
// outside its arguments — home directory, working directory, environment —
// is injected here, so tests run against a map and t.TempDir() instead of
// process-global state.
type Options struct {
	HomeDir    string
	WorkingDir string
	LookupEnv  func(string) (string, bool)
	Flags      Flags
	// Keyring resolves api_key: keyring:<account> references. Nil means
	// no secret store in this process: such a reference yields no key.
	Keyring Keyring
}

// Load resolves configuration against the real process environment.
func Load(flags Flags) (*Config, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		home = "" // no home directory: global config is simply not read
	}
	wd, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("config: resolving working directory: %w", err)
	}
	return LoadFrom(Options{
		HomeDir:    home,
		WorkingDir: wd,
		LookupEnv:  os.LookupEnv,
		Flags:      flags,
		Keyring:    OSKeyring{},
	})
}

// LoadFrom resolves configuration from the given inputs.
//
// Field priority, highest first: CLI flags → HINT_* environment → project
// ./.hint/config.yaml → global ~/.config/hint/config.yaml → built-in
// defaults. The default profile NAME is resolved first (--provider →
// HINT_PROVIDER → files), and only then are the field overlays (HINT_API_*,
// --api-*) applied to that selected profile — so an overlay always lands on
// the profile the run will actually use, and never on the fallback.
func LoadFrom(opts Options) (*Config, error) {
	l := &loader{opts: opts}
	if l.opts.LookupEnv == nil {
		l.opts.LookupEnv = func(string) (string, bool) { return "", false }
	}

	global, err := l.readGlobal()
	if err != nil {
		return nil, err
	}
	project, err := l.readProject()
	if err != nil {
		return nil, err
	}

	merged := mergeFiles(global, project)
	l.expand(&merged)

	cfg := &Config{
		Providers:        make([]Profile, 0, len(merged.Providers)),
		DefaultProvider:  merged.DefaultProvider,
		FallbackProvider: merged.FallbackProvider,
	}
	for _, p := range merged.Providers {
		prof := Profile{
			Name:     p.Name,
			Kind:     Kind(p.Kind),
			BaseURL:  p.BaseURL,
			APIKey:   p.APIKey,
			Model:    p.Model,
			KeyFrom:  p.keyFrom,
			KeyRef:   p.keyRef,
			KeyError: p.keyErr,

			MaxTokensField: p.MaxTokensField,
		}
		if p.ContextWindow != nil {
			prof.ContextWindow = *p.ContextWindow
		}
		if p.HeaderTimeout != nil {
			prof.HeaderTimeout = *p.HeaderTimeout
		}
		cfg.Providers = append(cfg.Providers, prof)
	}

	l.applyEnvScalars(cfg)
	if opts.Flags.Provider != "" {
		cfg.DefaultProvider = opts.Flags.Provider
	}

	l.synthesizeIfEmpty(cfg)
	if cfg.DefaultProvider == "" && len(cfg.Providers) == 1 {
		cfg.DefaultProvider = cfg.Providers[0].Name
	}

	if err := l.overlaySelected(cfg); err != nil {
		return nil, err
	}
	applyKindDefaults(cfg)
	if err := l.resolveLimits(cfg, merged); err != nil {
		return nil, err
	}

	if err := l.validate(cfg); err != nil {
		return nil, err
	}
	cfg.Warnings = l.warnings
	return cfg, nil
}

// loader threads Options and accumulated warnings through the pipeline.
type loader struct {
	opts     Options
	warnings []string
	// keyEnv collects the ${VAR} names api_key values referred to.
	keyEnv []string
}

func (l *loader) warnf(format string, args ...any) {
	l.warnings = append(l.warnings, fmt.Sprintf(format, args...))
}

// env returns the value of a set, non-empty environment variable. A variable
// set to the empty string is treated as unset.
func (l *loader) env(name string) (string, bool) {
	v, ok := l.opts.LookupEnv(name)
	if !ok || v == "" {
		return "", false
	}
	return v, true
}

// fileConfig mirrors the YAML schema of one config file, including the
// legacy flat keys that migrate() folds into a profile.
type fileConfig struct {
	Providers        []fileProfile `yaml:"providers"`
	DefaultProvider  string        `yaml:"default_provider"`
	FallbackProvider string        `yaml:"fallback_provider"`

	// Project-context limits. Pointers so that a file can set an explicit
	// 0 (overview: {depth: 0} switches the overview off) and still be told
	// apart from a file that says nothing.
	Instructions fileInstructions `yaml:"instructions"`
	Overview     fileOverview     `yaml:"overview"`
	Completion   fileCompletion   `yaml:"completion"`
	Secrets      fileSecrets      `yaml:"secrets"`

	// Legacy flat schema (pre-WP0.2).
	APIURL string `yaml:"api_url"`
	APIKey string `yaml:"api_key"`
	Model  string `yaml:"model"`
}

type fileProfile struct {
	Name          string `yaml:"name"`
	Kind          string `yaml:"kind"`
	BaseURL       string `yaml:"base_url"`
	APIKey        string `yaml:"api_key"`
	Model         string `yaml:"model"`
	ContextWindow *int   `yaml:"context_window"`
	// HeaderTimeout is a Go duration string ("90s", "15m"); yaml.v3
	// refuses a bare number for a time.Duration, so units are mandatory.
	HeaderTimeout  *time.Duration `yaml:"header_timeout"`
	MaxTokensField string         `yaml:"max_tokens_field"`
	// keyFrom, keyRef and keyErr are filled by expand: where APIKey came
	// from, and why a reference yielded nothing.
	keyFrom, keyRef string
	keyErr          error
}

type fileInstructions struct {
	Budget    *int  `yaml:"budget"`
	Summarize *bool `yaml:"summarize"`
}

type fileOverview struct {
	Depth      *int `yaml:"depth"`
	MaxEntries *int `yaml:"max_entries"`
}

type fileSecrets struct {
	Store string `yaml:"store"`
}

type fileCompletion struct {
	Mode      string         `yaml:"mode"`
	Profile   string         `yaml:"profile"`
	Model     string         `yaml:"model"`
	IdleAfter *time.Duration `yaml:"idle_after"`
	WarnAfter *time.Duration `yaml:"warn_after"`
}

// GlobalDir returns the directory of the global configuration —
// $XDG_CONFIG_HOME/hint when the variable is set, otherwise
// ~/.config/hint — or "" when neither the home directory nor the variable
// is known. The config file lives there, and so does the global HINT.md
// that internal/project reads before a repository's own files.
func GlobalDir(home string, lookupEnv func(string) (string, bool)) string {
	if lookupEnv != nil {
		if xdg, ok := lookupEnv("XDG_CONFIG_HOME"); ok && xdg != "" {
			return filepath.Join(xdg, "hint")
		}
	}
	if home == "" {
		return ""
	}
	return filepath.Join(home, ".config", "hint")
}

// readGlobal loads the global config: <GlobalDir>/config.yaml, falling
// back to the pre-WP0.2 path ~/.config/hint.yaml with a deprecation
// warning.
func (l *loader) readGlobal() (fileConfig, error) {
	dir := GlobalDir(l.opts.HomeDir, l.opts.LookupEnv)
	if dir == "" {
		return fileConfig{}, nil
	}
	canonical := filepath.Join(dir, "config.yaml")
	legacy := ""
	if l.opts.HomeDir != "" {
		legacy = filepath.Join(l.opts.HomeDir, ".config", "hint.yaml")
	}
	return l.readWithLegacy(canonical, legacy)
}

// readProject loads the project config <WorkingDir>/.hint/config.yaml,
// falling back to the pre-WP0.2 path <WorkingDir>/hint.yaml with a warning.
func (l *loader) readProject() (fileConfig, error) {
	if l.opts.WorkingDir == "" {
		return fileConfig{}, nil
	}
	canonical := filepath.Join(l.opts.WorkingDir, ".hint", "config.yaml")
	legacy := filepath.Join(l.opts.WorkingDir, "hint.yaml")
	return l.readWithLegacy(canonical, legacy)
}

func (l *loader) readWithLegacy(canonical, legacy string) (fileConfig, error) {
	fc, found, err := l.readFile(canonical)
	if err != nil {
		return fileConfig{}, err
	}
	if found {
		return fc, nil
	}
	if legacy == "" {
		return fileConfig{}, nil
	}
	fc, found, err = l.readFile(legacy)
	if err != nil {
		return fileConfig{}, err
	}
	if found {
		l.warnf("config path %s is deprecated; move it to %s", legacy, canonical)
		return fc, nil
	}
	return fileConfig{}, nil
}

// readFile parses one YAML config file. A missing file is not an error;
// unreadable or malformed files are, and the error names the path.
func (l *loader) readFile(path string) (fileConfig, bool, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return fileConfig{}, false, nil
	}
	if err != nil {
		return fileConfig{}, false, fmt.Errorf("config: reading %s: %w", path, err)
	}
	var fc fileConfig
	if err := yaml.Unmarshal(data, &fc); err != nil {
		return fileConfig{}, false, fmt.Errorf("config: parsing %s: %w", path, err)
	}
	seen := make(map[string]struct{}, len(fc.Providers))
	for _, p := range fc.Providers {
		if p.Name == "" {
			return fileConfig{}, false, fmt.Errorf("config: %s: provider profile without a name", path)
		}
		if _, dup := seen[p.Name]; dup {
			return fileConfig{}, false, fmt.Errorf("config: %s: duplicate provider profile %q", path, p.Name)
		}
		seen[p.Name] = struct{}{}
	}
	l.migrate(&fc, path)
	return fc, true, nil
}

// migrate folds the legacy flat api_url/api_key/model keys into a single
// implicit profile named "default". When both schemas are present the flat
// keys are ignored, with a warning either way.
func (l *loader) migrate(fc *fileConfig, path string) {
	flat := fc.APIURL != "" || fc.APIKey != "" || fc.Model != ""
	if !flat {
		return
	}
	if len(fc.Providers) > 0 {
		l.warnf("%s: flat api_url/api_key/model are ignored because providers is set; remove them", path)
	} else {
		fc.Providers = []fileProfile{{
			Name:    "default",
			Kind:    string(KindOpenAI),
			BaseURL: fc.APIURL,
			APIKey:  fc.APIKey,
			Model:   fc.Model,
		}}
		if fc.DefaultProvider == "" {
			fc.DefaultProvider = "default"
		}
		l.warnf("%s: flat api_url/api_key/model config is deprecated and was mapped to a single profile %q; switch to the providers list", path, "default")
	}
	fc.APIURL, fc.APIKey, fc.Model = "", "", ""
}

// mergeFiles overlays the project file onto the global one. Profiles merge
// by name — a non-empty project field wins, project-only profiles are
// appended — so a project can override just api_key or model of a globally
// defined profile. Scalars follow the same non-empty-wins rule.
func mergeFiles(global, project fileConfig) fileConfig {
	out := global
	for _, pp := range project.Providers {
		merged := false
		for i, gp := range out.Providers {
			if gp.Name != pp.Name {
				continue
			}
			if pp.Kind != "" {
				out.Providers[i].Kind = pp.Kind
			}
			if pp.BaseURL != "" {
				out.Providers[i].BaseURL = pp.BaseURL
			}
			if pp.APIKey != "" {
				out.Providers[i].APIKey = pp.APIKey
			}
			if pp.Model != "" {
				out.Providers[i].Model = pp.Model
			}
			if pp.ContextWindow != nil {
				out.Providers[i].ContextWindow = pp.ContextWindow
			}
			if pp.HeaderTimeout != nil {
				out.Providers[i].HeaderTimeout = pp.HeaderTimeout
			}
			if pp.MaxTokensField != "" {
				out.Providers[i].MaxTokensField = pp.MaxTokensField
			}
			merged = true
			break
		}
		if !merged {
			out.Providers = append(out.Providers, pp)
		}
	}
	if project.DefaultProvider != "" {
		out.DefaultProvider = project.DefaultProvider
	}
	if project.FallbackProvider != "" {
		out.FallbackProvider = project.FallbackProvider
	}
	if project.Instructions.Budget != nil {
		out.Instructions.Budget = project.Instructions.Budget
	}
	if project.Instructions.Summarize != nil {
		out.Instructions.Summarize = project.Instructions.Summarize
	}
	if project.Overview.Depth != nil {
		out.Overview.Depth = project.Overview.Depth
	}
	if project.Overview.MaxEntries != nil {
		out.Overview.MaxEntries = project.Overview.MaxEntries
	}
	// An explicit "off" in the project file is a non-empty value and
	// beats the global file: a team can keep completion out of a
	// repository a developer has it on everywhere else.
	if project.Completion.Mode != "" {
		out.Completion.Mode = project.Completion.Mode
	}
	if project.Completion.Profile != "" {
		out.Completion.Profile = project.Completion.Profile
	}
	if project.Completion.Model != "" {
		out.Completion.Model = project.Completion.Model
	}
	if project.Completion.IdleAfter != nil {
		out.Completion.IdleAfter = project.Completion.IdleAfter
	}
	if project.Completion.WarnAfter != nil {
		out.Completion.WarnAfter = project.Completion.WarnAfter
	}
	if project.Secrets.Store != "" {
		out.Secrets.Store = project.Secrets.Store
	}
	return out
}

// expand resolves $VAR/${VAR} references in file-sourced values. Flag and
// environment overlays are deliberately NOT expanded: their values come from
// the shell, which has already done its own expansion.
func (l *loader) expand(fc *fileConfig) {
	for i := range fc.Providers {
		p := &fc.Providers[i]
		p.BaseURL = expandValue(p.BaseURL, l.opts.LookupEnv)
		p.APIKey, p.keyFrom, p.keyRef, p.keyErr = l.resolveKey(p.Name, p.APIKey)
		p.Model = expandValue(p.Model, l.opts.LookupEnv)
	}
	fc.DefaultProvider = expandValue(fc.DefaultProvider, l.opts.LookupEnv)
	fc.FallbackProvider = expandValue(fc.FallbackProvider, l.opts.LookupEnv)
}

// envRefs matches the $VAR and ${VAR} references os.Expand understands.
var envRefs = regexp.MustCompile(`\$\{?([A-Za-z_][A-Za-z0-9_]*)\}?`)

// resolveKey turns an api_key value into the key and says where it came
// from: a keyring:<account> reference is read from the secret store (the
// profile's name when the account is empty), a ${VAR} reference from the
// environment — and its name is remembered, so no tool subprocess sees
// it — and anything else is the key itself.
func (l *loader) resolveKey(profile, value string) (key, from, ref string, err error) {
	if value == "" {
		return "", "", "", nil
	}
	if account, ok := KeyringRef(value); ok {
		if account == "" {
			account = profile
		}
		ref = KeyringValue(account)
		if l.opts.Keyring == nil {
			return "", KeyFromKeyring, ref, errors.New("no secret store in this process")
		}
		key, err := l.opts.Keyring.Get(account)
		if err != nil {
			return "", KeyFromKeyring, ref, err
		}
		return key, KeyFromKeyring, ref, nil
	}
	if names := envRefs.FindAllStringSubmatch(value, -1); len(names) > 0 {
		for _, m := range names {
			l.keyEnv = append(l.keyEnv, m[1])
		}
		return expandValue(value, l.opts.LookupEnv), KeyFromEnv, value, nil
	}
	return value, KeyFromFile, "", nil
}

// applyEnvScalars overlays the HINT_* variables that are not tied to a
// single profile: provider selection and the debug switch.
func (l *loader) applyEnvScalars(cfg *Config) {
	if v, ok := l.env("HINT_PROVIDER"); ok {
		cfg.DefaultProvider = v
	} else if v, ok := l.env("HINT_DEFAULT_PROVIDER"); ok {
		cfg.DefaultProvider = v
	}
	if v, ok := l.env("HINT_FALLBACK_PROVIDER"); ok {
		cfg.FallbackProvider = v
	}
	if v, ok := l.env("HINT_DEBUG"); ok {
		b, err := strconv.ParseBool(v)
		if err != nil {
			l.warnf("HINT_DEBUG=%q is not a boolean and was ignored", v)
		} else {
			cfg.Debug = b
		}
	}
}

// synthesizeIfEmpty provides the zero-config path: with no profiles from any
// file, a single profile is built from well-known key variables. Implicit
// key pickup happens ONLY here — an explicit profile must name its key via
// ${VAR}; the loader never guesses a key for it. Each hosted endpoint has
// its own variable (APIBAR_KEY or APIBAR_TOKEN for the api-bar gateway,
// OPENAI_API_KEY for api.openai.com) and api-bar wins when both are set;
// API_BAR_KEY is the earlier spelling and still works, with a warning.
// A bare HINT_API_KEY or --api-key lands on the kind default: a server on
// this machine.
func (l *loader) synthesizeIfEmpty(cfg *Config) {
	if len(cfg.Providers) > 0 {
		return
	}
	for _, name := range []string{"APIBAR_KEY", "APIBAR_TOKEN", "API_BAR_KEY"} {
		key, ok := l.env(name)
		if !ok {
			continue
		}
		if name == "API_BAR_KEY" {
			l.warnf("API_BAR_KEY is the old name of APIBAR_KEY; rename it")
		}
		l.addSynthetic(cfg, "api-bar", defaultAPIBarBaseURL, hostedDefaultModel, key)
		return
	}
	if key, ok := l.env("OPENAI_API_KEY"); ok {
		l.addSynthetic(cfg, "openai", defaultOpenAIBaseURL, hostedDefaultModel, key)
		return
	}
	// No well-known key, but a key may still arrive via HINT_API_KEY or
	// --api-key: give the overlays a profile to land on. The model has to
	// come the same way (HINT_MODEL, --model); validate says so otherwise.
	_, envKey := l.env("HINT_API_KEY")
	if envKey || l.opts.Flags.APIKey != "" {
		l.addSynthetic(cfg, "default", "", "", "")
	}
}

// addSynthetic appends an openai profile and makes it the default when
// nothing named one. An empty baseURL leaves the kind default to
// applyKindDefaults; an empty model stays empty.
func (l *loader) addSynthetic(cfg *Config, name, baseURL, model, key string) {
	cfg.Providers = append(cfg.Providers, Profile{
		Name:    name,
		Kind:    KindOpenAI,
		BaseURL: baseURL,
		Model:   model,
		APIKey:  key,
	})
	if cfg.DefaultProvider == "" {
		cfg.DefaultProvider = name
	}
}

// overlaySelected applies the per-profile overlays — HINT_API_URL /
// HINT_API_KEY / HINT_MODEL, then the CLI flags — to the profile selected as
// default. Other profiles, the fallback included, keep their own values:
// otherwise HINT_API_KEY would hand a cloud key to the offline profile.
func (l *loader) overlaySelected(cfg *Config) error {
	var p *Profile
	for i := range cfg.Providers {
		if cfg.Providers[i].Name == cfg.DefaultProvider {
			p = &cfg.Providers[i]
			break
		}
	}
	if p == nil {
		return nil // unknown default_provider: validate reports it
	}
	if v, ok := l.env("HINT_API_URL"); ok {
		p.BaseURL = v
	}
	if v, ok := l.env("HINT_API_KEY"); ok {
		p.APIKey, p.KeyFrom, p.KeyRef, p.KeyError = v, KeyFromOverlay, "", nil
	}
	if v, ok := l.env("HINT_MODEL"); ok {
		p.Model = v
	}
	if l.opts.Flags.APIURL != "" {
		p.BaseURL = l.opts.Flags.APIURL
	}
	if l.opts.Flags.APIKey != "" {
		p.APIKey, p.KeyFrom, p.KeyRef, p.KeyError = l.opts.Flags.APIKey, KeyFromOverlay, "", nil
	}
	if l.opts.Flags.Model != "" {
		p.Model = l.opts.Flags.Model
	}
	_, err := l.overlayInt(&p.ContextWindow, "context_window", "HINT_CONTEXT_WINDOW", "--context-window", l.opts.Flags.ContextWindow, 0)
	return err
}

// resolveLimits settles the project-context limits: the merged files'
// values, then the HINT_* variables, then the flags, then the built-in
// defaults for whatever is still unset. A negative value is rejected
// wherever it came from; a non-numeric one is an error from a flag and a
// warning from the environment, the way HINT_DEBUG is treated.
func (l *loader) resolveLimits(cfg *Config, files fileConfig) error {
	if files.Instructions.Budget != nil {
		cfg.Instructions.Budget = *files.Instructions.Budget
		cfg.Instructions.BudgetExplicit = true
	}
	if files.Instructions.Summarize != nil {
		cfg.Instructions.Summarize = *files.Instructions.Summarize
	}
	if files.Overview.Depth != nil {
		cfg.Overview.Depth = *files.Overview.Depth
	} else {
		cfg.Overview.Depth = DefaultOverviewDepth
	}
	if files.Overview.MaxEntries != nil {
		cfg.Overview.MaxEntries = *files.Overview.MaxEntries
	}
	set, err := l.overlayInt(&cfg.Instructions.Budget, "instructions.budget", "HINT_INSTRUCTION_BUDGET", "--instruction-budget", l.opts.Flags.InstructionBudget, 1)
	if err != nil {
		return err
	}
	cfg.Instructions.BudgetExplicit = cfg.Instructions.BudgetExplicit || set
	if _, err := l.overlayInt(&cfg.Overview.Depth, "overview.depth", "HINT_OVERVIEW_DEPTH", "--overview-depth", l.opts.Flags.OverviewDepth, 0); err != nil {
		return err
	}
	if _, err := l.overlayInt(&cfg.Overview.MaxEntries, "overview.max_entries", "HINT_OVERVIEW_ENTRIES", "--overview-entries", l.opts.Flags.OverviewEntries, 1); err != nil {
		return err
	}
	if err := l.overlayBool(&cfg.Instructions.Summarize, "HINT_INSTRUCTIONS_SUMMARIZE", "--summarize-instructions", l.opts.Flags.SummarizeInstructions); err != nil {
		return err
	}
	if cfg.Instructions.Budget <= 0 {
		cfg.Instructions.Budget = DefaultInstructionBudget
	}
	if cfg.Overview.MaxEntries <= 0 {
		cfg.Overview.MaxEntries = DefaultOverviewEntries
	}
	if err := l.resolveCompletion(cfg, files.Completion); err != nil {
		return err
	}
	cfg.keyEnv = append([]string(nil), l.keyEnv...)
	cfg.SecretStore.Store = SecretStore(files.Secrets.Store)
	switch cfg.SecretStore.Store {
	case "":
		cfg.SecretStore.Store = StoreAuto
	case StoreAuto, StoreKeyring, StoreFile:
	default:
		return fmt.Errorf("config: secrets.store must be auto, keyring or file, not %q", cfg.SecretStore.Store)
	}
	return nil
}

// resolveCompletion settles the completion: block: the merged files, then
// HINT_COMPLETION_MODE, then --completion for the mode; the durations
// from the files or the defaults. A profile it names must exist and be
// usable, or the block is switched off with a warning — a project file
// must not stop a run for a profile only one developer has.
func (l *loader) resolveCompletion(cfg *Config, fc fileCompletion) error {
	c := &cfg.Completion
	c.Mode = CompletionMode(fc.Mode)
	c.Profile, c.Model = fc.Profile, fc.Model
	c.IdleAfter, c.WarnAfter = DefaultCompletionIdleAfter, DefaultCompletionWarnAfter
	if fc.IdleAfter != nil {
		c.IdleAfter = *fc.IdleAfter
	}
	if fc.WarnAfter != nil {
		c.WarnAfter = *fc.WarnAfter
	}
	if v, ok := l.env("HINT_COMPLETION_MODE"); ok {
		if validCompletionMode(v) {
			c.Mode = CompletionMode(v)
		} else {
			l.warnf("HINT_COMPLETION_MODE=%q is not a completion mode and was ignored", v)
		}
	}
	if v := l.opts.Flags.Completion; v != "" {
		if !validCompletionMode(v) {
			return fmt.Errorf("config: --completion must be off, tab, idle or both, not %q", v)
		}
		c.Mode = CompletionMode(v)
	}
	switch {
	case c.Mode == "":
		c.Mode = CompletionOff
	case !validCompletionMode(string(c.Mode)):
		return fmt.Errorf("config: completion.mode must be off, tab, idle or both, not %q", c.Mode)
	}
	if c.IdleAfter <= 0 || c.WarnAfter <= 0 {
		return errors.New("config: completion.idle_after and completion.warn_after must be positive durations")
	}
	if c.Profile != "" && c.Mode != CompletionOff {
		if _, err := cfg.Usable(c.Profile, "completion"); err != nil {
			l.warnf("completion is off: %s", strings.TrimPrefix(err.Error(), "config: "))
			c.Mode = CompletionOff
		}
	}
	return nil
}

func validCompletionMode(v string) bool {
	switch CompletionMode(v) {
	case CompletionOff, CompletionTab, CompletionIdle, CompletionBoth:
		return true
	}
	return false
}

// overlayBool applies the environment variable and then the flag to *dst,
// each when set, with the same tolerance as HINT_DEBUG: a variable that
// is not a boolean is a warning that leaves *dst alone, a flag that is
// not one is an error.
func (l *loader) overlayBool(dst *bool, envName, flagName, flagValue string) error {
	if v, ok := l.env(envName); ok {
		b, err := strconv.ParseBool(v)
		if err != nil {
			l.warnf("%s=%q is not a boolean and was ignored", envName, v)
		} else {
			*dst = b
		}
	}
	if flagValue != "" {
		b, err := strconv.ParseBool(flagValue)
		if err != nil {
			return fmt.Errorf("config: %s: %q is not a boolean", flagName, flagValue)
		}
		*dst = b
	}
	return nil
}

// overlayInt applies the environment variable and then the flag to *dst,
// each when set, and reports whether either did. A value below min is
// refused: the flag as an error, the variable as a warning that leaves
// *dst alone. The file value already in *dst (named key in messages) is
// checked against min too, since a file can say -1 as easily; a file's 0
// is left for the defaults to fill.
func (l *loader) overlayInt(dst *int, key, envName, flagName, flagValue string, min int) (set bool, err error) {
	if *dst < min && *dst != 0 {
		return false, fmt.Errorf("config: %s must be at least %d, got %d", key, min, *dst)
	}
	if v, ok := l.env(envName); ok {
		n, err := strconv.Atoi(v)
		switch {
		case err != nil:
			l.warnf("%s=%q is not a number and was ignored", envName, v)
		case n < min:
			l.warnf("%s=%d is below the minimum of %d and was ignored", envName, n, min)
		default:
			*dst, set = n, true
		}
	}
	if flagValue != "" {
		n, err := strconv.Atoi(flagValue)
		if err != nil {
			return set, fmt.Errorf("config: %s: %q is not a number", flagName, flagValue)
		}
		if n < min {
			return set, fmt.Errorf("config: %s must be at least %d, got %d", flagName, min, n)
		}
		*dst, set = n, true
	}
	return set, nil
}

// applyKindDefaults fills fields that are still empty after every overlay.
func applyKindDefaults(cfg *Config) {
	for i := range cfg.Providers {
		p := &cfg.Providers[i]
		if p.Kind == "" {
			p.Kind = KindOpenAI
		}
		switch p.Kind {
		case KindOpenAI:
			if p.BaseURL == "" {
				p.BaseURL = defaultVLLMBaseURL
			}
		case KindOllama:
			if p.BaseURL == "" {
				p.BaseURL = defaultOllamaBaseURL
			}
		}
		// An empty model stays empty for either kind: a server serves
		// whatever was loaded into it, and validate rejects a selected
		// profile that names none.
	}
}

// validate checks structural rules for every profile, but usability rules
// (an openai key, an ollama model) only for the profiles the run can
// actually use — default and fallback. A spare profile missing its key must
// not prevent the run from starting; it fails when selected.
func (l *loader) validate(cfg *Config) error {
	if len(cfg.Providers) == 0 {
		return ErrNoProvider
	}
	for _, p := range cfg.Providers {
		switch p.Kind {
		case KindOpenAI, KindOllama:
		default:
			return fmt.Errorf("config: profile %q: unknown kind %q (want %q or %q)", p.Name, p.Kind, KindOpenAI, KindOllama)
		}
		if p.HeaderTimeout < 0 {
			return fmt.Errorf("config: profile %q: header_timeout %s is negative", p.Name, p.HeaderTimeout)
		}
		switch p.MaxTokensField {
		case "", FieldMaxTokens, FieldMaxCompletionTokens:
		default:
			return fmt.Errorf("config: profile %q: max_tokens_field must be %s or %s, not %q", p.Name, FieldMaxTokens, FieldMaxCompletionTokens, p.MaxTokensField)
		}
	}
	if cfg.DefaultProvider == "" {
		return errors.New("config: default_provider must be set when more than one profile is defined")
	}
	if _, err := cfg.Profile(cfg.DefaultProvider); err != nil {
		return fmt.Errorf("config: default_provider: %w", err)
	}
	if cfg.FallbackProvider != "" {
		if _, err := cfg.Profile(cfg.FallbackProvider); err != nil {
			return fmt.Errorf("config: fallback_provider: %w", err)
		}
		if cfg.FallbackProvider == cfg.DefaultProvider {
			l.warnf("fallback_provider %q equals default_provider and was ignored", cfg.FallbackProvider)
			cfg.FallbackProvider = ""
		}
	}
	if err := l.validateUsable(cfg, cfg.DefaultProvider, "default"); err != nil {
		return err
	}
	if cfg.FallbackProvider != "" {
		if err := l.validateUsable(cfg, cfg.FallbackProvider, "fallback"); err != nil {
			return err
		}
	}
	return nil
}

func (l *loader) validateUsable(cfg *Config, name, role string) error {
	_, err := cfg.Usable(name, role)
	return err
}
