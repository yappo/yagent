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
	ID           string           `toml:"id"`
	Name         string           `toml:"name"`
	Description  string           `toml:"description"`
	Instruction  string           `toml:"instruction"`
	Mode         domain.AgentMode `toml:"mode"`
	AllowedTools []string         `toml:"allowed_tools"`
	ReadOnly     bool             `toml:"read_only"`
	InputSchema  map[string]any   `toml:"input_schema"`
	OutputSchema map[string]any   `toml:"output_schema"`
	Model        string           `toml:"model"`
	Timeout      time.Duration    `toml:"timeout"`
	MaxTurns     int              `toml:"max_turns"`
	TokenBudget  int              `toml:"token_budget"`
	Tags         []string         `toml:"tags"`
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
		ID:           parsed.ID,
		Name:         fallback(parsed.Name, parsed.ID),
		Description:  parsed.Description,
		Instruction:  parsed.Instruction,
		Mode:         fallbackMode(parsed.Mode),
		AllowedTools: append([]string(nil), parsed.AllowedTools...),
		ReadOnly:     parsed.ReadOnly,
		InputSchema:  cloneMap(parsed.InputSchema),
		OutputSchema: cloneMap(parsed.OutputSchema),
		Model:        parsed.Model,
		Timeout:      parsed.Timeout,
		MaxTurns:     parsed.MaxTurns,
		TokenBudget:  parsed.TokenBudget,
		Tags:         append([]string(nil), parsed.Tags...),
	}
	c.agents[spec.ID] = spec
	return nil
}

func builtInAgents() map[string]domain.AgentSpec {
	return map[string]domain.AgentSpec{
		"manager": {
			ID:            "manager",
			Name:          "Manager",
			Description:   "ユーザー窓口として委譲と最終応答を担当します。",
			Instruction:   "You are the manager agent. Delegate research, testing, review, and implementation tasks when helpful. Keep the final response concise and grounded in available tool and agent results.",
			Mode:          domain.AgentModeManager,
			AllowedTools:  []string{"fs_read", "fs_write", "fs_list", "fs_stat", "search_text", "search_files", "git_status", "git_diff", "git_log", "git_show", "task_list", "task_run", "patch_apply"},
			MaxTurns:      200,
			BuiltIn:       true,
			AllowOverride: true,
		},
		"planner": {
			ID:           "planner",
			Name:         "Planner",
			Description:  "タスク分解を担当します。",
			Instruction:  "Break the task into a practical plan with explicit constraints and deliverables. Do not delegate to coder for simple repository inspection tasks. Prefer direct read-only tools such as fs_list and fs_read.",
			Mode:         domain.AgentModeTool,
			ReadOnly:     true,
			AllowedTools: []string{"fs_read", "fs_list", "fs_stat", "search_text", "search_files", "git_status", "git_diff", "git_log", "git_show", "task_list"},
			MaxTurns:     200,
			BuiltIn:      true,
		},
		"researcher": {
			ID:           "researcher",
			Name:         "Researcher",
			Description:  "関連ファイルの探索と要点抽出を担当します。",
			Instruction:  "Inspect available context and files, then return only the most relevant findings. Prefer fs_list and fs_read instead of asking another agent to write scripts.",
			Mode:         domain.AgentModeTool,
			ReadOnly:     true,
			AllowedTools: []string{"fs_read", "fs_list", "fs_stat", "search_text", "search_files", "git_status", "git_diff", "git_log", "git_show", "task_list"},
			MaxTurns:     200,
			BuiltIn:      true,
		},
		"coder": {
			ID:           "coder",
			Name:         "Coder",
			Description:  "実装ターンを主担当します。",
			Instruction:  "Implement the requested change directly when enough context is available. Prefer precise file edits and mention verification status. Do not delegate planning back to planner when the task is already an implementation handoff.",
			Mode:         domain.AgentModeHandoff,
			AllowedTools: []string{"fs_read", "fs_write", "fs_list", "fs_stat", "search_text", "search_files", "git_status", "git_diff", "git_log", "git_show", "task_list", "task_run", "patch_apply"},
			MaxTurns:     200,
			BuiltIn:      true,
		},
		"tester": {
			ID:           "tester",
			Name:         "Tester",
			Description:  "検証と要約を担当します。",
			Instruction:  "Verify behavior using available read-only context and report any validation gaps clearly.",
			Mode:         domain.AgentModeTool,
			ReadOnly:     true,
			AllowedTools: []string{"fs_read", "fs_list", "fs_stat", "search_text", "search_files", "git_status", "git_diff", "git_log", "git_show", "task_list", "task_run"},
			MaxTurns:     200,
			BuiltIn:      true,
		},
		"reviewer": {
			ID:           "reviewer",
			Name:         "Reviewer",
			Description:  "リスクと回帰確認を担当します。",
			Instruction:  "Review changes for bugs, regressions, and missing tests. Focus on findings first.",
			Mode:         domain.AgentModeTool,
			ReadOnly:     true,
			AllowedTools: []string{"fs_read", "fs_list", "fs_stat", "search_text", "search_files", "git_status", "git_diff", "git_log", "git_show", "task_list"},
			MaxTurns:     200,
			BuiltIn:      true,
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
