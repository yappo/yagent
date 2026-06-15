package fs

import (
	"context"
	"encoding/json"
	"fmt"
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

func TestWriteToolPermissionIncludesDiffPreview(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.txt")
	if err := os.WriteFile(path, []byte("old\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	approver := &stubApprover{decision: domain.PermissionAllowOnce}
	tool := NewWriteTool(policy.NewPathPolicy(dir, []string{dir}), policy.NewEngine(), approver)
	result := tool.Execute(context.Background(), domain.ToolCall{
		ID:   "1",
		Name: "fs_write",
		Arguments: map[string]any{
			"path":      path,
			"content":   "new\n",
			"overwrite": true,
		},
	})
	if !result.Success {
		t.Fatalf("expected success, got %s", result.Output)
	}
	if approver.last.PreviewKind != "diff" {
		t.Fatalf("expected diff preview kind, got %+v", approver.last)
	}
	if !strings.Contains(approver.last.Preview, "- old") || !strings.Contains(approver.last.Preview, "+ new") {
		t.Fatalf("expected old/new preview, got %q", approver.last.Preview)
	}
	if approver.last.ChangeFiles != 1 || approver.last.Additions != 1 || approver.last.Deletions != 1 {
		t.Fatalf("expected change stats files=1 +1 -1, got %+v", approver.last)
	}
}

func TestListToolReturnsBoundedSummary(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"a.txt", "b.txt", ".hidden"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(name), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(filepath.Join(dir, "subdir"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "subdir", "nested.txt"), []byte("nested"), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := NewListTool(policy.NewPathPolicy(dir, []string{dir}), policy.NewEngine(), &stubApprover{decision: domain.PermissionAllowOnce})
	result := tool.Execute(context.Background(), domain.ToolCall{
		ID:   "1",
		Name: "fs_list",
		Arguments: map[string]any{
			"path":          dir,
			"limit_entries": 2,
		},
	})
	if !result.Success {
		t.Fatalf("expected success, got %s", result.Output)
	}

	var got struct {
		Root    string `json:"root"`
		Request struct {
			Path          string `json:"path"`
			Depth         int    `json:"depth"`
			IncludeHidden bool   `json:"include_hidden"`
			LimitEntries  int    `json:"limit_entries"`
		} `json:"request"`
		Summary struct {
			ReturnedEntries int  `json:"returned_entries"`
			MatchedEntries  int  `json:"matched_entries"`
			OmittedEntries  int  `json:"omitted_entries"`
			HiddenOmitted   int  `json:"hidden_omitted"`
			Truncated       bool `json:"truncated"`
			Directories     int  `json:"directories"`
			Files           int  `json:"files"`
		} `json:"summary"`
		Entries []struct {
			Path  string `json:"path"`
			Type  string `json:"type"`
			Depth int    `json:"depth"`
		} `json:"entries"`
	}
	if err := json.Unmarshal([]byte(result.Output), &got); err != nil {
		t.Fatalf("failed to parse output: %v\n%s", err, result.Output)
	}
	resolvedDir, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got.Root != resolvedDir {
		t.Fatalf("expected root %q, got %q", resolvedDir, got.Root)
	}
	if got.Request.Depth != 0 || got.Request.IncludeHidden || got.Request.LimitEntries != 2 {
		t.Fatalf("unexpected request echo: %+v", got.Request)
	}
	if got.Summary.ReturnedEntries != 2 || got.Summary.MatchedEntries != 3 || got.Summary.OmittedEntries != 1 || !got.Summary.Truncated {
		t.Fatalf("unexpected summary: %+v", got.Summary)
	}
	if got.Summary.HiddenOmitted != 1 || got.Summary.Directories != 1 || got.Summary.Files != 2 {
		t.Fatalf("unexpected counts: %+v", got.Summary)
	}
	if len(got.Entries) != 2 {
		t.Fatalf("expected two entries, got %+v", got.Entries)
	}
	for _, entry := range got.Entries {
		if filepath.IsAbs(entry.Path) {
			t.Fatalf("expected relative entry path, got %+v", got.Entries)
		}
		if strings.HasPrefix(filepath.Base(entry.Path), ".") {
			t.Fatalf("hidden entry should be omitted by default: %+v", got.Entries)
		}
	}
}

func TestListToolIncludesHiddenAndClampsLimit(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".hidden"), []byte("hidden"), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := NewListTool(policy.NewPathPolicy(dir, []string{dir}), policy.NewEngine(), &stubApprover{decision: domain.PermissionAllowOnce})
	result := tool.Execute(context.Background(), domain.ToolCall{
		ID:   "1",
		Name: "fs_list",
		Arguments: map[string]any{
			"path":           dir,
			"include_hidden": true,
			"limit_entries":  maxListLimitEntries + 1,
		},
	})
	if !result.Success {
		t.Fatalf("expected success, got %s", result.Output)
	}

	var got struct {
		Request struct {
			LimitEntries int `json:"limit_entries"`
		} `json:"request"`
		Summary struct {
			HiddenOmitted int `json:"hidden_omitted"`
		} `json:"summary"`
		Entries []struct {
			Path string `json:"path"`
		} `json:"entries"`
	}
	if err := json.Unmarshal([]byte(result.Output), &got); err != nil {
		t.Fatalf("failed to parse output: %v\n%s", err, result.Output)
	}
	if got.Request.LimitEntries != maxListLimitEntries {
		t.Fatalf("expected clamped limit %d, got %d", maxListLimitEntries, got.Request.LimitEntries)
	}
	if got.Summary.HiddenOmitted != 0 {
		t.Fatalf("hidden entries should be included, got summary %+v", got.Summary)
	}
	if len(got.Entries) != 1 || got.Entries[0].Path != ".hidden" {
		t.Fatalf("expected hidden entry, got %+v", got.Entries)
	}
}

func TestListToolStopsAtScanLimit(t *testing.T) {
	dir := t.TempDir()
	for i := range maxListScanEntries + 1 {
		name := filepath.Join(dir, fmt.Sprintf("file-%05d.txt", i))
		if err := os.WriteFile(name, nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}

	tool := NewListTool(policy.NewPathPolicy(dir, []string{dir}), policy.NewEngine(), &stubApprover{decision: domain.PermissionAllowOnce})
	result := tool.Execute(context.Background(), domain.ToolCall{
		ID:   "1",
		Name: "fs_list",
		Arguments: map[string]any{
			"path":          dir,
			"limit_entries": 1,
		},
	})
	if !result.Success {
		t.Fatalf("expected success, got %s", result.Output)
	}

	var got struct {
		Summary struct {
			MatchedEntries      int  `json:"matched_entries"`
			OmittedEntries      int  `json:"omitted_entries"`
			OmittedEntriesExact bool `json:"omitted_entries_exact"`
			ScannedEntries      int  `json:"scanned_entries"`
			ScanTruncated       bool `json:"scan_truncated"`
			Truncated           bool `json:"truncated"`
		} `json:"summary"`
		Entries []struct {
			Path string `json:"path"`
		} `json:"entries"`
	}
	if err := json.Unmarshal([]byte(result.Output), &got); err != nil {
		t.Fatalf("failed to parse output: %v\n%s", err, result.Output)
	}
	if len(got.Entries) != 1 {
		t.Fatalf("expected one returned entry, got %+v", got.Entries)
	}
	if got.Summary.MatchedEntries != maxListScanEntries || got.Summary.ScannedEntries != maxListScanEntries {
		t.Fatalf("expected scan limit counts, got %+v", got.Summary)
	}
	if got.Summary.OmittedEntries != maxListScanEntries-1 || got.Summary.OmittedEntriesExact || !got.Summary.ScanTruncated || !got.Summary.Truncated {
		t.Fatalf("expected truncated lower-bound summary, got %+v", got.Summary)
	}
}

func TestListToolDefinitionUsesSemanticDefaults(t *testing.T) {
	tool := NewListTool(policy.NewPathPolicy(t.TempDir(), nil), nil, nil)
	def := tool.Definition()
	if def.Semantics.DuplicatePolicy != domain.ToolDuplicateSuppressSemantic {
		t.Fatalf("expected semantic duplicate suppression, got %q", def.Semantics.DuplicatePolicy)
	}
	if got, ok := def.Semantics.IdentityDefaults["limit_entries"].(int); !ok || got != defaultListLimitEntries {
		t.Fatalf("expected default limit %d, got %+v", defaultListLimitEntries, def.Semantics.IdentityDefaults)
	}
	if got, ok := def.Semantics.IdentityDefaults["depth"].(int); !ok || got != defaultListDepth {
		t.Fatalf("expected default depth %d, got %+v", defaultListDepth, def.Semantics.IdentityDefaults)
	}
	if got, ok := def.Semantics.IdentityDefaults["include_hidden"].(bool); !ok || got {
		t.Fatalf("expected default include_hidden false, got %+v", def.Semantics.IdentityDefaults)
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
