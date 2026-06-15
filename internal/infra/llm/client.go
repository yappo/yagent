package llm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"yagent/internal/domain"
)

const (
	apiChatCompletions = "chat_completions"
	apiResponses       = "responses"

	responsesOutputMetadataKey = "responses_output_json"
)

type Client struct {
	baseURL    string
	token      string
	api        string
	httpClient *http.Client
}

func NewClient(baseURL, token string, timeout time.Duration, api string) *Client {
	if timeout <= 0 {
		timeout = 20 * time.Minute
	}
	if api == "" {
		api = apiChatCompletions
	}
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		token:   token,
		api:     api,
		httpClient: &http.Client{
			Timeout: timeout,
		},
	}
}

type chatRequestDTO struct {
	Messages          []messageDTO           `json:"messages"`
	Model             string                 `json:"model,omitempty"`
	Stream            bool                   `json:"stream,omitempty"`
	Tools             []toolDefinitionDTO    `json:"tools,omitempty"`
	ResponseFormat    *chatResponseFormatDTO `json:"response_format,omitempty"`
	MaxTokens         int                    `json:"max_tokens,omitempty"`
	Temperature       *float64               `json:"temperature,omitempty"`
	TopP              *float64               `json:"top_p,omitempty"`
	TopK              int                    `json:"top_k,omitempty"`
	MinP              *float64               `json:"min_p,omitempty"`
	PresencePenalty   *float64               `json:"presence_penalty,omitempty"`
	RepetitionPenalty *float64               `json:"repetition_penalty,omitempty"`
	ReasoningEffort   string                 `json:"reasoning_effort,omitempty"`
	ParallelToolCalls *bool                  `json:"parallel_tool_calls,omitempty"`
	Store             *bool                  `json:"store,omitempty"`
}

type chatResponseFormatDTO struct {
	Type       string               `json:"type"`
	JSONSchema *jsonSchemaFormatDTO `json:"json_schema,omitempty"`
}

type jsonSchemaFormatDTO struct {
	Name   string         `json:"name"`
	Schema map[string]any `json:"schema"`
	Strict bool           `json:"strict"`
}

