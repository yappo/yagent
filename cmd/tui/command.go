package tui

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"yagent/cmd/llm"
)

// Command represents the tui command
var Command = &cobra.Command{
	Use:   "tui",
	Short: "TUI モードでの LLM インタラクティブな対話",
	Long:  `LM Studio との連携を用いた TUI インターフェースによるインタラクティブな AI 対話機能`,
	Run: func(cmd *cobra.Command, args []string) {
		configFile, _ := cmd.Flags().GetString("config")
		Start(configFile)
	},
}

func Start(configFile string) {
	var serverURL, token string
	var allowPaths []string
	pwd, _ := os.Getwd()

	if configFile != "" {
		config, err := llm.LoadConfig(configFile)
		if err != nil {
			fmt.Fprintf(os.Stderr, "設定ファイルの読み込みに失敗しました：%v\n", err)
			os.Exit(1)
		}

		defaultServer := config.Server.Default

		for _, srv := range config.Server.Servers {
			if srv.Name == defaultServer {
				serverURL = srv.URL
				token = srv.Token
				break
			}
		}

		if serverURL == "" {
			fmt.Fprintf(os.Stderr, "指定されたサーバー '%s' が見つかりません\n", defaultServer)
			os.Exit(1)
		}

		allowPaths = config.File.AllowPaths
	} else {
		serverURL = "http://localhost:1234"
		token = ""
		allowPaths = []string{}
	}

	allowPaths = append([]string{pwd}, allowPaths...)

	client := llm.NewLLMClient(serverURL, token)
	client.WithTools(
		llm.NewFileReadTool(allowPaths, true),
		llm.NewFileWriterTool(allowPaths, true),
	)

	Run(client)
}
