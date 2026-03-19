package mcp

import (
	"context"
	"testing"

	"yagent/internal/domain"
)

type stubBindings struct {
	tools []domain.BoundMCPTool
}

func (s stubBindings) Bind(context.Context, domain.TaskDefinition) ([]domain.MCPToolDescriptor, error) {
	return nil, nil
}

func (s stubBindings) BoundTools() []domain.BoundMCPTool {
	return append([]domain.BoundMCPTool(nil), s.tools...)
}

func (s stubBindings) CallTool(context.Context, string, string, map[string]any) (string, error) {
	return "ok", nil
}

func TestProviderInferRuntimeScopesStatefulToolsByTaskAndPath(t *testing.T) {
	provider := NewProvider(stubBindings{tools: []domain.BoundMCPTool{{
		TaskID:         "docs",
		ServerToolName: "write_doc",
		QualifiedName:  "mcp__docs__write_doc__docs",
		ReadOnly:       false,
		ParallelSafe:   false,
	}}}, nil, nil)

	hint, ok := provider.InferRuntime(context.Background(), domain.AgentSpec{}, domain.ToolCall{
		Name: "mcp__docs__write_doc__docs",
		Arguments: map[string]any{
			"source_path": "/repo/README.md",
			"target_path": "/repo/docs/README.md",
		},
	}, domain.ToolDefinition{})
	if !ok {
		t.Fatalf("expected runtime hint")
	}
	if len(hint.ReadSet) != 1 || hint.ReadSet[0] != "/repo/README.md" {
		t.Fatalf("expected source path read set, got %+v", hint)
	}
	if len(hint.WriteSet) != 2 {
		t.Fatalf("expected target path and task scope write set, got %+v", hint)
	}
	if hint.WriteSet[0] != "/repo/docs/README.md" && hint.WriteSet[1] != "/repo/docs/README.md" {
		t.Fatalf("expected target path in write set, got %+v", hint.WriteSet)
	}
	if hint.Source != "mcp:docs" {
		t.Fatalf("expected task-scoped source, got %+v", hint)
	}
}

func TestProviderInferRuntimeKeepsReadonlyMCPCallsReadable(t *testing.T) {
	provider := NewProvider(stubBindings{tools: []domain.BoundMCPTool{{
		TaskID:         "docs",
		ServerToolName: "read_doc",
		QualifiedName:  "mcp__docs__read_doc__docs",
		ReadOnly:       true,
		ParallelSafe:   true,
	}}}, nil, nil)

	hint, ok := provider.InferRuntime(context.Background(), domain.AgentSpec{}, domain.ToolCall{
		Name:      "mcp__docs__read_doc__docs",
		Arguments: map[string]any{"path": "/repo/README.md"},
	}, domain.ToolDefinition{})
	if !ok {
		t.Fatalf("expected runtime hint")
	}
	if len(hint.ReadSet) != 1 || hint.ReadSet[0] != "/repo/README.md" {
		t.Fatalf("expected read path, got %+v", hint)
	}
	if len(hint.WriteSet) != 0 {
		t.Fatalf("expected readonly call to avoid writes, got %+v", hint)
	}
}
