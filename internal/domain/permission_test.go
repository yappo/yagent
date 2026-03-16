package domain

import "testing"

func TestPermissionRequestSupportsPatternApproval(t *testing.T) {
	if !PermissionRequestSupportsPatternApproval(PermissionRequest{ToolName: "fs_read", Resource: "/tmp/a.txt"}) {
		t.Fatal("expected fs_read to support pattern approval")
	}
	if PermissionRequestSupportsPatternApproval(PermissionRequest{ToolName: "task_run", Resource: "go:test"}) {
		t.Fatal("did not expect task_run to support pattern approval")
	}
}

func TestPermissionRequestMatchesPattern(t *testing.T) {
	request := PermissionRequest{Resource: "/tmp/internal/model.go"}
	if !PermissionRequestMatchesPattern(request, "*.go") {
		t.Fatal("expected basename glob to match")
	}
	if !PermissionRequestMatchesPattern(request, "/tmp/internal/*") {
		t.Fatal("expected path glob to match")
	}
	if PermissionRequestMatchesPattern(request, "*.md") {
		t.Fatal("did not expect non-matching glob to match")
	}
}
