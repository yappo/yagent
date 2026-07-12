package setup

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"text/template"

	"yagent/internal/config"
	"yagent/internal/usecase/localmodel"
)

const (
	DefaultConfigPath  = ".yagent/config.toml"
	DefaultTasksPath   = ".yagent/tasks.toml"
	DefaultLocalURL    = "http://127.0.0.1:1234"
	DefaultLocalPreset = localmodel.PresetAuto
	DefaultLocalModel  = localmodel.DefaultQwen36Model
	DefaultGemma4Model = localmodel.DefaultGemma4Model
	DefaultOpenAIModel = "gpt-5.5"
)

type Options struct {
	WorkDir     string
	ConfigPath  string
	TasksPath   string
	LocalURL    string
	LocalPreset string
	LocalModel  string
	OpenAIModel string
	Force       bool
	DryRun      bool
	WriteConfig bool
	WriteTasks  bool
}

type Result struct {
	Files []FileResult
}

type FileResult struct {
	Path   string
	Kind   string
	Status string
	Bytes  int
}

func Run(options Options) (Result, error) {
	options = normalizeOptions(options)
	profile, err := localmodel.Resolve(options.LocalPreset, options.LocalModel)
	if err != nil {
		return Result{}, err
	}
	if options.LocalModel == "" {
		options.LocalModel = profile.DefaultModel
	}

	files := []targetFile{}
	if options.WriteConfig {
		files = append(files, targetFile{
			kind:    "config",
			path:    resolvePath(options.WorkDir, options.ConfigPath),
			content: renderConfig(options, profile.Generation),
		})
	}
	if options.WriteTasks {
		content, err := renderTasks(options.WorkDir)
		if err != nil {
			return Result{}, err
		}
		files = append(files, targetFile{
			kind:    "tasks",
			path:    resolvePath(options.WorkDir, options.TasksPath),
			content: content,
		})
	}

	result := Result{Files: make([]FileResult, 0, len(files))}
	for _, file := range files {
		status, err := writeTarget(file, options)
		if err != nil {
			return Result{}, err
		}
		result.Files = append(result.Files, FileResult{
			Path:   file.path,
			Kind:   file.kind,
			Status: status,
			Bytes:  len(file.content),
		})
	}
	return result, nil
}

func normalizeOptions(options Options) Options {
	if options.WorkDir == "" {
		if wd, err := os.Getwd(); err == nil {
			options.WorkDir = wd
		}
	}
	if options.ConfigPath == "" {
		options.ConfigPath = DefaultConfigPath
	}
	if options.TasksPath == "" {
		options.TasksPath = DefaultTasksPath
	}
	if options.LocalURL == "" {
		options.LocalURL = DefaultLocalURL
	}
	if options.LocalPreset == "" {
		options.LocalPreset = DefaultLocalPreset
	}
	if options.OpenAIModel == "" {
		options.OpenAIModel = DefaultOpenAIModel
	}
	if !options.WriteConfig && !options.WriteTasks {
		options.WriteConfig = true
		options.WriteTasks = true
	}
	return options
}

type targetFile struct {
	kind    string
	path    string
	content string
}

func resolvePath(workDir string, path string) string {
	if filepath.IsAbs(path) {
		return filepath.Clean(path)
	}
	return filepath.Join(workDir, path)
}

func writeTarget(file targetFile, options Options) (string, error) {
	_, statErr := os.Stat(file.path)
	exists := statErr == nil
	if statErr != nil && !errors.Is(statErr, os.ErrNotExist) {
		return "", fmt.Errorf("%s の確認に失敗しました: %w", file.path, statErr)
	}
	if exists && !options.Force {
		if options.DryRun {
			return "exists", nil
		}
		return "skipped", nil
	}
	if options.DryRun {
		if exists {
			return "would_overwrite", nil
		}
		return "would_create", nil
	}
	if err := os.MkdirAll(filepath.Dir(file.path), 0o755); err != nil {
		return "", fmt.Errorf("%s のディレクトリ作成に失敗しました: %w", file.path, err)
	}
	if err := os.WriteFile(file.path, []byte(file.content), 0o644); err != nil {
		return "", fmt.Errorf("%s の書き込みに失敗しました: %w", file.path, err)
	}
	if exists {
		return "overwritten", nil
	}
	return "created", nil
}

