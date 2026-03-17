package catalog

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/pelletier/go-toml/v2"

	"yagent/internal/config"
	"yagent/internal/domain"
)

type Catalog struct {
	agents map[string]domain.AgentSpec
}

type fileAgentSpec struct {
	ID                      string            `toml:"id"`
	Name                    string            `toml:"name"`
	Description             string            `toml:"description"`
	Instruction             string            `toml:"instruction"`
	Mode                    domain.AgentMode  `toml:"mode"`
	AllowedTools            []string          `toml:"allowed_tools"`
	ReadOnly                bool              `toml:"read_only"`
	InputSchema             map[string]any    `toml:"input_schema"`
	OutputSchema            map[string]any    `toml:"output_schema"`
	Model                   string            `toml:"model"`
	RoutingProfile          string            `toml:"routing_profile"`
	Timeout                 time.Duration     `toml:"timeout"`
	MaxTurns                int               `toml:"max_turns"`
	TokenBudget             int               `toml:"token_budget"`
	Tags                    []string          `toml:"tags"`
	TaskKinds               []domain.TaskKind `toml:"task_kinds"`
	Capabilities            []string          `toml:"capabilities"`
	PreferredPhases         []domain.RunPhase `toml:"preferred_phases"`
	ScopeHints              []string          `toml:"scope_hints"`
	VerificationRequired    *bool             `toml:"verification_required"`
	VerificationMaxAttempts int               `toml:"verification_max_attempts"`
}

func New(overrides map[string]config.AgentOverride) *Catalog {
	agents := builtInAgents()
	for id, override := range overrides {
		spec, ok := agents[id]
		if !ok {
			continue
		}
		agents[id] = applyOverride(spec, override)
	}
	for id, spec := range agents {
		agents[id] = normalizeAgentSpec(spec)
	}
	return &Catalog{agents: agents}
}

func (c *Catalog) LoadUserAgents(paths []string) error {
	for _, path := range paths {
		if err := c.loadPath(path); err != nil {
			return err
		}
	}
	return nil
}

func (c *Catalog) List() []domain.AgentSpec {
	names := make([]string, 0, len(c.agents))
	for name := range c.agents {
		names = append(names, name)
	}
	sort.Strings(names)

	agents := make([]domain.AgentSpec, 0, len(names))
	for _, name := range names {
		if c.agents[name].Disabled {
			continue
		}
		agents = append(agents, c.agents[name])
	}
	return agents
}

func (c *Catalog) Resolve(id string) (domain.AgentSpec, bool) {
	spec, ok := c.agents[id]
	if !ok || spec.Disabled {
		return domain.AgentSpec{}, false
	}
	return spec, true
}

func (c *Catalog) loadPath(path string) error {
	path = expandHome(path)
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("agent DSL の確認に失敗しました: %w", err)
	}

	if info.IsDir() {
		entries, err := os.ReadDir(path)
		if err != nil {
			return fmt.Errorf("agent DSL ディレクトリの読み込みに失敗しました: %w", err)
		}
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".toml") {
				continue
			}
			if err := c.loadFile(filepath.Join(path, entry.Name())); err != nil {
				return err
			}
		}
		return nil
	}

	return c.loadFile(path)
}

func expandHome(path string) string {
	if path == "" || path[0] != '~' {
		return path
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return path
	}
	if path == "~" {
		return home
	}
	if strings.HasPrefix(path, "~/") {
		return filepath.Join(home, path[2:])
	}
	return path
}

