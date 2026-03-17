package cli

import (
	"github.com/spf13/cobra"
)

func NewRootCommand() *cobra.Command {
	var configPath string
	var logPath string

	root := &cobra.Command{
		Use:   "yagent",
		Short: "A terminal AI coding agent",
		Long:  "yagent is a terminal AI coding agent with interactive TUI and tool support.",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runTUI(configPath, logPath)
		},
	}

	root.PersistentFlags().StringVar(&configPath, "config", "", "設定ファイルのパス")
	root.PersistentFlags().StringVar(&logPath, "log", "", "イベントログの出力先")
	root.AddCommand(newTUICommand(&configPath, &logPath))
	root.AddCommand(newExecCommand(&configPath, &logPath))
	root.AddCommand(newBenchmarkCommand(&configPath, &logPath))

	return root
}

func Execute() error {
	return NewRootCommand().Execute()
}
