package taskcatalog

import (
	"context"
	"path/filepath"
	"testing"

	"yagent/internal/domain"
)

type fakeSessionFactory struct {
	session domain.MCPSession
}

func (f fakeSessionFactory) Open(context.Context, domain.TaskDefinition) (domain.MCPSession, error) {
	return f.session, nil
}

type fakeSession struct {
	tools []domain.MCPToolDescriptor
}

func (f *fakeSession) Initialize(context.Context) error { return nil }
func (f *fakeSession) ListTools(context.Context) ([]domain.MCPToolDescriptor, error) {
	return append([]domain.MCPToolDescriptor(nil), f.tools...), nil
}
func (f *fakeSession) CallTool(context.Context, string, map[string]any) (string, error) {
	return "ok", nil
}
func (f *fakeSession) Close() error { return nil }

func TestMCPBindingsApplyFiltersAndQualifyNames(t *testing.T) {
	bindings := NewMCPBindings(fakeSessionFactory{session: &fakeSession{
		tools: []domain.MCPToolDescriptor{
			{Name: "allowed.tool:v1", InputSchema: map[string]any{"type": "object"}, ParallelSafe: true},
			{Name: "ignored", InputSchema: map[string]any{"type": "object"}, ParallelSafe: true},
		},
	}})
	task := domain.TaskDefinition{
		ID:   "docs:v1",
		Kind: domain.TaskSpecKindMCPServer,
		MCPServer: &domain.MCPServerSpec{
			ToolPrefix:   "docs.api/v1",
			ParallelSafe: true,
			IncludeTools: []string{"allowed.tool:v1"},
		},
	}

	tools, err := bindings.Bind(context.Background(), task)
	if err != nil {
		t.Fatalf("Bind returned error: %v", err)
	}
	if len(tools) != 1 || tools[0].Name != "allowed.tool:v1" {
		t.Fatalf("unexpected filtered tools: %+v", tools)
	}

	bound := bindings.BoundTools()
	if len(bound) != 1 || bound[0].QualifiedName == "" {
		t.Fatalf("unexpected bound tools: %+v", bound)
	}
	if bound[0].QualifiedName != "mcp__docs_api_v1__allowed_tool_v1__docs_v1" {
		t.Fatalf("unexpected qualified name: %s", bound[0].QualifiedName)
	}
}

func TestMCPBindingsStoreRuntimeRoots(t *testing.T) {
	root := t.TempDir()
	bindings := NewMCPBindings(fakeSessionFactory{session: &fakeSession{
		tools: []domain.MCPToolDescriptor{{Name: "search_docs", InputSchema: map[string]any{"type": "object"}}},
	}})
	task := domain.TaskDefinition{
		ID:   "docs",
		Kind: domain.TaskSpecKindMCPServer,
		MCPServer: &domain.MCPServerSpec{
			Cwd:   root,
			Roots: []string{filepath.Join(root, "docs"), filepath.Join(root, "docs")},
		},
	}

	if _, err := bindings.Bind(context.Background(), task); err != nil {
		t.Fatalf("Bind returned error: %v", err)
	}
	bound := bindings.BoundTools()
	if len(bound) != 1 {
		t.Fatalf("unexpected bound tools: %+v", bound)
	}
	if len(bound[0].Roots) != 1 || bound[0].Roots[0] != filepath.Join(root, "docs") {
		t.Fatalf("expected compact runtime roots, got %+v", bound[0].Roots)
	}

	fallbackBindings := NewMCPBindings(fakeSessionFactory{session: &fakeSession{
		tools: []domain.MCPToolDescriptor{{Name: "read_docs", InputSchema: map[string]any{"type": "object"}}},
	}})
	fallbackTask := domain.TaskDefinition{
		ID:   "fallback",
		Kind: domain.TaskSpecKindMCPServer,
		MCPServer: &domain.MCPServerSpec{
			Cwd: root,
		},
	}
	if _, err := fallbackBindings.Bind(context.Background(), fallbackTask); err != nil {
		t.Fatalf("fallback Bind returned error: %v", err)
	}
	fallbackBound := fallbackBindings.BoundTools()
	if len(fallbackBound) != 1 || len(fallbackBound[0].Roots) != 1 || fallbackBound[0].Roots[0] != root {
		t.Fatalf("expected cwd fallback root, got %+v", fallbackBound)
	}
}