func (c *Catalog) loadFile(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("agent DSL の読み込みに失敗しました: %w", err)
	}

	var parsed fileAgentSpec
	if err := toml.Unmarshal(data, &parsed); err != nil {
		return fmt.Errorf("agent DSL のパースに失敗しました: %w", err)
	}
	if parsed.ID == "" {
		return fmt.Errorf("agent DSL に id が必要です: %s", path)
	}
	if existing, ok := c.agents[parsed.ID]; ok && existing.BuiltIn && !existing.AllowOverride {
		return fmt.Errorf("built-in agent %q は外部 DSL で上書きできません", parsed.ID)
	}

	spec := domain.AgentSpec{
		ID:              parsed.ID,
		Name:            fallback(parsed.Name, parsed.ID),
		Description:     parsed.Description,
		Instruction:     parsed.Instruction,
		Mode:            fallbackMode(parsed.Mode),
		AllowedTools:    append([]string(nil), parsed.AllowedTools...),
		ReadOnly:        parsed.ReadOnly,
		InputSchema:     cloneMap(parsed.InputSchema),
		OutputSchema:    cloneMap(parsed.OutputSchema),
		Model:           parsed.Model,
		RoutingProfile:  parsed.RoutingProfile,
		Timeout:         parsed.Timeout,
		MaxTurns:        parsed.MaxTurns,
		TokenBudget:     parsed.TokenBudget,
		Tags:            append([]string(nil), parsed.Tags...),
		TaskKinds:       append([]domain.TaskKind(nil), parsed.TaskKinds...),
		Capabilities:    append([]string(nil), parsed.Capabilities...),
		PreferredPhases: append([]domain.RunPhase(nil), parsed.PreferredPhases...),
		ScopeHints:      append([]string(nil), parsed.ScopeHints...),
	}
	if parsed.VerificationRequired != nil || parsed.VerificationMaxAttempts > 0 {
		spec.VerificationPolicy = domain.VerificationPolicy{
			Required:    parsed.VerificationRequired != nil && *parsed.VerificationRequired,
			MaxAttempts: parsed.VerificationMaxAttempts,
		}
	}
	c.agents[spec.ID] = normalizeAgentSpec(spec)
	return nil
}

