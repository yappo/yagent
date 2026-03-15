package chat

import (
	"context"
	"fmt"
	"strings"

	"yagent/internal/domain"
)

type CompletionClient interface {
	Complete(context.Context, domain.CompletionRequest) (domain.CompletionResponse, error)
}

type Service struct {
	client        CompletionClient
	tools         domain.ToolRegistry
	maxIterations int
	observer      ToolObserver
}

type ToolObserver interface {
	OnToolEvent(context.Context, ToolEvent)
}

type ToolEvent struct {
	Phase  string
	Call   domain.ToolCall
	Result domain.ToolResult
}

func NewService(client CompletionClient, tools domain.ToolRegistry, maxIterations int) *Service {
	if maxIterations <= 0 {
		maxIterations = 100
	}

	return &Service{
		client:        client,
		tools:         tools,
		maxIterations: maxIterations,
	}
}

func (s *Service) SetObserver(observer ToolObserver) {
	s.observer = observer
}

type Input struct {
	Messages []domain.Message
	Model    string
	Stream   bool
}

type Output struct {
	Message domain.Message
}

func (s *Service) Run(ctx context.Context, input Input) (Output, error) {
	messages := append([]domain.Message{{
		Role:    domain.RoleSystem,
		Content: toolSystemPrompt(s.tools.Definitions()),
	}}, input.Messages...)

	for iteration := 0; iteration < s.maxIterations; iteration++ {
		response, err := s.client.Complete(ctx, domain.CompletionRequest{
			Messages: messages,
			Model:    input.Model,
			Stream:   input.Stream,
			Tools:    s.tools.Definitions(),
		})
		if err != nil {
			return Output{}, err
		}

		if len(response.Message.ToolCalls) == 0 {
			return Output{Message: response.Message}, nil
		}

		for _, call := range response.Message.ToolCalls {
			s.notifyToolEvent(ctx, ToolEvent{Phase: "start", Call: call})
			result := s.tools.Execute(ctx, call)
			s.notifyToolEvent(ctx, ToolEvent{Phase: "finish", Call: call, Result: result})
			messages = append(messages, domain.Message{
				Role:    domain.RoleTool,
				Content: result.Output,
			})
		}
	}

	return Output{}, fmt.Errorf("最大反復回数 (%d) に達しました", s.maxIterations)
}

func (s *Service) notifyToolEvent(ctx context.Context, event ToolEvent) {
	if s.observer != nil {
		s.observer.OnToolEvent(ctx, event)
	}
}

func toolSystemPrompt(definitions []domain.ToolDefinition) string {
	names := make([]string, 0, len(definitions))
	hasTaskList := false
	hasTaskRun := false
	for _, definition := range definitions {
		names = append(names, definition.Name)
		switch definition.Name {
		case "task_list":
			hasTaskList = true
		case "task_run":
			hasTaskRun = true
		}
	}

	prompt := "Use only the provided tools. Never invent tool names or parameters."
	if hasTaskList && hasTaskRun {
		prompt += " Before running any task, call task_list and choose a task_id from that list only."
	}
	if len(names) > 0 {
		prompt += " Available tools: " + strings.Join(names, ", ") + "."
	}
	return prompt
}