func renderConfig(options Options, generation config.GenerationConfig) string {
	data := struct {
		LocalURL        string
		LocalModel      string
		LocalGeneration string
		OpenAIModel     string
	}{
		LocalURL:        quoteTOMLString(options.LocalURL),
		LocalModel:      quoteTOMLString(options.LocalModel),
		LocalGeneration: renderGenerationConfig(generation),
		OpenAIModel:     quoteTOMLString(options.OpenAIModel),
	}
	return executeTemplate(configTemplate, data)
}

func renderGenerationConfig(generation config.GenerationConfig) string {
	lines := []string{}
	if generation.MaxOutputTokens > 0 {
		lines = append(lines, fmt.Sprintf("max_output_tokens = %d", generation.MaxOutputTokens))
	}
	appendFloat := func(name string, value *float64) {
		if value == nil {
			return
		}
		lines = append(lines, name+" = "+strconv.FormatFloat(*value, 'f', -1, 64))
	}
	appendFloat("temperature", generation.Temperature)
	appendFloat("top_p", generation.TopP)
	if generation.TopK > 0 {
		lines = append(lines, fmt.Sprintf("top_k = %d", generation.TopK))
	}
	appendFloat("min_p", generation.MinP)
	appendFloat("presence_penalty", generation.PresencePenalty)
	appendFloat("repetition_penalty", generation.RepetitionPenalty)
	return strings.Join(lines, "\n")
}

func renderTasks(workDir string) (string, error) {
	data := struct {
		Tasks []taskTemplateData
	}{
		Tasks: append(autoGoTasks(workDir), autoPackageTasks(workDir)...),
	}
	return executeTemplate(tasksTemplate, data), nil
}

type taskTemplateData struct {
	ID           string
	Description  string
	Command      string
	Args         []string
	Risk         string
	AllowNetwork bool
	Timeout      int
}

func autoGoTasks(workDir string) []taskTemplateData {
	if _, err := os.Stat(filepath.Join(workDir, "go.mod")); err != nil {
		return nil
	}
	return []taskTemplateData{
		{
			ID:          "go:test",
			Description: "Go の全テストを実行",
			Command:     "go",
			Args:        []string{"test", "./..."},
			Risk:        "medium",
			Timeout:     300,
		},
		{
			ID:          "go:build",
			Description: "Go の全 package をビルド",
			Command:     "go",
			Args:        []string{"build", "./..."},
			Risk:        "medium",
			Timeout:     300,
		},
	}
}

func autoPackageTasks(workDir string) []taskTemplateData {
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
	scripts := []struct {
		name        string
		description string
		risk        string
	}{
		{name: "test", description: "npm test script を実行", risk: "medium"},
		{name: "build", description: "npm build script を実行", risk: "medium"},
		{name: "lint", description: "npm lint script を実行", risk: "low"},
		{name: "typecheck", description: "npm typecheck script を実行", risk: "low"},
	}
	tasks := []taskTemplateData{}
	for _, script := range scripts {
		if _, ok := decoded.Scripts[script.name]; !ok {
			continue
		}
		tasks = append(tasks, taskTemplateData{
			ID:          "npm:" + script.name,
			Description: script.description,
			Command:     "npm",
			Args:        []string{"run", script.name},
			Risk:        script.risk,
			Timeout:     300,
		})
	}
	return tasks
}

func executeTemplate(source string, data any) string {
	funcs := template.FuncMap{
		"tomlString":      quoteTOMLString,
		"tomlStringArray": quoteTOMLStringArray,
	}
	tmpl := template.Must(template.New("setup").Funcs(funcs).Parse(source))
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		panic(err)
	}
	return buf.String()
}

