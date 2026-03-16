package file

import (
	"context"
	"os"
	"path/filepath"
	"strings"
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

func TestListToolRecursive(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "cmd"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "cmd", "main.go"), []byte("package main"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "cmd", "nested"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "cmd", "nested", "sub.go"), []byte("package nested"), 0o644); err != nil {
		t.Fatal(err)
	}

	validator := NewValidator(dir, []string{dir})
	tool := NewListTool(validator, stubApprover{decision: domain.PermissionAllowOnce})
	result := tool.Execute(context.Background(), domain.ToolCall{
		ID:   "1",
		Name: "directory_list",
		Arguments: map[string]any{
			"directory_path": "cmd",
			"recursive":      true,
		},
	})

	if !result.Success {
		t.Fatalf("expected success, got %s", result.Output)
	}
	if result.Output == "" || !containsAll(result.Output, []string{"main.go", "nested/", "nested/sub.go"}) {
		t.Fatalf("unexpected output: %q", result.Output)
	}
}

func containsAll(output string, values []string) bool {
	for _, value := range values {
		if !strings.Contains(output, value) {
			return false
		}
	}
	return true
}
