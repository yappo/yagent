package taskcatalog

import (
	"context"
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
func (f *fakeSession) CallTool(context.Context, string, map[string]any) (string, error) { return "ok", nil }
func (f *fakeSession) Close() error                                                      { return nil }

func TestMCPBindingsApplyFiltersAndQualifyNames(t *testing.T) {
	bindings := NewMCPBindings(fakeSessionFactory{session: &fakeSession{
		tools: []domain.MCPToolDescriptor{
			{Name: "allowed", InputSchema: map[string]any{"type": "object"}, ParallelSafe: true},
			{Name: "ignored", InputSchema: map[string]any{"type": "object"}, ParallelSafe: true},
		},
	}})
	task := domain.TaskDefinition{
		ID:   "docs",
		Kind: domain.TaskKindMCPServer,
		MCPServer: &domain.MCPServerSpec{
			ToolPrefix:   "docs",
			ParallelSafe: true,
			IncludeTools: []string{"allowed"},
		},
	}

	tools, err := bindings.Bind(context.Background(), task)
	if err != nil {
		t.Fatalf("Bind returned error: %v", err)
	}
	if len(tools) != 1 || tools[0].Name != "allowed" {
		t.Fatalf("unexpected filtered tools: %+v", tools)
	}

	bound := bindings.BoundTools()
	if len(bound) != 1 || bound[0].QualifiedName == "" {
		t.Fatalf("unexpected bound tools: %+v", bound)
	}
}