func builtInAgents() map[string]domain.AgentSpec {
	return map[string]domain.AgentSpec{
		"manager": {
			ID:              "manager",
			Name:            "Manager",
			Description:     "ユーザー窓口として委譲と最終応答を担当します。",
			Instruction:     "You are the manager agent. Delegate research, testing, review, and implementation tasks when helpful. Keep the final response concise and grounded in available tool and agent results.",
			Mode:            domain.AgentModeManager,
			AllowedTools:    []string{"fs_read", "fs_write", "fs_list", "fs_stat", "search_text", "search_files", "git_status", "git_diff", "git_log", "git_show", "task_list", "task_run", "task_bind", "mcp__*", "patch_apply"},
			RoutingProfile:  "strong",
			Tags:            []string{"coordination", "finalize", "manager"},
			TaskKinds:       []domain.TaskKind{domain.TaskKindQuestion, domain.TaskKindResearch, domain.TaskKindDocs, domain.TaskKindReview, domain.TaskKindTest, domain.TaskKindMutate},
			Capabilities:    []string{"coordination", "synthesis", "response"},
			PreferredPhases: []domain.RunPhase{domain.RunPhaseExecute, domain.RunPhaseFinalize},
			ScopeHints:      []string{"conversation", "handoff orchestration", "final response"},
			PhasePolicies: []domain.PhasePolicy{
				{Phase: domain.RunPhasePlan, RequiredAgentIDs: []string{"planner"}, ForceDelegation: true},
				{Phase: domain.RunPhaseExecute, RequiredAgentIDs: []string{"coder"}, ForceDelegation: true},
				{Phase: domain.RunPhaseVerify, RequiredAgentIDs: []string{"tester", "reviewer"}, ForceDelegation: true},
			},
			MaxTurns:      200,
			BuiltIn:       true,
			AllowOverride: true,
		},
		"planner": {
			ID:              "planner",
			Name:            "Planner",
			Description:     "タスク分解を担当します。",
			Instruction:     "Break the task into a practical plan with explicit constraints and deliverables. Do not delegate to coder for simple repository inspection tasks. Prefer direct read-only tools such as fs_list and fs_read.",
			Mode:            domain.AgentModeTool,
			ReadOnly:        true,
			RoutingProfile:  "fast",
			Tags:            []string{"plan", "planning", "strategy"},
			TaskKinds:       []domain.TaskKind{domain.TaskKindQuestion, domain.TaskKindResearch, domain.TaskKindDocs, domain.TaskKindReview, domain.TaskKindTest, domain.TaskKindMutate},
			Capabilities:    []string{"planning", "decomposition"},
			PreferredPhases: []domain.RunPhase{domain.RunPhasePlan},
			ScopeHints:      []string{"execution planning", "agent selection"},
			AllowedTools:    []string{"fs_read", "fs_list", "fs_stat", "search_text", "search_files", "git_status", "git_diff", "git_log", "git_show", "task_list", "task_bind", "mcp__*"},
			MaxTurns:        200,
			BuiltIn:         true,
		},
		"researcher": {
			ID:              "researcher",
			Name:            "Researcher",
			Description:     "関連ファイルの探索と要点抽出を担当します。",
			Instruction:     "Inspect available context and files, then return only the most relevant findings. Prefer fs_list and fs_read instead of asking another agent to write scripts.",
			Mode:            domain.AgentModeTool,
			ReadOnly:        true,
			RoutingProfile:  "fast",
			Tags:            []string{"research", "analysis", "inspect"},
			TaskKinds:       []domain.TaskKind{domain.TaskKindQuestion, domain.TaskKindResearch, domain.TaskKindDocs},
			Capabilities:    []string{"inspection", "repository reading"},
			PreferredPhases: []domain.RunPhase{domain.RunPhaseExecute},
			ScopeHints:      []string{"file discovery", "focused context prep"},
			AllowedTools:    []string{"fs_read", "fs_list", "fs_stat", "search_text", "search_files", "git_status", "git_diff", "git_log", "git_show", "task_list", "task_bind", "mcp__*"},
			MaxTurns:        200,
			BuiltIn:         true,
		},
		"coder": {
			ID:                 "coder",
			Name:               "Coder",
			Description:        "実装ターンを主担当します。",
			Instruction:        "Implement the requested change directly when enough context is available. Prefer precise file edits and mention verification status. Do not delegate planning back to planner when the task is already an implementation handoff.",
			Mode:               domain.AgentModeHandoff,
			RoutingProfile:     "strong",
			VerificationPolicy: domain.VerificationPolicy{Required: true, MaxAttempts: 2},
			Tags:               []string{"code", "implement", "edit", "patch"},
			TaskKinds:          []domain.TaskKind{domain.TaskKindMutate, domain.TaskKindDocs},
			Capabilities:       []string{"implementation", "workspace edits"},
			PreferredPhases:    []domain.RunPhase{domain.RunPhaseExecute, domain.RunPhaseRecover},
			ScopeHints:         []string{"code changes", "repo updates"},
			AllowedTools:       []string{"fs_read", "fs_write", "fs_list", "fs_stat", "search_text", "search_files", "git_status", "git_diff", "git_log", "git_show", "task_list", "task_run", "task_bind", "mcp__*", "patch_apply"},
			MaxTurns:           200,
			BuiltIn:            true,
		},
		"tester": {
			ID:              "tester",
			Name:            "Tester",
			Description:     "検証と要約を担当します。",
			Instruction:     "Verify behavior using available read-only context and report any validation gaps clearly.",
			Mode:            domain.AgentModeTool,
			ReadOnly:        true,
			RoutingProfile:  "fast",
			Tags:            []string{"test", "verify", "validation", "regression"},
			TaskKinds:       []domain.TaskKind{domain.TaskKindTest, domain.TaskKindMutate},
			Capabilities:    []string{"verification", "task execution"},
			PreferredPhases: []domain.RunPhase{domain.RunPhaseVerify},
			ScopeHints:      []string{"regression checks", "validation"},
			AllowedTools:    []string{"fs_read", "fs_list", "fs_stat", "search_text", "search_files", "git_status", "git_diff", "git_log", "git_show", "task_list", "task_run", "task_bind", "mcp__*"},
			MaxTurns:        200,
			BuiltIn:         true,
		},
		"reviewer": {
			ID:              "reviewer",
			Name:            "Reviewer",
			Description:     "リスクと回帰確認を担当します。",
			Instruction:     "Review changes for bugs, regressions, and missing tests. Focus on findings first.",
			Mode:            domain.AgentModeTool,
			ReadOnly:        true,
			RoutingProfile:  "strong",
			Tags:            []string{"review", "audit", "risk", "regression"},
			TaskKinds:       []domain.TaskKind{domain.TaskKindReview, domain.TaskKindMutate, domain.TaskKindDocs},
			Capabilities:    []string{"review", "risk assessment"},
			PreferredPhases: []domain.RunPhase{domain.RunPhaseVerify},
			ScopeHints:      []string{"bug finding", "regression review"},
			AllowedTools:    []string{"fs_read", "fs_list", "fs_stat", "search_text", "search_files", "git_status", "git_diff", "git_log", "git_show", "task_list", "task_bind", "mcp__*"},
			MaxTurns:        200,
			BuiltIn:         true,
		},
	}
}

