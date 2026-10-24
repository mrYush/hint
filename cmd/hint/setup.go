package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"strings"

	"github.com/charmbracelet/x/term"
	"github.com/spf13/cobra"

	"github.com/mrYush/hint/internal/config"
	"github.com/mrYush/hint/internal/console"
	"github.com/mrYush/hint/internal/rpc"
	"github.com/mrYush/hint/internal/tui"
	"github.com/mrYush/hint/pkg/agentapi"
)

// defaultBaseURL is offered for a profile that has none: the kind default,
// a vLLM-style server on this machine.
const defaultBaseURL = "http://localhost:8000/v1"

// setupInfo is what the shell knows about the configuration this run
// uses, for the moment a credential is rejected: which profile, which
// files, and the one place setup writes to.
type setupInfo struct {
	profile config.Profile
	locs    config.Locations
	// cwd and flags are what the run was started with, so that showing
	// the effective configuration resolves the same layers.
	cwd   string
	flags config.Flags
	// store is where a key goes (secrets.store); keyring is the secret
	// store it goes into, nil for none in this process.
	store   config.SecretStore
	keyring config.Keyring
}

// needsKey says whether the profile has no key yet, so that an empty
// answer would leave it unusable. An ollama profile needs none.
func (s *setupInfo) needsKey() bool {
	return s.profile.APIKey == "" && s.profile.Kind != config.KindOllama
}

// newSetupInfo describes the configuration cfg was loaded from. cfg may
// be nil (loading failed): setup then starts from nothing.
func newSetupInfo(cfg *config.Config, cwd string, flags config.Flags) *setupInfo {
	home, err := os.UserHomeDir()
	if err != nil {
		home = ""
	}
	s := &setupInfo{locs: config.Locate(home, cwd, os.LookupEnv), cwd: cwd, flags: flags, store: config.StoreAuto, keyring: config.OSKeyring{}}
	if cfg != nil {
		if p, err := cfg.Default(); err == nil {
			s.profile = p
		}
		if cfg.SecretStore.Store != "" {
			s.store = cfg.SecretStore.Store
		}
	}
	if s.profile.Name == "" {
		s.profile.Name = "vllm"
	}
	if s.profile.BaseURL == "" {
		s.profile.BaseURL = defaultBaseURL
	}
	return s
}

// describe says which configuration is in use and where setup would
// write, in the words of the "rejected key" message.
func (s *setupInfo) describe() string {
	var b strings.Builder
	if len(s.locs.Read) == 0 {
		b.WriteString("no config file is in use; the profile comes from the environment and the built-in defaults\n")
	} else {
		b.WriteString("config in use: " + strings.Join(s.locs.Read, ", ") + " (the later one wins)\n")
	}
	fmt.Fprintf(&b, "profile %q at %s", s.profile.Name, s.profile.BaseURL)
	if s.locs.Global != "" {
		b.WriteString("\nsetup writes to " + s.locs.Global)
	}
	return b.String()
}

// save writes the answers to the global config and says what the user has
// to do next. The key is written as given: a literal, or ${VAR} to be
// read from the environment. Putting the profile to use is the shell's
// job: a running session switches to it, which makes the core re-read
// the file.
func (s *setupInfo) save(profile string, values map[string]string) (string, error) {
	if s.locs.Global == "" {
		return "", errNowhere
	}
	values = maps.Clone(values)
	key := values["api_key"]
	stored, storeErr := s.storeKey(profile, key)
	if storeErr != nil && s.store == config.StoreKeyring {
		return "", storeErr
	}
	if stored {
		values["api_key"] = config.KeyringValue(profile)
	}
	if err := config.WriteProfile(s.locs.Global, profile, values); err != nil {
		return "", err
	}
	msg := "saved to " + s.locs.Global
	switch {
	case stored:
		msg = "the API key is in the OS secret store (hint config show-key " + profile + " shows it again); " + msg
	case storeErr != nil:
		msg += "; no secret store answered (" + strings.TrimPrefix(storeErr.Error(), "config: ") + "), so the key is in that file, mode 0600: anyone running as you can read it"
	}
	if name, ok := envReference(key); ok {
		if _, set := os.LookupEnv(name); !set {
			msg += "; " + name + " is not set in this shell: export it before starting hint"
		}
	}
	for _, p := range s.locs.Read {
		if p != s.locs.Global {
			msg += "; " + p + " is read after the global file and may override these values"
		}
	}
	return msg, nil
}

