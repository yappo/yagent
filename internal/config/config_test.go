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
timeout = "20m"

[file]
allow_paths = ["/tmp"]

[execution]
max_parallel_agents = 1
default_timeout = "600s"

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
	if server.Timeout.Duration != 20*time.Minute {
		t.Fatalf("unexpected server timeout: %s", server.Timeout.Duration)
	}
	if cfg.Execution.MaxParallelAgents != 1 {
		t.Fatalf("unexpected max_parallel_agents: %d", cfg.Execution.MaxParallelAgents)
	}
	if cfg.Execution.DefaultTimeout.Duration.Seconds() != 600 {
		t.Fatalf("unexpected default_timeout: %s", cfg.Execution.DefaultTimeout.Duration)
	}
	if cfg.Agents["coder"].Instruction != "custom coder" {
		t.Fatalf("unexpected coder override: %+v", cfg.Agents["coder"])
	}
}
