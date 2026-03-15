package registry

import (
	"context"
	"testing"

	"yagent/internal/domain"
)

type stubTool struct {
	definition domain.ToolDefinition
}

func (s stubTool) Definition() domain.ToolDefinition { return s.definition }
func (s stubTool) Execute(context.Context, domain.ToolCall) domain.ToolResult {
	return domain.ToolResult{Success: true, Output: "ok"}
}

func TestRegistryDefinitionsSorted(t *testing.T) {
	r := New(
		stubTool{definition: domain.ToolDefinition{Name: "b"}},
		stubTool{definition: domain.ToolDefinition{Name: "a"}},
	)

	definitions := r.Definitions()
	if len(definitions) != 2 || definitions[0].Name != "a" {
		t.Fatalf("definitions are not sorted")
	}
}
