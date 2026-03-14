package llm

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// LLMClient represents a client for communicating with the LLM server
type LLMClient struct {
	baseURL     string
	httpClient  *http.Client
	token       string
	config      *Config
	toolHandler *ToolHandler
}

// NewLLMClient creates a new LLM client instance
func NewLLMClient(baseURL string, token string) *LLMClient {
	return &LLMClient{
		baseURL: baseURL,
		httpClient: &http.Client{
			Timeout: 120 * time.Second,
		},
		token: token,
	}
}

// NewLLMClientWithConfig creates a new LLM client instance with configuration
func NewLLMClientWithConfig(baseURL string, token string, config *Config) *LLMClient {
	return &LLMClient{
		baseURL: baseURL,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
		token:  token,
		config: config,
	}
}

// WithToolHandler ツールハンドラを設定
func (c *LLMClient) WithToolHandler(handler *ToolHandler) *LLMClient {
	c.toolHandler = handler
	return c
}

// WithTools ツールを登録
func (c *LLMClient) WithTools(tools ...ToolInterface) *LLMClient {
	registry := NewToolRegistry()
	for _, tool := range tools {
		registry.Register(tool)
	}
	c.toolHandler = NewToolHandler(registry)
	return c
}

// GetToolHandler ツールハンドラを取得
func (c *LLMClient) GetToolHandler() *ToolHandler {
	return c.toolHandler
}

// ChatRequest represents a chat request to the LLM server
type ChatRequest struct {
	Messages []Message        `json:"messages"`
	Model    string           `json:"model,omitempty"`
	Stream   bool             `json:"stream,omitempty"`
	Tools    []ToolDefinition `json:"tools,omitempty"`
}

// Message represents a single message in the conversation
type Message struct {
	Role      string             `json:"role"`
	Content   string             `json:"content"`
	ToolCalls []ToolCallResponse `json:"tool_calls,omitempty"`
}

// ChatResponse represents a response from the LLM server
type ChatResponse struct {
	ID      string   `json:"id"`
	Object  string   `json:"object"`
	Created int64    `json:"created"`
	Model   string   `json:"model"`
	Choices []Choice `json:"choices"`
}

// Choice represents a choice in the response
type Choice struct {
	Index        int     `json:"index"`
	Message      Message `json:"message"`
	FinishReason string  `json:"finish_reason"`
}

// SendChat sends a chat request to the LLM server and returns the response
func (c *LLMClient) SendChat(request ChatRequest) (*ChatResponse, error) {
	url := fmt.Sprintf("%s/v1/chat/completions", c.baseURL)

	jsonData, err := json.Marshal(request)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	req, err := http.NewRequest("POST", url, bytes.NewBuffer(jsonData))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("LLM サーバーとの通信に失敗しました：%w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("サーバーがステータス %d を返しました：%s", resp.StatusCode, string(body))
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	var chatResponse ChatResponse
	if err := json.Unmarshal(body, &chatResponse); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return &chatResponse, nil
}

// SendChatWithTools sends a chat request with tool definitions and handles tool calls
func (c *LLMClient) SendChatWithTools(request ChatRequest, maxIterations int) (string, error) {
	if c.toolHandler == nil {
		return "", fmt.Errorf("ツールハンドラが設定されていません")
	}

	// ツール定義を取得
	registry := c.toolHandler.GetRegistry()
	toolDefinitions := registry.List()
	if len(toolDefinitions) > 0 {
		request.Tools = toolDefinitions
	}

	messages := request.Messages
	iteration := 0

	for iteration < maxIterations {
		iteration++

		response, err := c.SendChat(request)
		if err != nil {
			return "", err
		}

		if len(response.Choices) == 0 {
			return "", fmt.Errorf("LLM サーバーから応答がありません")
		}

		message := response.Choices[0].Message

		// ツール呼び出しがあるか確認
		if len(message.ToolCalls) > 0 || (message.Content == "" && response.Choices[0].FinishReason == "tool_calls") {
			// ツール呼び出しを処理
			toolCalls, err := parseToolCalls(message)
			if err != nil {
				return "", fmt.Errorf("ツール呼び出しの解析に失敗しました：%w", err)
			}

			if len(toolCalls) == 0 {
				// ツール呼び出しが解析されなかった場合は通常応答として扱う
				return message.Content, nil
			}

			results := c.toolHandler.HandleToolCalls(nil, toolCalls)

			// ツール結果をメッセージに追加
			for _, result := range results {
				toolMessage := Message{
					Role:    "tool",
					Content: "",
				}
				if result.Success {
					toolMessage.Content = fmt.Sprintf("%v", result.Data)
				} else {
					toolMessage.Content = fmt.Sprintf("エラー：%s", result.Error)
				}
				messages = append(messages, toolMessage)
			}

			request.Messages = messages
			continue
		}

		// 通常応答
		return message.Content, nil
	}

	return "", fmt.Errorf("最大反復回数 (%d) に達しました", maxIterations)
}

// parseToolCalls メッセージからツール呼び出しを解析
func parseToolCalls(message Message) ([]ToolCall, error) {
	var toolCalls []ToolCall

	// tool_calls フィールドから解析
	for _, tc := range message.ToolCalls {
		var args map[string]interface{}
		if err := json.Unmarshal([]byte(tc.Function.Arguments), &args); err != nil {
			return toolCalls, fmt.Errorf("arguments の解析に失敗しました：%w", err)
		}

		toolCalls = append(toolCalls, ToolCall{
			ID:        tc.ID,
			Name:      tc.Function.Name,
			Arguments: args,
		})
	}

	return toolCalls, nil
}

// ConfirmFileOperation asks the user for confirmation before file operation
func (c *LLMClient) ConfirmFileOperation(operation, filePath string) bool {
	fmt.Printf("LLM が %s を要求しました。ファイル：%s\n", operation, filePath)
	fmt.Print("実行しますか？ (y/n): ")
	scanner := bufio.NewScanner(os.Stdin)
	if scanner.Scan() {
		input := strings.TrimSpace(scanner.Text())
		return strings.ToLower(input) == "y"
	}
	return false
}
