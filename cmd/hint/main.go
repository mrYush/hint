package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/mrYush/hint/internal/config"
	"github.com/mrYush/hint/internal/context"
	"github.com/mrYush/hint/internal/llm"
)

func main() {
	var flags config.Flags

	rootCmd := &cobra.Command{
		Use:   "hint [question]",
		Short: "A utility for getting contextual hints using LLM",
		Args:  cobra.MinimumNArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			// Load configuration
			cfg, err := config.Load(flags)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error loading configuration: %v\n", err)
				os.Exit(1)
			}
			for _, w := range cfg.Warnings {
				fmt.Fprintf(os.Stderr, "warning: %s\n", w)
			}

			// Get directory context
			ctx, err := context.GetDirectoryContext()
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error getting context: %v\n", err)
				os.Exit(1)
			}

			// Form question from arguments
			question := args[0]
			for i := 1; i < len(args); i++ {
				question += " " + args[i]
			}

			// Request to LLM
			response, err := llm.AskLLM(cfg, ctx, question)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error querying LLM: %v\n", err)
				os.Exit(1)
			}

			// Output the response
			fmt.Println(response)
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
		fmt.Println(err)
		os.Exit(1)
	}
}
