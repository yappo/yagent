package config

import (
	"fmt"
	"os"
	"time"

	"github.com/pelletier/go-toml/v2"
)

const repoConfigPath = ".yagent/config.toml"

type Config struct {
	Server       ServerConfig             `toml:"server"`
	File         FileConfig               `toml:"file"`
	Permission   PermissionConfig         `toml:"permission"`
	Execution    ExecutionConfig          `toml:"execution"`
	Features     FeaturesConfig           `toml:"features"`
	Routing      RoutingConfig            `toml:"routing"`
	Harness      HarnessConfig            `toml:"harness"`
	Context      ContextConfig            `toml:"context"`
	Memory       MemoryConfig             `toml:"memory"`
	Benchmark    BenchmarkConfig          `toml:"benchmark"`
	AgentCatalog AgentCatalogConfig       `toml:"agent_catalog"`
	Agents       map[string]AgentOverride `toml:"agents"`
}

type ServerConfig struct {
	Default string         `toml:"default"`
	Servers []ServerTarget `toml:"servers"`
}

type ServerTarget struct {
	Name       string           `toml:"name"`
	URL        string           `toml:"url"`
	Token      string           `toml:"token"`
	TokenEnv   string           `toml:"token_env"`
	Model      string           `toml:"model"`
	API        string           `toml:"api"`
	Timeout    Duration         `toml:"timeout"`
	Generation GenerationConfig `toml:"generation"`
}

type FileConfig struct {
	AllowPaths []string         `toml:"allow_paths"`
	DenyPaths  []string         `toml:"deny_paths"`
	Rules      []FileRuleConfig `toml:"rules"`
}

type FileRuleConfig struct {
	Decision string   `toml:"decision"`
	Path     string   `toml:"path"`
	Paths    []string `toml:"paths"`
}

type PermissionConfig struct {
	Rules []PermissionRuleConfig `toml:"rules"`
}

type PermissionRuleConfig struct {
	Decision     string   `toml:"decision"`
	Tool         string   `toml:"tool"`
	Action       string   `toml:"action"`
	ResourceKind string   `toml:"resource_kind"`
	Risk         string   `toml:"risk"`
	Resource     string   `toml:"resource"`
	Resources    []string `toml:"resources"`
	Agent        string   `toml:"agent"`
	SideEffect   string   `toml:"side_effect"`
	SideEffects  []string `toml:"side_effects"`
}

type ExecutionConfig struct {
	MaxParallelAgents int                    `toml:"max_parallel_agents"`
	MaxHandoffDepth   int                    `toml:"max_handoff_depth"`
	DefaultTimeout    Duration               `toml:"default_timeout"`
	ProcessIsolation  ProcessIsolationConfig `toml:"process_isolation"`
}

// ProcessIsolationConfig names a trusted host-side proxy that executes an
// untrusted task inside a disposable VM or container and relays its stdio.
type ProcessIsolationConfig struct {
	Backend string   `toml:"backend"`
	Runner  string   `toml:"runner"`
	Args    []string `toml:"args"`
}

type FeaturesConfig struct {
	PhaseHarness       bool `toml:"phase_harness"`
	AdaptiveCompaction bool `toml:"adaptive_compaction"`
	RoleRouting        bool `toml:"role_routing"`
	RepoMemory         bool `toml:"repo_memory"`
}

type RoutingConfig struct {
	Profiles map[string]RoutingProfileConfig `toml:"profiles"`
}

type RoutingProfileConfig struct {
	Server         string           `toml:"server"`
	Model          string           `toml:"model"`
	FallbackServer string           `toml:"fallback_server"`
	FallbackModel  string           `toml:"fallback_model"`
	Generation     GenerationConfig `toml:"generation"`
}

type GenerationConfig struct {
	MaxOutputTokens   int      `toml:"max_output_tokens"`
	Temperature       *float64 `toml:"temperature"`
	TopP              *float64 `toml:"top_p"`
	TopK              int      `toml:"top_k"`
	MinP              *float64 `toml:"min_p"`
	PresencePenalty   *float64 `toml:"presence_penalty"`
	RepetitionPenalty *float64 `toml:"repetition_penalty"`
	ReasoningEffort   string   `toml:"reasoning_effort"`
	TextVerbosity     string   `toml:"text_verbosity"`
	ParallelToolCalls *bool    `toml:"parallel_tool_calls"`
	Store             *bool    `toml:"store"`
}

type HarnessConfig struct {
	MaxVerificationAttempts int    `toml:"max_verification_attempts"`
	ForcePlanner            bool   `toml:"force_planner"`
	ForceResearcher         bool   `toml:"force_researcher"`
	ContinuationPolicy      string `toml:"continuation_policy"`
}

