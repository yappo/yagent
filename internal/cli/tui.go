package cli

import "github.com/spf13/cobra"

func newTUICommand(configPath *string, logPath *string) *cobra.Command {
	return &cobra.Command{
		Use:   "tui",
		Short: "TUI モードで起動",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runTUI(*configPath, *logPath)
		},
	}
}
