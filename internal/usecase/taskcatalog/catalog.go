package taskcatalog

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
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

type LoadOptions struct {
	IncludeUserTasks bool
	ConfineToWorkDir bool
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
	ID                   string            `toml:"id"`
	Description          string            `toml:"description"`
	Transport            string            `toml:"transport"`
	Command              string            `toml:"command"`
	Args                 []string          `toml:"args"`
	Cwd                  string            `toml:"cwd"`
	Roots                []string          `toml:"roots"`
	Env                  map[string]string `toml:"env"`
	Risk                 string            `toml:"risk"`
	AllowNetwork         bool              `toml:"allow_network"`
	Timeout              int               `toml:"timeout"`
	ToolPrefix           string            `toml:"tool_prefix"`
	Trust                string            `toml:"trust"`
	TrustToolAnnotations bool              `toml:"trust_tool_annotations"`
	ParallelSafe         bool              `toml:"parallel_safe"`
	ReadOnlyTools        []string          `toml:"read_only_tools"`
	MutatingTools        []string          `toml:"mutating_tools"`
	ParallelSafeTools    []string          `toml:"parallel_safe_tools"`
	IncludeTools         []string          `toml:"include_tools"`
	ExcludeTools         []string          `toml:"exclude_tools"`
}

func New(workDir string) (*Catalog, error) {
	return NewWithOptions(workDir, LoadOptions{IncludeUserTasks: true})
}

func NewWithOptions(workDir string, options LoadOptions) (*Catalog, error) {
	merged := map[string]domain.TaskDefinition{}
	order := []string{}

	addAll := func(items []domain.TaskDefinition) error {
		for _, item := range items {
			if item.ID == "" {
				continue
			}
			if options.ConfineToWorkDir {
				if err := validateTaskWithinWorkDir(workDir, item); err != nil {
					return err
				}
			}
			if _, ok := merged[item.ID]; !ok {
				order = append(order, item.ID)
			}
			merged[item.ID] = item
		}
		return nil
	}

	if err := addAll(autoTasks(workDir)); err != nil {
		return nil, err
	}
	if options.IncludeUserTasks {
		if userPath, ok := defaultUserTasksPath(); ok {
			items, err := loadFile(userPath, workDir)
			if err != nil {
				return nil, err
			}
			if err := addAll(items); err != nil {
				return nil, err
			}
		}
	}
	items, err := loadFile(filepath.Join(workDir, repoTasksPath), workDir)
	if err != nil {
		return nil, err
	}
	if err := addAll(items); err != nil {
		return nil, err
	}
	slices.Sort(order)

	return &Catalog{tasks: merged, order: order}, nil
}

func validateTaskWithinWorkDir(workDir string, item domain.TaskDefinition) error {
	check := func(label, path string) error {
		if path == "" {
			return nil
		}
		if !pathWithinWorkDir(workDir, path) {
			return fmt.Errorf("isolated workspace rejects task %q %s outside %s: %s", item.ID, label, workDir, path)
		}
		return nil
	}
	if item.Command != nil {
		if err := check("cwd", item.Command.Cwd); err != nil {
			return err
		}
		for _, path := range append(append([]string(nil), item.Command.ReadPaths...), item.Command.WritePaths...) {
			if err := check("declared path", path); err != nil {
				return err
			}
		}
	}
	if item.MCPServer != nil {
		if err := check("cwd", item.MCPServer.Cwd); err != nil {
			return err
		}
		for _, path := range item.MCPServer.Roots {
			if err := check("root", path); err != nil {
				return err
			}
		}
	}
	return nil
}

func pathWithinWorkDir(workDir, path string) bool {
	root, err := filepath.Abs(workDir)
	if err != nil {
		return false
	}
	if resolved, err := filepath.EvalSymlinks(root); err == nil {
		root = resolved
	}
	candidate, err := filepath.Abs(path)
	if err != nil {
		return false
	}
	if resolved, err := filepath.EvalSymlinks(candidate); err == nil {
		candidate = resolved
	}
	rel, err := filepath.Rel(root, candidate)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
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
	if err := decodeTasksFile(data, &decoded); err != nil {
		return nil, fmt.Errorf("task 設定のパースに失敗しました: %w", err)
	}
	if err := validateTasksFile(path, decoded); err != nil {
		return nil, err
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
				Transport:            transport,
				Command:              entry.Command,
				Args:                 append([]string(nil), entry.Args...),
				Cwd:                  cwd,
				Env:                  cloneEnv(entry.Env),
				Risk:                 normalizeRisk(entry.Risk),
				AllowNetwork:         entry.AllowNetwork,
				Timeout:              entry.Timeout,
				ToolPrefix:           toolPrefix,
				Roots:                resolveTaskPaths(baseDir, cwd, entry.Roots),
				Trust:                normalizeTrust(entry.Trust),
				TrustToolAnnotations: entry.TrustToolAnnotations || normalizeTrust(entry.Trust) == "trusted",
				ParallelSafe:         entry.ParallelSafe,
				ReadOnlyTools:        append([]string(nil), entry.ReadOnlyTools...),
				MutatingTools:        append([]string(nil), entry.MutatingTools...),
				ParallelSafeTools:    append([]string(nil), entry.ParallelSafeTools...),
				IncludeTools:         append([]string(nil), entry.IncludeTools...),
				ExcludeTools:         append([]string(nil), entry.ExcludeTools...),
			},
			Source: path,
		})
	}
	return result, nil
}

