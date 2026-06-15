package search

import (
	"context"
	"encoding/json"
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

func TestTextToolFindsUTF8MatchesAndSkipsGitAndBinary(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("alpha\nneedle one\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "b.txt"), []byte("needle two\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".git", "ignored.txt"), []byte("needle hidden\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "binary.dat"), []byte{'n', 'e', 'e', 'd', 'l', 'e', 0}, 0o644); err != nil {
		t.Fatal(err)
	}

	approver := &stubApprover{decision: domain.PermissionAllowOnce}
	tool := NewTextTool(policy.NewPathPolicy(dir, []string{dir}), policy.NewEngine(), approver)
	result := tool.Execute(context.Background(), domain.ToolCall{
		ID:   "1",
		Name: "search_text",
		Arguments: map[string]any{
			"root":        dir,
			"query":       "needle",
			"max_results": 10,
		},
	})
	if !result.Success {
		t.Fatalf("expected success, got %s", result.Output)
	}

	var matches []struct {
		Path   string `json:"path"`
		Line   int    `json:"line"`
		Column int    `json:"column"`
		Text   string `json:"text"`
	}
	if err := json.Unmarshal([]byte(result.Output), &matches); err != nil {
		t.Fatal(err)
	}
	if len(matches) != 2 {
		t.Fatalf("expected two visible text matches, got %+v", matches)
	}
	if matches[0].Line != 2 || matches[0].Column != 1 || matches[0].Text != "needle one" {
		t.Fatalf("unexpected first match: %+v", matches[0])
	}
	for _, match := range matches {
		if strings.Contains(match.Path, ".git") || strings.Contains(match.Path, "binary.dat") {
			t.Fatalf("expected .git and binary files to be skipped, got %+v", matches)
		}
	}
	if approver.last.ToolName != "search_text" || approver.last.Resource != dir {
		t.Fatalf("expected search permission request, got %+v", approver.last)
	}
}

func TestTextToolHonorsMaxResults(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("needle one\nneedle two\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := NewTextTool(policy.NewPathPolicy(dir, []string{dir}), policy.NewEngine(), &stubApprover{decision: domain.PermissionAllowOnce})
	result := tool.Execute(context.Background(), domain.ToolCall{
		ID:   "1",
		Name: "search_text",
		Arguments: map[string]any{
			"root":        dir,
			"query":       "needle",
			"max_results": 1,
		},
	})
	if !result.Success {
		t.Fatalf("expected success, got %s", result.Output)
	}

	var matches []map[string]any
	if err := json.Unmarshal([]byte(result.Output), &matches); err != nil {
		t.Fatal(err)
	}
	if len(matches) != 1 {
		t.Fatalf("expected one limited match, got %+v", matches)
	}
}

func TestFilesToolMatchesNamesAndSkipsGit(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"alpha.go", "beta.go", "notes.txt"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(name), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".git", "ignored.go"), []byte("ignored"), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := NewFilesTool(policy.NewPathPolicy(dir, []string{dir}), policy.NewEngine(), &stubApprover{decision: domain.PermissionAllowOnce})
	result := tool.Execute(context.Background(), domain.ToolCall{
		ID:   "1",
		Name: "search_files",
		Arguments: map[string]any{
			"root":         dir,
			"name_pattern": "*.go",
			"max_results":  10,
		},
	})
	if !result.Success {
		t.Fatalf("expected success, got %s", result.Output)
	}

	var paths []string
	if err := json.Unmarshal([]byte(result.Output), &paths); err != nil {
		t.Fatal(err)
	}
	if len(paths) != 2 {
		t.Fatalf("expected two Go files, got %+v", paths)
	}
	for _, path := range paths {
		if strings.Contains(path, ".git") || !strings.HasSuffix(path, ".go") {
			t.Fatalf("unexpected match path: %s in %+v", path, paths)
		}
	}
}
