package processisolation

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"yagent/internal/config"
)

type Mode string

const (
	ModeCommand  Mode = "command"
	ModeMCPStdio Mode = "mcp_stdio"
	ProtocolV1        = "yagent.process.v1"
)

type Request struct {
	Protocol     string            `json:"protocol"`
	Mode         Mode              `json:"mode"`
	Command      string            `json:"command"`
	Args         []string          `json:"args,omitempty"`
	Cwd          string            `json:"cwd,omitempty"`
	Env          map[string]string `json:"env,omitempty"`
	ReadPaths    []string          `json:"read_paths,omitempty"`
	WritePaths   []string          `json:"write_paths,omitempty"`
	AllowNetwork bool              `json:"allow_network"`
}

type CommandSpec struct {
	Command string
	Args    []string
	Cwd     string
	Env     map[string]string
}

type Wrapper interface {
	Wrap(Request) (CommandSpec, error)
}

type Backend struct {
	kind   string
	runner string
	args   []string
}

func New(cfg config.ProcessIsolationConfig) (*Backend, error) {
	backend := strings.TrimSpace(cfg.Backend)
	runner := strings.TrimSpace(cfg.Runner)
	if backend != "" && runner != "" {
		return nil, fmt.Errorf("process isolation backend and runner are mutually exclusive")
	}
	if backend != "" && backend != "macos-sandbox-exec" {
		return nil, fmt.Errorf("unsupported process isolation backend %q", backend)
	}
	if runner == "" {
		if backend != "macos-sandbox-exec" || runtime.GOOS != "darwin" {
			return nil, nil
		}
		return &Backend{kind: backend}, nil
	}
	return &Backend{kind: "proxy", runner: runner, args: append([]string(nil), cfg.Args...)}, nil
}

func (b *Backend) Wrap(request Request) (CommandSpec, error) {
	if b == nil || (b.kind != "macos-sandbox-exec" && strings.TrimSpace(b.runner) == "") {
		return CommandSpec{}, fmt.Errorf("process isolation backend is not configured")
	}
	if request.Mode != ModeCommand && request.Mode != ModeMCPStdio {
		return CommandSpec{}, fmt.Errorf("unsupported process isolation mode %q", request.Mode)
	}
	if strings.TrimSpace(request.Command) == "" {
		return CommandSpec{}, fmt.Errorf("isolated command is required")
	}
	request.Protocol = ProtocolV1
	if b.kind == "macos-sandbox-exec" {
		return macSandboxCommand(request)
	}
	payload, err := json.Marshal(request)
	if err != nil {
		return CommandSpec{}, err
	}
	args := append(append([]string(nil), b.args...), "--yagent-process-spec", base64.RawURLEncoding.EncodeToString(payload), "--", request.Command)
	args = append(args, request.Args...)
	return CommandSpec{Command: b.runner, Args: args, Cwd: request.Cwd, Env: cloneEnv(request.Env)}, nil
}

func macSandboxCommand(request Request) (CommandSpec, error) {
	profile := []string{
		"(version 1)",
		"(deny default)",
		"(allow process-fork)",
		"(allow process-exec)",
		"(allow signal)",
		"(allow file-read* (subpath \"/System\"))",
		"(allow file-read* (subpath \"/usr\"))",
		"(allow file-read* (subpath \"/bin\"))",
		"(allow file-read* (subpath \"/sbin\"))",
		"(allow file-read* (subpath \"/private/var/db/dyld\"))",
		"(allow file-read* (literal \"/dev/null\"))",
		"(allow file-read* (literal \"/dev/urandom\"))",
		"(allow file-write* (literal \"/dev/null\"))",
	}
	readPaths := append([]string(nil), request.ReadPaths...)
	if len(readPaths) == 0 && strings.TrimSpace(request.Cwd) != "" {
		readPaths = append(readPaths, request.Cwd)
	}
	for _, path := range readPaths {
		path, err := filepath.Abs(path)
		if err != nil {
			return CommandSpec{}, err
		}
		profile = append(profile, `(allow file-read* (subpath "`+sandboxPath(path)+`"))`)
	}
	commandPath := request.Command
	if !filepath.IsAbs(commandPath) {
		if resolved, err := exec.LookPath(commandPath); err == nil {
			commandPath = resolved
		}
	}
	if filepath.IsAbs(commandPath) {
		profile = append(profile, `(allow file-read* (literal "`+sandboxPath(commandPath)+`"))`)
	}
	for _, path := range request.WritePaths {
		path, err := filepath.Abs(path)
		if err != nil {
			return CommandSpec{}, err
		}
		profile = append(profile, `(allow file-write* (subpath "`+sandboxPath(path)+`"))`)
	}
	if request.AllowNetwork {
		profile = append(profile, "(allow network*)")
	}
	command, err := exec.LookPath("sandbox-exec")
	if err != nil {
		return CommandSpec{}, fmt.Errorf("macOS sandbox-exec is unavailable: %w", err)
	}
	return CommandSpec{Command: command, Args: []string{"-p", strings.Join(profile, "\n"), request.Command}, Cwd: request.Cwd}, nil
}

func sandboxPath(path string) string {
	return strings.ReplaceAll(strings.ReplaceAll(path, `\`, `\\`), `"`, `\"`)
}

func cloneEnv(source map[string]string) map[string]string {
	if len(source) == 0 {
		return nil
	}
	copy := make(map[string]string, len(source))
	for key, value := range source {
		copy[key] = value
	}
	return copy
}
