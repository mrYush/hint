package main

import (
	"github.com/spf13/cobra"

	"github.com/mrYush/hint/internal/repomap/worker"
)

// extractionCommand is a bounded protocol endpoint for the core's worker.
// It deliberately does not load configuration, credentials or project files.
func extractionCommand() *cobra.Command {
	return &cobra.Command{Use: "internal-extract", Hidden: true, Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cmd.SilenceUsage, cmd.SilenceErrors = true, true
			return worker.Serve(cmd.Context(), cmd.InOrStdin(), cmd.OutOrStdout())
		}}
}
