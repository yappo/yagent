package app

import (
	"fmt"
	"os"

	"yagent/internal/config"
	"yagent/internal/domain"
	infraLLM "yagent/internal/infra/llm"
	filetools "yagent/internal/infra/tools/file"
	"yagent/internal/infra/tools/registry"
	chatusecase "yagent/internal/usecase/chat"
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
	validator := filetools.NewValidator(pwd, allowPaths)
	tools := registry.New(
		filetools.NewReadTool(validator, approver),
		filetools.NewWriteTool(validator, approver),
	)

	client := infraLLM.NewClient(server.URL, server.Token)

	return &Container{
		Config:      cfg,
		ChatService: chatusecase.NewService(client, tools, 20),
		WorkingDir:  pwd,
	}, nil
}
