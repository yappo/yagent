package taskcatalog

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/pelletier/go-toml/v2"

	"yagent/internal/domain"
)

const (
	repoTasksPath = ".yagent/tasks.toml"
)

type Catalog struct {
	tasks map[string]domain.TaskDefinition
	order []string
}

type tasksFile struct {
	Tasks      []taskEntry      `toml:"tasks"`
	MCPServers []mcpServerEntry `toml:"mcpservers"`
}

type taskEntry struct {
	ID           string   `toml:"id"`
	Description  string   `toml:"description"`
	Command      string   `toml:"command"`
	Args         []string `toml:"args"`
	Cwd          string   `toml:"cwd"`
	ReadPaths    []string `toml:"read_paths"`
	WritePaths   []string `toml:"write_paths"`
	Risk         string   `toml:"risk"`
	AllowNetwork bool     `toml:"allow_network"`
	Timeout      int      `toml:"timeout"`
}

type mcpServerEntry struct {
	ID           string            `toml:"id"`
	Description  string            `toml:"description"`
	Transport    string            `toml:"transport"`
	Command      string            `toml:"command"`
	Args         []string          `toml:"args"`
	Cwd          string            `toml:"cwd"`
	Env          map[string]string `toml:"env"`
	Risk         string            `toml:"risk"`
	AllowNetwork bool              `toml:"allow_network"`
	Timeout      int               `toml:"timeout"`
	ToolPrefix   string            `toml:"tool_prefix"`
	ParallelSafe bool              `toml:"parallel_safe"`
	IncludeTools []string          `toml:"include_tools"`
	ExcludeTools []string          `toml:"exclude_tools"`
}

func New(workDir string) (*Catalog, error) {
	merged := map[string]domain.TaskDefinition{}
	order := []string{}

	addAll := func(items []domain.TaskDefinition) {
		for _, item := range items {
			if item.ID == "" {
				continue
			}
			if _, ok := merged[item.ID]; !ok {
				order = append(order, item.ID)
			}
			merged[item.ID] = item
		}
	}

	if userPath, ok := defaultUserTasksPath(); ok {
		items, err := loadFile(userPath, workDir)
		if err != nil {
			return nil, err
		}
		addAll(items)
	}
	items, err := loadFile(filepath.Join(workDir, repoTasksPath), workDir)
	if err != nil {
		return nil, err
	}
	addAll(items)
	slices.Sort(order)

	return &Catalog{tasks: merged, order: order}, nil
}

func (c *Catalog) List(_ context.Context) []domain.TaskDefinition {
	items := make([]domain.TaskDefinition, 0, len(c.order))
	for _, id := range c.order {
		items = append(items, c.tasks[id])
	}
	return items
}

func (c *Catalog) Get(_ context.Context, id string) (domain.TaskDefinition, bool) {
	item, ok := c.tasks[id]
	return item, ok
}

func loadFile(path string, baseDir string) ([]domain.TaskDefinition, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("task 設定の読み込みに失敗しました: %w", err)
	}

	var decoded tasksFile
	if err := toml.Unmarshal(data, &decoded); err != nil {
		return nil, fmt.Errorf("task 設定のパースに失敗しました: %w", err)
	}

	result := make([]domain.TaskDefinition, 0, len(decoded.Tasks))
	for _, entry := range decoded.Tasks {
		cwd := entry.Cwd
		if cwd == "" {
			cwd = baseDir
		} else if !filepath.IsAbs(cwd) {
			cwd = filepath.Join(baseDir, cwd)
		}
		result = append(result, domain.TaskDefinition{
			ID:          entry.ID,
			Description: entry.Description,
			Kind:        domain.TaskSpecKindCommand,
			Command: &domain.CommandTaskSpec{
				Command:      entry.Command,
				Args:         append([]string(nil), entry.Args...),
				Cwd:          cwd,
				ReadPaths:    resolveTaskPaths(baseDir, cwd, entry.ReadPaths),
				WritePaths:   resolveTaskPaths(baseDir, cwd, entry.WritePaths),
				Risk:         normalizeRisk(entry.Risk),
				AllowNetwork: entry.AllowNetwork,
				Timeout:      entry.Timeout,
			},
			Source: path,
		})
	}
	for _, entry := range decoded.MCPServers {
		cwd := entry.Cwd
		if cwd == "" {
			cwd = baseDir
		} else if !filepath.IsAbs(cwd) {
			cwd = filepath.Join(baseDir, cwd)
		}
		transport := domain.MCPTransport(entry.Transport)
		if transport == "" {
			transport = domain.MCPTransportStdio
		}
		toolPrefix := entry.ToolPrefix
		if toolPrefix == "" {
			toolPrefix = entry.ID
		}
		result = append(result, domain.TaskDefinition{
			ID:          entry.ID,
			Description: entry.Description,
			Kind:        domain.TaskSpecKindMCPServer,
			MCPServer: &domain.MCPServerSpec{
				Transport:    transport,
				Command:      entry.Command,
				Args:         append([]string(nil), entry.Args...),
				Cwd:          cwd,
				Env:          cloneEnv(entry.Env),
				Risk:         normalizeRisk(entry.Risk),
				AllowNetwork: entry.AllowNetwork,
				Timeout:      entry.Timeout,
				ToolPrefix:   toolPrefix,
				ParallelSafe: entry.ParallelSafe,
				IncludeTools: append([]string(nil), entry.IncludeTools...),
				ExcludeTools: append([]string(nil), entry.ExcludeTools...),
			},
			Source: path,
		})
	}
	return result, nil
}

func defaultUserTasksPath() (string, bool) {
	if path := os.Getenv("YAGENT_TASKS_USER_PATH"); path != "" {
		return path, true
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return "", false
	}
	return filepath.Join(home, ".config", "yagent", "tasks.toml"), true
}

func normalizeRisk(risk string) string {
	switch risk {
	case "low", "medium", "high":
		return risk
	default:
		return "medium"
	}
}

func cloneEnv(env map[string]string) map[string]string {
	if len(env) == 0 {
		return nil
	}
	cloned := make(map[string]string, len(env))
	for key, value := range env {
		cloned[key] = value
	}
	return cloned
}

func resolveTaskPaths(baseDir string, cwd string, paths []string) []string {
	if len(paths) == 0 {
		return nil
	}
	resolved := make([]string, 0, len(paths))
	for _, item := range paths {
		item = filepath.Clean(item)
		switch {
		case item == ".":
			item = cwd
		case filepath.IsAbs(item):
		case strings.HasPrefix(item, "./") || strings.HasPrefix(item, "../"):
			item = filepath.Join(cwd, item)
		default:
			item = filepath.Join(baseDir, item)
		}
		resolved = append(resolved, filepath.Clean(item))
	}
	return resolved
}