// storeKey puts a literal key into the secret store under the profile's
// name, when the store policy and the store allow. A reference (${VAR},
// keyring:) or an empty key is written as it is.
func (s *setupInfo) storeKey(profile, key string) (bool, error) {
	if key == "" || s.store == config.StoreFile {
		return false, nil
	}
	if _, ok := envReference(key); ok {
		return false, nil
	}
	if _, ok := config.KeyringRef(key); ok {
		return false, nil
	}
	if s.keyring == nil {
		return false, errors.New("no secret store in this process")
	}
	if err := s.keyring.Set(profile, key); err != nil {
		return false, err
	}
	return true, nil
}

// showKey is `hint config show-key`: the one way to see a profile's key
// again. It asks first, and prints the key masked unless reveal; it is
// not reachable over RPC or from a tool, by construction.
func showKey(cfg *config.Config, name string, reveal bool, in setupIO, out io.Writer) error {
	p, err := cfg.Profile(name)
	if err != nil {
		return err
	}
	if p.APIKey == "" {
		if p.KeyError != nil {
			return fmt.Errorf("profile %q has no key: %s (%w)", name, p.KeyRef, p.KeyError)
		}
		return fmt.Errorf("profile %q has no key", name)
	}
	where := "a config file"
	switch p.KeyFrom {
	case config.KeyFromKeyring:
		where = "the OS secret store (" + p.KeyRef + ")"
	case config.KeyFromEnv:
		where = "the environment (" + p.KeyRef + ")"
	case config.KeyFromOverlay:
		where = "HINT_API_KEY or --api-key"
	}
	what := "masked"
	if reveal {
		what = "in full"
	}
	answer, err := in.Line(context.Background(), fmt.Sprintf("Show the API key of profile %q from %s, %s? [y/N]: ", name, where, what))
	if err != nil {
		return err
	}
	if console.Answer(answer) != "y" {
		return errors.New("not shown")
	}
	if reveal {
		fmt.Fprintln(out, p.APIKey)
	} else {
		fmt.Fprintln(out, config.Mask(p.APIKey))
	}
	return nil
}

// envReference returns the variable name of a ${NAME} or $NAME key.
func envReference(key string) (string, bool) {
	name := strings.TrimPrefix(key, "$")
	if name == key {
		return "", false
	}
	name = strings.TrimSuffix(strings.TrimPrefix(name, "{"), "}")
	return name, name != ""
}

// tuiSetup is the same setup as the TUI's form.
func (s *setupInfo) tuiSetup() *tui.Setup {
	return &tui.Setup{
		Describe: s.describe(),
		Profile:  s.profile.Name,
		Values:   s.currentValues(),
		Save:     s.save,
		Set:      s.set,
		Unset:    s.unset,
		Show:     s.show,
	}
}

// authHelp is what a shell adds to a turn that failed, when the failure
// is a rejected credential: where the configuration is and how to fix it.
func (a *app) authHelp(err error) string {
	var ae *agentapi.Error
	if !errors.As(err, &ae) || ae.Kind != agentapi.ErrAuth {
		return ""
	}
	if a.setup == nil {
		return "this core runs in another process (hint serve); its configuration is on that machine"
	}
	return a.setup.describe()
}

// setupIO is where setup asks its questions. Two implementations: the
// REPL's line reader, which cannot hide what is typed, and the bare
// terminal before a session starts, which can.
type setupIO interface {
	// Line asks one question and returns the trimmed answer.
	Line(ctx context.Context, prompt string) (string, error)
	// Secret asks for something that must not be shown. ok is false when
	// this input cannot hide it; the caller then asks for something that
	// is not secret instead.
	Secret(ctx context.Context, prompt string) (value string, ok bool, err error)
}

