package fs

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"yagent/internal/domain"
	"yagent/internal/infra/policy"
)

type stubApprover struct {
	decision domain.PermissionDecision
	last     domain.PermissionRequest
}

func (s *stubApprover) Approve(_ context.Context, request domain.PermissionRequest) (domain.PermissionDecision, error) {
	s.last = request
	return s.decision, nil
}

func TestReadToolRequiresPermissionAndReadsText(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.txt")
	if err := os.WriteFile(path, []byte("hello world"), 0o644); err != nil {
		t.Fatal(err)
	}

	approver := &stubApprover{decision: domain.PermissionAllowOnce}
	tool := NewReadTool(policy.NewPathPolicy(dir, []string{dir}), policy.NewEngine(), approver)
	result := tool.Execute(context.Background(), domain.ToolCall{
		ID:   "1",
		Name: "fs_read",
		Arguments: map[string]any{
			"path": path,
		},
	})
	if !result.Success {
		t.Fatalf("expected success, got %s", result.Output)
	}
	if !strings.Contains(result.Output, "hello world") {
		t.Fatalf("unexpected output: %s", result.Output)
	}
	if approver.last.Scope != path || approver.last.Action != "read" {
		t.Fatalf("unexpected permission request: %+v", approver.last)
	}
}

func TestReadToolRejectsSymlinkEscape(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	target := filepath.Join(outside, "secret.txt")
	if err := os.WriteFile(target, []byte("secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link.txt")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}

	tool := NewReadTool(policy.NewPathPolicy(root, []string{root}), policy.NewEngine(), &stubApprover{decision: domain.PermissionAllowOnce})
	result := tool.Execute(context.Background(), domain.ToolCall{
		ID:   "1",
		Name: "fs_read",
		Arguments: map[string]any{
			"path": link,
		},
	})
	if result.Success {
		t.Fatalf("expected failure for symlink escape")
	}
}

func TestRemoveToolRejectsRootDeletion(t *testing.T) {
	root := t.TempDir()
	tool := NewRemoveTool(policy.NewPathPolicy(root, []string{root}), policy.NewEngine(), &stubApprover{decision: domain.PermissionAllowOnce})
	result := tool.Execute(context.Background(), domain.ToolCall{
		ID:   "1",
		Name: "fs_remove",
		Arguments: map[string]any{
			"path":      root,
			"recursive": true,
		},
	})
	if result.Success {
		t.Fatalf("expected root deletion to fail")
	}
}
