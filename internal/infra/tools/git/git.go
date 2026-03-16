package git

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"

	"yagent/internal/domain"
	"yagent/internal/infra/policy"
	"yagent/internal/infra/tools/execctx"
)

type tool struct {
	name        string
	description string
	argsBuilder func(map[string]any) ([]string, error)
	paths       *policy.PathPolicy
	engine      domain.PolicyEngine
	approver    domain.Approver
}

func NewStatusTool(paths *policy.PathPolicy, engine domain.PolicyEngine, approver domain.Approver) domain.Tool {
	return &tool{
		name:        "git_status",
		description: "Git リポジトリの status --short を返します。",
		argsBuilder: func(_ map[string]any) ([]string, error) { return []string{"status", "--short"}, nil },
		paths:       paths, engine: engine, approver: approver,
	}
}

func NewDiffTool(paths *policy.PathPolicy, engine domain.PolicyEngine, approver domain.Approver) domain.Tool {
	return &tool{
		name:        "git_diff",
		description: "Git diff を返します。",
		argsBuilder: func(args map[string]any) ([]string, error) {
			result := []string{"diff"}
			if path, ok := args["path"].(string); ok && path != "" {
				result = append(result, "--", path)
			}
			return result, nil
		},
		paths: paths, engine: engine, approver: approver,
	}
}

func NewLogTool(paths *policy.PathPolicy, engine domain.PolicyEngine, approver domain.Approver) domain.Tool {
	return &tool{
		name:        "git_log",
		description: "Git log を返します。",
		argsBuilder: func(args map[string]any) ([]string, error) {
			limit := 10
			if v, ok := args["limit"].(float64); ok && int(v) > 0 {
				limit = int(v)
			}
			return []string{"log", fmt.Sprintf("-%d", limit), "--oneline", "--decorate"}, nil
		},
		paths: paths, engine: engine, approver: approver,
	}
}

func NewShowTool(paths *policy.PathPolicy, engine domain.PolicyEngine, approver domain.Approver) domain.Tool {
	return &tool{
		name:        "git_show",
		description: "commit または file の git show 結果を返します。",
		argsBuilder: func(args map[string]any) ([]string, error) {
			target, ok := args["target"].(string)
			if !ok || target == "" {
				return nil, fmt.Errorf("target パラメータが必要です")
			}
			return []string{"show", "--stat", "--oneline", target}, nil
		},
		paths: paths, engine: engine, approver: approver,
	}
}

func (t *tool) Definition() domain.ToolDefinition {
	props := map[string]any{
		"repo_path": map[string]any{"type": "string", "description": "対象 Git リポジトリのパス"},
	}
	required := []string{"repo_path"}
	switch t.name {
	case "git_diff":
		props["path"] = map[string]any{"type": "string", "description": "差分対象 path"}
	case "git_log":
		props["limit"] = map[string]any{"type": "integer", "description": "表示件数"}
	case "git_show":
		props["target"] = map[string]any{"type": "string", "description": "commit hash または object spec"}
		required = append(required, "target")
	}
	return domain.ToolDefinition{
		Name:        t.name,
		Description: t.description,
		Parameters: map[string]any{
			"type":       "object",
			"properties": props,
			"required":   required,
		},
		Metadata: map[string]any{"category": "git"},
	}
}

func (t *tool) Execute(ctx context.Context, call domain.ToolCall) domain.ToolResult {
	repoPath, ok := call.Arguments["repo_path"].(string)
	if !ok || repoPath == "" {
		return failure(call, "repo_path パラメータが必要です")
	}
	if err := authorize(ctx, t.engine, t.approver, call); err != nil {
		return failure(call, err.Error())
	}
	resolvedRepo, err := t.paths.ResolveDir(repoPath)
	if err != nil {
		return failure(call, err.Error())
	}
	args, err := t.argsBuilder(call.Arguments)
	if err != nil {
		return failure(call, err.Error())
	}
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = resolvedRepo
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return failure(call, fmt.Sprintf("git command failed: %v: %s", err, stderr.String()))
	}
	return domain.ToolResult{CallID: call.ID, Name: call.Name, Success: true, Output: stdout.String()}
}

func authorize(ctx context.Context, engine domain.PolicyEngine, approver domain.Approver, call domain.ToolCall) error {
	if engine == nil || approver == nil {
		return nil
	}
	decision, request, err := engine.Evaluate(ctx, call)
	if err != nil {
		return err
	}
	request.AgentID = execctx.AgentID(ctx)
	request.Purpose = execctx.Purpose(ctx)
	if decision == domain.PolicyAllow {
		return nil
	}
	if decision == domain.PolicyDeny {
		return fmt.Errorf("この操作は policy により拒否されました")
	}
	userDecision, err := approver.Approve(ctx, request)
	if err != nil {
		return err
	}
	if userDecision == domain.PermissionDeny {
		return fmt.Errorf("ユーザーによってキャンセルされました")
	}
	return nil
}

func failure(call domain.ToolCall, output string) domain.ToolResult {
	return domain.ToolResult{CallID: call.ID, Name: call.Name, Success: false, Output: "エラー: " + output}
}
