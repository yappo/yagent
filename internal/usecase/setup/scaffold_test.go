package setup

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"yagent/internal/config"
	"yagent/internal/usecase/taskcatalog"
)

func TestRunCreatesConfigAndDetectedTasks(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("YAGENT_TASKS_USER_PATH", filepath.Join(dir, "missing.toml"))
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{
  "scripts": {
    "test": "vitest",
    "lint": "eslint .",
    "dev": "vite"
  }
}`), 0o644); err != nil {
		t.Fatal(err)
	}

	result, err := Run(Options{WorkDir: dir})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if len(result.Files) != 2 {
		t.Fatalf("expected two generated files, got %+v", result.Files)
	}
	for _, file := range result.Files {
		if file.Status != "created" {
			t.Fatalf("expected created status, got %+v", result.Files)
		}
	}

	cfg, err := config.Load(filepath.Join(dir, DefaultConfigPath))
	if err != nil {
		t.Fatalf("generated config did not parse: %v", err)
	}
	server, err := cfg.ResolveServer()
	if err != nil {
		t.Fatalf("ResolveServer returned error: %v", err)
	}
	if server.Name != "local" || server.Model != DefaultLocalModel {
		t.Fatalf("unexpected generated server: %+v", server)
	}
	if cfg.Server.Servers[1].TokenEnv != "OPENAI_API_KEY" {
		t.Fatalf("expected generated OpenAI server to use token_env, got %+v", cfg.Server.Servers[1])
	}

	catalog, err := taskcatalog.New(dir)
	if err != nil {
		t.Fatalf("generated task catalog did not parse: %v", err)
	}
	for _, id := range []string{"go:test", "go:build", "npm:test", "npm:lint"} {
		if _, ok := catalog.Get(nil, id); !ok {
			t.Fatalf("expected generated task %s", id)
		}
	}
	if _, ok := catalog.Get(nil, "npm:dev"); ok {
		t.Fatalf("did not expect npm:dev task")
	}
}

func TestRunDoesNotOverwriteWithoutForce(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, DefaultConfigPath)
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, []byte("sentinel"), 0o644); err != nil {
		t.Fatal(err)
	}

	result, err := Run(Options{WorkDir: dir, WriteConfig: true, WriteTasks: false})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if len(result.Files) != 1 || result.Files[0].Status != "skipped" {
		t.Fatalf("expected skipped existing config, got %+v", result.Files)
	}
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "sentinel" {
		t.Fatalf("expected existing file to remain unchanged, got %q", string(data))
	}
}

func TestRunForceOverwritesExistingFile(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, DefaultConfigPath)
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, []byte("sentinel"), 0o644); err != nil {
		t.Fatal(err)
	}

	result, err := Run(Options{
		WorkDir:     dir,
		WriteConfig: true,
		WriteTasks:  false,
		Force:       true,
		LocalModel:  "local-model",
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if len(result.Files) != 1 || result.Files[0].Status != "overwritten" {
		t.Fatalf("expected overwritten existing config, got %+v", result.Files)
	}
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `model = "local-model"`) {
		t.Fatalf("expected overwritten model, got %q", string(data))
	}
}

func TestRunGeneratesGemma4Preset(t *testing.T) {
	dir := t.TempDir()
	result, err := Run(Options{
		WorkDir:     dir,
		WriteConfig: true,
		WriteTasks:  false,
		LocalPreset: "gemma4",
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if len(result.Files) != 1 || result.Files[0].Status != "created" {
		t.Fatalf("expected generated config, got %+v", result.Files)
	}
	data, err := os.ReadFile(filepath.Join(dir, DefaultConfigPath))
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, want := range []string{
		`model = "google/gemma-4-26b-a4b"`,
		"temperature = 1",
		"top_p = 0.95",
		"top_k = 64",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("expected %q in Gemma config, got %q", want, text)
		}
	}
	for _, unwanted := range []string{"min_p =", "presence_penalty =", "repetition_penalty ="} {
		if strings.Contains(text, unwanted) {
			t.Fatalf("did not expect Qwen-only setting %q in Gemma config, got %q", unwanted, text)
		}
	}
}

func TestRunDryRunDoesNotWriteFiles(t *testing.T) {
	dir := t.TempDir()
	result, err := Run(Options{WorkDir: dir, DryRun: true})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if len(result.Files) != 2 {
		t.Fatalf("expected two files in dry-run, got %+v", result.Files)
	}
	for _, file := range result.Files {
		if file.Status != "would_create" {
			t.Fatalf("expected dry-run would_create, got %+v", result.Files)
		}
		if _, err := os.Stat(file.Path); !os.IsNotExist(err) {
			t.Fatalf("expected %s to remain absent, stat err=%v", file.Path, err)
		}
	}
}
