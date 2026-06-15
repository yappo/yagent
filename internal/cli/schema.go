package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	agentcatalog "yagent/internal/infra/agents/catalog"
	"yagent/internal/usecase/taskcatalog"
)

func newSchemaCommand() *cobra.Command {
	command := &cobra.Command{
		Use:   "schema",
		Short: "yagent DSL の JSON Schema を出力",
	}
	command.AddCommand(newSchemaOutputCommand("tasks", []string{"task-catalog", "tasks.toml"}, "task catalog TOML schema", taskcatalog.JSONSchema))
	command.AddCommand(newSchemaOutputCommand("agent", []string{"agent-dsl", "agent.toml"}, "Agent DSL TOML schema", agentcatalog.AgentDSLJSONSchema))
	return command
}

func newSchemaOutputCommand(name string, aliases []string, short string, schema func() map[string]any) *cobra.Command {
	var outPath string
	command := &cobra.Command{
		Use:     name,
		Aliases: aliases,
		Short:   short,
		RunE: func(cmd *cobra.Command, args []string) error {
			data, err := json.MarshalIndent(schema(), "", "  ")
			if err != nil {
				return fmt.Errorf("schema JSON の生成に失敗しました: %w", err)
			}
			data = append(data, '\n')
			if outPath != "" {
				if err := writeSchemaFile(outPath, data); err != nil {
					return err
				}
				fmt.Fprintf(cmd.OutOrStdout(), "wrote %s\n", outPath)
				return nil
			}
			_, err = cmd.OutOrStdout().Write(data)
			return err
		},
	}
	command.Flags().StringVar(&outPath, "out", "", "schema JSON の出力先。未指定時は stdout")
	return command
}

func writeSchemaFile(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("%s のディレクトリ作成に失敗しました: %w", path, err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("%s の書き込みに失敗しました: %w", path, err)
	}
	return nil
}
