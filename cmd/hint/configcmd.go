package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/mrYush/hint/internal/config"
)

// errNowhere is a run with neither a home directory nor XDG_CONFIG_HOME.
var errNowhere = errors.New("no home directory and no XDG_CONFIG_HOME: there is nowhere to write the config")

// currentValues are the profile's fields as the form starts with them.
// A field whose zero value means "not set" stays empty, so the form shows
// the default as a hint instead of writing it out.
func (s *setupInfo) currentValues() map[string]string {
	p := s.profile
	v := map[string]string{"kind": string(p.Kind), "base_url": p.BaseURL, "model": p.Model}
	if v["kind"] == "" {
		v["kind"] = string(config.KindOpenAI)
	}
	if p.ContextWindow > 0 {
		v["context_window"] = strconv.Itoa(p.ContextWindow)
	}
	if p.HeaderTimeout > 0 {
		v["header_timeout"] = p.HeaderTimeout.String()
	}
	return v
}

// target is the file a change goes to: the global config, or with
// project the repository's .hint/config.yaml.
func (s *setupInfo) target(project bool) (string, error) {
	if project {
		return filepath.Join(s.cwd, ".hint", "config.yaml"), nil
	}
	if s.locs.Global == "" {
		return "", errNowhere
	}
	return s.locs.Global, nil
}

// set writes one key. A literal API key is refused for the project file,
// which sits in the repository; ${VAR} is fine there.
func (s *setupInfo) set(key, value string, project bool) (string, error) {
	k, _, err := config.LookupKey(key)
	if err != nil {
		return "", err
	}
	if _, isRef := envReference(strings.TrimSpace(value)); k.Type == config.TypeSecret && project && !isRef {
		return "", errors.New("a literal API key does not go in the project file, it would end up in git: use ${VAR} there, or set it without --project")
	}
	path, err := s.target(project)
	if err != nil {
		return "", err
	}
	if err := config.Edit(path, func(d *config.Document) error { return d.Set(key, value) }); err != nil {
		return "", err
	}
	return fmt.Sprintf("%s set in %s; it applies from the next start", key, path), nil
}

// unset removes one key, so that the next layer down applies.
func (s *setupInfo) unset(key string, project bool) (string, error) {
	path, err := s.target(project)
	if err != nil {
		return "", err
	}
	if err := config.Edit(path, func(d *config.Document) error { return d.Unset(key) }); err != nil {
		return "", err
	}
	return fmt.Sprintf("%s removed from %s; the next layer down applies from the next start", key, path), nil
}

// show is the configuration the next start resolves: every key of the
// table with its value after all layers, keys masked.
func (s *setupInfo) show() (string, error) {
	cfg, err := config.LoadFrom(config.Options{HomeDir: homeDir(), WorkingDir: s.cwd, LookupEnv: os.LookupEnv, Flags: s.flags})
	if err != nil {
		return "", err
	}
	var b strings.Builder
	if len(s.locs.Read) == 0 {
		b.WriteString("files: none; values come from the environment and the defaults\n")
	} else {
		b.WriteString("files (the later one wins): " + strings.Join(s.locs.Read, ", ") + "\n")
	}
	top := map[string]string{
		"default_provider":       cfg.DefaultProvider,
		"fallback_provider":      cfg.FallbackProvider,
		"instructions.budget":    strconv.Itoa(cfg.Instructions.Budget),
		"instructions.summarize": strconv.FormatBool(cfg.Instructions.Summarize),
		"overview.depth":         strconv.Itoa(cfg.Overview.Depth),
		"overview.max_entries":   strconv.Itoa(cfg.Overview.MaxEntries),
		"completion.mode":        string(cfg.Completion.Mode),
		"completion.profile":     cfg.Completion.Profile,
		"completion.model":       cfg.Completion.Model,
		"completion.idle_after":  cfg.Completion.IdleAfter.String(),
		"completion.warn_after":  cfg.Completion.WarnAfter.String(),
		"secrets.store":          string(cfg.SecretStore.Store),
	}
	for _, k := range config.Keys() {
		if _, ok := k.Profile(); !ok {
			fmt.Fprintf(&b, "%s = %s\n", k.Path, orUnset(top[k.Path]))
		}
	}
	for _, p := range cfg.Providers {
		fields := map[string]string{
			"kind": string(p.Kind), "base_url": p.BaseURL, "api_key": config.Mask(p.APIKey), "model": p.Model,
		}
		// A reference that yielded no key is shown as the reference, so
		// the user sees what the file says and that it did not resolve.
		if p.APIKey == "" && p.KeyRef != "" {
			fields["api_key"] = p.KeyRef + " (no key came back)"
		}
		if p.ContextWindow > 0 {
			fields["context_window"] = strconv.Itoa(p.ContextWindow)
		}
		if p.HeaderTimeout > 0 {
			fields["header_timeout"] = p.HeaderTimeout.String()
		}
		for _, k := range config.ProfileKeys() {
			field, _ := k.Profile()
			fmt.Fprintf(&b, "providers.%s.%s = %s\n", p.Name, field, orUnset(fields[field]))
		}
	}
	return strings.TrimRight(b.String(), "\n"), nil
}

