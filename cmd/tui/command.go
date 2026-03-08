package tui

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"yagent/cmd/llm"
)

// Command represents the tui command
var Command = &cobra.Command{
	Use:   "tui",
	Short: "TUIモードでのLLMインタラクティブな対話",
	Long:  `LM Studioとの連携を用いたTUIインターフェースによるインタラクティブなAI対話機能`,
	Run: func(cmd *cobra.Command, args []string) {
		var serverURL, token string
		var configFile string

		if cmd.Flags().Changed("config") {
			configFile, _ = cmd.Flags().GetString("config")
		}

		if configFile != "" {
			// 設定ファイルから読み込み
			config, err := llm.LoadConfig(configFile)
			if err != nil {
				fmt.Fprintf(os.Stderr, "設定ファイルの読み込みに失敗しました: %v\n", err)
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
		} else {
			// 設定ファイルが指定されていない場合は、デフォルト値を使用
			serverURL = "http://localhost:1234"
			token = ""
		}

		client := llm.NewLLMClient(serverURL, token)

		fmt.Println("yagent TUIモードへようこそ！")
		fmt.Println("質問を入力してください (終了するには 'quit' と入力)")
		fmt.Println("コマンド:")
		fmt.Println("  /help - このヘルプを表示")
		fmt.Println("  /clear - チャット履歴をクリア")
		fmt.Println("  /quit - 終了")
		fmt.Println("----------------------------------------")

		scanner := bufio.NewScanner(os.Stdin)
		var messages []llm.Message

		// 初期メッセージを追加
		messages = append(messages, llm.Message{
			Role:    "system",
			Content: "あなたは役立つアシスタントです。",
		})

		for {
			fmt.Print("質問: ")
			if !scanner.Scan() {
				break
			}

			input := strings.TrimSpace(scanner.Text())
			if input == "" {
				continue
			}

			// コマンド処理
			if strings.HasPrefix(input, "/") {
				switch input {
				case "/quit", "/exit":
					fmt.Println("さようなら！")
					return
				case "/help":
					fmt.Println("コマンド:")
					fmt.Println("  /help - このヘルプを表示")
					fmt.Println("  /clear - チャット履歴をクリア")
					fmt.Println("  /quit - 終了")
					continue
				case "/clear":
					messages = messages[:1] // systemメッセージのみ残す
					fmt.Println("チャット履歴をクリアしました")
					continue
				default:
					fmt.Println("不明なコマンドです。/help でヘルプを表示します")
					continue
				}
			}

			if strings.ToLower(input) == "quit" || strings.ToLower(input) == "exit" {
				fmt.Println("さようなら！")
				break
			}

			// ユーザーメッセージを追加
			messages = append(messages, llm.Message{
				Role:    "user",
				Content: input,
			})

			// AIに質問を送信
			request := llm.ChatRequest{
				Messages: messages,
			}

			response, err := client.SendChat(request)
			if err != nil {
				fmt.Fprintf(os.Stderr, "LLMサーバーとの通信に失敗しました: %v\n", err)
				// ユーザーメッセージを削除（送信失敗）
				messages = messages[:len(messages)-1]
				continue
			}

			if len(response.Choices) > 0 {
				fmt.Println("AI:", response.Choices[0].Message.Content)
				// AIの応答をメッセージ履歴に追加
				messages = append(messages, response.Choices[0].Message)
			} else {
				fmt.Println("LLMサーバーから応答がありません")
			}
			fmt.Println()
		}

		if err := scanner.Err(); err != nil {
			fmt.Fprintf(os.Stderr, "入力エラー: %v\n", err)
		}
	},
}

var configFile string

func init() {
	Command.Flags().StringVar(&configFile, "config", "", "設定ファイルのパス")
}
