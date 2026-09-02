package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/mrYush/hint/internal/agent"
	"github.com/mrYush/hint/internal/config"
	dirctx "github.com/mrYush/hint/internal/context"
	"github.com/mrYush/hint/internal/provider"
	"github.com/mrYush/hint/pkg/agentapi"
)

func main() {
	var flags config.Flags

	rootCmd := &cobra.Command{
		Use:   "hint [question]",
		Short: "A utility for getting contextual hints using LLM",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cmd.SilenceUsage = true
			// Ctrl-C cancels the context, which the provider layer reports
			// as a cancel — never as a network failure that would trigger a
			// fake failover.
			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			return run(ctx, flags, strings.Join(args, " "))
		},
	}

	// Configuration flags. Defaults stay empty on purpose: a non-empty flag
	// default would override values from config files (the pre-WP0.2
	// --model=gpt-4 bug). Built-in defaults live in internal/config.
	rootCmd.PersistentFlags().StringVar(&flags.Provider, "provider", "", "Provider profile to use for this run")
	rootCmd.PersistentFlags().StringVar(&flags.APIURL, "api-url", "", "Base URL of the provider API (overrides the selected profile)")
	rootCmd.PersistentFlags().StringVar(&flags.APIKey, "api-key", "", "API key (overrides the selected profile)")
	rootCmd.PersistentFlags().StringVar(&flags.Model, "model", "", "Model name (overrides the selected profile)")

	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// run loads the configuration, builds the provider stack, and streams one
// answer to stdout.
func run(ctx context.Context, flags config.Flags, question string) error {
	cfg, err := config.Load(flags)
	if err != nil {
		return fmt.Errorf("loading configuration: %w", err)
	}
	for _, w := range cfg.Warnings {
		fmt.Fprintf(os.Stderr, "warning: %s\n", w)
	}

	// The WP0.9 file logger does not exist yet; when HINT_DEBUG is set the
	// clients' one-line notes go to stderr, redacted against every
	// configured secret.
	var debugLog func(string)
	if cfg.Debug {
		secrets := cfg.Secrets()
		debugLog = func(line string) {
			fmt.Fprintln(os.Stderr, "hint debug:", config.Redact(line, secrets))
		}
	}

	chat, err := provider.Chat(cfg, os.Stderr, debugLog)
	if err != nil {
		return err
	}

	dc, err := dirctx.GetDirectoryContext()
	if err != nil {
		return fmt.Errorf("getting context: %w", err)
	}
	history := []agentapi.Message{
		agentapi.SystemMessage(systemPrompt(dc)),
		agentapi.UserMessage(question),
	}

	// No WithTools yet: WP0.5's built-in tools plug in here once they
	// exist, and the loop above already knows how to run them. Without
	// any, every turn is the single-Stream, plain-text exchange the CLI
	// had before WP0.4 — RunTurn just runs it through the same tool-calling
	// state machine that a tool-using turn would.
	a := agent.New(chat)

	printedText := false
	// lastErr remembers the most recent EventError. Per the contract,
	// EventError does not necessarily end the turn — a non-terminal
	// compaction failure is reported this way too, and the turn carries on
	// — so it only becomes the command's result if EventTurnEnd actually
	// closes with FinishError.
	var lastErr *agentapi.Error
	var runErr error
	for ev := range a.RunTurn(ctx, history) {
		switch ev.Kind {
		case agentapi.EventTextDelta:
			fmt.Print(ev.Text)
			printedText = true
		case agentapi.EventCompaction:
			fmt.Fprintf(os.Stderr, "hint: compacted %d messages into a summary\n", ev.Compaction.MessagesReplaced)
		case agentapi.EventError:
			lastErr = ev.Err
		case agentapi.EventTurnEnd:
			if printedText {
				fmt.Println()
			}
			switch ev.FinishReason {
			case agentapi.FinishStop, agentapi.FinishToolCalls, agentapi.FinishContentFilter:
				// Nothing beyond the newline above: a normal stop, and
				// FinishToolCalls never actually reaches EventTurnEnd (a
				// tool round always loops back into the agent, never ends
				// the turn directly).
			case agentapi.FinishCanceled:
				runErr = fmt.Errorf("interrupted")
			case agentapi.FinishLength:
				fmt.Fprintln(os.Stderr, "hint: response was truncated by the provider's token limit")
			case agentapi.FinishError:
				runErr = lastErr
			}
		}
	}
	return runErr
}

// systemPrompt is the same prompt the pre-WP0.3 CLI sent; WP0.8 replaces it
// with real project context.
func systemPrompt(dc *dirctx.DirectoryContext) string {
	return fmt.Sprintf(
		"You are a helpful assistant aiding a developer with their project. "+
			"Current directory: %s\n"+
			"Files in directory: %s\n\n"+
			"Answer the developer's question with this context in mind.",
		dc.CurrentDir,
		strings.Join(dc.Files, ", "),
	)
}
