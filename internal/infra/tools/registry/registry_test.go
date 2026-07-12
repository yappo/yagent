package registry

import (
	"context"
	"strings"
	"testing"

	"yagent/internal/domain"
)

type stubTool struct {
	definition domain.ToolDefinition
}

type rejectingDurableActionGuard struct{}

func (rejectingDurableActionGuard) ValidateDurableAction(context.Context, domain.DurableActionExecutionContext) error {
	return domain.ErrDurableActionNotExecutable
}

type recordingTool struct {
	definition domain.ToolDefinition
	calls      *int
}

func (t recordingTool) Definition() domain.ToolDefinition { return t.definition }
func (t recordingTool) Execute(context.Context, domain.ToolCall) domain.ToolResult {
	*t.calls++
	return domain.ToolResult{Success: true, Output: "unexpected"}
}

func TestRegistryRejectsStaleDurableActionBeforeProviderOrTool(t *testing.T) {
	calls := 0
	r := New(recordingTool{definition: domain.ToolDefinition{Name: "fs_write"}, calls: &calls})
	r.SetDurableActionGuard(rejectingDurableActionGuard{})
	ctx := domain.WithDurableActionExecutionContext(context.Background(), domain.DurableActionExecutionContext{ActionID: "action-1", WorkflowID: "workflow-1", WorkUnitID: "unit-1", IdempotencyKey: "idem-1", LeaseToken: "lease-1", FencingToken: 1})
	result := r.Execute(ctx, domain.AgentSpec{}, domain.ToolCall{ID: "call-1", Name: "fs_write"})
	if result.Success || calls != 0 || !strings.Contains(result.Output, domain.ErrDurableActionNotExecutable.Error()) {
		t.Fatalf("expected stale action rejection before execution, result=%+v calls=%d", result, calls)
	}
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