// lineIO asks through the shared line reader of a running session.
// Stdin belongs to that reader's goroutine, so echo cannot be switched
// off: Secret declines.
type lineIO struct {
	lines *console.LineReader
	out   io.Writer
}

func (l lineIO) Line(ctx context.Context, prompt string) (string, error) {
	fmt.Fprint(l.out, prompt)
	line, err := l.lines.ReadLine(ctx)
	return strings.TrimSpace(line), err
}

func (l lineIO) Secret(context.Context, string) (string, bool, error) { return "", false, nil }

// ttyIO asks on the bare terminal, before any line reader exists to own
// stdin, so the key can be read with echo off.
type ttyIO struct {
	in  *os.File
	r   *bufio.Reader
	out io.Writer
}

func newTTYIO(in *os.File, out io.Writer) *ttyIO {
	return &ttyIO{in: in, r: bufio.NewReader(in), out: out}
}

func (t *ttyIO) Line(_ context.Context, prompt string) (string, error) {
	fmt.Fprint(t.out, prompt)
	line, err := t.r.ReadString('\n')
	if err != nil && line == "" {
		return "", err
	}
	return strings.TrimSpace(line), nil
}

func (t *ttyIO) Secret(_ context.Context, prompt string) (string, bool, error) {
	fmt.Fprint(t.out, prompt)
	b, err := term.ReadPassword(t.in.Fd())
	fmt.Fprintln(t.out)
	if err != nil {
		return "", false, err
	}
	return strings.TrimSpace(string(b)), true, nil
}

// askSetup asks for the profile, the base URL, the model and the key,
// with what is known as the default of each, writes the answers and
// returns the profile's name. The model is required — a server serves
// what was loaded into it, so there is nothing to default to — and an
// empty answer is asked again. Where the input can hide what is typed the
// key is typed (or given as ${VAR}); where it cannot, the question is the
// name of the environment variable that holds it.
func askSetup(ctx context.Context, in setupIO, out io.Writer, s *setupInfo) (string, error) {
	ask := func(prompt, def, empty string) (string, error) {
		switch {
		case def != "":
			prompt += " [" + def + "]"
		case empty != "":
			prompt += " [" + empty + "]"
		}
		line, err := in.Line(ctx, prompt+": ")
		if err != nil {
			return "", err
		}
		if line != "" {
			return line, nil
		}
		return def, nil
	}
	profile, err := ask("profile name", s.profile.Name, "")
	if err != nil {
		return "", err
	}
	baseURL, err := ask("base URL", s.profile.BaseURL, "")
	if err != nil {
		return "", err
	}
	// A model is required: a server serves what was loaded into it, and
	// the loader guesses none, so an empty answer is asked again.
	model, err := ask("model", s.profile.Model, "required; hint models lists what the server serves")
	for err == nil && model == "" {
		fmt.Fprintln(out, "hint: a model is needed — the name of one the server serves (hint models lists them); Ctrl-C stops the setup")
		model, err = ask("model", "", "required")
	}
	if err != nil {
		return "", err
	}
	key, err := askKey(ctx, in, out, s.needsKey())
	if err != nil {
		return "", err
	}
	msg, err := s.save(profile, map[string]string{"base_url": baseURL, "model": model, "api_key": key})
	if err != nil {
		return "", err
	}
	fmt.Fprintln(out, "hint:", msg)
	return profile, nil
}

// keyHelp is what the key question says about the answer it wants.
const keyHelp = "the provider's API key (what your vLLM or llama-server was started with, or the key from " +
	"your account at an aggregator such as OpenRouter or api-bar.ru), or ${VARIABLE} to read it from the environment at every start"

// askKey asks for the key. Where the input can hide it, the key itself
// (or ${VAR}) is typed; where it cannot, the name of the variable that
// holds it. When the profile has no key yet an empty answer is not
// accepted — it would write a profile that cannot answer — and the
// question is repeated; Ctrl-C leaves.
func askKey(ctx context.Context, in setupIO, out io.Writer, required bool) (string, error) {
	empty := "Enter keeps the current key"
	if required {
		empty = "required: this profile has no key yet"
	}
	for {
		key, ok, err := in.Secret(ctx, "API key, input hidden ("+empty+"): ")
		if err != nil {
			return "", err
		}
		if !ok {
			name, err := in.Line(ctx, "name of the environment variable that holds the API key, e.g. VLLM_API_KEY ("+empty+"): ")
			if err != nil {
				return "", err
			}
			if name != "" {
				key = "${" + strings.Trim(name, "${}") + "}"
			}
		}
		if key != "" || !required {
			return key, nil
		}
		fmt.Fprintln(out, "hint: a key is needed —", keyHelp+"; Ctrl-C stops the setup")
	}
}