func applyOverride(spec domain.AgentSpec, override config.AgentOverride) domain.AgentSpec {
	if override.Instruction != "" {
		spec.Instruction = override.Instruction
	}
	if override.Model != "" {
		spec.Model = override.Model
	}
	if override.RoutingProfile != "" {
		spec.RoutingProfile = override.RoutingProfile
	}
	if len(override.AllowedTools) > 0 {
		spec.AllowedTools = append([]string(nil), override.AllowedTools...)
	}
	spec.Disabled = override.Disabled
	return spec
}

func fallbackMode(mode domain.AgentMode) domain.AgentMode {
	if mode == "" {
		return domain.AgentModeTool
	}
	return mode
}

func fallback(value, defaultValue string) string {
	if value == "" {
		return defaultValue
	}
	return value
}

func cloneMap(input map[string]any) map[string]any {
	if len(input) == 0 {
		return nil
	}
	cloned := make(map[string]any, len(input))
	for key, value := range input {
		cloned[key] = value
	}
	return cloned
}

func normalizeAgentSpec(spec domain.AgentSpec) domain.AgentSpec {
	spec.Tags = uniqueStrings(spec.Tags)
	spec.TaskKinds = normalizeTaskKinds(spec)
	spec.Capabilities = normalizeCapabilities(spec)
	spec.PreferredPhases = normalizePreferredPhases(spec)
	spec.ScopeHints = uniqueStrings(spec.ScopeHints)
	if len(spec.ScopeHints) == 0 {
		spec.ScopeHints = defaultScopeHints(spec)
	}
	if spec.VerificationPolicy.MaxAttempts < 0 {
		spec.VerificationPolicy.MaxAttempts = 0
	}
	return spec
}

func normalizeTaskKinds(spec domain.AgentSpec) []domain.TaskKind {
	if len(spec.TaskKinds) > 0 {
		return uniqueTaskKinds(spec.TaskKinds)
	}
	kinds := []domain.TaskKind{}
	switch spec.Mode {
	case domain.AgentModeManager:
		kinds = append(kinds, domain.TaskKindQuestion, domain.TaskKindResearch, domain.TaskKindDocs, domain.TaskKindReview, domain.TaskKindTest, domain.TaskKindMutate)
	case domain.AgentModeHandoff:
		kinds = append(kinds, domain.TaskKindMutate, domain.TaskKindDocs)
	default:
		if spec.ReadOnly {
			kinds = append(kinds, domain.TaskKindQuestion, domain.TaskKindResearch, domain.TaskKindDocs)
		} else {
			kinds = append(kinds, domain.TaskKindQuestion, domain.TaskKindResearch, domain.TaskKindDocs, domain.TaskKindMutate)
		}
	}
	for _, tag := range spec.Tags {
		switch strings.ToLower(tag) {
		case "docs", "readme", "documentation":
			kinds = append(kinds, domain.TaskKindDocs)
		case "review", "audit", "risk":
			kinds = append(kinds, domain.TaskKindReview)
		case "test", "verify", "validation", "regression":
			kinds = append(kinds, domain.TaskKindTest)
		case "research", "analysis", "inspect":
			kinds = append(kinds, domain.TaskKindResearch)
		case "code", "implement", "edit", "patch":
			kinds = append(kinds, domain.TaskKindMutate)
		}
	}
	return uniqueTaskKinds(kinds)
}

