package llm

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"yagent/internal/domain"
)

func TestComplete(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"hello"}}]}`))
	}))
	defer server.Close()

	client := NewClient(server.URL, "")
	response, err := client.Complete(context.Background(), domain.CompletionRequest{
		Messages: []domain.Message{{Role: domain.RoleUser, Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("Complete returned error: %v", err)
	}

	if response.Message.Content != "hello" {
		t.Fatalf("unexpected content: %s", response.Message.Content)
	}
}
