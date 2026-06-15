package patch

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

func TestPatchToolPermissionIncludesPreview(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.txt")
	if err := os.WriteFile(path, []byte("alpha\nbeta\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	approver := &stubApprover{decision: domain.PermissionAllowOnce}
	tool := New(policy.NewPathPolicy(dir, []string{dir}), policy.NewEngine(), approver)
	result := tool.Execute(context.Background(), domain.ToolCall{
		ID:   "1",
		Name: "patch_apply",
		Arguments: map[string]any{
			"operations": []any{
				map[string]any{
					"path":     path,
					"old_text": "beta",
					"new_text": "gamma",
				},
			},
		},
	})
	if !result.Success {
		t.Fatalf("expected success, got %s", result.Output)
	}
	if approver.last.PreviewKind != "patch" {
		t.Fatalf("expected patch preview kind, got %+v", approver.last)
	}
	if !strings.Contains(approver.last.Preview, "- beta") || !strings.Contains(approver.last.Preview, "+ gamma") {
		t.Fatalf("expected replacement preview, got %q", approver.last.Preview)
	}
	if approver.last.ChangeFiles != 1 || approver.last.Additions != 1 || approver.last.Deletions != 1 {
		t.Fatalf("expected change stats files=1 +1 -1, got %+v", approver.last)
	}
}

func TestPatchToolDenyLeavesFileUnchanged(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.txt")
	if err := os.WriteFile(path, []byte("alpha\nbeta\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := New(policy.NewPathPolicy(dir, []string{dir}), policy.NewEngine(), &stubApprover{decision: domain.PermissionDeny})
	result := tool.Execute(context.Background(), domain.ToolCall{
		ID:   "1",
		Name: "patch_apply",
		Arguments: map[string]any{
			"operations": []any{
				map[string]any{
					"path":     path,
					"old_text": "beta",
					"new_text": "gamma",
				},
			},
		},
	})
	if result.Success {
		t.Fatalf("expected denied patch to fail")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "alpha\nbeta\n" {
		t.Fatalf("expected unchanged file, got %q", string(data))
	}
}

func TestPatchToolAppliesMultipleOperationsInOrder(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.txt")
	if err := os.WriteFile(path, []byte("alpha\nbeta\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	approver := &stubApprover{decision: domain.PermissionAllowOnce}
	tool := New(policy.NewPathPolicy(dir, []string{dir}), policy.NewEngine(), approver)
	result := tool.Execute(context.Background(), domain.ToolCall{
		ID:   "1",
		Name: "patch_apply",
		Arguments: map[string]any{
			"operations": []any{
				map[string]any{
					"path":     path,
					"old_text": "alpha",
					"new_text": "one",
				},
				map[string]any{
					"path":     path,
					"old_text": "beta",
					"new_text": "two",
				},
			},
		},
	})
	if !result.Success {
		t.Fatalf("expected success, got %s", result.Output)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "one\ntwo\n" {
		t.Fatalf("expected both replacements, got %q", string(data))
	}
	if approver.last.ChangeFiles != 1 || approver.last.Additions != 2 || approver.last.Deletions != 2 {
		t.Fatalf("expected unique file stats files=1 +2 -2, got %+v", approver.last)
	}
}
