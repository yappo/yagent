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

	if server.URL != "http://127.0.0.1:1234" {
		t.Fatalf("unexpected default URL: %s", server.URL)
	}
	if server.API != "chat_completions" {
		t.Fatalf("unexpected default API: %s", server.API)
	}
	if server.Model != "Qwen/Qwen3.6-35B-A3B" {
		t.Fatalf("unexpected default model: %s", server.Model)
	}
	if cfg.Execution.MaxParallelAgents != 2 {
		t.Fatalf("unexpected default max parallel agents: %d", cfg.Execution.MaxParallelAgents)
	}
	if cfg.Harness.MaxVerificationAttempts != 2 {
		t.Fatalf("unexpected default max verification attempts: %d", cfg.Harness.MaxVerificationAttempts)
	}
	if cfg.Harness.ContinuationPolicy != "prompt" {
		t.Fatalf("unexpected default continuation policy: %s", cfg.Harness.ContinuationPolicy)
	}
}

func TestLoadDiscoversRepoLocalConfig(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	if err := os.Mkdir(".yagent", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(".yagent", "config.toml"), []byte(`
[server]
default = "local"

[[server.servers]]
name = "local"
url = "http://127.0.0.1:1234"
model = "repo-local-model"
api = "chat_completions"
`), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	server, err := cfg.ResolveServer()
	if err != nil {
		t.Fatalf("ResolveServer returned error: %v", err)
	}
	if server.Model != "repo-local-model" {
		t.Fatalf("expected repo-local config model, got %+v", server)
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
token_env = "IGNORED_TOKEN_ENV"
model = "gpt-5"
api = "responses"
timeout = "20m"

[server.servers.generation]
reasoning_effort = "high"
text_verbosity = "low"
max_output_tokens = 4096
temperature = 0.7
parallel_tool_calls = false

[file]
allow_paths = ["/tmp"]
deny_paths = ["*.pem"]

[[file.rules]]
decision = "deny"
path = ".env"

[[file.rules]]
decision = "allow"
paths = ["/var/tmp/yagent-cache/*"]

[permission]
[[permission.rules]]
tool = "fs_read"
action = "read"
resources = ["*.go", "README.md"]
decision = "allow"

[[permission.rules]]
tool = "task_run"
risk = "high"
decision = "deny"

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

[routing.profiles.fast.generation]
reasoning_effort = "low"

[harness]
max_verification_attempts = 3
force_planner = true
continuation_policy = "allow"

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
	if server.TokenEnv != "IGNORED_TOKEN_ENV" {
		t.Fatalf("unexpected token env: %s", server.TokenEnv)
	}
	if server.Model != "gpt-5" {
		t.Fatalf("unexpected server model: %s", server.Model)
	}
	if server.API != "responses" {
		t.Fatalf("unexpected server API: %s", server.API)
	}
	if server.Timeout.Duration != 20*time.Minute {
		t.Fatalf("unexpected server timeout: %s", server.Timeout.Duration)
	}
	if server.Generation.MaxOutputTokens != 4096 || server.Generation.ReasoningEffort != "high" || server.Generation.TextVerbosity != "low" {
		t.Fatalf("unexpected generation config: %+v", server.Generation)
	}
	if server.Generation.Temperature == nil || *server.Generation.Temperature != 0.7 {
		t.Fatalf("unexpected temperature: %+v", server.Generation.Temperature)
	}
	if server.Generation.ParallelToolCalls == nil || *server.Generation.ParallelToolCalls {
		t.Fatalf("unexpected parallel tool calls setting: %+v", server.Generation.ParallelToolCalls)
	}
	if len(cfg.File.DenyPaths) != 1 || cfg.File.DenyPaths[0] != "*.pem" || len(cfg.File.Rules) != 2 {
		t.Fatalf("unexpected file rules: %+v", cfg.File)
	}
	if cfg.File.Rules[0].Decision != "deny" || cfg.File.Rules[0].Path != ".env" {
		t.Fatalf("unexpected first file rule: %+v", cfg.File.Rules[0])
	}
	if len(cfg.Permission.Rules) != 2 {
		t.Fatalf("unexpected permission rules: %+v", cfg.Permission.Rules)
	}
	if cfg.Permission.Rules[0].Decision != "allow" || cfg.Permission.Rules[0].Resources[0] != "*.go" {
		t.Fatalf("unexpected first permission rule: %+v", cfg.Permission.Rules[0])
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
	if cfg.Routing.Profiles["fast"].Model != "gpt-5-mini" || cfg.Routing.Profiles["fast"].Generation.ReasoningEffort != "low" {
		t.Fatalf("unexpected routing profile: %+v", cfg.Routing.Profiles["fast"])
	}
	if cfg.Harness.MaxVerificationAttempts != 3 {
		t.Fatalf("unexpected harness config: %+v", cfg.Harness)
	}
	if cfg.Harness.ContinuationPolicy != "allow" {
		t.Fatalf("unexpected continuation policy: %+v", cfg.Harness)
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

func TestServerTargetResolvedTokenUsesEnvWhenTokenIsEmpty(t *testing.T) {
	t.Setenv("YAGENT_TEST_TOKEN", "from-env")

	server := ServerTarget{TokenEnv: "YAGENT_TEST_TOKEN"}
	if got := server.ResolvedToken(); got != "from-env" {
		t.Fatalf("expected env token, got %q", got)
	}

	server.Token = "explicit"
	if got := server.ResolvedToken(); got != "explicit" {
		t.Fatalf("expected explicit token to win, got %q", got)
	}
}

func TestMarshalRoundTripDefaultConfig(t *testing.T) {
	cfg := Default()
	cfg.Server.Servers[0].Generation.MaxOutputTokens = 4096
	cfg.Server.Servers[0].Timeout = Duration{Duration: 20 * time.Minute}
	temperature := 1.0
	cfg.Server.Servers[0].Generation.Temperature = &temperature

	data, err := Marshal(cfg)
	if err != nil {
		t.Fatalf("Marshal returned error: %v", err)
	}
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("Load marshaled config returned error: %v\n%s", err, string(data))
	}
	server, err := loaded.ResolveServer()
	if err != nil {
		t.Fatalf("ResolveServer returned error: %v", err)
	}
	if server.Generation.MaxOutputTokens != 4096 || server.Timeout.Duration != 20*time.Minute {
		t.Fatalf("unexpected round-trip server: %+v", server)
	}
	if server.Generation.Temperature == nil || *server.Generation.Temperature != 1.0 {
		t.Fatalf("unexpected round-trip temperature: %+v", server.Generation.Temperature)
	}
}

func TestLoadConfigExample(t *testing.T) {
	cfg, err := Load(filepath.Join("..", "..", "config_example.toml"))
	if err != nil {
		t.Fatalf("Load config_example.toml returned error: %v", err)
	}
	server, err := cfg.ResolveServer()
	if err != nil {
		t.Fatalf("ResolveServer returned error: %v", err)
	}
	if server.Name != "local" || server.API != "chat_completions" {
		t.Fatalf("unexpected example default server: %+v", server)
	}
	if cfg.Routing.Profiles["strong"].FallbackServer != "openai" {
		t.Fatalf("unexpected strong profile: %+v", cfg.Routing.Profiles["strong"])
	}
}

func TestLoadRejectsInvalidModelAPI(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	content := `
[server]
default = "bad"

[[server.servers]]
name = "bad"
url = "http://127.0.0.1:1234"
api = "unknown"
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := Load(path); err == nil {
		t.Fatal("expected error")
	}
}

func TestLoadRejectsInvalidPermissionRuleDecision(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	content := `
[permission]
[[permission.rules]]
tool = "fs_read"
decision = "ask"
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := Load(path); err == nil {
		t.Fatal("expected error")
	}
}

func TestLoadRejectsInvalidContinuationPolicy(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	content := `
[harness]
continuation_policy = "forever"
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := Load(path); err == nil {
		t.Fatal("expected error")
	}
}

func TestLoadRejectsPermissionRuleWithoutSelector(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	content := `
[permission]
[[permission.rules]]
decision = "allow"
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := Load(path); err == nil {
		t.Fatal("expected error")
	}
}

func TestLoadRejectsInvalidFileRule(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	content := `
[[file.rules]]
decision = "prompt"
path = ".env"
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := Load(path); err == nil {
		t.Fatal("expected error")
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
