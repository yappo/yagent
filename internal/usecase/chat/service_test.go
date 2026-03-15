package chat

import (
	"context"
	"testing"

	"yagent/internal/domain"
)

type fakeClient struct {
	responses []domain.CompletionResponse
	index     int
}

func (f *fakeClient) Complete(context.Context, domain.CompletionRequest) (domain.CompletionResponse, error) {
	response := f.responses[f.index]
	f.index++
	return response, nil
}

type fakeTools struct{}

func (fakeTools) Definitions() []domain.ToolDefinition { return nil }
func (fakeTools) Execute(context.Context, domain.ToolCall) domain.ToolResult {
	return domain.ToolResult{Success: true, Output: "tool output"}
}

type recordingObserver struct {
	events []ToolEvent
}

func (r *recordingObserver) OnToolEvent(_ context.Context, event ToolEvent) {
	r.events = append(r.events, event)
}

func TestServiceRunWithoutTools(t *testing.T) {
	service := NewService(&fakeClient{
		responses: []domain.CompletionResponse{
			{Message: domain.Message{Role: domain.RoleAssistant, Content: "done"}},
		},
	}, fakeTools{}, 5)

	result, err := service.Run(context.Background(), Input{
		Messages: []domain.Message{{Role: domain.RoleUser, Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	if result.Message.Content != "done" {
		t.Fatalf("unexpected content: %s", result.Message.Content)
	}
}

func TestServiceRunWithTools(t *testing.T) {
	observer := &recordingObserver{}
	service := NewService(&fakeClient{
		responses: []domain.CompletionResponse{
			{
				Message: domain.Message{
					Role: domain.RoleAssistant,
					ToolCalls: []domain.ToolCall{
						{ID: "1", Name: "tool", Arguments: map[string]any{}},
					},
				},
			},
			{Message: domain.Message{Role: domain.RoleAssistant, Content: "done"}},
		},
	}, fakeTools{}, 5)
	service.SetObserver(observer)

	result, err := service.Run(context.Background(), Input{
		Messages: []domain.Message{{Role: domain.RoleUser, Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	if result.Message.Content != "done" {
		t.Fatalf("unexpected content: %s", result.Message.Content)
	}
	if len(observer.events) != 2 || observer.events[0].Phase != "start" || observer.events[1].Phase != "finish" {
		t.Fatalf("unexpected observer events: %+v", observer.events)
	}
}
