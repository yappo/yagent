package config

import (
	"os"
	"path/filepath"
	"testing"
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
	if cfg.Agent.MaxIterations != 100 {
		t.Fatalf("unexpected default max iterations: %d", cfg.Agent.MaxIterations)
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
timeout = 1200

[file]
allow_paths = ["/tmp"]

[agent]
max_iterations = 42
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
	if server.Timeout != 1200 {
		t.Fatalf("unexpected server timeout: %d", server.Timeout)
	}
	if cfg.Agent.MaxIterations != 42 {
		t.Fatalf("unexpected max iterations: %d", cfg.Agent.MaxIterations)
	}
}
