package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

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

// askSetup is the line-based setup: it asks for the four values with the
// current ones as defaults and writes them. The key is never typed here —
// stdin is shared with the line reader and cannot be switched to
// no-echo — so it is the name of the environment variable that holds it.
func askSetup(ctx context.Context, lines *console.LineReader, out io.Writer, s *setupInfo) error {
	ask := func(prompt, def string) (string, error) {
		if def != "" {
			fmt.Fprintf(out, "%s [%s]: ", prompt, def)
		} else {
			fmt.Fprintf(out, "%s: ", prompt)
		}
		line, err := lines.ReadLine(ctx)
		if err != nil {
			return "", err
		}
		if line = strings.TrimSpace(line); line != "" {
			return line, nil
		}
		return def, nil
	}
	profile, err := ask("profile name", s.profile.Name)
	if err != nil {
		return err
	}
	baseURL, err := ask("base URL", s.profile.BaseURL)
	if err != nil {
		return err
	}
	model, err := ask("model", s.profile.Model)
	if err != nil {
		return err
	}
	name, err := ask("environment variable that holds the API key (e.g. API_BAR_KEY; empty keeps the key as it is)", "")
	if err != nil {
		return err
	}
	key := ""
	if name != "" {
		key = "${" + strings.Trim(name, "${}") + "}"
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
	if err := askSetup(ctx, a.lines, a.io.err, a.setup); err != nil && ctx.Err() == nil {
		fmt.Fprintln(a.io.err, "hint: setup failed:", err)
	}
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
			return askSetup(cmd.Context(), console.NewLineReader(os.Stdin), os.Stderr, info)
		},
	}
}