func quoteTOMLString(value string) string {
	data, err := json.Marshal(value)
	if err != nil {
		return `""`
	}
	return string(data)
}

func quoteTOMLStringArray(values []string) string {
	quoted := make([]string, 0, len(values))
	for _, value := range values {
		quoted = append(quoted, quoteTOMLString(value))
	}
	return "[" + strings.Join(quoted, ", ") + "]"
}

const configTemplate = `# Generated by yagent setup.
# Start LM Studio's local server first, then run:
#   yagent doctor --runtime --probe-structured

[server]
default = "local"

[[server.servers]]
name = "local"
url = {{.LocalURL}}
token = ""
model = {{.LocalModel}}
api = "chat_completions"
timeout = "20m"

[server.servers.generation]
{{.LocalGeneration}}

[[server.servers]]
name = "openai"
url = "https://api.openai.com"
token = ""
token_env = "OPENAI_API_KEY"
model = {{.OpenAIModel}}
api = "responses"
timeout = "20m"

[server.servers.generation]
reasoning_effort = "high"
text_verbosity = "low"
parallel_tool_calls = true
store = false

[file]
# The current working directory is always added at runtime.
allow_paths = []
deny_paths = [".env", "*.pem"]

[[file.rules]]
decision = "deny"
paths = [".git", ".yagent/state/*"]

[permission]
[[permission.rules]]
tool = "fs_stat"
risk = "low"
decision = "allow"

[[permission.rules]]
tool = "task_run"
side_effect = "network_access"
decision = "deny"

[[permission.rules]]
tool = "fs_write"
agent = "researcher"
decision = "deny"

[execution]
max_parallel_agents = 2
max_handoff_depth = 2
default_timeout = "120s"

[features]
phase_harness = true
adaptive_compaction = true
role_routing = true
repo_memory = true

[routing.profiles.fast]
server = "local"
model = {{.LocalModel}}

[routing.profiles.strong]
server = "local"
model = {{.LocalModel}}
fallback_server = "openai"
fallback_model = {{.OpenAIModel}}

[routing.profiles.strong.generation]
reasoning_effort = "high"

[harness]
max_verification_attempts = 2
force_planner = true
force_researcher = false
continuation_policy = "prompt"

[context]
max_recent_messages = 12
max_artifacts = 8
max_relevant_files = 16
compact_after_turns = 12
compact_after_tool_calls = 12
compact_after_est_tokens = 12000
compact_after_verify_cycles = 2

[memory]
enabled = true
state_dir = ".yagent/state"
max_runs = 20
max_facts = 50

[benchmark]
default_runs = 2

[agent_catalog]
paths = []

[agents.coder]
model = {{.LocalModel}}
routing_profile = "strong"
`

const tasksTemplate = `# Generated by yagent setup.
# Repo tasks override auto-detected templates with the same id.
{{- if not .Tasks }}
#
# No go.mod or package.json harness tasks were detected.
# Add project commands as [[tasks]] entries.
{{- end }}
{{ range .Tasks }}
[[tasks]]
id = {{ tomlString .ID }}
description = {{ tomlString .Description }}
command = {{ tomlString .Command }}
args = {{ tomlStringArray .Args }}
read_paths = ["."]
write_paths = ["."]
risk = {{ tomlString .Risk }}
allow_network = {{ .AllowNetwork }}
timeout = {{ .Timeout }}
{{ end }}
# MCP servers can be exposed lazily through task_bind.
#
# [[mcpservers]]
# id = "docs"
# description = "Docs MCP server"
# transport = "stdio"
# command = "npx"
# args = ["-y", "@example/docs-mcp"]
# tool_prefix = "docs"
# trust = "untrusted"
# read_only_tools = ["search_docs"]
# parallel_safe_tools = ["search_docs"]
`
