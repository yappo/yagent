package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	setupusecase "yagent/internal/usecase/setup"
)

func newSetupCommand(configPath *string) *cobra.Command {
	options := setupusecase.Options{}
	var noConfig bool
	var noTasks bool

	command := &cobra.Command{
		Use:   "setup",
		Short: "local Qwen / LM Studio 用の starter files を生成",
		RunE: func(cmd *cobra.Command, args []string) error {
			options.WriteConfig = !noConfig
			options.WriteTasks = !noTasks
			if !options.WriteConfig && !options.WriteTasks {
				return fmt.Errorf("--no-config と --no-tasks を同時には指定できません")
			}
			if !cmd.Flags().Changed("config-out") && configPath != nil && *configPath != "" {
				options.ConfigPath = *configPath
			}
			result, err := setupusecase.Run(options)
			if err != nil {
				return err
			}
			fmt.Print(renderSetupResult(result))
			return nil
		},
	}

	command.Flags().StringVar(&options.ConfigPath, "config-out", setupusecase.DefaultConfigPath, "生成する config TOML のパス")
	command.Flags().StringVar(&options.TasksPath, "tasks-out", setupusecase.DefaultTasksPath, "生成する task catalog TOML のパス")
	command.Flags().StringVar(&options.LocalURL, "local-url", setupusecase.DefaultLocalURL, "LM Studio OpenAI-compatible endpoint")
	command.Flags().StringVar(&options.LocalModel, "local-model", setupusecase.DefaultLocalModel, "local server で使う model identifier")
	command.Flags().StringVar(&options.OpenAIModel, "openai-model", setupusecase.DefaultOpenAIModel, "fallback 用 OpenAI model")
	command.Flags().BoolVar(&options.Force, "force", false, "既存ファイルを上書きする")
	command.Flags().BoolVar(&options.DryRun, "dry-run", false, "ファイルを書かずに作成/上書き予定だけ表示する")
	command.Flags().BoolVar(&noConfig, "no-config", false, "config TOML を生成しない")
	command.Flags().BoolVar(&noTasks, "no-tasks", false, "task catalog TOML を生成しない")
	return command
}

func renderSetupResult(result setupusecase.Result) string {
	var sb strings.Builder
	sb.WriteString("yagent setup\n")
	for _, file := range result.Files {
		sb.WriteString(fmt.Sprintf("  %s: %s %s (%d bytes)\n", file.Kind, file.Status, file.Path, file.Bytes))
	}
	sb.WriteString("\nNext steps:\n")
	sb.WriteString("  1. Start LM Studio's local server.\n")
	sb.WriteString("  2. Run `yagent doctor --runtime --probe-structured`.\n")
	sb.WriteString("  3. Run `yagent benchmark --list-cases`.\n")
	return sb.String()
}
