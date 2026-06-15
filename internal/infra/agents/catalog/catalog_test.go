package catalog

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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
		"fs_write or patch_apply is visible",
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
timeout = "30s"
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
	if spec.Timeout != 30*time.Second {
		t.Fatalf("unexpected timeout: %+v", spec.Timeout)
	}
}

func TestLoadUserAgentsRejectsInvalidDSL(t *testing.T) {
	cases := []struct {
		name    string
		content string
		want    string
	}{
		{
			name: "unknown field",
			content: `
id = "bad-agent"
instruction = "Inspect"
unknown = true
`,
			want: "unknown",
		},
		{
			name: "invalid mode",
			content: `
id = "bad-agent"
mode = "daemon"
`,
			want: `mode="daemon" は不正です`,
		},
		{
			name: "invalid task kind",
			content: `
id = "bad-agent"
task_kinds = ["deploy"]
`,
			want: `task_kinds[0]="deploy" は不正です`,
		},
		{
			name: "negative token budget",
			content: `
id = "bad-agent"
token_budget = -1
`,
			want: "token_budget は 0 以上",
		},
		{
			name: "empty allowed tool",
			content: `
id = "bad-agent"
allowed_tools = [""]
`,
			want: "allowed_tools[0] が空です",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "bad.toml")
			if err := os.WriteFile(path, []byte(tc.content), 0o644); err != nil {
				t.Fatal(err)
			}

			catalog := New(nil)
			err := catalog.LoadUserAgents([]string{path})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("expected error containing %q, got %v", tc.want, err)
			}
		})
	}
}

func TestAgentDSLJSONSchemaDescribesPlannerMetadata(t *testing.T) {
	data, err := json.Marshal(AgentDSLJSONSchema())
	if err != nil {
		t.Fatalf("AgentDSLJSONSchema did not marshal: %v", err)
	}
	text := string(data)
	for _, want := range []string{
		`"id"`,
		`"allowed_tools"`,
		`"task_kinds"`,
		`"preferred_phases"`,
		`"verification_required"`,
		`"additionalProperties":false`,
		`"handoff"`,
		`"mutate"`,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("expected schema to contain %s, got %s", want, text)
		}
	}
}
