package file

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"yagent/internal/domain"
)

type stubApprover struct {
	decision domain.PermissionDecision
}

func (s stubApprover) Approve(context.Context, domain.PermissionRequest) (domain.PermissionDecision, error) {
	return s.decision, nil
}

func TestReadToolExecute(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.txt")
	if err := os.WriteFile(path, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}

	validator := NewValidator(dir, []string{dir})
	tool := NewReadTool(validator, stubApprover{decision: domain.PermissionAllowOnce})
	result := tool.Execute(context.Background(), domain.ToolCall{
		ID:   "1",
		Name: "file_reader",
		Arguments: map[string]any{
			"file_path": path,
		},
	})

	if !result.Success {
		t.Fatalf("expected success, got %s", result.Output)
	}
}

func TestWriteToolDenied(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.txt")
	validator := NewValidator(dir, []string{dir})
	tool := NewWriteTool(validator, stubApprover{decision: domain.PermissionDeny})
	result := tool.Execute(context.Background(), domain.ToolCall{
		ID:   "1",
		Name: "file_writer",
		Arguments: map[string]any{
			"file_path": path,
			"content":   "hello",
		},
	})

	if result.Success {
		t.Fatalf("expected denied result")
	}
}