// offerSetup runs after a rejected credential in the REPL: it shows
// where the configuration is, asks whether to fill it in now, and puts
// the written profile to use in the session so the next question goes
// out with the new key.
func (a *app) offerSetup(ctx context.Context, help string) {
	fmt.Fprintln(a.io.err, "hint:", help)
	if a.setup == nil || !a.io.tty {
		return
	}
	fmt.Fprint(a.io.err, "Fill the configuration in now? [y/N]: ")
	line, err := a.lines.ReadLine(ctx)
	if err != nil || console.Answer(line) != "y" {
		return
	}
	profile, err := askSetup(ctx, lineIO{lines: a.lines, out: a.io.err}, a.io.err, a.setup)
	if err != nil {
		if ctx.Err() == nil {
			fmt.Fprintln(a.io.err, "hint: setup failed:", err)
		}
		return
	}
	a.useProfile(ctx, rpc.ProfileRef{Profile: profile})
}

// firstRunSetup is the answer to a start that failed for want of a
// provider or a key: on a terminal, say so, offer the questions, and
// report whether the configuration was written and loading should be
// tried again. Anywhere else the original error stands, and it already
// names `hint setup`.
func firstRunSetup(ctx context.Context, sio stdio, loadErr error) bool {
	var noKey *config.NoKeyError
	missingKey := errors.As(loadErr, &noKey)
	if !missingKey && !errors.Is(loadErr, config.ErrNoProvider) {
		return false
	}
	in, ok := sio.in.(*os.File)
	if !sio.tty || !ok {
		return false
	}
	cwd, err := os.Getwd()
	if err != nil {
		return false
	}
	info := newSetupInfo(nil, cwd, config.Flags{})
	if missingKey {
		info.profile.Name = noKey.Profile
	}
	fmt.Fprintln(sio.err, "hint: you will need "+keyHelp+".")
	fmt.Fprintln(sio.err, "hint:", strings.TrimPrefix(loadErr.Error(), "config: "))
	fmt.Fprintln(sio.err, info.describe())
	tty := newTTYIO(in, sio.err)
	answer, err := tty.Line(ctx, "Set it up now? [Y/n]: ")
	if a := console.Answer(answer); err != nil || a == "n" || a == "no" {
		return false
	}
	if _, err := askSetup(ctx, tty, sio.err, info); err != nil {
		fmt.Fprintln(sio.err, "hint: setup failed:", err)
		return false
	}
	return true
}

// newSetupCommand is `hint setup`: the same questions without a session,
// for the first run and for a key that is wrong before anything starts.
func newSetupCommand(flags *config.Flags) *cobra.Command {
	return &cobra.Command{
		Use:   "setup",
		Short: "Fill in the provider configuration interactively",
		Long: `hint setup asks for the profile, base URL, model and the environment
variable that holds the API key, and writes them to the global config
(~/.config/hint/config.yaml, mode 0600), keeping the comments and keys
already there. A project config is never written: a key does not belong
in a repository.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cmd.SilenceUsage = true
			cmd.SilenceErrors = true
			if !isTerminal(os.Stdin) {
				return errors.New("setup asks questions; stdin is not a terminal")
			}
			cwd, err := os.Getwd()
			if err != nil {
				return err
			}
			// A config that does not load yet is the reason to run setup.
			cfg, _ := config.Load(*flags)
			info := newSetupInfo(cfg, cwd, *flags)
			fmt.Fprintln(os.Stderr, info.describe())
			_, err = askSetup(cmd.Context(), newTTYIO(os.Stdin, os.Stderr), os.Stderr, info)
			return err
		},
	}
}
