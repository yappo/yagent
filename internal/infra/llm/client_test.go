package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"yagent/internal/domain"
)

func TestGenerate(t *testing.T) {
	client := NewClient("http://llm.test", "", time.Minute, "chat_completions")
	client.httpClient = fakeHTTPClient(func(r *http.Request) (int, string) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		return http.StatusOK, `{"choices":[{"message":{"role":"assistant","content":"hello"}}]}`
	})

	response, err := client.Generate(context.Background(), domain.ModelRequest{
		Agent:    domain.AgentSpec{ID: "manager"},
		Messages: []domain.Message{{Role: domain.RoleUser, Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}

	if response.Message.Content != "hello" {
		t.Fatalf("unexpected content: %s", response.Message.Content)
	}
}

func TestGenerateChatCompletionsStream(t *testing.T) {
	client := NewClient("http://llm.test", "", time.Minute, "chat_completions")
	client.httpClient = fakeHTTPClient(func(r *http.Request) (int, string) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		var body chatRequestDTO
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if !body.Stream {
			t.Fatalf("expected stream request")
		}
		return http.StatusOK, strings.Join([]string{
			`data: {"choices":[{"delta":{"role":"assistant","content":"hel"}}]}`,
			"",
			`data: {"choices":[{"delta":{"content":"lo"},"finish_reason":"stop"}]}`,
			"",
			`data: [DONE]`,
			"",
		}, "\n")
	})

	deltas := []string{}
	response, err := client.Generate(context.Background(), domain.ModelRequest{
		Agent:    domain.AgentSpec{ID: "manager"},
		Messages: []domain.Message{{Role: domain.RoleUser, Content: "hi"}},
		Stream:   true,
		StreamHandler: func(event domain.ModelStreamEvent) {
			deltas = append(deltas, event.ContentDelta)
		},
	})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}

	if response.Message.Content != "hello" || response.FinishReason != "stop" {
		t.Fatalf("unexpected streaming response: %+v", response)
	}
	if strings.Join(deltas, "") != "hello" {
		t.Fatalf("unexpected deltas: %+v", deltas)
	}
}

func TestGenerateChatCompletionsStreamAccumulatesToolCalls(t *testing.T) {
	client := NewClient("http://llm.test", "", time.Minute, "chat_completions")
	client.httpClient = fakeHTTPClient(func(r *http.Request) (int, string) {
		return http.StatusOK, strings.Join([]string{
			`data: {"choices":[{"delta":{"role":"assistant","tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"lookup","arguments":"{\"query\""}}]}}]}`,
			"",
			`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":":\"q\"}"}}]},"finish_reason":"tool_calls"}]}`,
			"",
			`data: [DONE]`,
			"",
		}, "\n")
	})

	response, err := client.Generate(context.Background(), domain.ModelRequest{
		Agent:    domain.AgentSpec{ID: "manager"},
		Messages: []domain.Message{{Role: domain.RoleUser, Content: "hi"}},
		Stream:   true,
	})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}
	if len(response.Message.ToolCalls) != 1 {
		t.Fatalf("expected tool call, got %+v", response.Message.ToolCalls)
	}
	call := response.Message.ToolCalls[0]
	if call.ID != "call_1" || call.Name != "lookup" || call.Arguments["query"] != "q" {
		t.Fatalf("unexpected tool call: %+v", call)
	}
}

func TestNewClientUsesDefaultTimeout(t *testing.T) {
	client := NewClient("http://localhost:1234", "", 0, "")
	if client.httpClient.Timeout != 20*time.Minute {
		t.Fatalf("unexpected default timeout: %s", client.httpClient.Timeout)
	}
}

func TestGenerateResponses(t *testing.T) {
	client := NewClient("http://llm.test", "", time.Minute, "responses")
	client.httpClient = fakeHTTPClient(func(r *http.Request) (int, string) {
		if r.URL.Path != "/v1/responses" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		var body responsesRequestDTO
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if body.Model != "gpt-5.5" {
			t.Fatalf("unexpected model: %s", body.Model)
		}
		if body.Reasoning == nil || body.Reasoning.Effort != "high" {
			t.Fatalf("unexpected reasoning settings: %+v", body.Reasoning)
		}
		if body.Text == nil || body.Text.Verbosity != "medium" {
			t.Fatalf("unexpected text settings: %+v", body.Text)
		}
		if body.Text.Format == nil || body.Text.Format.Type != "json_schema" || body.Text.Format.Name != "answer" || !body.Text.Format.Strict {
			t.Fatalf("unexpected response format: %+v", body.Text.Format)
		}
		if len(body.Tools) != 1 || body.Tools[0].Name != "lookup" {
			t.Fatalf("unexpected tools: %+v", body.Tools)
		}
		return http.StatusOK, `{"id":"resp_1","status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"hello"}]}]}`
	})

	response, err := client.Generate(context.Background(), domain.ModelRequest{
		Model:        "gpt-5.5",
		Instructions: "Be direct.",
		Agent:        domain.AgentSpec{ID: "manager"},
		Messages:     []domain.Message{{Role: domain.RoleUser, Content: "hi"}},
		Tools: []domain.ToolDefinition{{
			Name:        "lookup",
			Description: "Lookup a value.",
			Parameters:  map[string]any{"type": "object"},
		}},
		ResponseFormat: &domain.ResponseFormat{
			Type:   "json_schema",
			Name:   "answer",
			Schema: map[string]any{"type": "object"},
			Strict: true,
		},
		Settings: domain.ModelSettings{ReasoningEffort: "high", TextVerbosity: "medium"},
	})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}
	if response.Message.Content != "hello" {
		t.Fatalf("unexpected content: %s", response.Message.Content)
	}
}

