package app

import (
	"fmt"
	"io"
	"os"

	"yagent/internal/config"
	"yagent/internal/domain"
	agentcatalog "yagent/internal/infra/agents/catalog"
	infraLLM "yagent/internal/infra/llm"
	"yagent/internal/infra/logging"
	filetools "yagent/internal/infra/tools/file"
	"yagent/internal/infra/tools/registry"
	"yagent/internal/usecase/orchestrator"
)

type Container struct {
	Config       config.Config
	Orchestrator *orchestrator.Service
	WorkingDir   string
	Closer       io.Closer
}

type BuildOptions struct {
	LogPath string
}

func Build(configPath string, approver domain.Approver, options BuildOptions) (*Container, error) {
	cfg, err := config.Load(configPath)
	if err != nil {
		return nil, err
	}

	server, err := cfg.ResolveServer()
	if err != nil {
		return nil, err
	}

	pwd, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("カレントディレクトリの取得に失敗しました: %w", err)
	}

	var logger *logging.Logger
	if options.LogPath != "" {
		logger, err = logging.NewFileLogger(options.LogPath)
		if err != nil {
			return nil, fmt.Errorf("ログファイルのオープンに失敗しました: %w", err)
		}
		approver = logging.LoggingApprover{Base: approver, Logger: logger}
	}

	allowPaths := append([]string{pwd}, cfg.File.AllowPaths...)
	validator := filetools.NewValidator(pwd, allowPaths)
	tools := registry.New(
		filetools.NewReadTool(validator, approver),
		filetools.NewWriteTool(validator, approver),
		filetools.NewListTool(validator, approver),
	)

	catalog := agentcatalog.New(cfg.Agents)
	if err := catalog.LoadUserAgents(cfg.AgentCatalog.Paths); err != nil {
		return nil, err
	}

	client := infraLLM.NewClient(server.URL, server.Token, server.Timeout.Duration)

	return &Container{
		Config: cfg,
		Orchestrator: orchestrator.New(client, tools, catalog, orchestrator.Config{
			MaxParallelAgents: cfg.Execution.MaxParallelAgents,
			MaxHandoffDepth:   cfg.Execution.MaxHandoffDepth,
			DefaultTimeout:    cfg.Execution.DefaultTimeout.Duration,
			TraceSink:         logger,
			Approver:          approver,
		}),
		WorkingDir: pwd,
		Closer:     logger,
	}, nil
}
