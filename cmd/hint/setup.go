package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/charmbracelet/x/term"
	"github.com/spf13/cobra"

	"github.com/mrYush/hint/internal/config"
	"github.com/mrYush/hint/internal/console"
	"github.com/mrYush/hint/internal/tui"
	"github.com/mrYush/hint/pkg/agentapi"
)

// defaultBaseURL is offered for a profile that has none: the gateway the
// built-in defaults point at.
const defaultBaseURL = "https://api-bar.ru/route/openai"

// setupInfo is what the shell knows about the configuration this run
// uses, for the moment a credential is rejected: which profile, which
// files, and the one place setup writes to.
type setupInfo struct {
	profile config.Profile
	locs    config.Locations
}

// newSetupInfo describes the configuration cfg was loaded from. cfg may
// be nil (loading failed): setup then starts from nothing.
func newSetupInfo(cfg *config.Config, cwd string) *setupInfo {
	home, err := os.UserHomeDir()
	if err != nil {
		home = ""
	}
	s := &setupInfo{locs: config.Locate(home, cwd, os.LookupEnv)}
	if cfg != nil {
		if p, err := cfg.Default(); err == nil {
			s.profile = p
		}
	}
	if s.profile.Name == "" {
		s.profile.Name = "api-bar"
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
// read from the environment.
func (s *setupInfo) save(profile, baseURL, model, key string) (string, error) {
	if s.locs.Global == "" {
		return "", errors.New("no home directory and no XDG_CONFIG_HOME: there is nowhere to write the config")
	}
	if err := config.WriteProfile(s.locs.Global, config.ProfileUpdate{
		Name: profile, BaseURL: baseURL, Model: model, APIKey: key,
	}); err != nil {
		return "", err
	}
	msg := "saved to " + s.locs.Global + "; restart hint to use it (hint models tries the profile first)"
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
		BaseURL:  s.profile.BaseURL,
		Model:    s.profile.Model,
		Save:     s.save,
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
// with what is known as the default of each, and writes the answers. A
// model left empty stays out of the file, so the built-in default for the
// kind applies. Where the input can hide what is typed the key is typed
// (or given as ${VAR}); where it cannot, the question is the name of the
// environment variable that holds it.
func askSetup(ctx context.Context, in setupIO, out io.Writer, s *setupInfo) error {
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
		return err
	}
	baseURL, err := ask("base URL", s.profile.BaseURL, "")
	if err != nil {
		return err
	}
	model, err := ask("model", s.profile.Model, "the built-in default of the kind")
	if err != nil {
		return err
	}
	key, ok, err := in.Secret(ctx, "API key (input hidden; ${VARIABLE} to read it from the environment; empty keeps the current one): ")
	if err != nil {
		return err
	}
	if !ok {
		name, err := ask("environment variable that holds the API key (e.g. API_BAR_KEY; empty keeps the key as it is)", "", "")
		if err != nil {
			return err
		}
		if name != "" {
			key = "${" + strings.Trim(name, "${}") + "}"
		}
	}
	msg, err := s.save(profile, baseURL, model, key)
	if err != nil {
		return err
	}
	fmt.Fprintln(out, "hint:", msg)
	return nil
}

// offerSetup runs after a rejected credential in the REPL: it shows
// where the configuration is and asks whether to fill it in now.
func (a *app) offerSetup(ctx context.Context, help string) {
	fmt.Fprintln(a.io.err, "hint:", help)
	if a.setup == nil || !a.io.tty {
		return
	}
	fmt.Fprint(a.io.err, "Fill the configuration in now? [y/N]: ")
	line, err := a.lines.ReadLine(ctx)
	if err != nil || !strings.EqualFold(strings.TrimSpace(line), "y") {
		return
	}
	if err := askSetup(ctx, lineIO{lines: a.lines, out: a.io.err}, a.io.err, a.setup); err != nil && ctx.Err() == nil {
		fmt.Fprintln(a.io.err, "hint: setup failed:", err)
	}
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
	info := newSetupInfo(nil, cwd)
	if missingKey {
		info.profile.Name = noKey.Profile
	}
	fmt.Fprintln(sio.err, "hint:", strings.TrimPrefix(loadErr.Error(), "config: "))
	fmt.Fprintln(sio.err, info.describe())
	tty := newTTYIO(in, sio.err)
	answer, err := tty.Line(ctx, "Set it up now? [Y/n]: ")
	if err != nil || strings.EqualFold(answer, "n") || strings.EqualFold(answer, "no") {
		return false
	}
	if err := askSetup(ctx, tty, sio.err, info); err != nil {
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
			info := newSetupInfo(cfg, cwd)
			fmt.Fprintln(os.Stderr, info.describe())
			return askSetup(cmd.Context(), newTTYIO(os.Stdin, os.Stderr), os.Stderr, info)
		},
	}
}
