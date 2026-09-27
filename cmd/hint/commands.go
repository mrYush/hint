package main

import (
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/mrYush/hint/internal/config"
	"github.com/mrYush/hint/internal/provider"
	"github.com/mrYush/hint/internal/session"
)

// newSessionsCommand lists the recorded sessions of the working directory,
// newest first, the way the -r picker shows them — but all of them, and
// without asking anything.
func newSessionsCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "sessions",
		Short: "List this directory's recorded sessions",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cmd.SilenceUsage = true
			cmd.SilenceErrors = true
			home, err := os.UserHomeDir()
			if err != nil {
				home = ""
			}
			cwd, err := os.Getwd()
			if err != nil {
				return fmt.Errorf("working directory: %w", err)
			}
			store := session.NewStore(session.DefaultDir(home, os.LookupEnv))
			return listSessions(store, cwd, os.Stdout, os.Stderr)
		},
	}
}

// listSessions prints cwd's sessions on out; an empty directory is a
// notice on stderr, not an error, so a script can tell "none" from a
// failure by the exit status.
func listSessions(store *session.Store, cwd string, out, stderr io.Writer) error {
	infos, err := store.List(cwd)
	if err != nil {
		return err
	}
	if len(infos) == 0 {
		fmt.Fprintf(stderr, "hint: no recorded sessions in %s\n", cwd)
		return nil
	}
	fmt.Fprintf(out, "sessions in %s, newest first (continue one with --session <id>):\n", cwd)
	session.Print(out, infos)
	return nil
}

// newModelsCommand lists what the selected profile's endpoint serves:
// Ollama's installed models with their sizes, or the ids behind GET
// /models for any other profile. --provider picks another profile, as it
// does for a run.
func newModelsCommand(flags *config.Flags) *cobra.Command {
	return &cobra.Command{
		Use:   "models",
		Short: "List the models the selected provider profile serves",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cmd.SilenceUsage = true
			cmd.SilenceErrors = true
			cfg, err := config.Load(*flags)
			if err != nil {
				return fmt.Errorf("loading configuration: %w", err)
			}
			for _, w := range cfg.Warnings {
				fmt.Fprintf(os.Stderr, "warning: %s\n", w)
			}
			p, err := cfg.Default()
			if err != nil {
				return err
			}
			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			models, err := provider.Models(ctx, p)
			if err != nil {
				return err
			}
			fmt.Fprintf(os.Stderr, "hint: %d models served by profile %q at %s\n", len(models), p.Name, p.BaseURL)
			return printModels(os.Stdout, models)
		},
	}
}

// printModels writes one model per line: the name first, so `hint models
// | cut -f1` or a plain grep works, then whatever else the source knew.
func printModels(out io.Writer, models []provider.Model) error {
	tw := tabwriter.NewWriter(out, 0, 8, 2, ' ', 0)
	for _, m := range models {
		size, modified := "", ""
		if m.SizeBytes > 0 {
			size = humanSize(m.SizeBytes)
		}
		if !m.Modified.IsZero() {
			modified = m.Modified.Local().Format("2006-01-02")
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", m.Name, size, modified, m.Owner)
	}
	return tw.Flush()
}

// humanSize renders bytes in the unit that keeps the number short.
func humanSize(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for n/div >= unit && exp < 4 {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGTP"[exp])
}
