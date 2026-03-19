package taskcatalog

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"yagent/internal/domain"
)

func TestCatalogLoadsRepoTasksOnly(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("YAGENT_TASKS_USER_PATH", filepath.Join(dir, "missing.toml"))
	if err := os.Mkdir(filepath.Join(dir, ".yagent"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".yagent", "tasks.toml"), []byte(`
[[tasks]]
id = "repo:test"
description = "repo task"
command = "make"
args = ["test"]
`), 0o644); err != nil {
		t.Fatal(err)
	}

	catalog, err := New(dir)
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	task, ok := catalog.Get(context.Background(), "repo:test")
	if !ok {
		t.Fatalf("expected repo:test task")
	}
	if task.Description != "repo task" {
		t.Fatalf("expected repo task, got %+v", task)
	}
	if task.Kind != domain.TaskSpecKindCommand || task.Command == nil || task.Command.Command != "make" {
		t.Fatalf("expected command task, got %+v", task)
	}
}

func TestCatalogLoadsMCPServers(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("YAGENT_TASKS_USER_PATH", filepath.Join(dir, "missing.toml"))
	if err := os.Mkdir(filepath.Join(dir, ".yagent"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".yagent", "tasks.toml"), []byte(`
[[mcpservers]]
id = "docs"
description = "Docs MCP"
command = "npx"
args = ["-y", "@example/docs-mcp"]
cwd = "."
parallel_safe = true
include_tools = ["search_docs"]
`), 0o644); err != nil {
		t.Fatal(err)
	}

	catalog, err := New(dir)
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	task, ok := catalog.Get(context.Background(), "docs")
	if !ok {
		t.Fatalf("expected docs mcp server")
	}
	if task.Kind != domain.TaskSpecKindMCPServer || task.MCPServer == nil {
		t.Fatalf("expected mcp server task, got %+v", task)
	}
	if task.MCPServer.Transport != domain.MCPTransportStdio {
		t.Fatalf("expected stdio transport, got %+v", task.MCPServer)
	}
	if task.MCPServer.ToolPrefix != "docs" {
		t.Fatalf("expected default tool prefix, got %+v", task.MCPServer)
	}
}

func TestCatalogLoadsTaskAccessHints(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("YAGENT_TASKS_USER_PATH", filepath.Join(dir, "missing.toml"))
	if err := os.Mkdir(filepath.Join(dir, ".yagent"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".yagent", "tasks.toml"), []byte(`
[[tasks]]
id = "lint"
description = "Run linter"
command = "golangci-lint"
args = ["run", "./..."]
cwd = "."
read_paths = ["./internal", "./README.md"]
write_paths = ["./reports/lint.txt"]
`), 0o644); err != nil {
		t.Fatal(err)
	}

	catalog, err := New(dir)
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	task, ok := catalog.Get(context.Background(), "lint")
	if !ok || task.Command == nil {
		t.Fatalf("expected lint task, got %+v", task)
	}
	expectedRead := []string{
		filepath.Join(dir, "internal"),
		filepath.Join(dir, "README.md"),
	}
	expectedWrite := []string{
		filepath.Join(dir, "reports", "lint.txt"),
	}
	if got := task.Command.ReadPaths; len(got) != len(expectedRead) || got[0] != expectedRead[0] || got[1] != expectedRead[1] {
		t.Fatalf("expected read paths %v, got %v", expectedRead, got)
	}
	if got := task.Command.WritePaths; len(got) != len(expectedWrite) || got[0] != expectedWrite[0] {
		t.Fatalf("expected write paths %v, got %v", expectedWrite, got)
	}
}
