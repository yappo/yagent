package llm

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var (
	model     string
	server    string
	stream    bool
	prompt    string
	maxTokens int
	token     string
)

// Command represents the llm command
var Command = &cobra.Command{
	Use:   "llm",
	Short: "LLMサーバーとの通信機能",
	Long:  `LM StudioなどのLLMサーバーと連携してAIとの対話機能を提供します`,
	Run: func(cmd *cobra.Command, args []string) {
		if prompt == "" {
			fmt.Println("プロンプトを指定してください。")
			os.Exit(1)
		}

		client := NewLLMClient(server, token)

		request := ChatRequest{
			Messages: []Message{
				{
					Role:    "user",
					Content: prompt,
				},
			},
			Model:  model,
			Stream: stream,
		}

		response, err := client.SendChat(request)
		if err != nil {
			fmt.Fprintf(os.Stderr, "LLMサーバーとの通信に失敗しました: %v\n", err)
			os.Exit(1)
		}

		if len(response.Choices) > 0 {
			fmt.Println(response.Choices[0].Message.Content)
		} else {
			fmt.Println("LLMサーバーから応答がありません")
		}
	},
}

func init() {
	Command.Flags().StringVar(&model, "model", "", "使用するモデル名")
	Command.Flags().StringVar(&server, "server", "http://localhost:1234", "LLMサーバーのURL")
	Command.Flags().BoolVar(&stream, "stream", false, "ストリーミング応答を有効にする")
	Command.Flags().StringVar(&prompt, "prompt", "", "AIに送信するプロンプト")
	Command.Flags().IntVar(&maxTokens, "max-tokens", 1000, "最大トークン数")

	Command.Flags().StringVar(&token, "token", "", "APIトークン (オプション)")
	Command.MarkFlagRequired("prompt")
}