func TestMCPBindingsDoNotTrustAnnotationsByDefault(t *testing.T) {
	bindings := NewMCPBindings(fakeSessionFactory{session: &fakeSession{
		tools: []domain.MCPToolDescriptor{{
			Name:         "search_docs",
			InputSchema:  map[string]any{"type": "object"},
			ReadOnly:     true,
			ParallelSafe: true,
		}},
	}})
	task := domain.TaskDefinition{
		ID:   "docs",
		Kind: domain.TaskSpecKindMCPServer,
		MCPServer: &domain.MCPServerSpec{
			Trust:        "untrusted",
			ToolPrefix:   "docs",
			ParallelSafe: true,
		},
	}

	if _, err := bindings.Bind(context.Background(), task); err != nil {
		t.Fatalf("Bind returned error: %v", err)
	}
	bound := bindings.BoundTools()
	if len(bound) != 1 {
		t.Fatalf("unexpected bound tools: %+v", bound)
	}
	if bound[0].ReadOnly {
		t.Fatalf("untrusted annotations must not mark tools read-only: %+v", bound[0])
	}
	if bound[0].ParallelSafe {
		t.Fatalf("untrusted annotations must not mark tools parallel-safe: %+v", bound[0])
	}
	if bound[0].Risk != "high" || bound[0].TrustBoundary != "untrusted" {
		t.Fatalf("unexpected safety metadata: %+v", bound[0])
	}
}

func TestMCPBindingsUseTrustedAnnotations(t *testing.T) {
	bindings := NewMCPBindings(fakeSessionFactory{session: &fakeSession{
		tools: []domain.MCPToolDescriptor{{
			Name:         "search_docs",
			InputSchema:  map[string]any{"type": "object"},
			ReadOnly:     true,
			ParallelSafe: true,
		}},
	}})
	task := domain.TaskDefinition{
		ID:   "docs",
		Kind: domain.TaskSpecKindMCPServer,
		MCPServer: &domain.MCPServerSpec{
			Trust:                "trusted",
			TrustToolAnnotations: true,
			ToolPrefix:           "docs",
			Risk:                 "low",
			ParallelSafe:         true,
		},
	}

	if _, err := bindings.Bind(context.Background(), task); err != nil {
		t.Fatalf("Bind returned error: %v", err)
	}
	bound := bindings.BoundTools()
	if len(bound) != 1 {
		t.Fatalf("unexpected bound tools: %+v", bound)
	}
	if !bound[0].ReadOnly || !bound[0].ParallelSafe {
		t.Fatalf("trusted annotations should mark tool safe: %+v", bound[0])
	}
	if bound[0].Risk != "low" || bound[0].SafetySource != "trusted_mcp_annotations" {
		t.Fatalf("unexpected safety metadata: %+v", bound[0])
	}
}

func TestMCPBindingsUseExplicitSafetyLists(t *testing.T) {
	bindings := NewMCPBindings(fakeSessionFactory{session: &fakeSession{
		tools: []domain.MCPToolDescriptor{{
			Name:        "search_docs",
			InputSchema: map[string]any{"type": "object"},
		}},
	}})
	task := domain.TaskDefinition{
		ID:   "docs",
		Kind: domain.TaskSpecKindMCPServer,
		MCPServer: &domain.MCPServerSpec{
			Trust:             "untrusted",
			ToolPrefix:        "docs",
			Risk:              "medium",
			ReadOnlyTools:     []string{"search_*"},
			ParallelSafeTools: []string{"search_docs"},
		},
	}

	if _, err := bindings.Bind(context.Background(), task); err != nil {
		t.Fatalf("Bind returned error: %v", err)
	}
	bound := bindings.BoundTools()
	if len(bound) != 1 {
		t.Fatalf("unexpected bound tools: %+v", bound)
	}
	if !bound[0].ReadOnly || !bound[0].ParallelSafe {
		t.Fatalf("explicit safety lists should be applied: %+v", bound[0])
	}
	if bound[0].Risk != "medium" || bound[0].SafetySource != "task_read_only_tools" {
		t.Fatalf("unexpected safety metadata: %+v", bound[0])
	}
}

func TestMCPBindingsPreferExplicitMutatingToolsOverReadOnlyTools(t *testing.T) {
	bindings := NewMCPBindings(fakeSessionFactory{session: &fakeSession{
		tools: []domain.MCPToolDescriptor{{
			Name:        "write_docs",
			InputSchema: map[string]any{"type": "object"},
		}},
	}})
	task := domain.TaskDefinition{
		ID:   "docs",
		Kind: domain.TaskSpecKindMCPServer,
		MCPServer: &domain.MCPServerSpec{
			Trust:         "untrusted",
			ToolPrefix:    "docs",
			Risk:          "medium",
			ReadOnlyTools: []string{"write_*"},
			MutatingTools: []string{"write_docs"},
		},
	}

	if _, err := bindings.Bind(context.Background(), task); err != nil {
		t.Fatalf("Bind returned error: %v", err)
	}
	bound := bindings.BoundTools()
	if len(bound) != 1 {
		t.Fatalf("unexpected bound tools: %+v", bound)
	}
	if bound[0].ReadOnly || bound[0].Risk != "high" || bound[0].SafetySource != "task_mutating_tools" {
		t.Fatalf("mutating tools should override read-only patterns: %+v", bound[0])
	}
}
