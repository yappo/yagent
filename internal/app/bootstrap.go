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
	mcpstdio "yagent/internal/infra/mcp/stdio"
	"yagent/internal/infra/policy"
	fstools "yagent/internal/infra/tools/fs"
	gittools "yagent/internal/infra/tools/git"
	mcptools "yagent/internal/infra/tools/mcp"
	patchtools "yagent/internal/infra/tools/patch"
	"yagent/internal/infra/tools/registry"
	searchtools "yagent/internal/infra/tools/search"
	tasktools "yagent/internal/infra/tools/task"
	"yagent/internal/usecase/orchestrator"
	"yagent/internal/usecase/taskcatalog"
)

type Container struct {
	Config       config.Config
	Orchestrator *orchestrator.Service
	WorkingDir   string
	DefaultModel string
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
	pathPolicy := policy.NewPathPolicy(pwd, allowPaths)
	policyEngine := policy.NewEngine()

	taskCatalog, err := taskcatalog.New(pwd)
	if err != nil {
		return nil, err
	}
	mcpBindings := taskcatalog.NewMCPBindings(mcpstdio.NewFactory(logger))

	tools := registry.New(
		fstools.NewReadTool(pathPolicy, policyEngine, approver),
		fstools.NewWriteTool(pathPolicy, policyEngine, approver),
		fstools.NewListTool(pathPolicy, policyEngine, approver),
		fstools.NewStatTool(pathPolicy, policyEngine, approver),
		fstools.NewRemoveTool(pathPolicy, policyEngine, approver),
		fstools.NewMoveTool(pathPolicy, policyEngine, approver),
		searchtools.NewTextTool(pathPolicy, policyEngine, approver),
		searchtools.NewFilesTool(pathPolicy, policyEngine, approver),
		gittools.NewStatusTool(pathPolicy, policyEngine, approver),
		gittools.NewDiffTool(pathPolicy, policyEngine, approver),
		gittools.NewLogTool(pathPolicy, policyEngine, approver),
		gittools.NewShowTool(pathPolicy, policyEngine, approver),
		tasktools.NewListTool(taskCatalog, mcpBindings),
		tasktools.NewRunTool(taskCatalog, policyEngine, approver),
		tasktools.NewBindTool(taskCatalog, mcpBindings, policyEngine, approver),
		patchtools.New(pathPolicy, policyEngine, approver),
	)
	tools.RegisterProvider(mcptools.NewProvider(mcpBindings, policyEngine, approver))

	agents := agentcatalog.New(cfg.Agents)
	if err := agents.LoadUserAgents(cfg.AgentCatalog.Paths); err != nil {
		return nil, err
	}

	client := infraLLM.NewClient(server.URL, server.Token, server.Timeout.Duration)

	return &Container{
		Config: cfg,
		Orchestrator: orchestrator.New(client, tools, agents, orchestrator.Config{
			MaxParallelAgents: cfg.Execution.MaxParallelAgents,
			MaxHandoffDepth:   cfg.Execution.MaxHandoffDepth,
			DefaultTimeout:    cfg.Execution.DefaultTimeout.Duration,
			DefaultModel:      server.Model,
			TraceSink:         logger,
			Approver:          approver,
		}),
		WorkingDir:   pwd,
		DefaultModel: server.Model,
		Closer:       logger,
	}, nil
}