func orUnset(v string) string {
	if v == "" {
		return "(unset)"
	}
	return v
}

// keysText lists every key with its default, its overlays and its help.
func keysText() string {
	var b strings.Builder
	for _, k := range config.Keys() {
		path := strings.Replace(k.Path, "*", "<profile>", 1)
		fmt.Fprintf(&b, "%s\n    %s\n", path, k.Help)
		var meta []string
		if len(k.Enum) > 0 {
			meta = append(meta, "one of "+strings.Join(k.Enum, ", "))
		}
		if k.Default != "" {
			meta = append(meta, "default "+k.Default)
		}
		if k.Env != "" {
			meta = append(meta, k.Env)
		}
		if k.Flag != "" {
			meta = append(meta, k.Flag)
		}
		if len(meta) > 0 {
			fmt.Fprintf(&b, "    (%s)\n", strings.Join(meta, "; "))
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

func homeDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return home
}

// newConfigCommand is `hint config`: show, list, set and unset keys of
// the config files without opening them in an editor.
func newConfigCommand(flags *config.Flags) *cobra.Command {
	info := func() (*setupInfo, error) {
		cwd, err := os.Getwd()
		if err != nil {
			return nil, err
		}
		cfg, _ := config.Load(*flags) // a broken config is one reason to be here
		return newSetupInfo(cfg, cwd, *flags), nil
	}
	var project bool
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Show, list, set and unset configuration keys",
		Long: `hint config shows the configuration the next start resolves, every
layer applied and API keys masked. Its subcommands change one key of a
config file at a time, keeping the file's comments: the global file by
default, the repository's .hint/config.yaml with --project. Each value
is checked against the key's type before anything is written.

The keys, their defaults and their HINT_* variables: hint config keys,
or config.example.yaml in the repository.`,
		Example: `  hint config                                   # what the next start uses
  hint config keys                              # every key, with defaults
  hint config set overview.depth 3              # global
  hint config set --project providers.vllm.model Qwen/Qwen2.5-Coder-7B-Instruct
  hint config unset --project overview.depth    # back to the global value`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cmd.SilenceUsage, cmd.SilenceErrors = true, true
			s, err := info()
			if err != nil {
				return err
			}
			out, err := s.show()
			if err != nil {
				return err
			}
			fmt.Println(out)
			return nil
		},
	}
	cmd.AddCommand(&cobra.Command{
		Use:   "keys",
		Short: "List every configuration key with its default and help",
		Args:  cobra.NoArgs,
		Run:   func(*cobra.Command, []string) { fmt.Println(keysText()) },
	})
	set := &cobra.Command{
		Use:   "set <key> <value>",
		Short: "Set one key in a config file",
		Args:  cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			cmd.SilenceUsage, cmd.SilenceErrors = true, true
			s, err := info()
			if err != nil {
				return err
			}
			msg, err := s.set(args[0], strings.Join(args[1:], " "), project)
			if err != nil {
				return err
			}
			fmt.Fprintln(os.Stderr, "hint:", msg)
			return nil
		},
	}
	unset := &cobra.Command{
		Use:   "unset <key>",
		Short: "Remove one key from a config file, so the next layer down applies",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cmd.SilenceUsage, cmd.SilenceErrors = true, true
			s, err := info()
			if err != nil {
				return err
			}
			msg, err := s.unset(args[0], project)
			if err != nil {
				return err
			}
			fmt.Fprintln(os.Stderr, "hint:", msg)
			return nil
		},
	}
	for _, c := range []*cobra.Command{set, unset} {
		c.Flags().BoolVar(&project, "project", false, "Change the repository's .hint/config.yaml instead of the global file")
		cmd.AddCommand(c)
	}
	var reveal bool
	showKeyCmd := &cobra.Command{
		Use:   "show-key <profile>",
		Short: "Show a profile's API key again, masked unless --reveal; asks first, terminal only",
		Long: `hint config show-key is the one way to see an API key hint holds, wherever
it is: the OS secret store, the environment or a config file. It asks
before printing, prints the key masked unless --reveal, and works only
on a terminal. No RPC method and no tool reaches it.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cmd.SilenceUsage, cmd.SilenceErrors = true, true
			if !isTerminal(os.Stdin) || !isTerminal(os.Stdout) {
				return errors.New("show-key prints a secret: it needs a terminal on stdin and stdout")
			}
			cfg, err := config.Load(*flags)
			if err != nil {
				return err
			}
			return showKey(cfg, args[0], reveal, newTTYIO(os.Stdin, os.Stderr), os.Stdout)
		},
	}
	showKeyCmd.Flags().BoolVar(&reveal, "reveal", false, "Print the key in full instead of masked")
	cmd.AddCommand(showKeyCmd)
	return cmd
}
