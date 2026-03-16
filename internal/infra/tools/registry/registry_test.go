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

	definitions := r.Definitions(domain.AgentSpec{})
	if len(definitions) != 2 || definitions[0].Name != "a" {
		t.Fatalf("definitions are not sorted")
	}
}

type stubProvider struct {
	defs []domain.ToolDefinition
}

func (s stubProvider) Definitions(domain.AgentSpec) []domain.ToolDefinition { return s.defs }
func (s stubProvider) Execute(context.Context, domain.AgentSpec, domain.ToolCall) (domain.ToolResult, bool) {
	return domain.ToolResult{}, false
}

func TestRegistryIncludesDynamicProviderDefinitions(t *testing.T) {
	r := New(stubTool{definition: domain.ToolDefinition{Name: "z"}})
	r.RegisterProvider(stubProvider{defs: []domain.ToolDefinition{{Name: "a"}}})

	definitions := r.Definitions(domain.AgentSpec{})
	if len(definitions) != 2 || definitions[0].Name != "a" || definitions[1].Name != "z" {
		t.Fatalf("unexpected definitions: %+v", definitions)
	}
}