func decodeTasksFile(data []byte, decoded *tasksFile) error {
	err := toml.NewDecoder(bytes.NewReader(data)).DisallowUnknownFields().Decode(decoded)
	if err == nil {
		return nil
	}
	var missing *toml.StrictMissingError
	if errors.As(err, &missing) {
		return fmt.Errorf("%w: %s", err, missing.String())
	}
	return err
}

func autoTasks(workDir string) []domain.TaskDefinition {
	items := []domain.TaskDefinition{}
	items = append(items, autoGoTasks(workDir)...)
	items = append(items, autoPackageJSONTasks(workDir)...)
	return items
}

func autoGoTasks(workDir string) []domain.TaskDefinition {
	if _, err := os.Stat(filepath.Join(workDir, "go.mod")); err != nil {
		return nil
	}
	return []domain.TaskDefinition{
		{
			ID:          "go:test",
			Description: "Go の全テストを実行",
			Kind:        domain.TaskSpecKindCommand,
			Command: &domain.CommandTaskSpec{
				Command:      "go",
				Args:         []string{"test", "./..."},
				Cwd:          workDir,
				ReadPaths:    []string{workDir},
				WritePaths:   []string{workDir},
				Risk:         "medium",
				AllowNetwork: false,
				Timeout:      300,
			},
			Source: "auto:go.mod",
		},
		{
			ID:          "go:build",
			Description: "Go の全 package をビルド",
			Kind:        domain.TaskSpecKindCommand,
			Command: &domain.CommandTaskSpec{
				Command:      "go",
				Args:         []string{"build", "./..."},
				Cwd:          workDir,
				ReadPaths:    []string{workDir},
				WritePaths:   []string{workDir},
				Risk:         "medium",
				AllowNetwork: false,
				Timeout:      300,
			},
			Source: "auto:go.mod",
		},
	}
}

