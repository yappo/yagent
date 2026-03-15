package chat

import (
	"context"
	"fmt"

	"yagent/internal/domain"
)

type CompletionClient interface {
	Complete(context.Context, domain.CompletionRequest) (domain.CompletionResponse, error)
}

type Service struct {
	client        CompletionClient
	tools         domain.ToolRegistry
	maxIterations int
}

func NewService(client CompletionClient, tools domain.ToolRegistry, maxIterations int) *Service {
	if maxIterations <= 0 {
		maxIterations = 20
	}

	return &Service{
		client:        client,
		tools:         tools,
		maxIterations: maxIterations,
	}
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
	messages := append([]domain.Message(nil), input.Messages...)

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
			result := s.tools.Execute(ctx, call)
			messages = append(messages, domain.Message{
				Role:    domain.RoleTool,
				Content: result.Output,
			})
		}
	}

	return Output{}, fmt.Errorf("最大反復回数 (%d) に達しました", s.maxIterations)
}