type ContextConfig struct {
	MaxRecentMessages        int `toml:"max_recent_messages"`
	MaxArtifacts             int `toml:"max_artifacts"`
	MaxRelevantFiles         int `toml:"max_relevant_files"`
	CompactAfterTurns        int `toml:"compact_after_turns"`
	CompactAfterToolCalls    int `toml:"compact_after_tool_calls"`
	CompactAfterEstTokens    int `toml:"compact_after_est_tokens"`
	CompactAfterVerifyCycles int `toml:"compact_after_verify_cycles"`
}

type MemoryConfig struct {
	Enabled  bool   `toml:"enabled"`
	StateDir string `toml:"state_dir"`
	MaxRuns  int    `toml:"max_runs"`
	MaxFacts int    `toml:"max_facts"`
}

type BenchmarkConfig struct {
	DefaultRuns int `toml:"default_runs"`
}

type AgentCatalogConfig struct {
	Paths []string `toml:"paths"`
}

type AgentOverride struct {
	Instruction    string   `toml:"instruction"`
	Model          string   `toml:"model"`
	RoutingProfile string   `toml:"routing_profile"`
	AllowedTools   []string `toml:"allowed_tools"`
	Disabled       bool     `toml:"disabled"`
}

type Duration struct {
	time.Duration
}

func Load(path string) (Config, error) {
	if path == "" {
		discovered, ok, err := discoverConfigPath()
		if err != nil {
			return Config{}, err
		}
		if !ok {
			return Default(), nil
		}
		path = discovered
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("設定ファイルの読み込みに失敗しました: %w", err)
	}

	cfg := Default()
	if err := toml.Unmarshal(data, &cfg); err != nil {
		return Config{}, fmt.Errorf("設定ファイルのパースに失敗しました: %w", err)
	}

	if cfg.Execution.MaxParallelAgents < 1 {
		return Config{}, fmt.Errorf("execution.max_parallel_agents は 1 以上である必要があります")
	}
	if cfg.Execution.MaxHandoffDepth < 1 {
		return Config{}, fmt.Errorf("execution.max_handoff_depth は 1 以上である必要があります")
	}
	if cfg.Agents == nil {
		cfg.Agents = map[string]AgentOverride{}
	}
	if cfg.Routing.Profiles == nil {
		cfg.Routing.Profiles = map[string]RoutingProfileConfig{}
	}
	if cfg.Harness.MaxVerificationAttempts < 1 {
		return Config{}, fmt.Errorf("harness.max_verification_attempts は 1 以上である必要があります")
	}
	if err := validateContinuationPolicy(cfg.Harness.ContinuationPolicy); err != nil {
		return Config{}, err
	}
	if cfg.Context.MaxRecentMessages < 1 {
		return Config{}, fmt.Errorf("context.max_recent_messages は 1 以上である必要があります")
	}
	if cfg.Memory.MaxRuns < 1 {
		return Config{}, fmt.Errorf("memory.max_runs は 1 以上である必要があります")
	}
	if cfg.Memory.MaxFacts < 1 {
		return Config{}, fmt.Errorf("memory.max_facts は 1 以上である必要があります")
	}
	if cfg.Benchmark.DefaultRuns < 1 {
		return Config{}, fmt.Errorf("benchmark.default_runs は 1 以上である必要があります")
	}
	for _, server := range cfg.Server.Servers {
		if err := validateModelAPI(server.API); err != nil {
			return Config{}, err
		}
	}
	for _, rule := range cfg.File.Rules {
		if err := validateFileRule(rule); err != nil {
			return Config{}, err
		}
	}
	for _, rule := range cfg.Permission.Rules {
		if err := validatePermissionRule(rule); err != nil {
			return Config{}, err
		}
	}

	return cfg, nil
}

func Marshal(cfg Config) ([]byte, error) {
	data, err := toml.Marshal(cfg)
	if err != nil {
		return nil, fmt.Errorf("設定ファイルの生成に失敗しました: %w", err)
	}
	return data, nil
}

func discoverConfigPath() (string, bool, error) {
	if _, err := os.Stat(repoConfigPath); err == nil {
		return repoConfigPath, true, nil
	} else if !os.IsNotExist(err) {
		return "", false, fmt.Errorf("repo-local config の確認に失敗しました: %w", err)
	}
	return "", false, nil
}

