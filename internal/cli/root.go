package cli

import (
	"github.com/spf13/cobra"
)

func NewRootCommand() *cobra.Command {
	var configPath string

	root := &cobra.Command{
		Use:   "yagent",
		Short: "A terminal AI coding agent",
		Long:  "yagent is a terminal AI coding agent with interactive TUI and tool support.",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runTUI(configPath)
		},
	}

	root.PersistentFlags().StringVar(&configPath, "config", "", "設定ファイルのパス")
	root.AddCommand(newTUICommand(&configPath))
	root.AddCommand(newExecCommand(&configPath))

	return root
}

func Execute() error {
	return NewRootCommand().Execute()
}
