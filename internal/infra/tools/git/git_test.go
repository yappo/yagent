package git

import (
	"context"
	"os"
	"os/exec"
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

func TestStatusToolReturnsShortStatus(t *testing.T) {
	repo := newGitRepo(t)
	if err := os.WriteFile(filepath.Join(repo, "scratch.txt"), []byte("scratch\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	approver := &stubApprover{decision: domain.PermissionAllowOnce}
	tool := NewStatusTool(policy.NewPathPolicy(repo, []string{repo}), policy.NewEngine(), approver)
	result := tool.Execute(context.Background(), domain.ToolCall{
		ID:   "1",
		Name: "git_status",
		Arguments: map[string]any{
			"repo_path": repo,
		},
	})
	if !result.Success {
		t.Fatalf("expected success, got %s", result.Output)
	}
	if !strings.Contains(result.Output, "?? scratch.txt") {
		t.Fatalf("expected untracked file in status, got %q", result.Output)
	}
	if approver.last.ToolName != "git_status" || approver.last.Resource != repo {
		t.Fatalf("expected git permission request, got %+v", approver.last)
	}
}

func TestDiffToolAppliesPathFilter(t *testing.T) {
	repo := newGitRepo(t)
	if err := os.WriteFile(filepath.Join(repo, "a.txt"), []byte("tracked\na-change\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "b.txt"), []byte("tracked\nb-change\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := NewDiffTool(policy.NewPathPolicy(repo, []string{repo}), policy.NewEngine(), &stubApprover{decision: domain.PermissionAllowOnce})
	result := tool.Execute(context.Background(), domain.ToolCall{
		ID:   "1",
		Name: "git_diff",
		Arguments: map[string]any{
			"repo_path": repo,
			"path":      "a.txt",
		},
	})
	if !result.Success {
		t.Fatalf("expected success, got %s", result.Output)
	}
	if !strings.Contains(result.Output, "+a-change") || strings.Contains(result.Output, "+b-change") {
		t.Fatalf("expected path-filtered diff, got %q", result.Output)
	}
}

func TestShowToolRequiresTarget(t *testing.T) {
	repo := newGitRepo(t)
	tool := NewShowTool(policy.NewPathPolicy(repo, []string{repo}), policy.NewEngine(), &stubApprover{decision: domain.PermissionAllowOnce})
	result := tool.Execute(context.Background(), domain.ToolCall{
		ID:   "1",
		Name: "git_show",
		Arguments: map[string]any{
			"repo_path": repo,
		},
	})
	if result.Success {
		t.Fatalf("expected missing target failure")
	}
	if !strings.Contains(result.Output, "target パラメータが必要です") {
		t.Fatalf("expected target error, got %q", result.Output)
	}
}

func TestBranchToolReturnsBranches(t *testing.T) {
	repo := newGitRepo(t)
	tool := NewBranchTool(policy.NewPathPolicy(repo, []string{repo}), policy.NewEngine(), &stubApprover{decision: domain.PermissionAllowOnce})
	result := tool.Execute(context.Background(), domain.ToolCall{
		ID:   "1",
		Name: "git_branch",
		Arguments: map[string]any{
			"repo_path": repo,
		},
	})
	if !result.Success {
		t.Fatalf("expected success, got %s", result.Output)
	}
	if !strings.Contains(result.Output, "initial") {
		t.Fatalf("expected current branch output with commit summary, got %q", result.Output)
	}
}

func TestBlameToolSupportsLineRange(t *testing.T) {
	repo := newGitRepo(t)
	tool := NewBlameTool(policy.NewPathPolicy(repo, []string{repo}), policy.NewEngine(), &stubApprover{decision: domain.PermissionAllowOnce})
	result := tool.Execute(context.Background(), domain.ToolCall{
		ID:   "1",
		Name: "git_blame",
		Arguments: map[string]any{
			"repo_path":  repo,
			"path":       "a.txt",
			"line_start": 1,
			"line_end":   1,
		},
	})
	if !result.Success {
		t.Fatalf("expected success, got %s", result.Output)
	}
	if !strings.Contains(result.Output, "tracked") || strings.Count(result.Output, "\n") > 1 {
		t.Fatalf("expected one blame line for a.txt, got %q", result.Output)
	}
}

func TestFileHistoryToolReturnsFollowedHistory(t *testing.T) {
	repo := newGitRepo(t)
	if err := os.WriteFile(filepath.Join(repo, "a.txt"), []byte("tracked\nsecond\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, repo, "add", "a.txt")
	runGit(t, repo, "-c", "user.name=Yagent Test", "-c", "user.email=test@example.com", "-c", "commit.gpgsign=false", "commit", "-m", "second")

	tool := NewFileHistoryTool(policy.NewPathPolicy(repo, []string{repo}), policy.NewEngine(), &stubApprover{decision: domain.PermissionAllowOnce})
	result := tool.Execute(context.Background(), domain.ToolCall{
		ID:   "1",
		Name: "git_file_history",
		Arguments: map[string]any{
			"repo_path": repo,
			"path":      "a.txt",
			"limit":     5,
		},
	})
	if !result.Success {
		t.Fatalf("expected success, got %s", result.Output)
	}
	if !strings.Contains(result.Output, "second") || !strings.Contains(result.Output, "initial") {
		t.Fatalf("expected file history to include both commits, got %q", result.Output)
	}
}

func TestBlameToolValidatesLineRange(t *testing.T) {
	repo := newGitRepo(t)
	tool := NewBlameTool(policy.NewPathPolicy(repo, []string{repo}), policy.NewEngine(), &stubApprover{decision: domain.PermissionAllowOnce})
	result := tool.Execute(context.Background(), domain.ToolCall{
		ID:   "1",
		Name: "git_blame",
		Arguments: map[string]any{
			"repo_path":  repo,
			"path":       "a.txt",
			"line_start": 2,
			"line_end":   1,
		},
	})
	if result.Success {
		t.Fatalf("expected invalid range failure")
	}
	if !strings.Contains(result.Output, "line_end") {
		t.Fatalf("expected line range error, got %q", result.Output)
	}
}

func newGitRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	repo := t.TempDir()
	runGit(t, repo, "init")
	if err := os.WriteFile(filepath.Join(repo, "a.txt"), []byte("tracked\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "b.txt"), []byte("tracked\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, repo, "add", ".")
	runGit(t, repo, "-c", "user.name=Yagent Test", "-c", "user.email=test@example.com", "-c", "commit.gpgsign=false", "commit", "-m", "initial")
	return repo
}

func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v failed: %v\n%s", args, err, string(output))
	}
	return string(output)
}
