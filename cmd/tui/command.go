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
	Short: "TUI モードでの LLM インタラクティブな対話",
	Long:  `LM Studio との連携を用いた TUI インターフェースによるインタラクティブな AI 対話機能`,
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
			Role: "system",
			Content: `あなたは役立つアシスタントです。

ファイル操作機能を使用できます。以下のツールを利用できます：

1. ファイル読み込み (/file-read <ファイルパス>)
   - ファイルの内容を読み込むことができます
   - 例：/file-read /tmp/test.txt

2. ファイル書き込み (/file-write <ファイルパス>)
   - ファイルに内容を書き込むことができます
   - 例：/file-write /tmp/output.txt

ファイル操作を実行する場合は、必ずファイルパスを指定してください。
また、ファイル操作を実行する前に、ユーザーに確認が行われます。

ファイル書き込みの場合、書き込む内容は以下の形式で指定してください：

/file-write /path/to/file.txt
このファイルに以下の内容を書き込んでください：

[ここに書き込む内容]

または、単に以下のように指定してください：

/file-write /path/to/file.txt

この場合、LLM が適切な内容自动生成します。`,
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

				// ファイル操作の処理
				content := response.Choices[0].Message.Content
				if strings.Contains(content, "/file-read ") {
					// /file-read の後のファイルパスを抽出
					parts := strings.SplitN(content, "/file-read ", 2)
					if len(parts) > 1 {
						filePath := strings.TrimSpace(parts[1])
						// Markdown コードブロックや余計なテキストを除去
						filePath = strings.Trim(filePath, "`")
						filePath = strings.Trim(filePath, "\n")
						filePath = strings.Split(filePath, "\n")[0]
						filePath = strings.TrimSpace(filePath)

						// ユーザーに確認を求める
						if client.ConfirmFileOperation("ファイル読み込み", filePath) {
							readContent, err := client.ReadFile(filePath)
							if err != nil {
								fmt.Fprintf(os.Stderr, "ファイル読み込みエラー：%v (ファイルパス：%s)\n", err, filePath)
								// 失敗した内容を LLM に送信
								messages = append(messages, llm.Message{
									Role:    "user",
									Content: fmt.Sprintf("ファイル %s の読み込みに失敗しました：%v", filePath, err),
								})
							} else {
								fmt.Printf("ファイル内容:\n%s\n", readContent)
								// 読み込んだ内容を LLM に送信
								messages = append(messages, llm.Message{
									Role:    "user",
									Content: fmt.Sprintf("ファイル %s の内容:\n%s", filePath, readContent),
								})
							}
							// 再度 LLM に送信
							request := llm.ChatRequest{
								Messages: messages,
							}
							response, err := client.SendChat(request)
							if err != nil {
								fmt.Fprintf(os.Stderr, "LLM サーバーとの通信に失敗しました：%v\n", err)
								messages = messages[:len(messages)-1]
								continue
							}
							if len(response.Choices) > 0 {
								fmt.Println("AI:", response.Choices[0].Message.Content)
								messages = append(messages, response.Choices[0].Message)
							}
						} else {
							fmt.Println("ファイル読み込みをキャンセルしました")
						}
					}
				} else if strings.Contains(content, "/file-write ") {
					// /file-write の後のファイルパスを抽出
					parts := strings.SplitN(content, "/file-write ", 2)
					if len(parts) > 1 {
						filePath := strings.TrimSpace(parts[1])
						// Markdown コードブロックや余計なテキストを除去
						filePath = strings.Trim(filePath, "`")
						filePath = strings.Trim(filePath, "\n")
						filePath = strings.Split(filePath, "\n")[0]
						filePath = strings.TrimSpace(filePath)

						// ユーザーに確認を求める
						if client.ConfirmFileOperation("ファイル書き込み", filePath) {
							// LLM の応答から Markdown コードブロック内の内容を抽出
							var writeContent string

							// ``` で囲まれたコードブロックを探す
							startIndex := strings.Index(content, "```")
							if startIndex != -1 {
								rest := content[startIndex+3:]
								endIndex := strings.Index(rest, "```")
								if endIndex != -1 {
									writeContent = strings.TrimSpace(rest[:endIndex])
								}
							}

							// コードブロックが見つからない場合は、ファイルパスの行以降の内容を使用
							if writeContent == "" {
								lines := strings.Split(content, "\n")
								var contentLines []string
								foundPath := false

								for _, line := range lines {
									if foundPath && strings.TrimSpace(line) != "" {
										contentLines = append(contentLines, line)
									}
									if strings.Contains(line, "/file-write ") {
										foundPath = true
									}
								}
								writeContent = strings.Join(contentLines, "\n")
							}

							if writeContent == "" {
								fmt.Fprintf(os.Stderr, "ファイル書き込み内容が指定されていません\n")
								continue
							}

							err := client.WriteFile(filePath, writeContent)
							if err != nil {
								fmt.Fprintf(os.Stderr, "ファイル書き込みエラー：%v\n", err)
							} else {
								fmt.Printf("ファイル %s に書き込みました\n", filePath)
							}
						} else {
							fmt.Println("ファイル書き込みをキャンセルしました")
						}
					}
				} else {
					fmt.Println("LLM サーバーから応答がありません")
				}
				fmt.Println()
			}

			if err := scanner.Err(); err != nil {
				fmt.Fprintf(os.Stderr, "入力エラー：%v\n", err)
			}
		}
	},
}

var configFile string

func init() {
	Command.Flags().StringVar(&configFile, "config", "", "設定ファイルのパス")
}
