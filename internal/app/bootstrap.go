package app

import (
	"fmt"
	"os"

	"yagent/internal/config"
	"yagent/internal/domain"
	infraLLM "yagent/internal/infra/llm"
	"yagent/internal/infra/policy"
	fstools "yagent/internal/infra/tools/fs"
	gittools "yagent/internal/infra/tools/git"
	patchtools "yagent/internal/infra/tools/patch"
	"yagent/internal/infra/tools/registry"
	searchtools "yagent/internal/infra/tools/search"
	tasktools "yagent/internal/infra/tools/task"
	chatusecase "yagent/internal/usecase/chat"
	"yagent/internal/usecase/taskcatalog"
)

type Container struct {
	Config      config.Config
	ChatService *chatusecase.Service
	WorkingDir  string
}

func Build(configPath string, approver domain.Approver) (*Container, error) {
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

	allowPaths := append([]string{pwd}, cfg.File.AllowPaths...)
	pathPolicy := policy.NewPathPolicy(pwd, allowPaths)
	policyEngine := policy.NewEngine()
	catalog, err := taskcatalog.New(pwd)
	if err != nil {
		return nil, err
	}
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
		tasktools.NewListTool(catalog),
		tasktools.NewRunTool(catalog, policyEngine, approver),
		patchtools.New(pathPolicy, policyEngine, approver),
	)

	client := infraLLM.NewClient(server.URL, server.Token, server.Timeout)

	return &Container{
		Config:      cfg,
		ChatService: chatusecase.NewService(client, tools, cfg.Agent.MaxIterations),
		WorkingDir:  pwd,
	}, nil
}
