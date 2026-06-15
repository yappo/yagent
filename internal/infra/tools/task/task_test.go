package task

import (
	"context"
	"encoding/json"
	"path/filepath"
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
	tool := NewRunTool(fakeCatalog{items: map[string]domain.TaskDefinition{}}, nil, nil, nil)
	result := tool.Execute(context.Background(), domain.ToolCall{
		ID:        "1",
		Name:      "task_run",
		Arguments: map[string]any{"task_id": "missing"},
	})
	if result.Success {
		t.Fatalf("expected failure")
	}
	var payload taskErrorOutput
	if err := json.Unmarshal([]byte(result.Output), &payload); err != nil {
		t.Fatalf("expected structured error JSON, got %s: %v", result.Output, err)
	}
	if payload.OK || payload.Error.Code != "unknown_task" || payload.Error.TaskID != "missing" || payload.Error.Tool != "task_run" {
		t.Fatalf("unexpected structured error: %+v", payload)
	}
}

func TestRunToolAddsNetworkSideEffect(t *testing.T) {
	approver := &fakeApprover{}
	tool := NewRunTool(fakeCatalog{items: map[string]domain.TaskDefinition{
		"go:test": {
			ID:          "go:test",
			Description: "Go version",
			Kind:        domain.TaskSpecKindCommand,
			Command: &domain.CommandTaskSpec{
				Command:      "go",
				Args:         []string{"version"},
				Cwd:          ".",
				Risk:         "high",
				AllowNetwork: true,
				Timeout:      60,
			},
		},
	}}, nilPolicyEngine{}, approver, nil)
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
			Kind:        domain.TaskSpecKindCommand,
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
	var payload taskErrorOutput
	if err := json.Unmarshal([]byte(result.Output), &payload); err != nil {
		t.Fatalf("expected structured error JSON, got %s: %v", result.Output, err)
	}
	if payload.Error.Code != "invalid_task_kind" || payload.Error.TaskID != "go:test" || payload.Error.Details["kind"] != string(domain.TaskSpecKindCommand) {
		t.Fatalf("unexpected structured error: %+v", payload)
	}
}

func TestListToolIncludesMCPBindingState(t *testing.T) {
	tool := NewListTool(fakeCatalog{items: map[string]domain.TaskDefinition{
		"docs": {
			ID:          "docs",
			Description: "Docs MCP",
			Kind:        domain.TaskSpecKindMCPServer,
			MCPServer: &domain.MCPServerSpec{
				Command: "npx",
				Roots:   []string{"/repo/docs"},
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
	if !strings.Contains(result.Output, "\"bind_hint\":") || !strings.Contains(result.Output, "\"usage_hint\":") || !strings.Contains(result.Output, "\"exposed_tool_prefix\": \"docs\"") {
		t.Fatalf("expected MCP hints, got %s", result.Output)
	}
	if !strings.Contains(result.Output, "\"exposed_tools\": [") || !strings.Contains(result.Output, "\"mcp__docs\"") {
		t.Fatalf("expected bind metadata, got %s", result.Output)
	}
	if !strings.Contains(result.Output, "\"roots\": [") || !strings.Contains(result.Output, "\"/repo/docs\"") {
		t.Fatalf("expected MCP roots, got %s", result.Output)
	}
}

func TestBindToolReturnsExposedToolNamesAndNextActionHint(t *testing.T) {
	var bindings *fakeBindings
	bindings = &fakeBindings{
		bind: func(_ context.Context, task domain.TaskDefinition) ([]domain.MCPToolDescriptor, error) {
			bindings.tools = []domain.BoundMCPTool{{
				TaskID:        task.ID,
				QualifiedName: "mcp__docs__search_docs__docs",
			}}
			return []domain.MCPToolDescriptor{{
				Name: "search_docs",
			}}, nil
		},
	}
	tool := NewBindTool(fakeCatalog{items: map[string]domain.TaskDefinition{
		"docs": {
			ID:          "docs",
			Description: "Docs MCP",
			Kind:        domain.TaskSpecKindMCPServer,
			MCPServer: &domain.MCPServerSpec{
				ToolPrefix: "docs",
			},
		},
	}}, bindings, nil, nil)

	result := tool.Execute(context.Background(), domain.ToolCall{
		ID:        "1",
		Name:      "task_bind",
		Arguments: map[string]any{"task_id": "docs"},
	})
	if !result.Success {
		t.Fatalf("expected success, got %s", result.Output)
	}
	for _, fragment := range []string{
		"\"tool_names\": [",
		"\"mcp__docs__search_docs__docs\"",
		"\"server_tool_names\": [",
		"\"search_docs\"",
		"\"next_action_hint\":",
		"\"Use one of tool_names directly in your next tool call.\"",
	} {
		if !strings.Contains(result.Output, fragment) {
			t.Fatalf("expected bind output to contain %q, got %s", fragment, result.Output)
		}
	}
}

func TestRunToolInferRuntimeUsesConfiguredWorkspaceAccess(t *testing.T) {
	root := t.TempDir()
	tool := NewRunTool(fakeCatalog{items: map[string]domain.TaskDefinition{
		"generic": {
			ID:          "generic",
			Description: "Generic task",
			Kind:        domain.TaskSpecKindCommand,
			Command: &domain.CommandTaskSpec{
				Command: "make",
				Args:    []string{"all"},
				Cwd:     root,
			},
		},
		"scoped": {
			ID:          "scoped",
			Description: "Scoped task",
			Kind:        domain.TaskSpecKindCommand,
			Command: &domain.CommandTaskSpec{
				Command:    "custom-runner",
				Args:       []string{"--check"},
				Cwd:        root,
				ReadPaths:  []string{filepath.Join(root, "internal")},
				WritePaths: []string{filepath.Join(root, "internal")},
			},
		},
	}}, nil, nil, nil)

	inspector, ok := tool.(domain.ToolRuntimeInspector)
	if !ok {
		t.Fatalf("run tool should expose runtime inference")
	}

	generic, ok := inspector.InferRuntime(context.Background(), domain.AgentSpec{}, domain.ToolCall{
		Name:      "task_run",
		Arguments: map[string]any{"task_id": "generic"},
	}, domain.ToolDefinition{})
	if !ok {
		t.Fatalf("expected runtime hint for generic task")
	}
	if len(generic.ReadSet) != 0 {
		t.Fatalf("expected no implicit read set, got %+v", generic)
	}
	if len(generic.WriteSet) != 1 || generic.WriteSet[0] != root {
		t.Fatalf("expected conservative cwd write set, got %+v", generic)
	}

	scoped, ok := inspector.InferRuntime(context.Background(), domain.AgentSpec{}, domain.ToolCall{
		Name:      "task_run",
		Arguments: map[string]any{"task_id": "scoped"},
	}, domain.ToolDefinition{})
	if !ok {
		t.Fatalf("expected runtime hint for scoped task")
	}
	if len(scoped.ReadSet) != 1 || scoped.ReadSet[0] != filepath.Join(root, "internal") {
		t.Fatalf("expected explicit read set, got %+v", scoped)
	}
	if len(scoped.WriteSet) != 1 || scoped.WriteSet[0] != filepath.Join(root, "internal") {
		t.Fatalf("expected explicit write set, got %+v", scoped)
	}
}

type nilPolicyEngine struct{}

func (nilPolicyEngine) Evaluate(context.Context, domain.ToolCall) (domain.PolicyDecision, domain.PermissionRequest, error) {
	return domain.PolicyRequireApproval, domain.PermissionRequest{Scope: "go:test"}, nil
}