type messageDTO struct {
	Role       string        `json:"role"`
	Content    string        `json:"content"`
	ToolCallID string        `json:"tool_call_id,omitempty"`
	ToolCalls  []toolCallDTO `json:"tool_calls,omitempty"`
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

type chatStreamChunkDTO struct {
	Choices []struct {
		Delta        chatStreamDeltaDTO `json:"delta"`
		FinishReason string             `json:"finish_reason"`
	} `json:"choices"`
}

type chatStreamDeltaDTO struct {
	Role      string                    `json:"role,omitempty"`
	Content   string                    `json:"content,omitempty"`
	ToolCalls []chatStreamToolCallDelta `json:"tool_calls,omitempty"`
}

type chatStreamToolCallDelta struct {
	Index    int    `json:"index"`
	ID       string `json:"id,omitempty"`
	Type     string `json:"type,omitempty"`
	Function struct {
		Name      string `json:"name,omitempty"`
		Arguments string `json:"arguments,omitempty"`
	} `json:"function,omitempty"`
}

type streamToolCallState struct {
	id        string
	name      string
	arguments strings.Builder
}

type responsesRequestDTO struct {
	Model             string                       `json:"model,omitempty"`
	Instructions      string                       `json:"instructions,omitempty"`
	Input             []json.RawMessage            `json:"input,omitempty"`
	Stream            bool                         `json:"stream,omitempty"`
	Tools             []responsesToolDefinitionDTO `json:"tools,omitempty"`
	MaxOutputTokens   int                          `json:"max_output_tokens,omitempty"`
	Temperature       *float64                     `json:"temperature,omitempty"`
	TopP              *float64                     `json:"top_p,omitempty"`
	Reasoning         *responsesReasoningDTO       `json:"reasoning,omitempty"`
	Text              *responsesTextDTO            `json:"text,omitempty"`
	ParallelToolCalls *bool                        `json:"parallel_tool_calls,omitempty"`
	Store             *bool                        `json:"store,omitempty"`
}

type responsesReasoningDTO struct {
	Effort string `json:"effort,omitempty"`
}

type responsesTextDTO struct {
	Verbosity string                  `json:"verbosity,omitempty"`
	Format    *responsesTextFormatDTO `json:"format,omitempty"`
}

type responsesTextFormatDTO struct {
	Type   string         `json:"type"`
	Name   string         `json:"name,omitempty"`
	Schema map[string]any `json:"schema,omitempty"`
	Strict bool           `json:"strict,omitempty"`
}

type responsesToolDefinitionDTO struct {
	Type        string         `json:"type"`
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Parameters  map[string]any `json:"parameters,omitempty"`
}

type responsesMessageInputDTO struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type responsesFunctionCallOutputDTO struct {
	Type   string `json:"type"`
	CallID string `json:"call_id"`
	Output string `json:"output"`
}

type responsesResponseDTO struct {
	ID     string            `json:"id"`
	Status string            `json:"status"`
	Output []json.RawMessage `json:"output"`
	Error  *struct {
		Message string `json:"message"`
		Type    string `json:"type"`
	} `json:"error,omitempty"`
}

type responsesOutputItemDTO struct {
	Type      string                `json:"type"`
	ID        string                `json:"id,omitempty"`
	CallID    string                `json:"call_id,omitempty"`
	Name      string                `json:"name,omitempty"`
	Arguments string                `json:"arguments,omitempty"`
	Role      string                `json:"role,omitempty"`
	Content   []responsesContentDTO `json:"content,omitempty"`
}

type responsesStreamEventDTO struct {
	Type     string                  `json:"type"`
	Delta    string                  `json:"delta,omitempty"`
	Text     string                  `json:"text,omitempty"`
	Response *responsesResponseDTO   `json:"response,omitempty"`
	Item     *responsesOutputItemDTO `json:"item,omitempty"`
	Error    *struct {
		Message string `json:"message"`
		Type    string `json:"type"`
	} `json:"error,omitempty"`
}

type responsesContentDTO struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

func (c *Client) Generate(ctx context.Context, request domain.ModelRequest) (domain.ModelResponse, error) {
	switch c.api {
	case apiResponses:
		return c.generateResponses(ctx, request)
	default:
		return c.generateChatCompletions(ctx, request)
	}
}

func (c *Client) generateChatCompletions(ctx context.Context, request domain.ModelRequest) (domain.ModelResponse, error) {
	if request.Stream {
		return c.generateChatCompletionsStream(ctx, request)
	}

	payload, err := json.Marshal(toChatRequestDTO(request))
	if err != nil {
		return domain.ModelResponse{}, fmt.Errorf("request の JSON 変換に失敗しました: %w", err)
	}

	body, err := c.post(ctx, "/v1/chat/completions", payload)
	if err != nil {
		return domain.ModelResponse{}, err
	}

	var decoded chatResponseDTO
	if err := json.Unmarshal(body, &decoded); err != nil {
		return domain.ModelResponse{}, fmt.Errorf("レスポンスのデコードに失敗しました: %w", err)
	}

	if len(decoded.Choices) == 0 {
		return domain.ModelResponse{}, fmt.Errorf("LLM サーバーから応答がありません")
	}

	return domain.ModelResponse{
		Message:      fromMessageDTO(decoded.Choices[0].Message),
		FinishReason: decoded.Choices[0].FinishReason,
	}, nil
}

func (c *Client) generateChatCompletionsStream(ctx context.Context, request domain.ModelRequest) (domain.ModelResponse, error) {
	payload, err := json.Marshal(toChatRequestDTO(request))
	if err != nil {
		return domain.ModelResponse{}, fmt.Errorf("request の JSON 変換に失敗しました: %w", err)
	}

	resp, err := c.postRaw(ctx, "/v1/chat/completions", payload)
	if err != nil {
		return domain.ModelResponse{}, err
	}
	defer resp.Body.Close()

	message := domain.Message{Role: domain.RoleAssistant}
	var finishReason string
	var toolCalls []streamToolCallState
	err = readSSE(resp.Body, func(data []byte) error {
		if bytes.Equal(bytes.TrimSpace(data), []byte("[DONE]")) {
			return nil
		}
		var chunk chatStreamChunkDTO
		if err := json.Unmarshal(data, &chunk); err != nil {
			return fmt.Errorf("stream chunk のデコードに失敗しました: %w", err)
		}
		for _, choice := range chunk.Choices {
			if choice.Delta.Role != "" {
				message.Role = domain.Role(choice.Delta.Role)
			}
			if choice.Delta.Content != "" {
				message.Content += choice.Delta.Content
				emitStreamContent(request.StreamHandler, choice.Delta.Content, "chat.completion.chunk")
			}
			for _, delta := range choice.Delta.ToolCalls {
				for len(toolCalls) <= delta.Index {
					toolCalls = append(toolCalls, streamToolCallState{})
				}
				state := &toolCalls[delta.Index]
				if delta.ID != "" {
					state.id = delta.ID
				}
				if delta.Function.Name != "" {
					state.name = delta.Function.Name
				}
				if delta.Function.Arguments != "" {
					state.arguments.WriteString(delta.Function.Arguments)
				}
			}
			if choice.FinishReason != "" {
				finishReason = choice.FinishReason
			}
		}
		return nil
	})
	if err != nil {
		return domain.ModelResponse{}, err
	}
	message.ToolCalls = streamToolCalls(toolCalls)
	return domain.ModelResponse{Message: message, FinishReason: finishReason}, nil
}

func (c *Client) generateResponses(ctx context.Context, request domain.ModelRequest) (domain.ModelResponse, error) {
	if request.Stream {
		return c.generateResponsesStream(ctx, request)
	}

	payload, err := json.Marshal(toResponsesRequestDTO(request))
	if err != nil {
		return domain.ModelResponse{}, fmt.Errorf("request の JSON 変換に失敗しました: %w", err)
	}

	body, err := c.post(ctx, "/v1/responses", payload)
	if err != nil {
		return domain.ModelResponse{}, err
	}

	var decoded responsesResponseDTO
	if err := json.Unmarshal(body, &decoded); err != nil {
		return domain.ModelResponse{}, fmt.Errorf("レスポンスのデコードに失敗しました: %w", err)
	}
	if decoded.Error != nil {
		return domain.ModelResponse{}, fmt.Errorf("Responses API error: %s", decoded.Error.Message)
	}
	if len(decoded.Output) == 0 {
		return domain.ModelResponse{}, fmt.Errorf("LLM サーバーから応答がありません")
	}
	message := fromResponsesOutput(decoded.Output)
	return domain.ModelResponse{Message: message, FinishReason: decoded.Status}, nil
}

func (c *Client) generateResponsesStream(ctx context.Context, request domain.ModelRequest) (domain.ModelResponse, error) {
	payload, err := json.Marshal(toResponsesRequestDTO(request))
	if err != nil {
		return domain.ModelResponse{}, fmt.Errorf("request の JSON 変換に失敗しました: %w", err)
	}

	resp, err := c.postRaw(ctx, "/v1/responses", payload)
	if err != nil {
		return domain.ModelResponse{}, err
	}
	defer resp.Body.Close()

	var completed *responsesResponseDTO
	output := []json.RawMessage{}
	var content strings.Builder
	finishReason := ""
	err = readSSE(resp.Body, func(data []byte) error {
		if bytes.Equal(bytes.TrimSpace(data), []byte("[DONE]")) {
			return nil
		}
		var event responsesStreamEventDTO
		if err := json.Unmarshal(data, &event); err != nil {
			return fmt.Errorf("stream event のデコードに失敗しました: %w", err)
		}
		if event.Error != nil {
			return fmt.Errorf("Responses API stream error: %s", event.Error.Message)
		}
		switch event.Type {
		case "response.output_text.delta":
			if event.Delta != "" {
				content.WriteString(event.Delta)
				emitStreamContent(request.StreamHandler, event.Delta, event.Type)
			}
		case "response.output_text.done":
			if event.Text != "" && content.Len() == 0 {
				content.WriteString(event.Text)
			}
		case "response.output_item.done":
			if event.Item != nil {
				if raw, err := json.Marshal(event.Item); err == nil {
					output = append(output, raw)
				}
			}
		case "response.completed":
			completed = event.Response
			finishReason = "completed"
		case "response.failed", "response.incomplete":
			if event.Response != nil && event.Response.Error != nil {
				return fmt.Errorf("Responses API stream error: %s", event.Response.Error.Message)
			}
			finishReason = strings.TrimPrefix(event.Type, "response.")
		}
		return nil
	})
	if err != nil {
		return domain.ModelResponse{}, err
	}
	if completed != nil {
		if completed.Error != nil {
			return domain.ModelResponse{}, fmt.Errorf("Responses API error: %s", completed.Error.Message)
		}
		if len(completed.Output) > 0 {
			return domain.ModelResponse{Message: fromResponsesOutput(completed.Output), FinishReason: completed.Status}, nil
		}
		if completed.Status != "" {
			finishReason = completed.Status
		}
	}
	if len(output) > 0 {
		return domain.ModelResponse{Message: fromResponsesOutput(output), FinishReason: finishReason}, nil
	}
	return domain.ModelResponse{
		Message:      domain.Message{Role: domain.RoleAssistant, Content: content.String()},
		FinishReason: finishReason,
	}, nil
}

func (c *Client) post(ctx context.Context, path string, payload []byte) ([]byte, error) {
	resp, err := c.postRaw(ctx, path, payload)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, readErr := io.ReadAll(resp.Body)
	if readErr != nil {
		return nil, fmt.Errorf("レスポンスの読み込みに失敗しました: %w", readErr)
	}
	return body, nil
}

func (c *Client) postRaw(ctx context.Context, path string, payload []byte) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("HTTP リクエストの作成に失敗しました: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("LLM サーバーとの通信に失敗しました: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("サーバーがステータス %d を返しました: %s", resp.StatusCode, string(body))
	}
	return resp, nil
}

