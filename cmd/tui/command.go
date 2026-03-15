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
		var serverURL, token, configFile string
		var allowPaths []string
		pwd, _ := os.Getwd()

		if cmd.Flags().Changed("config") {
			configFile, _ = cmd.Flags().GetString("config")
		}

		if configFile != "" {
			// 設定ファイルから読み込み
			config, err := llm.LoadConfig(configFile)
			if err != nil {
				fmt.Fprintf(os.Stderr, "設定ファイルの読み込みに失敗しました：%v\n", err)
				os.Exit(1)
			}

			// デフォルトサーバーの設定を取得
			defaultServer := config.Server.Default

			// 指定されたサーバー名の設定を検索
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

			// ファイル操作の許可パスを取得
			allowPaths = config.File.AllowPaths
		} else {
			// 設定ファイルが指定されていない場合は、デフォルト値を使用
			serverURL = "http://localhost:1234"
			token = ""
			allowPaths = []string{}
		}

		// 起動時のカレントディレクトリを許可パスの先頭に追加
		allowPaths = append([]string{pwd}, allowPaths...)

		client := llm.NewLLMClient(serverURL, token)

		// ツールを登録（ユーザー確認あり）
		client.WithTools(
			llm.NewFileReadTool(allowPaths, true),
			llm.NewFileWriterTool(allowPaths, true),
		)

		Run(client)
	},
}

var configFile string

func init() {
	Command.Flags().StringVar(&configFile, "config", "", "設定ファイルのパス")
}
