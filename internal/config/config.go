package config

import (
	"fmt"
	"os"
	"time"

	"github.com/pelletier/go-toml/v2"
)

type Config struct {
	Server       ServerConfig             `toml:"server"`
	File         FileConfig               `toml:"file"`
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
	Name    string   `toml:"name"`
	URL     string   `toml:"url"`
	Token   string   `toml:"token"`
	Model   string   `toml:"model"`
	Timeout Duration `toml:"timeout"`
}

type FileConfig struct {
	AllowPaths []string `toml:"allow_paths"`
}

type ExecutionConfig struct {
	MaxParallelAgents int      `toml:"max_parallel_agents"`
	MaxHandoffDepth   int      `toml:"max_handoff_depth"`
	DefaultTimeout    Duration `toml:"default_timeout"`
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
	Server         string `toml:"server"`
	Model          string `toml:"model"`
	FallbackServer string `toml:"fallback_server"`
	FallbackModel  string `toml:"fallback_model"`
}

type HarnessConfig struct {
	MaxVerificationAttempts int  `toml:"max_verification_attempts"`
	ForcePlanner            bool `toml:"force_planner"`
	ForceResearcher         bool `toml:"force_researcher"`
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
		return Default(), nil
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

	return cfg, nil
}

func Default() Config {
	return Config{
		Server: ServerConfig{
			Default: "default",
			Servers: []ServerTarget{
				{
					Name:    "default",
					URL:     "http://localhost:1234",
					Timeout: Duration{Duration: 20 * time.Minute},
				},
			},
		},
		File: FileConfig{
			AllowPaths: []string{},
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
