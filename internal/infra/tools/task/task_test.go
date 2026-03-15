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

func (f *fakeApprover) Approve(_ context.Context, request domain.PermissionRequest) (domain.PermissionDecision, error) {
	f.last = request
	return domain.PermissionAllowOnce, nil
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
			ID:           "go:test",
			Description:  "Go version",
			Command:      "go",
			Args:         []string{"version"},
			Cwd:          ".",
			Risk:         "high",
			AllowNetwork: true,
			Timeout:      60,
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

type nilPolicyEngine struct{}

func (nilPolicyEngine) Evaluate(context.Context, domain.ToolCall) (domain.PolicyDecision, domain.PermissionRequest, error) {
	return domain.PolicyRequireApproval, domain.PermissionRequest{Scope: "go:test"}, nil
}
