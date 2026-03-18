package catalog

import (
	"os"
	"path/filepath"
	"strings"
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

func TestBuiltInAgentsIncludeToolDiscoveryGuidance(t *testing.T) {
	catalog := New(nil)
	planner, ok := catalog.Resolve("planner")
	if !ok {
		t.Fatalf("planner agent not found")
	}

	for _, fragment := range []string{
		`kind="mcp_server"`,
		"task_bind(task_id=...)",
		"use the returned tool_names directly",
		"fs_write is visible",
		"use enable_capability",
		"approval dialog automatically",
		"delegate or handoff to a write-capable agent",
	} {
		if !strings.Contains(planner.Instruction, fragment) {
			t.Fatalf("expected planner instruction to contain %q, got %q", fragment, planner.Instruction)
		}
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
allowed_tools = ["fs_read"]
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
	if len(spec.TaskKinds) == 0 {
		t.Fatalf("expected task kinds to be normalized")
	}
	if len(spec.PreferredPhases) == 0 {
		t.Fatalf("expected preferred phases to be normalized")
	}
}

func TestLoadUserAgentsSupportsPlannerMetadata(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "docs-writer.toml")
	content := `
id = "docs-writer"
name = "Docs Writer"
instruction = "Write docs"
mode = "handoff"
allowed_tools = ["fs_read", "fs_write", "patch_apply"]
read_only = false
task_kinds = ["docs", "mutate"]
capabilities = ["documentation"]
preferred_phases = ["execute", "recover"]
scope_hints = ["README", "design docs"]
verification_required = true
verification_max_attempts = 3
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
	if len(spec.TaskKinds) != 2 || spec.TaskKinds[0] != "docs" {
		t.Fatalf("unexpected task kinds: %+v", spec.TaskKinds)
	}
	if len(spec.Capabilities) == 0 || spec.Capabilities[0] != "documentation" {
		t.Fatalf("unexpected capabilities: %+v", spec.Capabilities)
	}
	if len(spec.PreferredPhases) != 2 || spec.PreferredPhases[0] != "execute" {
		t.Fatalf("unexpected preferred phases: %+v", spec.PreferredPhases)
	}
	if len(spec.ScopeHints) != 2 {
		t.Fatalf("unexpected scope hints: %+v", spec.ScopeHints)
	}
	if !spec.VerificationPolicy.Required || spec.VerificationPolicy.MaxAttempts != 3 {
		t.Fatalf("unexpected verification policy: %+v", spec.VerificationPolicy)
	}
}
