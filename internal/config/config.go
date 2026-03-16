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

type AgentCatalogConfig struct {
	Paths []string `toml:"paths"`
}

type AgentOverride struct {
	Instruction  string   `toml:"instruction"`
	Model        string   `toml:"model"`
	AllowedTools []string `toml:"allowed_tools"`
	Disabled     bool     `toml:"disabled"`
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
