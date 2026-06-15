package app

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"

	"yagent/internal/config"
	"yagent/internal/domain"
	agentcatalog "yagent/internal/infra/agents/catalog"
	"yagent/internal/infra/audit"
	infraLLM "yagent/internal/infra/llm"
	"yagent/internal/infra/logging"
	mcpstdio "yagent/internal/infra/mcp/stdio"
	"yagent/internal/infra/policy"
	"yagent/internal/infra/state"
	fstools "yagent/internal/infra/tools/fs"
	gittools "yagent/internal/infra/tools/git"
	mcptools "yagent/internal/infra/tools/mcp"
	patchtools "yagent/internal/infra/tools/patch"
	"yagent/internal/infra/tools/registry"
	searchtools "yagent/internal/infra/tools/search"
	tasktools "yagent/internal/infra/tools/task"
	"yagent/internal/usecase/contextengine"
	"yagent/internal/usecase/orchestrator"
	"yagent/internal/usecase/taskcatalog"
)

type Container struct {
	Config          config.Config
	Orchestrator    *orchestrator.Service
	Tools           domain.ToolExecutor
	TaskCatalog     domain.TaskCatalog
	MCPBindings     domain.MCPConnectionManager
	AgentCatalog    domain.AgentCatalog
	RunStore        domain.RunStateStore
	MemoryStore     domain.RepoMemoryStore
	WorkingDir      string
	DefaultModel    string
	RoutingProfiles []string
	Closer          io.Closer
}

type BuildOptions struct {
	LogPath string
}

func Build(configPath string, approver domain.Approver, options BuildOptions) (*Container, error) {
	cfg, err := config.Load(configPath)
	if err != nil {
		return nil, err
	}
	return BuildFromConfig(cfg, approver, options)
}

func BuildFromConfig(cfg config.Config, approver domain.Approver, options BuildOptions) (*Container, error) {
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
	pathPolicy := policy.NewPathPolicyWithRules(pwd, allowPaths, pathRulesFromConfig(cfg.File))
	policyEngine := policy.NewEngine(policyRulesFromConfig(cfg.Permission.Rules)...)
	var runStore *state.FileStore
	if cfg.Memory.Enabled {
		stateDir := cfg.Memory.StateDir
		if !filepath.IsAbs(stateDir) {
			stateDir = filepath.Join(pwd, stateDir)
		}
		runStore, err = state.NewFileStore(stateDir)
		if err != nil {
			return nil, err
		}
	}
	if runStore != nil && approver != nil {
		approver = audit.PermissionAuditApprover{Base: approver, Store: runStore}
	}
	memoryStore := domain.RepoMemoryStore(nil)
	if cfg.Features.RepoMemory {
		memoryStore = runStore
	}
	contextCfg := cfg.Context
	if !cfg.Features.AdaptiveCompaction {
		contextCfg.CompactAfterTurns = 1 << 30
		contextCfg.CompactAfterToolCalls = 1 << 30
		contextCfg.CompactAfterEstTokens = 1 << 30
		contextCfg.CompactAfterVerifyCycles = 1 << 30
	}
	contextEngine := contextengine.New(contextCfg, memoryStore, runStore, cfg.Memory.MaxFacts)

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
		gittools.NewBranchTool(pathPolicy, policyEngine, approver),
		gittools.NewBlameTool(pathPolicy, policyEngine, approver),
		gittools.NewFileHistoryTool(pathPolicy, policyEngine, approver),
		tasktools.NewListTool(taskCatalog, mcpBindings),
		tasktools.NewRunTool(taskCatalog, policyEngine, approver, memoryStore),
		tasktools.NewBindTool(taskCatalog, mcpBindings, policyEngine, approver),
		patchtools.New(pathPolicy, policyEngine, approver),
	)
	tools.RegisterProvider(mcptools.NewProvider(mcpBindings, policyEngine, approver))

	agents := agentcatalog.New(cfg.Agents)
	if err := agents.LoadUserAgents(cfg.AgentCatalog.Paths); err != nil {
		return nil, err
	}

	client, err := infraLLM.NewRouter(cfg)
	if err != nil {
		return nil, err
	}
	if runStore != nil {
		client.SetAuditStore(runStore)
	}

	return &Container{
		Config: cfg,
		Orchestrator: orchestrator.New(client, tools, agents, orchestrator.Config{
			MaxParallelAgents:       cfg.Execution.MaxParallelAgents,
			MaxHandoffDepth:         cfg.Execution.MaxHandoffDepth,
			MaxVerificationAttempts: cfg.Harness.MaxVerificationAttempts,
			DefaultTimeout:          cfg.Execution.DefaultTimeout.Duration,
			DefaultModel:            server.Model,
			DisablePhaseHarness:     !cfg.Features.PhaseHarness,
			ForcePlanner:            cfg.Harness.ForcePlanner,
			ForceResearcher:         cfg.Harness.ForceResearcher,
			ContinuationPolicy:      cfg.Harness.ContinuationPolicy,
			TraceSink:               logger,
			Approver:                approver,
			ContextEngine:           contextEngine,
			RunStore:                runStore,
			MemoryStore:             memoryStore,
			RuntimeStore:            runStore,
			ConversationStore:       runStore,
		}),
		Tools:           tools,
		TaskCatalog:     taskCatalog,
		MCPBindings:     mcpBindings,
		AgentCatalog:    agents,
		RunStore:        runStore,
		MemoryStore:     memoryStore,
		WorkingDir:      pwd,
		DefaultModel:    server.Model,
		RoutingProfiles: routingProfileNames(cfg.Routing.Profiles),
		Closer:          logger,
	}, nil
}

func routingProfileNames(profiles map[string]config.RoutingProfileConfig) []string {
	names := make([]string, 0, len(profiles))
	for name := range profiles {
		if name == "" {
			continue
		}
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func pathRulesFromConfig(cfg config.FileConfig) []policy.PathRule {
	rules := make([]policy.PathRule, 0, len(cfg.DenyPaths)+len(cfg.Rules))
	for _, path := range cfg.DenyPaths {
		if path == "" {
			continue
		}
		rules = append(rules, policy.PathRule{
			Decision: policy.PathDecisionDeny,
			Patterns: []string{path},
		})
	}
	for _, item := range cfg.Rules {
		patterns := append([]string(nil), item.Paths...)
		if item.Path != "" {
			patterns = append(patterns, item.Path)
		}
		decision := policy.PathDecisionDeny
		if item.Decision == "allow" {
			decision = policy.PathDecisionAllow
		}
		rules = append(rules, policy.PathRule{
			Decision: decision,
			Patterns: patterns,
		})
	}
	return rules
}

func policyRulesFromConfig(items []config.PermissionRuleConfig) []policy.Rule {
	rules := make([]policy.Rule, 0, len(items))
	for _, item := range items {
		resources := append([]string(nil), item.Resources...)
		if item.Resource != "" {
			resources = append(resources, item.Resource)
		}
		sideEffects := append([]string(nil), item.SideEffects...)
		if item.SideEffect != "" {
			sideEffects = append(sideEffects, item.SideEffect)
		}
		rules = append(rules, policy.Rule{
			Decision:     domain.PolicyDecision(item.Decision),
			Tool:         item.Tool,
			Action:       item.Action,
			ResourceKind: item.ResourceKind,
			Risk:         item.Risk,
			Resources:    resources,
			Agent:        item.Agent,
			SideEffects:  sideEffects,
		})
	}
	return rules
}