func normalizeCapabilities(spec domain.AgentSpec) []string {
	values := append([]string(nil), spec.Capabilities...)
	values = append(values, allowedToolGroups(spec.AllowedTools)...)
	if len(values) == 0 {
		switch spec.Mode {
		case domain.AgentModeManager:
			values = append(values, "coordination")
		case domain.AgentModeHandoff:
			values = append(values, "implementation")
		default:
			values = append(values, "analysis")
		}
	}
	return uniqueStrings(values)
}

func normalizePreferredPhases(spec domain.AgentSpec) []domain.RunPhase {
	if len(spec.PreferredPhases) > 0 {
		return uniqueRunPhases(spec.PreferredPhases)
	}
	phases := []domain.RunPhase{}
	switch spec.Mode {
	case domain.AgentModeManager:
		phases = append(phases, domain.RunPhaseExecute, domain.RunPhaseFinalize)
	case domain.AgentModeHandoff:
		phases = append(phases, domain.RunPhaseExecute, domain.RunPhaseRecover)
	default:
		phases = append(phases, domain.RunPhasePlan, domain.RunPhaseExecute, domain.RunPhaseVerify)
	}
	for _, kind := range normalizeTaskKinds(spec) {
		switch kind {
		case domain.TaskKindReview, domain.TaskKindTest:
			phases = append(phases, domain.RunPhaseVerify)
		case domain.TaskKindMutate:
			phases = append(phases, domain.RunPhaseExecute, domain.RunPhaseRecover)
		case domain.TaskKindResearch, domain.TaskKindDocs, domain.TaskKindQuestion:
			phases = append(phases, domain.RunPhasePlan, domain.RunPhaseExecute)
		}
	}
	return uniqueRunPhases(phases)
}

func defaultScopeHints(spec domain.AgentSpec) []string {
	values := []string{}
	for _, tag := range spec.Tags {
		values = append(values, tag)
	}
	if spec.ReadOnly {
		values = append(values, "read-only")
	}
	if spec.Mode == domain.AgentModeHandoff {
		values = append(values, "write-capable")
	}
	return uniqueStrings(values)
}

func allowedToolGroups(names []string) []string {
	groups := []string{}
	for _, name := range names {
		switch {
		case strings.HasPrefix(name, "fs_"):
			groups = append(groups, "filesystem")
		case strings.HasPrefix(name, "git_"):
			groups = append(groups, "git")
		case strings.HasPrefix(name, "task_"):
			groups = append(groups, "task")
		case strings.HasPrefix(name, "mcp__"):
			groups = append(groups, "mcp")
		case name == "patch_apply":
			groups = append(groups, "patch")
		}
	}
	return uniqueStrings(groups)
}

func uniqueStrings(values []string) []string {
	out := make([]string, 0, len(values))
	seen := map[string]struct{}{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		key := strings.ToLower(value)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, value)
	}
	return out
}

func uniqueTaskKinds(values []domain.TaskKind) []domain.TaskKind {
	out := make([]domain.TaskKind, 0, len(values))
	seen := map[domain.TaskKind]struct{}{}
	for _, value := range values {
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func uniqueRunPhases(values []domain.RunPhase) []domain.RunPhase {
	out := make([]domain.RunPhase, 0, len(values))
	seen := map[domain.RunPhase]struct{}{}
	for _, value := range values {
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}
