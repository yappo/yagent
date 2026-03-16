package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoadDefault(t *testing.T) {
	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}

	server, err := cfg.ResolveServer()
	if err != nil {
		t.Fatalf("ResolveServer returned error: %v", err)
	}

	if server.URL != "http://localhost:1234" {
		t.Fatalf("unexpected default URL: %s", server.URL)
	}
	if cfg.Execution.MaxParallelAgents != 2 {
		t.Fatalf("unexpected default max parallel agents: %d", cfg.Execution.MaxParallelAgents)
	}
	if cfg.Harness.MaxVerificationAttempts != 2 {
		t.Fatalf("unexpected default max verification attempts: %d", cfg.Harness.MaxVerificationAttempts)
	}
}

func TestLoadFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	content := `
[server]
default = "lmstudio"

[[server.servers]]
name = "lmstudio"
url = "http://127.0.0.1:1234"
token = "secret"
model = "gpt-5"
timeout = "20m"

[file]
allow_paths = ["/tmp"]

[execution]
max_parallel_agents = 1
max_handoff_depth = 3
default_timeout = "600s"

[features]
phase_harness = true
adaptive_compaction = false
role_routing = true
repo_memory = false

[routing.profiles.fast]
server = "lmstudio"
model = "gpt-5-mini"

[harness]
max_verification_attempts = 3
force_planner = true

[context]
max_recent_messages = 8
max_artifacts = 5
max_relevant_files = 6
compact_after_turns = 9
compact_after_tool_calls = 10
compact_after_est_tokens = 11000
compact_after_verify_cycles = 2

[memory]
enabled = true
state_dir = ".yagent/state"
max_runs = 10
max_facts = 20

[benchmark]
default_runs = 3

[agent_catalog]
paths = ["/tmp/agents"]

[agents.coder]
instruction = "custom coder"
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}

	server, err := cfg.ResolveServer()
	if err != nil {
		t.Fatalf("ResolveServer returned error: %v", err)
	}

	if server.Token != "secret" {
		t.Fatalf("unexpected token: %s", server.Token)
	}
	if server.Model != "gpt-5" {
		t.Fatalf("unexpected server model: %s", server.Model)
	}
	if server.Timeout.Duration != 20*time.Minute {
		t.Fatalf("unexpected server timeout: %s", server.Timeout.Duration)
	}
	if cfg.Execution.MaxParallelAgents != 1 {
		t.Fatalf("unexpected max_parallel_agents: %d", cfg.Execution.MaxParallelAgents)
	}
	if cfg.Execution.MaxHandoffDepth != 3 {
		t.Fatalf("unexpected max_handoff_depth: %d", cfg.Execution.MaxHandoffDepth)
	}
	if cfg.Execution.DefaultTimeout.Duration != 600*time.Second {
		t.Fatalf("unexpected default_timeout: %s", cfg.Execution.DefaultTimeout.Duration)
	}
	if cfg.Features.AdaptiveCompaction {
		t.Fatalf("unexpected features config: %+v", cfg.Features)
	}
	if cfg.Routing.Profiles["fast"].Model != "gpt-5-mini" {
		t.Fatalf("unexpected routing profile: %+v", cfg.Routing.Profiles["fast"])
	}
	if cfg.Harness.MaxVerificationAttempts != 3 {
		t.Fatalf("unexpected harness config: %+v", cfg.Harness)
	}
	if cfg.Context.MaxRecentMessages != 8 || cfg.Context.CompactAfterEstTokens != 11000 {
		t.Fatalf("unexpected context config: %+v", cfg.Context)
	}
	if cfg.Benchmark.DefaultRuns != 3 {
		t.Fatalf("unexpected benchmark config: %+v", cfg.Benchmark)
	}
	if cfg.Agents["coder"].Instruction != "custom coder" {
		t.Fatalf("unexpected coder override: %+v", cfg.Agents["coder"])
	}
}

func TestLoadRejectsInvalidParallelAgents(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	content := `
[execution]
max_parallel_agents = 0
max_handoff_depth = 1
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := Load(path); err == nil {
		t.Fatal("expected error")
	}
}