func autoPackageJSONTasks(workDir string) []domain.TaskDefinition {
	data, err := os.ReadFile(filepath.Join(workDir, "package.json"))
	if err != nil {
		return nil
	}
	var decoded struct {
		Scripts map[string]string `json:"scripts"`
	}
	if err := json.Unmarshal(data, &decoded); err != nil {
		return nil
	}
	if len(decoded.Scripts) == 0 {
		return nil
	}
	knownScripts := []struct {
		Name        string
		Description string
		Risk        string
	}{
		{Name: "test", Description: "npm test script を実行", Risk: "medium"},
		{Name: "build", Description: "npm build script を実行", Risk: "medium"},
		{Name: "lint", Description: "npm lint script を実行", Risk: "low"},
		{Name: "typecheck", Description: "npm typecheck script を実行", Risk: "low"},
	}
	items := []domain.TaskDefinition{}
	for _, script := range knownScripts {
		if _, ok := decoded.Scripts[script.Name]; !ok {
			continue
		}
		items = append(items, domain.TaskDefinition{
			ID:          "npm:" + script.Name,
			Description: script.Description,
			Kind:        domain.TaskSpecKindCommand,
			Command: &domain.CommandTaskSpec{
				Command:      "npm",
				Args:         []string{"run", script.Name},
				Cwd:          workDir,
				ReadPaths:    []string{workDir},
				WritePaths:   []string{workDir},
				Risk:         script.Risk,
				AllowNetwork: false,
				Timeout:      300,
			},
			Source: "auto:package.json",
		})
	}
	return items
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

func normalizeTrust(trust string) string {
	switch trust {
	case "trusted":
		return "trusted"
	default:
		return "untrusted"
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

func validateTasksFile(path string, decoded tasksFile) error {
	seen := map[string]string{}
	for idx, entry := range decoded.Tasks {
		location := fmt.Sprintf("%s [[tasks]] #%d", path, idx+1)
		if err := validateTaskEntry(location, entry); err != nil {
			return err
		}
		if err := rememberTaskID(seen, location, entry.ID); err != nil {
			return err
		}
	}
	for idx, entry := range decoded.MCPServers {
		location := fmt.Sprintf("%s [[mcpservers]] #%d", path, idx+1)
		if err := validateMCPServerEntry(location, entry); err != nil {
			return err
		}
		if err := rememberTaskID(seen, location, entry.ID); err != nil {
			return err
		}
	}
	return nil
}

func validateTaskEntry(location string, entry taskEntry) error {
	if strings.TrimSpace(entry.ID) == "" {
		return fmt.Errorf("%s: id が必要です", location)
	}
	if strings.TrimSpace(entry.Command) == "" {
		return fmt.Errorf("%s id=%q: command が必要です", location, entry.ID)
	}
	if err := validateRisk(location, entry.ID, entry.Risk); err != nil {
		return err
	}
	if entry.Timeout < 0 {
		return fmt.Errorf("%s id=%q: timeout は 0 以上である必要があります", location, entry.ID)
	}
	if err := validatePathList(location, entry.ID, "read_paths", entry.ReadPaths); err != nil {
		return err
	}
	if err := validatePathList(location, entry.ID, "write_paths", entry.WritePaths); err != nil {
		return err
	}
	return nil
}

func validateMCPServerEntry(location string, entry mcpServerEntry) error {
	if strings.TrimSpace(entry.ID) == "" {
		return fmt.Errorf("%s: id が必要です", location)
	}
	transport := domain.MCPTransport(entry.Transport)
	if transport == "" {
		transport = domain.MCPTransportStdio
	}
	if transport != domain.MCPTransportStdio {
		return fmt.Errorf("%s id=%q: transport=%q は未対応です。対応値: stdio", location, entry.ID, entry.Transport)
	}
	if strings.TrimSpace(entry.Command) == "" {
		return fmt.Errorf("%s id=%q: command が必要です", location, entry.ID)
	}
	if err := validateRisk(location, entry.ID, entry.Risk); err != nil {
		return err
	}
	if entry.Trust != "" && normalizeTrust(entry.Trust) != entry.Trust {
		return fmt.Errorf("%s id=%q: trust=%q は不正です。対応値: untrusted, trusted", location, entry.ID, entry.Trust)
	}
	if entry.Timeout < 0 {
		return fmt.Errorf("%s id=%q: timeout は 0 以上である必要があります", location, entry.ID)
	}
	if entry.ToolPrefix != "" && strings.TrimSpace(entry.ToolPrefix) == "" {
		return fmt.Errorf("%s id=%q: tool_prefix が空白だけです", location, entry.ID)
	}
	if err := validatePathList(location, entry.ID, "roots", entry.Roots); err != nil {
		return err
	}
	for key := range entry.Env {
		if strings.TrimSpace(key) == "" {
			return fmt.Errorf("%s id=%q: env の key が空です", location, entry.ID)
		}
	}
	if err := validateStringList(location, entry.ID, "read_only_tools", entry.ReadOnlyTools); err != nil {
		return err
	}
	if err := validateStringList(location, entry.ID, "mutating_tools", entry.MutatingTools); err != nil {
		return err
	}
	if err := validateStringList(location, entry.ID, "parallel_safe_tools", entry.ParallelSafeTools); err != nil {
		return err
	}
	if err := validateStringList(location, entry.ID, "include_tools", entry.IncludeTools); err != nil {
		return err
	}
	if err := validateStringList(location, entry.ID, "exclude_tools", entry.ExcludeTools); err != nil {
		return err
	}
	if value := firstOverlap(entry.ReadOnlyTools, entry.MutatingTools); value != "" {
		return fmt.Errorf("%s id=%q: tool %q は read_only_tools と mutating_tools の両方に指定されています", location, entry.ID, value)
	}
	if value := firstOverlap(entry.IncludeTools, entry.ExcludeTools); value != "" {
		return fmt.Errorf("%s id=%q: tool %q は include_tools と exclude_tools の両方に指定されています", location, entry.ID, value)
	}
	return nil
}

func rememberTaskID(seen map[string]string, location string, id string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil
	}
	if previous, ok := seen[id]; ok {
		return fmt.Errorf("%s: id=%q が重複しています。既存定義: %s", location, id, previous)
	}
	seen[id] = location
	return nil
}

func validateRisk(location string, id string, risk string) error {
	if risk == "" || normalizeRisk(risk) == risk {
		return nil
	}
	return fmt.Errorf("%s id=%q: risk=%q は不正です。対応値: low, medium, high", location, id, risk)
}

func validatePathList(location string, id string, name string, values []string) error {
	for idx, value := range values {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("%s id=%q: %s[%d] が空です", location, id, name, idx)
		}
	}
	return nil
}

func validateStringList(location string, id string, name string, values []string) error {
	for idx, value := range values {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("%s id=%q: %s[%d] が空です", location, id, name, idx)
		}
	}
	return nil
}

func firstOverlap(left []string, right []string) string {
	seen := map[string]struct{}{}
	for _, value := range left {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		seen[value] = struct{}{}
	}
	for _, value := range right {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			return value
		}
	}
	return ""
}