func Default() Config {
	return Config{
		Server: ServerConfig{
			Default: "local",
			Servers: []ServerTarget{
				{
					Name:    "local",
					URL:     "http://127.0.0.1:1234",
					Model:   "Qwen/Qwen3.6-35B-A3B",
					API:     "chat_completions",
					Timeout: Duration{Duration: 20 * time.Minute},
				},
			},
		},
		File: FileConfig{
			AllowPaths: []string{},
		},
		Permission: PermissionConfig{
			Rules: []PermissionRuleConfig{},
		},
		Execution: ExecutionConfig{
			MaxParallelAgents: 2,
			MaxHandoffDepth:   2,
			DefaultTimeout:    Duration{Duration: 120 * time.Second},
		},
		Features: FeaturesConfig{
			PhaseHarness:       true,
			AdaptiveCompaction: true,
			RoleRouting:        true,
			RepoMemory:         true,
		},
		Routing: RoutingConfig{
			Profiles: map[string]RoutingProfileConfig{
				"default": {},
				"fast":    {},
				"strong":  {},
				"summary": {},
			},
		},
		Harness: HarnessConfig{
			MaxVerificationAttempts: 2,
			ForcePlanner:            true,
			ForceResearcher:         false,
			ContinuationPolicy:      "prompt",
		},
		Context: ContextConfig{
			MaxRecentMessages:        12,
			MaxArtifacts:             8,
			MaxRelevantFiles:         16,
			CompactAfterTurns:        12,
			CompactAfterToolCalls:    12,
			CompactAfterEstTokens:    12000,
			CompactAfterVerifyCycles: 2,
		},
		Memory: MemoryConfig{
			Enabled:  true,
			StateDir: ".yagent/state",
			MaxRuns:  20,
			MaxFacts: 50,
		},
		Benchmark: BenchmarkConfig{
			DefaultRuns: 2,
		},
		AgentCatalog: AgentCatalogConfig{
			Paths: []string{},
		},
		Agents: map[string]AgentOverride{},
	}
}

func (d *Duration) UnmarshalText(text []byte) error {
	parsed, err := time.ParseDuration(string(text))
	if err != nil {
		return fmt.Errorf("duration のパースに失敗しました: %w", err)
	}
	d.Duration = parsed
	return nil
}

func (d Duration) MarshalText() ([]byte, error) {
	return []byte(d.Duration.String()), nil
}

func (c Config) ResolveServer() (ServerTarget, error) {
	if len(c.Server.Servers) == 0 {
		return ServerTarget{}, fmt.Errorf("サーバー設定がありません")
	}

	target := c.Server.Default
	if target == "" {
		return c.Server.Servers[0], nil
	}

	for _, server := range c.Server.Servers {
		if server.Name == target {
			return server, nil
		}
	}

	return ServerTarget{}, fmt.Errorf("指定されたサーバー %q が見つかりません", target)
}

func (s ServerTarget) ResolvedToken() string {
	if s.Token != "" {
		return s.Token
	}
	if s.TokenEnv == "" {
		return ""
	}
	return os.Getenv(s.TokenEnv)
}

func validateModelAPI(api string) error {
	switch api {
	case "", "chat_completions", "responses":
		return nil
	default:
		return fmt.Errorf("server.servers[].api は chat_completions または responses を指定してください: %q", api)
	}
}

func validateContinuationPolicy(policy string) error {
	switch policy {
	case "", "prompt", "allow", "deny":
		return nil
	default:
		return fmt.Errorf("harness.continuation_policy は prompt, allow, deny のいずれかを指定してください: %q", policy)
	}
}

func validateFileRule(rule FileRuleConfig) error {
	switch rule.Decision {
	case "allow", "deny":
	default:
		return fmt.Errorf("file.rules[].decision は allow または deny を指定してください: %q", rule.Decision)
	}
	if rule.Path == "" && len(rule.Paths) == 0 {
		return fmt.Errorf("file.rules[] には path または paths が必要です")
	}
	return nil
}

func validatePermissionRule(rule PermissionRuleConfig) error {
	switch rule.Decision {
	case "allow", "require_approval", "deny":
	default:
		return fmt.Errorf("permission.rules[].decision は allow, require_approval, deny のいずれかを指定してください: %q", rule.Decision)
	}
	if rule.Tool == "" &&
		rule.Action == "" &&
		rule.ResourceKind == "" &&
		rule.Risk == "" &&
		rule.Resource == "" &&
		len(rule.Resources) == 0 &&
		rule.Agent == "" &&
		rule.SideEffect == "" &&
		len(rule.SideEffects) == 0 {
		return fmt.Errorf("permission.rules[] には少なくとも 1 つの selector が必要です")
	}
	return nil
}
