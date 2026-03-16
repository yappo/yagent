package catalog

import (
	"os"
	"path/filepath"
	"testing"

	"yagent/internal/config"
)

func TestBuiltInCatalogAndOverrides(t *testing.T) {
	catalog := New(map[string]config.AgentOverride{
		"manager": {Instruction: "custom manager"},
	})

	manager, ok := catalog.Resolve("manager")
	if !ok {
		t.Fatalf("manager agent not found")
	}
	if manager.Instruction != "custom manager" {
		t.Fatalf("unexpected override: %q", manager.Instruction)
	}
}

func TestLoadUserAgents(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "docs-writer.toml")
	content := `
id = "docs-writer"
name = "Docs Writer"
instruction = "Write docs"
mode = "tool"
allowed_tools = ["file_reader"]
read_only = true
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	catalog := New(nil)
	if err := catalog.LoadUserAgents([]string{dir}); err != nil {
		t.Fatalf("LoadUserAgents returned error: %v", err)
	}

	spec, ok := catalog.Resolve("docs-writer")
	if !ok {
		t.Fatalf("docs-writer not loaded")
	}
	if spec.Name != "Docs Writer" {
		t.Fatalf("unexpected name: %s", spec.Name)
	}
}
