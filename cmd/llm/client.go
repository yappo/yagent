package llm

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// LLMClient represents a client for communicating with the LLM server
type LLMClient struct {
	baseURL    string
	httpClient *http.Client
	token      string
	config     *Config
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

// ChatRequest represents a chat request to the LLM server
type ChatRequest struct {
	Messages []Message `json:"messages"`
	Model    string    `json:"model,omitempty"`
	Stream   bool      `json:"stream,omitempty"`
}

// Message represents a single message in the conversation
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// ChatResponse represents a response from the LLM server
type ChatResponse struct {
	ID      string   `json:"id"`
	Object  string   `json:"object"`
	Created int64    `json:"created"` // Unix timestamp instead of time.Time
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
		return nil, fmt.Errorf("LLM サーバーとの通信に失敗しました: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("サーバーがステータス %d を返しました: %s", resp.StatusCode, string(body))
	}

	// Debug: Read the raw response to see what we're getting
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	// Log the raw response for debugging
	fmt.Printf("Raw response: %s\n", string(body))

	// Try to parse as JSON
	var chatResponse ChatResponse
	if err := json.Unmarshal(body, &chatResponse); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return &chatResponse, nil
}

// ReadFile reads a file and returns its content
func (c *LLMClient) ReadFile(filePath string) (string, error) {
	// ファイルパスの安全チェック
	if !c.isPathSafe(filePath) {
		return "", fmt.Errorf("安全でないファイルパスです：%s", filePath)
	}

	// ファイルパスの制限チェック
	if !c.isPathAllowed(filePath) {
		return "", fmt.Errorf("アクセスが許可されていないファイルパスです：%s", filePath)
	}

	// ファイルが存在するかどうか確認
	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		return "", fmt.Errorf("ファイルが存在しません：%s", filePath)
	}

	content, err := os.ReadFile(filePath)
	if err != nil {
		return "", fmt.Errorf("failed to read file %s: %w", filePath, err)
	}
	return string(content), nil
}

// WriteFile writes content to a file
func (c *LLMClient) WriteFile(filePath string, content string) error {
	err := os.WriteFile(filePath, []byte(content), 0644)
	if err != nil {
		return fmt.Errorf("failed to write file %s: %w", filePath, err)
	}
	return nil
}

// ConfirmFileOperation asks the user for confirmation before file operation
func (c *LLMClient) ConfirmFileOperation(operation, filePath string) bool {
	fmt.Printf("LLM が %s を要求しました。ファイル: %s\n", operation, filePath)
	fmt.Print("実行しますか? (y/n): ")
	scanner := bufio.NewScanner(os.Stdin)
	if scanner.Scan() {
		input := strings.TrimSpace(scanner.Text())
		return strings.ToLower(input) == "y"
	}
	return false
}

// isPathSafe checks if the file path is safe (no directory traversal)
func (c *LLMClient) isPathSafe(filePath string) bool {
	// パスを正規化
	cleanPath := filepath.Clean(filePath)

	// 正規化後のパスが相対パスの場合、".." が含まれていないか確認
	if strings.HasPrefix(cleanPath, "..") {
		return false
	}

	// 絶対パスに変換して確認
	absPath, err := filepath.Abs(filePath)
	if err != nil {
		return false
	}

	// 正規化後の絶対パスも確認
	cleanAbsPath := filepath.Clean(absPath)
	if strings.HasPrefix(cleanAbsPath, "..") {
		return false
	}

	return true
}

// isPathAllowed checks if the file path is allowed based on configuration
func (c *LLMClient) isPathAllowed(filePath string) bool {
	// 設定がなければすべて許可
	if c.config == nil || len(c.config.File.AllowPaths) == 0 {
		return true
	}

	// ファイルパスを絶対パスに変換
	absFilePath, err := filepath.Abs(filePath)
	if err != nil {
		return false // 絶対パスに変換できない場合は許可しない
	}

	// 許可されたパスを確認
	for _, allowedPath := range c.config.File.AllowPaths {
		// 許可されたパスを絶対パスに変換
		absAllowedPath, err := filepath.Abs(allowedPath)
		if err != nil {
			continue // 絶対パスに変換できない場合はスキップ
		}

		if strings.HasPrefix(absFilePath, absAllowedPath) {
			return true
		}
	}

	return false
}
