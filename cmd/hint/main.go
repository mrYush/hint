package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/spf13/cobra"

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
	req := agentapi.ChatRequest{
		Messages: []agentapi.Message{
			agentapi.SystemMessage(systemPrompt(dc)),
			agentapi.UserMessage(question),
		},
	}

	events, err := chat.Stream(ctx, req)
	if err != nil {
		return err
	}

	printedText := false
	for ev := range events {
		switch ev.Kind {
		case agentapi.ChatTextDelta:
			fmt.Print(ev.Text)
			printedText = true
		case agentapi.ChatThinkingDelta, agentapi.ChatUsage, agentapi.ChatToolCall:
			// Thinking stays off stdout; usage and tool calls have no
			// consumer until WP0.4.
		case agentapi.ChatDone:
			if printedText {
				fmt.Println()
			}
			if ev.FinishReason == agentapi.FinishCanceled {
				return fmt.Errorf("interrupted")
			}
			if ev.FinishReason == agentapi.FinishLength {
				fmt.Fprintln(os.Stderr, "hint: response was truncated by the provider's token limit")
			}
		case agentapi.ChatError:
			if printedText {
				fmt.Println()
			}
			return ev.Err
		}
	}
	return nil
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
