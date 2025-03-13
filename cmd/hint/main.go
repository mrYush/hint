package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/mrYush/hint/internal/config"
	"github.com/mrYush/hint/internal/context"
	"github.com/mrYush/hint/internal/llm"
	"github.com/spf13/viper"
)

func main() {
	var rootCmd = &cobra.Command{
		Use:   "hint [question]",
		Short: "A utility for getting contextual hints using LLM",
		Args:  cobra.MinimumNArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			// Load configuration
			cfg, err := config.Load()
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error loading configuration: %v\n", err)
				os.Exit(1)
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

	// Configuration flags
	rootCmd.PersistentFlags().String("api-url", "", "API URL (OpenAI is used by default)")
	rootCmd.PersistentFlags().String("api-key", "", "API Key")
	rootCmd.PersistentFlags().String("model", "gpt-4", "Model name")
	
	// Binding flags with Viper
	viper.BindPFlag("api_url", rootCmd.PersistentFlags().Lookup("api-url"))
	viper.BindPFlag("api_key", rootCmd.PersistentFlags().Lookup("api-key"))
	viper.BindPFlag("model", rootCmd.PersistentFlags().Lookup("model"))
	
	if err := rootCmd.Execute(); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
} 