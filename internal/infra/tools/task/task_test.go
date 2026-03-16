package task

import (
	"context"
	"strings"
	"testing"

	"yagent/internal/domain"
)

type fakeCatalog struct {
	items map[string]domain.TaskDefinition
}

func (f fakeCatalog) List(context.Context) []domain.TaskDefinition {
	out := make([]domain.TaskDefinition, 0, len(f.items))
	for _, item := range f.items {
		out = append(out, item)
	}
	return out
}

func (f fakeCatalog) Get(_ context.Context, id string) (domain.TaskDefinition, bool) {
	item, ok := f.items[id]
	return item, ok
}

type fakeApprover struct {
	last domain.PermissionRequest
}

type fakeBindings struct {
	tools []domain.BoundMCPTool
	bind  func(context.Context, domain.TaskDefinition) ([]domain.MCPToolDescriptor, error)
}

func (f *fakeApprover) Approve(_ context.Context, request domain.PermissionRequest) (domain.PermissionDecision, error) {
	f.last = request
	return domain.PermissionAllowOnce, nil
}

func (f *fakeBindings) Bind(ctx context.Context, task domain.TaskDefinition) ([]domain.MCPToolDescriptor, error) {
	if f.bind != nil {
		return f.bind(ctx, task)
	}
	return nil, nil
}

func (f *fakeBindings) BoundTools() []domain.BoundMCPTool {
	return append([]domain.BoundMCPTool(nil), f.tools...)
}

func (f *fakeBindings) CallTool(context.Context, string, string, map[string]any) (string, error) {
	return "", nil
}

func TestRunToolRejectsUnknownTask(t *testing.T) {
	tool := NewRunTool(fakeCatalog{items: map[string]domain.TaskDefinition{}}, nil, nil)
	result := tool.Execute(context.Background(), domain.ToolCall{
		ID:        "1",
		Name:      "task_run",
		Arguments: map[string]any{"task_id": "missing"},
	})
	if result.Success {
		t.Fatalf("expected failure")
	}
}

func TestRunToolAddsNetworkSideEffect(t *testing.T) {
	approver := &fakeApprover{}
	tool := NewRunTool(fakeCatalog{items: map[string]domain.TaskDefinition{
		"go:test": {
			ID:          "go:test",
			Description: "Go version",
			Kind:        domain.TaskKindCommand,
			Command: &domain.CommandTaskSpec{
				Command:      "go",
				Args:         []string{"version"},
				Cwd:          ".",
				Risk:         "high",
				AllowNetwork: true,
				Timeout:      60,
			},
		},
	}}, nilPolicyEngine{}, approver)
	result := tool.Execute(context.Background(), domain.ToolCall{
		ID:        "1",
		Name:      "task_run",
		Arguments: map[string]any{"task_id": "go:test"},
	})
	if !result.Success {
		t.Fatalf("expected success, got %s", result.Output)
	}
	if !strings.Contains(strings.Join(approver.last.SideEffects, ","), "network_access") {
		t.Fatalf("expected network side effect, got %+v", approver.last)
	}
}

func TestBindToolRejectsCommandTask(t *testing.T) {
	tool := NewBindTool(fakeCatalog{items: map[string]domain.TaskDefinition{
		"go:test": {
			ID:          "go:test",
			Description: "Go test",
			Kind:        domain.TaskKindCommand,
			Command:     &domain.CommandTaskSpec{Command: "go"},
		},
	}}, &fakeBindings{}, nil, nil)
	result := tool.Execute(context.Background(), domain.ToolCall{
		ID:        "1",
		Name:      "task_bind",
		Arguments: map[string]any{"task_id": "go:test"},
	})
	if result.Success {
		t.Fatalf("expected failure")
	}
}

func TestListToolIncludesMCPBindingState(t *testing.T) {
	tool := NewListTool(fakeCatalog{items: map[string]domain.TaskDefinition{
		"docs": {
			ID:          "docs",
			Description: "Docs MCP",
			Kind:        domain.TaskKindMCPServer,
			MCPServer: &domain.MCPServerSpec{
				Command: "npx",
			},
		},
	}}, &fakeBindings{
		tools: []domain.BoundMCPTool{{TaskID: "docs", QualifiedName: "mcp__docs"}},
	})
	result := tool.Execute(context.Background(), domain.ToolCall{ID: "1", Name: "task_list"})
	if !result.Success {
		t.Fatalf("expected success")
	}
	if !strings.Contains(result.Output, "\"bind_required\": true") || !strings.Contains(result.Output, "\"bound\": true") {
		t.Fatalf("expected bind metadata, got %s", result.Output)
	}
}

type nilPolicyEngine struct{}

func (nilPolicyEngine) Evaluate(context.Context, domain.ToolCall) (domain.PolicyDecision, domain.PermissionRequest, error) {
	return domain.PolicyRequireApproval, domain.PermissionRequest{Scope: "go:test"}, nil
}
