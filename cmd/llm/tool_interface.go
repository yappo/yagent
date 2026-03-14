package llm

import (
	"context"
	"encoding/json"
	"fmt"
)

// ToolOutput ツールの実行結果
type ToolOutput struct {
	Success bool        `json:"success"`
	Data    interface{} `json:"data,omitempty"`
	Error   string      `json:"error,omitempty"`
}

// ToolCall ツールの呼び出し情報
type ToolCall struct {
	ID        string                 `json:"id"`
	Name      string                 `json:"name"`
	Arguments map[string]interface{} `json:"arguments"`
}

// ToolCallResponse LLM 応答用のツール呼び出し
type ToolCallResponse struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

// ToolDefinition ツールの定義情報（OpenAI API 形式）
type ToolDefinition struct {
	Type     string             `json:"type"`
	Function ToolFunctionConfig `json:"function"`
}

// ToolFunctionConfig ツールの関数設定
type ToolFunctionConfig struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	Parameters  map[string]interface{} `json:"parameters"`
}

// ToolInterface ツールの実装インターフェース
type ToolInterface interface {
	// Name ツール名
	Name() string
	// Description ツールの説明
	Description() string
	// Parameters パラメータ定義（JSON Schema 形式）
	Parameters() map[string]interface{}
	// Execute ツールの実行
	Execute(ctx context.Context, args map[string]interface{}) *ToolOutput
}

// ToolHandler ツール呼び出しのハンドリング
type ToolHandler struct {
	registry *ToolRegistry
}

// NewToolHandler ツールハンドラの作成
func NewToolHandler(registry *ToolRegistry) *ToolHandler {
	return &ToolHandler{
		registry: registry,
	}
}

// GetRegistry レジストリを取得
func (h *ToolHandler) GetRegistry() *ToolRegistry {
	return h.registry
}

// HandleToolCalls ツール呼び出しを処理
func (h *ToolHandler) HandleToolCalls(ctx context.Context, toolCalls []ToolCall) []ToolOutput {
	results := make([]ToolOutput, 0, len(toolCalls))

	for _, tc := range toolCalls {
		result := h.executeTool(ctx, tc)
		results = append(results, result)
	}

	return results
}

// executeTool 単一のツールを実行
func (h *ToolHandler) executeTool(ctx context.Context, tc ToolCall) ToolOutput {
	tool := h.registry.Get(tc.Name)
	if tool == nil {
		return ToolOutput{
			Success: false,
			Error:   fmt.Sprintf("ツール '%s' が見つかりません", tc.Name),
		}
	}

	return *tool.Execute(ctx, tc.Arguments)
}

// BuildToolResponse LLM 応答用のレスポンスを構築
func (h *ToolHandler) BuildToolResponse(id string, toolName string, arguments map[string]interface{}) ToolCallResponse {
	argsJSON, _ := json.Marshal(arguments)
	return ToolCallResponse{
		ID:   id,
		Type: "function",
		Function: struct {
			Name      string `json:"name"`
			Arguments string `json:"arguments"`
		}{
			Name:      toolName,
			Arguments: string(argsJSON),
		},
	}
}
