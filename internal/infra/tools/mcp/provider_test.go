package mcp

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"yagent/internal/domain"
	"yagent/internal/infra/policy"
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

type stubApprover struct {
	last domain.PermissionRequest
}

func (s *stubApprover) Approve(_ context.Context, request domain.PermissionRequest) (domain.PermissionDecision, error) {
	s.last = request
	return domain.PermissionAllowOnce, nil
}

func TestProviderDefinitionsExposeMCPTrustMetadata(t *testing.T) {
	provider := NewProvider(stubBindings{tools: []domain.BoundMCPTool{{
		TaskID:         "docs",
		ServerToolName: "search_docs",
		QualifiedName:  "mcp__docs__search_docs__docs",
		ReadOnly:       true,
		ParallelSafe:   true,
		Risk:           "low",
		TrustBoundary:  "trusted",
		SafetySource:   "trusted_mcp_annotations",
	}}}, nil, nil)

	defs := provider.Definitions(domain.AgentSpec{})
	if len(defs) != 1 {
		t.Fatalf("expected one definition, got %+v", defs)
	}
	if !defs[0].ReadOnly || !defs[0].ParallelSafe || defs[0].Risk != "low" {
		t.Fatalf("unexpected definition safety metadata: %+v", defs[0])
	}
	if defs[0].Metadata["trust_boundary"] != "trusted" || defs[0].Metadata["safety_source"] != "trusted_mcp_annotations" {
		t.Fatalf("expected trust metadata, got %+v", defs[0].Metadata)
	}
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

func TestProviderInferRuntimeResolvesRelativeMCPPathsFromRoots(t *testing.T) {
	root := t.TempDir()
	provider := NewProvider(stubBindings{tools: []domain.BoundMCPTool{{
		TaskID:         "docs",
		ServerToolName: "write_doc",
		QualifiedName:  "mcp__docs__write_doc__docs",
		ReadOnly:       false,
		Roots:          []string{root},
	}}}, nil, nil)

	hint, ok := provider.InferRuntime(context.Background(), domain.AgentSpec{}, domain.ToolCall{
		Name: "mcp__docs__write_doc__docs",
		Arguments: map[string]any{
			"source_path": "README.md",
			"target_path": "docs/README.md",
		},
	}, domain.ToolDefinition{})
	if !ok {
		t.Fatalf("expected runtime hint")
	}
	if len(hint.ReadSet) != 1 || hint.ReadSet[0] != filepath.Join(root, "README.md") {
		t.Fatalf("expected root-relative read set, got %+v", hint)
	}
	if !containsString(hint.WriteSet, filepath.Join(root, "docs", "README.md")) {
		t.Fatalf("expected root-relative write set, got %+v", hint.WriteSet)
	}
}

func TestProviderInferRuntimeFallsBackToRootsWhenMCPPathArgsAreAbsent(t *testing.T) {
	root := t.TempDir()
	readProvider := NewProvider(stubBindings{tools: []domain.BoundMCPTool{{
		TaskID:         "docs",
		ServerToolName: "search_docs",
		QualifiedName:  "mcp__docs__search_docs__docs",
		ReadOnly:       true,
		Roots:          []string{root},
	}}}, nil, nil)

	readHint, ok := readProvider.InferRuntime(context.Background(), domain.AgentSpec{}, domain.ToolCall{
		Name:      "mcp__docs__search_docs__docs",
		Arguments: map[string]any{"query": "planner"},
	}, domain.ToolDefinition{})
	if !ok {
		t.Fatalf("expected readonly runtime hint")
	}
	if len(readHint.ReadSet) != 1 || readHint.ReadSet[0] != root {
		t.Fatalf("expected root fallback read set, got %+v", readHint)
	}
	if len(readHint.WriteSet) != 0 {
		t.Fatalf("expected no readonly writes, got %+v", readHint)
	}

	writeProvider := NewProvider(stubBindings{tools: []domain.BoundMCPTool{{
		TaskID:         "docs",
		ServerToolName: "reindex",
		QualifiedName:  "mcp__docs__reindex__docs",
		ReadOnly:       false,
		Roots:          []string{root},
	}}}, nil, nil)
	writeHint, ok := writeProvider.InferRuntime(context.Background(), domain.AgentSpec{}, domain.ToolCall{
		Name:      "mcp__docs__reindex__docs",
		Arguments: map[string]any{"mode": "full"},
	}, domain.ToolDefinition{})
	if !ok {
		t.Fatalf("expected mutating runtime hint")
	}
	if !containsString(writeHint.WriteSet, root) || !containsString(writeHint.WriteSet, "mcp/docs/state") {
		t.Fatalf("expected root and state fallback write set, got %+v", writeHint.WriteSet)
	}
}

func TestProviderPolicyCanMatchBoundMCPResource(t *testing.T) {
	provider := NewProvider(stubBindings{tools: []domain.BoundMCPTool{{
		TaskID:         "docs",
		ServerToolName: "write_doc",
		QualifiedName:  "mcp__docs__write_doc__docs",
		ReadOnly:       false,
		Risk:           "high",
	}}}, policy.NewEngine(policy.Rule{
		Decision:  domain.PolicyDeny,
		Resources: []string{"docs:write_doc"},
	}), &stubApprover{})

	result, ok := provider.Execute(context.Background(), domain.AgentSpec{}, domain.ToolCall{
		ID:        "call-1",
		Name:      "mcp__docs__write_doc__docs",
		Arguments: map[string]any{"target_path": "/repo/docs.md"},
	})
	if !ok {
		t.Fatalf("expected MCP provider to handle call")
	}
	if result.Success || !strings.Contains(result.Output, "policy") {
		t.Fatalf("expected policy denial, got %+v", result)
	}
}

func TestProviderApprovalSeesMCPNetworkMetadata(t *testing.T) {
	approver := &stubApprover{}
	provider := NewProvider(stubBindings{tools: []domain.BoundMCPTool{{
		TaskID:         "docs",
		ServerToolName: "fetch_doc",
		QualifiedName:  "mcp__docs__fetch_doc__docs",
		ReadOnly:       true,
		Risk:           "medium",
		AllowNetwork:   true,
	}}}, policy.NewEngine(), approver)

	result, ok := provider.Execute(context.Background(), domain.AgentSpec{}, domain.ToolCall{
		ID:        "call-1",
		Name:      "mcp__docs__fetch_doc__docs",
		Arguments: map[string]any{"query": "x"},
	})
	if !ok || !result.Success {
		t.Fatalf("expected successful call, got ok=%v result=%+v", ok, result)
	}
	if approver.last.Scope != "docs:fetch_doc" {
		t.Fatalf("expected task-scoped MCP approval, got %+v", approver.last)
	}
	if !strings.Contains(strings.Join(approver.last.SideEffects, ","), "network_access") {
		t.Fatalf("expected network side effect, got %+v", approver.last)
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

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
