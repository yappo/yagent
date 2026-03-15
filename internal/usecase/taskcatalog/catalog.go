package taskcatalog

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"

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
	Tasks []taskEntry `toml:"tasks"`
}

type taskEntry struct {
	ID           string   `toml:"id"`
	Description  string   `toml:"description"`
	Command      string   `toml:"command"`
	Args         []string `toml:"args"`
	Cwd          string   `toml:"cwd"`
	Risk         string   `toml:"risk"`
	AllowNetwork bool     `toml:"allow_network"`
	Timeout      int      `toml:"timeout"`
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

	addAll(detectTemplates(workDir))
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
			ID:           entry.ID,
			Description:  entry.Description,
			Command:      entry.Command,
			Args:         append([]string(nil), entry.Args...),
			Cwd:          cwd,
			Risk:         normalizeRisk(entry.Risk),
			AllowNetwork: entry.AllowNetwork,
			Timeout:      entry.Timeout,
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

func detectTemplates(workDir string) []domain.TaskDefinition {
	var result []domain.TaskDefinition
	if exists(filepath.Join(workDir, "go.mod")) {
		result = append(result,
			task("go:test", "Go test を実行します", "go", []string{"test", "./..."}, workDir, "medium", false, 300),
			task("go:build", "Go build を実行します", "go", []string{"build", "./..."}, workDir, "medium", false, 300),
			task("go:mod-download", "Go 依存関係を取得します", "go", []string{"mod", "download"}, workDir, "high", true, 300),
		)
	}
	if exists(filepath.Join(workDir, "package.json")) {
		result = append(result,
			task("npm:install", "npm install を実行します", "npm", []string{"install"}, workDir, "high", true, 600),
			task("npm:test", "npm test を実行します", "npm", []string{"test"}, workDir, "medium", false, 600),
			task("npm:build", "npm run build を実行します", "npm", []string{"run", "build"}, workDir, "medium", false, 600),
		)
	}
	if exists(filepath.Join(workDir, "pyproject.toml")) || exists(filepath.Join(workDir, "requirements.txt")) {
		result = append(result,
			task("python:install", "Python 依存関係を取得します", "python3", []string{"-m", "pip", "install", "-r", "requirements.txt"}, workDir, "high", true, 600),
			task("python:test", "pytest を実行します", "pytest", nil, workDir, "medium", false, 600),
		)
	}
	if exists(filepath.Join(workDir, "Cargo.toml")) {
		result = append(result,
			task("cargo:test", "cargo test を実行します", "cargo", []string{"test"}, workDir, "medium", false, 600),
			task("cargo:build", "cargo build を実行します", "cargo", []string{"build"}, workDir, "medium", false, 600),
		)
	}
	return result
}

func task(id, desc, command string, args []string, cwd, risk string, allowNetwork bool, timeout int) domain.TaskDefinition {
	return domain.TaskDefinition{
		ID:           id,
		Description:  desc,
		Command:      command,
		Args:         append([]string(nil), args...),
		Cwd:          cwd,
		Risk:         normalizeRisk(risk),
		AllowNetwork: allowNetwork,
		Timeout:      timeout,
	}
}

func normalizeRisk(risk string) string {
	switch risk {
	case "low", "medium", "high":
		return risk
	default:
		return "medium"
	}
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
