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
enable_planning = true

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
	if !cfg.Execution.EnablePlanning {
		t.Fatal("expected enable_planning to be true")
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
