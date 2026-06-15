package taskcatalog

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
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

func TestCatalogAddsAutoGoTasks(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("YAGENT_TASKS_USER_PATH", filepath.Join(dir, "missing.toml"))
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	catalog, err := New(dir)
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	testTask, ok := catalog.Get(context.Background(), "go:test")
	if !ok || testTask.Command == nil {
		t.Fatalf("expected auto go:test task, got %+v", testTask)
	}
	if testTask.Source != "auto:go.mod" || testTask.Command.Command != "go" || len(testTask.Command.Args) != 2 || testTask.Command.Args[0] != "test" {
		t.Fatalf("unexpected auto go:test task: %+v", testTask)
	}
	buildTask, ok := catalog.Get(context.Background(), "go:build")
	if !ok || buildTask.Command == nil || buildTask.Command.Args[0] != "build" {
		t.Fatalf("expected auto go:build task, got %+v", buildTask)
	}
}

func TestCatalogAddsAutoPackageJSONTasks(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("YAGENT_TASKS_USER_PATH", filepath.Join(dir, "missing.toml"))
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{
  "scripts": {
    "test": "vitest",
    "build": "vite build",
    "lint": "eslint .",
    "dev": "vite"
  }
}`), 0o644); err != nil {
		t.Fatal(err)
	}

	catalog, err := New(dir)
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	for _, id := range []string{"npm:test", "npm:build", "npm:lint"} {
		task, ok := catalog.Get(context.Background(), id)
		if !ok || task.Command == nil {
			t.Fatalf("expected auto %s task, got %+v", id, task)
		}
		if task.Command.Command != "npm" || task.Command.Args[0] != "run" {
			t.Fatalf("unexpected auto npm task: %+v", task)
		}
	}
	if _, ok := catalog.Get(context.Background(), "npm:dev"); ok {
		t.Fatalf("did not expect auto npm:dev task")
	}
}

func TestCatalogRepoTaskOverridesAutoTask(t *testing.T) {
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
description = "custom go test"
command = "go"
args = ["test", "./internal/..."]
`), 0o644); err != nil {
		t.Fatal(err)
	}

	catalog, err := New(dir)
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	task, ok := catalog.Get(context.Background(), "go:test")
	if !ok || task.Command == nil {
		t.Fatalf("expected go:test task, got %+v", task)
	}
	if task.Description != "custom go test" || len(task.Command.Args) != 2 || task.Command.Args[1] != "./internal/..." {
		t.Fatalf("expected repo override, got %+v", task)
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
roots = ["."]
trust = "trusted"
parallel_safe = true
read_only_tools = ["search_docs"]
mutating_tools = ["write_docs"]
parallel_safe_tools = ["search_docs"]
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
	if len(task.MCPServer.Roots) != 1 || task.MCPServer.Roots[0] != dir {
		t.Fatalf("expected resolved MCP roots, got %+v", task.MCPServer.Roots)
	}
	if task.MCPServer.Trust != "trusted" || !task.MCPServer.TrustToolAnnotations {
		t.Fatalf("expected trusted mcp server, got %+v", task.MCPServer)
	}
	if len(task.MCPServer.ReadOnlyTools) != 1 || task.MCPServer.ReadOnlyTools[0] != "search_docs" {
		t.Fatalf("expected read only tools, got %+v", task.MCPServer)
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

func TestCatalogRejectsInvalidTaskDefinitions(t *testing.T) {
	cases := []struct {
		name string
		body string
		want string
	}{
		{
			name: "missing command",
			body: `
[[tasks]]
id = "repo:test"
description = "missing command"
`,
			want: `[[tasks]] #1 id="repo:test": command が必要です`,
		},
		{
			name: "invalid risk",
			body: `
[[tasks]]
id = "repo:test"
command = "make"
risk = "danger"
`,
			want: `risk="danger" は不正です`,
		},
		{
			name: "duplicate id",
			body: `
[[tasks]]
id = "dup"
command = "make"

[[mcpservers]]
id = "dup"
command = "npx"
`,
			want: `id="dup" が重複しています`,
		},
		{
			name: "invalid mcp transport",
			body: `
[[mcpservers]]
id = "docs"
transport = "http"
command = "npx"
`,
			want: `transport="http" は未対応です`,
		},
		{
			name: "conflicting mcp safety lists",
			body: `
[[mcpservers]]
id = "docs"
command = "npx"
read_only_tools = ["search_docs"]
mutating_tools = ["search_docs"]
`,
			want: `read_only_tools と mutating_tools の両方`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			t.Setenv("HOME", dir)
			t.Setenv("YAGENT_TASKS_USER_PATH", filepath.Join(dir, "missing.toml"))
			if err := os.Mkdir(filepath.Join(dir, ".yagent"), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, ".yagent", "tasks.toml"), []byte(tc.body), 0o644); err != nil {
				t.Fatal(err)
			}
			_, err := New(dir)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("expected error containing %q, got %v", tc.want, err)
			}
			if !strings.Contains(err.Error(), filepath.Join(dir, ".yagent", "tasks.toml")) {
				t.Fatalf("expected error to include source path, got %v", err)
			}
		})
	}
}

func TestCatalogRejectsUnknownTaskFields(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("YAGENT_TASKS_USER_PATH", filepath.Join(dir, "missing.toml"))
	if err := os.Mkdir(filepath.Join(dir, ".yagent"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".yagent", "tasks.toml"), []byte(`
[[tasks]]
id = "repo:test"
command = "make"
unknown = true
`), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := New(dir)
	if err == nil || !strings.Contains(err.Error(), "task 設定のパースに失敗しました") || !strings.Contains(err.Error(), "unknown") {
		t.Fatalf("expected unknown field parse error, got %v", err)
	}
}

func TestJSONSchemaDescribesTasksAndMCPServers(t *testing.T) {
	data, err := json.Marshal(JSONSchema())
	if err != nil {
		t.Fatalf("JSONSchema did not marshal: %v", err)
	}
	text := string(data)
	for _, want := range []string{
		`"tasks"`,
		`"mcpservers"`,
		`"read_paths"`,
		`"write_paths"`,
		`"roots"`,
		`"trust_tool_annotations"`,
		`"additionalProperties":false`,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("expected schema to contain %s, got %s", want, text)
		}
	}
}
