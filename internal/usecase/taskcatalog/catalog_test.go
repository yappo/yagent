package taskcatalog

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestCatalogLoadsTemplateAndRepoOverride(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("YAGENT_TASKS_USER_PATH", filepath.Join(dir, "missing.toml"))
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, ".yagent"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".yagent", "tasks.toml"), []byte(`
[[tasks]]
id = "go:test"
description = "repo override"
command = "go"
args = ["test", "./..."]
`), 0o644); err != nil {
		t.Fatal(err)
	}

	catalog, err := New(dir)
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	task, ok := catalog.Get(context.Background(), "go:test")
	if !ok {
		t.Fatalf("expected go:test task")
	}
	if task.Description != "repo override" {
		t.Fatalf("expected repo override, got %+v", task)
	}
}