func toChatRequestDTO(request domain.ModelRequest) chatRequestDTO {
	messages := make([]domain.Message, 0, len(request.Messages)+1)
	if request.Instructions != "" {
		messages = append(messages, domain.Message{
			Role:    domain.RoleSystem,
			Content: request.Instructions,
			AgentID: request.Agent.ID,
		})
	}
	messages = append(messages, request.Messages...)

	dto := chatRequestDTO{
		Messages:          make([]messageDTO, 0, len(messages)),
		Model:             request.Model,
		Stream:            request.Stream,
		Tools:             make([]toolDefinitionDTO, 0, len(request.Tools)),
		ResponseFormat:    toChatResponseFormatDTO(request.ResponseFormat),
		MaxTokens:         request.Settings.MaxOutputTokens,
		Temperature:       request.Settings.Temperature,
		TopP:              request.Settings.TopP,
		TopK:              request.Settings.TopK,
		MinP:              request.Settings.MinP,
		PresencePenalty:   request.Settings.PresencePenalty,
		RepetitionPenalty: request.Settings.RepetitionPenalty,
		ReasoningEffort:   request.Settings.ReasoningEffort,
		ParallelToolCalls: request.Settings.ParallelToolCalls,
		Store:             request.Settings.Store,
	}

	for _, message := range messages {
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

func toResponsesRequestDTO(request domain.ModelRequest) responsesRequestDTO {
	dto := responsesRequestDTO{
		Model:             request.Model,
		Instructions:      request.Instructions,
		Input:             toResponsesInput(request.Messages),
		Stream:            request.Stream,
		Tools:             make([]responsesToolDefinitionDTO, 0, len(request.Tools)),
		MaxOutputTokens:   request.Settings.MaxOutputTokens,
		Temperature:       request.Settings.Temperature,
		TopP:              request.Settings.TopP,
		ParallelToolCalls: request.Settings.ParallelToolCalls,
		Store:             request.Settings.Store,
	}
	if request.Settings.ReasoningEffort != "" {
		dto.Reasoning = &responsesReasoningDTO{Effort: request.Settings.ReasoningEffort}
	}
	if request.Settings.TextVerbosity != "" {
		dto.Text = &responsesTextDTO{Verbosity: request.Settings.TextVerbosity}
	}
	if format := toResponsesTextFormatDTO(request.ResponseFormat); format != nil {
		if dto.Text == nil {
			dto.Text = &responsesTextDTO{}
		}
		dto.Text.Format = format
	}
	for _, tool := range request.Tools {
		dto.Tools = append(dto.Tools, responsesToolDefinitionDTO{
			Type:        "function",
			Name:        tool.Name,
			Description: tool.Description,
			Parameters:  tool.Parameters,
		})
	}
	return dto
}

func toChatResponseFormatDTO(format *domain.ResponseFormat) *chatResponseFormatDTO {
	if format == nil {
		return nil
	}
	formatType := strings.TrimSpace(format.Type)
	if formatType == "" {
		formatType = "json_schema"
	}
	if formatType != "json_schema" {
		return &chatResponseFormatDTO{Type: formatType}
	}
	if strings.TrimSpace(format.Name) == "" || len(format.Schema) == 0 {
		return nil
	}
	return &chatResponseFormatDTO{
		Type: "json_schema",
		JSONSchema: &jsonSchemaFormatDTO{
			Name:   format.Name,
			Schema: format.Schema,
			Strict: format.Strict,
		},
	}
}

func toResponsesTextFormatDTO(format *domain.ResponseFormat) *responsesTextFormatDTO {
	if format == nil {
		return nil
	}
	formatType := strings.TrimSpace(format.Type)
	if formatType == "" {
		formatType = "json_schema"
	}
	if formatType != "json_schema" {
		return &responsesTextFormatDTO{Type: formatType}
	}
	if strings.TrimSpace(format.Name) == "" || len(format.Schema) == 0 {
		return nil
	}
	return &responsesTextFormatDTO{
		Type:   "json_schema",
		Name:   format.Name,
		Schema: format.Schema,
		Strict: format.Strict,
	}
}

func toResponsesInput(messages []domain.Message) []json.RawMessage {
	out := make([]json.RawMessage, 0, len(messages))
	for _, message := range messages {
		if raw := message.Metadata[responsesOutputMetadataKey]; raw != "" {
			var items []json.RawMessage
			if err := json.Unmarshal([]byte(raw), &items); err == nil {
				out = append(out, items...)
				continue
			}
		}
		switch message.Role {
		case domain.RoleTool:
			out = appendRaw(out, responsesFunctionCallOutputDTO{
				Type:   "function_call_output",
				CallID: message.ToolCallID,
				Output: message.Content,
			})
		case domain.RoleUser, domain.RoleAssistant, domain.RoleSystem:
			out = appendRaw(out, responsesMessageInputDTO{
				Role:    string(message.Role),
				Content: message.Content,
			})
			for _, call := range message.ToolCalls {
				out = appendRaw(out, responsesOutputItemDTO{
					Type:      "function_call",
					CallID:    call.ID,
					Name:      call.Name,
					Arguments: mustMarshalString(call.Arguments),
				})
			}
		}
	}
	return out
}

func appendRaw(out []json.RawMessage, value any) []json.RawMessage {
	data, err := json.Marshal(value)
	if err != nil {
		return out
	}
	return append(out, data)
}

func readSSE(reader io.Reader, handle func([]byte) error) error {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	dataLines := []string{}
	flush := func() error {
		if len(dataLines) == 0 {
			return nil
		}
		data := []byte(strings.Join(dataLines, "\n"))
		dataLines = dataLines[:0]
		return handle(data)
	}
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			if err := flush(); err != nil {
				return err
			}
			continue
		}
		if strings.HasPrefix(line, ":") {
			continue
		}
		if strings.HasPrefix(line, "data:") {
			dataLines = append(dataLines, strings.TrimSpace(strings.TrimPrefix(line, "data:")))
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("stream の読み込みに失敗しました: %w", err)
	}
	return flush()
}

func emitStreamContent(handler domain.ModelStreamHandler, delta string, rawEventType string) {
	if handler == nil || delta == "" {
		return
	}
	handler(domain.ModelStreamEvent{
		Type:         "content_delta",
		ContentDelta: delta,
		RawEventType: rawEventType,
	})
}

func streamToolCalls(states []streamToolCallState) []domain.ToolCall {
	calls := make([]domain.ToolCall, 0, len(states))
	for _, state := range states {
		if state.id == "" && state.name == "" && state.arguments.Len() == 0 {
			continue
		}
		args := map[string]any{}
		if raw := state.arguments.String(); raw != "" {
			_ = json.Unmarshal([]byte(raw), &args)
		}
		calls = append(calls, domain.ToolCall{
			ID:        state.id,
			Name:      state.name,
			Arguments: args,
		})
	}
	return calls
}

func toMessageDTO(message domain.Message) messageDTO {
	dto := messageDTO{
		Role:       string(message.Role),
		Content:    message.Content,
		ToolCallID: message.ToolCallID,
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
		function.Name = call.Name
		function.Arguments = mustMarshalString(call.Arguments)
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
		Role:       domain.Role(message.Role),
		Content:    message.Content,
		ToolCallID: message.ToolCallID,
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

func fromResponsesOutput(output []json.RawMessage) domain.Message {
	result := domain.Message{
		Role:     domain.RoleAssistant,
		Metadata: map[string]string{},
	}
	for _, raw := range output {
		var item responsesOutputItemDTO
		if err := json.Unmarshal(raw, &item); err != nil {
			continue
		}
		switch item.Type {
		case "message":
			for _, content := range item.Content {
				if content.Type == "output_text" || content.Type == "text" {
					if result.Content != "" {
						result.Content += "\n"
					}
					result.Content += content.Text
				}
			}
		case "function_call":
			args := map[string]any{}
			_ = json.Unmarshal([]byte(item.Arguments), &args)
			result.ToolCalls = append(result.ToolCalls, domain.ToolCall{
				ID:        item.CallID,
				Name:      item.Name,
				Arguments: args,
			})
		}
	}
	if data, err := json.Marshal(output); err == nil {
		result.Metadata[responsesOutputMetadataKey] = string(data)
	}
	if len(result.Metadata) == 0 {
		result.Metadata = nil
	}
	return result
}

func mustMarshalString(value any) string {
	data, err := json.Marshal(value)
	if err != nil {
		return "{}"
	}
	return string(data)
}
