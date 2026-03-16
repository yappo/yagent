package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"yagent/internal/domain"
)

func TestGenerate(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"hello"}}]}`))
	}))
	defer server.Close()

	client := NewClient(server.URL, "", time.Minute)
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

func TestNewClientUsesDefaultTimeout(t *testing.T) {
	client := NewClient("http://localhost:1234", "", 0)
	if client.httpClient.Timeout != 20*time.Minute {
		t.Fatalf("unexpected default timeout: %s", client.httpClient.Timeout)
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
