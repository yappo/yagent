package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"yagent/internal/domain"
)

type Client struct {
	baseURL    string
	token      string
	httpClient *http.Client
}

func NewClient(baseURL, token string) *Client {
	return &Client{
		baseURL: baseURL,
		token:   token,
		httpClient: &http.Client{
			Timeout: 120 * time.Second,
		},
	}
}

type chatRequestDTO struct {
	Messages []messageDTO        `json:"messages"`
	Model    string              `json:"model,omitempty"`
	Stream   bool                `json:"stream,omitempty"`
	Tools    []toolDefinitionDTO `json:"tools,omitempty"`
}

type messageDTO struct {
	Role      string        `json:"role"`
	Content   string        `json:"content"`
	ToolCalls []toolCallDTO `json:"tool_calls,omitempty"`
}

type toolCallDTO struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

type toolDefinitionDTO struct {
	Type     string `json:"type"`
	Function struct {
		Name        string         `json:"name"`
		Description string         `json:"description"`
		Parameters  map[string]any `json:"parameters"`
	} `json:"function"`
}

type chatResponseDTO struct {
	Choices []struct {
		Message      messageDTO `json:"message"`
		FinishReason string     `json:"finish_reason"`
	} `json:"choices"`
}

func (c *Client) Complete(ctx context.Context, request domain.CompletionRequest) (domain.CompletionResponse, error) {
	payload, err := json.Marshal(toChatRequestDTO(request))
	if err != nil {
		return domain.CompletionResponse{}, fmt.Errorf("request の JSON 変換に失敗しました: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/v1/chat/completions", bytes.NewReader(payload))
	if err != nil {
		return domain.CompletionResponse{}, fmt.Errorf("HTTP リクエストの作成に失敗しました: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return domain.CompletionResponse{}, fmt.Errorf("LLM サーバーとの通信に失敗しました: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return domain.CompletionResponse{}, fmt.Errorf("サーバーがステータス %d を返しました: %s", resp.StatusCode, string(body))
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return domain.CompletionResponse{}, fmt.Errorf("レスポンスの読み込みに失敗しました: %w", err)
	}

	var decoded chatResponseDTO
	if err := json.Unmarshal(body, &decoded); err != nil {
		return domain.CompletionResponse{}, fmt.Errorf("レスポンスのデコードに失敗しました: %w", err)
	}

	if len(decoded.Choices) == 0 {
		return domain.CompletionResponse{}, fmt.Errorf("LLM サーバーから応答がありません")
	}

	return domain.CompletionResponse{
		Message:      fromMessageDTO(decoded.Choices[0].Message),
		FinishReason: decoded.Choices[0].FinishReason,
	}, nil
}

func toChatRequestDTO(request domain.CompletionRequest) chatRequestDTO {
	dto := chatRequestDTO{
		Messages: make([]messageDTO, 0, len(request.Messages)),
		Model:    request.Model,
		Stream:   request.Stream,
		Tools:    make([]toolDefinitionDTO, 0, len(request.Tools)),
	}

	for _, message := range request.Messages {
		dto.Messages = append(dto.Messages, toMessageDTO(message))
	}

	for _, tool := range request.Tools {
		var fn struct {
			Name        string         `json:"name"`
			Description string         `json:"description"`
			Parameters  map[string]any `json:"parameters"`
		}
		fn.Name = tool.Name
		fn.Description = tool.Description
		fn.Parameters = tool.Parameters

		dto.Tools = append(dto.Tools, toolDefinitionDTO{
			Type:     "function",
			Function: fn,
		})
	}

	return dto
}

func toMessageDTO(message domain.Message) messageDTO {
	dto := messageDTO{
		Role:    string(message.Role),
		Content: message.Content,
	}

	if len(message.ToolCalls) == 0 {
		return dto
	}

	dto.ToolCalls = make([]toolCallDTO, 0, len(message.ToolCalls))
	for _, call := range message.ToolCalls {
		var function struct {
			Name      string `json:"name"`
			Arguments string `json:"arguments"`
		}
		arguments, _ := json.Marshal(call.Arguments)
		function.Name = call.Name
		function.Arguments = string(arguments)
		dto.ToolCalls = append(dto.ToolCalls, toolCallDTO{
			ID:       call.ID,
			Type:     "function",
			Function: function,
		})
	}

	return dto
}

func fromMessageDTO(message messageDTO) domain.Message {
	result := domain.Message{
		Role:    domain.Role(message.Role),
		Content: message.Content,
	}

	if len(message.ToolCalls) == 0 {
		return result
	}

	result.ToolCalls = make([]domain.ToolCall, 0, len(message.ToolCalls))
	for _, call := range message.ToolCalls {
		args := map[string]any{}
		_ = json.Unmarshal([]byte(call.Function.Arguments), &args)
		result.ToolCalls = append(result.ToolCalls, domain.ToolCall{
			ID:        call.ID,
			Name:      call.Function.Name,
			Arguments: args,
		})
	}

	return result
}
