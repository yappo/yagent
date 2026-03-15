package config

import (
	"fmt"
	"os"

	"github.com/pelletier/go-toml/v2"
)

type Config struct {
	Server ServerConfig `toml:"server"`
	File   FileConfig   `toml:"file"`
}

type ServerConfig struct {
	Default string         `toml:"default"`
	Servers []ServerTarget `toml:"servers"`
}

type ServerTarget struct {
	Name  string `toml:"name"`
	URL   string `toml:"url"`
	Token string `toml:"token"`
}

type FileConfig struct {
	AllowPaths []string `toml:"allow_paths"`
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

	return cfg, nil
}

func Default() Config {
	return Config{
		Server: ServerConfig{
			Default: "default",
			Servers: []ServerTarget{
				{
					Name: "default",
					URL:  "http://localhost:1234",
				},
			},
		},
		File: FileConfig{
			AllowPaths: []string{},
		},
	}
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
