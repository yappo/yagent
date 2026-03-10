package llm

import (
	"fmt"
	"os"

	"github.com/pelletier/go-toml/v2"
	"github.com/spf13/cobra"
)

var (
	model      string
	stream     bool
	prompt     string
	maxTokens  int
	configFile string
)

// Config represents the configuration structure
type Config struct {
	Server struct {
		Default string `toml:"default"`
		Servers []struct {
			Name  string `toml:"name"`
			URL   string `toml:"url"`
			Token string `toml:"token"`
		} `toml:"servers"`
	} `toml:"server"`
	File struct {
		AllowPaths []string `toml:"allow_paths"`
	} `toml:"file"`
}

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

		var serverURL, token string
		var client *LLMClient

		if configFile != "" {
			// 設定ファイルから読み込み
			config, err := LoadConfig(configFile)
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

			client = NewLLMClientWithConfig(serverURL, token, config)
		} else {
			// 設定ファイルが指定されていない場合は、デフォルト値を使用
			serverURL = "http://localhost:1234"
			token = ""

			client = NewLLMClient(serverURL, token)
		}

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

// LoadConfig loads the configuration from a TOML file
func LoadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("設定ファイルの読み込みに失敗しました: %w", err)
	}

	var config Config
	if err := toml.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("設定ファイルのパースに失敗しました: %w", err)
	}

	return &config, nil
}

func init() {
	Command.Flags().StringVar(&configFile, "config", "", "設定ファイルのパス")
	Command.Flags().StringVar(&model, "model", "", "使用するモデル名")
	Command.Flags().BoolVar(&stream, "stream", false, "ストリーミング応答を有効にする")
	Command.Flags().StringVar(&prompt, "prompt", "", "AIに送信するプロンプト")
	Command.Flags().IntVar(&maxTokens, "max-tokens", 1000, "最大トークン数")

	// フラグの必須項目を設定
	Command.MarkFlagRequired("prompt")
}