func TestGenerateResponsesStream(t *testing.T) {
	client := NewClient("http://llm.test", "", time.Minute, "responses")
	client.httpClient = fakeHTTPClient(func(r *http.Request) (int, string) {
		if r.URL.Path != "/v1/responses" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		var body responsesRequestDTO
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if !body.Stream {
			t.Fatalf("expected stream request")
		}
		return http.StatusOK, strings.Join([]string{
			`data: {"type":"response.output_text.delta","delta":"hel"}`,
			"",
			`data: {"type":"response.output_text.delta","delta":"lo"}`,
			"",
			`data: {"type":"response.completed","response":{"id":"resp_1","status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"hello"}]}]}}`,
			"",
			`data: [DONE]`,
			"",
		}, "\n")
	})

	deltas := []string{}
	response, err := client.Generate(context.Background(), domain.ModelRequest{
		Model:    "gpt-5.5",
		Agent:    domain.AgentSpec{ID: "manager"},
		Messages: []domain.Message{{Role: domain.RoleUser, Content: "hi"}},
		Stream:   true,
		StreamHandler: func(event domain.ModelStreamEvent) {
			deltas = append(deltas, event.ContentDelta)
		},
	})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}
	if response.Message.Content != "hello" || response.FinishReason != "completed" {
		t.Fatalf("unexpected streaming response: %+v", response)
	}
	if strings.Join(deltas, "") != "hello" {
		t.Fatalf("unexpected deltas: %+v", deltas)
	}
}

func TestToChatRequestDTOIncludesResponseFormat(t *testing.T) {
	dto := toChatRequestDTO(domain.ModelRequest{
		Messages: []domain.Message{{Role: domain.RoleUser, Content: "plan"}},
		ResponseFormat: &domain.ResponseFormat{
			Type:   "json_schema",
			Name:   "execution_plan",
			Schema: map[string]any{"type": "object"},
			Strict: true,
		},
	})

	if dto.ResponseFormat == nil {
		t.Fatalf("expected response format")
	}
	if dto.ResponseFormat.Type != "json_schema" {
		t.Fatalf("unexpected response format type: %+v", dto.ResponseFormat)
	}
	if dto.ResponseFormat.JSONSchema == nil || dto.ResponseFormat.JSONSchema.Name != "execution_plan" {
		t.Fatalf("unexpected json schema wrapper: %+v", dto.ResponseFormat.JSONSchema)
	}
	if !dto.ResponseFormat.JSONSchema.Strict {
		t.Fatalf("expected strict json schema")
	}
	if dto.ResponseFormat.JSONSchema.Schema["type"] != "object" {
		t.Fatalf("unexpected schema: %+v", dto.ResponseFormat.JSONSchema.Schema)
	}
}

func TestToChatRequestDTOIncludesToolCallIDForToolMessages(t *testing.T) {
	dto := toChatRequestDTO(domain.ModelRequest{
		Messages: []domain.Message{
			{
				Role:       domain.RoleTool,
				Content:    "tool output",
				ToolCallID: "call-123",
			},
		},
	})

	if len(dto.Messages) != 1 {
		t.Fatalf("unexpected message count: %d", len(dto.Messages))
	}
	if dto.Messages[0].ToolCallID != "call-123" {
		t.Fatalf("expected tool_call_id to be encoded, got %+v", dto.Messages[0])
	}
}

func TestFromMessageDTOReadsToolCallID(t *testing.T) {
	raw := []byte(`{"role":"tool","content":"done","tool_call_id":"call-123"}`)
	var dto messageDTO
	if err := json.Unmarshal(raw, &dto); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}

	msg := fromMessageDTO(dto)
	if msg.ToolCallID != "call-123" {
		t.Fatalf("expected tool call id, got %+v", msg)
	}
}

func TestResponsesOutputItemsAreReplayed(t *testing.T) {
	output := []json.RawMessage{
		json.RawMessage(`{"type":"reasoning","id":"rs_123","summary":[]}`),
		json.RawMessage(`{"type":"function_call","call_id":"call_123","name":"lookup","arguments":"{\"query\":\"q\"}"}`),
	}
	msg := fromResponsesOutput(output)
	if len(msg.ToolCalls) != 1 {
		t.Fatalf("expected tool call, got %+v", msg.ToolCalls)
	}
	if msg.ToolCalls[0].ID != "call_123" || msg.ToolCalls[0].Name != "lookup" {
		t.Fatalf("unexpected tool call: %+v", msg.ToolCalls[0])
	}

	input := toResponsesInput([]domain.Message{
		msg,
		{Role: domain.RoleTool, ToolCallID: "call_123", Content: "result"},
	})
	if len(input) != 3 {
		t.Fatalf("expected raw output items plus tool output, got %d: %s", len(input), input)
	}
	if !bytes.Contains(input[0], []byte(`"type":"reasoning"`)) {
		t.Fatalf("expected reasoning item to be replayed: %s", input[0])
	}
	if !bytes.Contains(input[1], []byte(`"type":"function_call"`)) {
		t.Fatalf("expected function call item to be replayed: %s", input[1])
	}
	if !bytes.Contains(input[2], []byte(`"type":"function_call_output"`)) {
		t.Fatalf("expected function output item: %s", input[2])
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func fakeHTTPClient(handler func(*http.Request) (int, string)) *http.Client {
	return &http.Client{
		Timeout: time.Second,
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			status, body := handler(r)
			return &http.Response{
				StatusCode: status,
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body:       io.NopCloser(strings.NewReader(body)),
				Request:    r,
			}, nil
		}),
	}
}
