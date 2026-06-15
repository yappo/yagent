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

func NewBranchTool(paths *policy.PathPolicy, engine domain.PolicyEngine, approver domain.Approver) domain.Tool {
	return &tool{
		name:        "git_branch",
		description: "Git branch 一覧を返します。",
		argsBuilder: func(_ map[string]any) ([]string, error) {
			return []string{"branch", "--all", "--verbose", "--no-abbrev"}, nil
		},
		paths: paths, engine: engine, approver: approver,
	}
}

func NewBlameTool(paths *policy.PathPolicy, engine domain.PolicyEngine, approver domain.Approver) domain.Tool {
	return &tool{
		name:        "git_blame",
		description: "指定ファイルの git blame を返します。",
		argsBuilder: func(args map[string]any) ([]string, error) {
			target, ok := args["path"].(string)
			if !ok || target == "" {
				return nil, fmt.Errorf("path パラメータが必要です")
			}
			result := []string{"blame", "--date=short"}
			start := intArg(args, "line_start", 0)
			end := intArg(args, "line_end", 0)
			if start > 0 {
				if end <= 0 {
					end = start
				}
				if end < start {
					return nil, fmt.Errorf("line_end は line_start 以上である必要があります")
				}
				result = append(result, "-L", fmt.Sprintf("%d,%d", start, end))
			}
			return append(result, "--", target), nil
		},
		paths: paths, engine: engine, approver: approver,
	}
}

func NewFileHistoryTool(paths *policy.PathPolicy, engine domain.PolicyEngine, approver domain.Approver) domain.Tool {
	return &tool{
		name:        "git_file_history",
		description: "指定ファイルの Git 履歴を --follow 付きで返します。",
		argsBuilder: func(args map[string]any) ([]string, error) {
			target, ok := args["path"].(string)
			if !ok || target == "" {
				return nil, fmt.Errorf("path パラメータが必要です")
			}
			limit := intArg(args, "limit", 20)
			if limit <= 0 {
				limit = 20
			}
			if limit > 200 {
				limit = 200
			}
			return []string{"log", fmt.Sprintf("-%d", limit), "--follow", "--date=short", "--pretty=format:%h %ad %an %s", "--", target}, nil
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
	case "git_blame":
		props["path"] = map[string]any{"type": "string", "description": "blame 対象 path"}
		props["line_start"] = map[string]any{"type": "integer", "description": "開始行"}
		props["line_end"] = map[string]any{"type": "integer", "description": "終了行"}
		required = append(required, "path")
	case "git_file_history":
		props["path"] = map[string]any{"type": "string", "description": "履歴対象 path"}
		props["limit"] = map[string]any{"type": "integer", "description": "表示件数"}
		required = append(required, "path")
	}
	return domain.ToolDefinition{
		Name:             t.name,
		Description:      t.description,
		CapabilityGroup:  "git_read",
		Risk:             "medium",
		RequiresApproval: true,
		Parameters: map[string]any{
			"type":       "object",
			"properties": props,
			"required":   required,
		},
		Metadata: map[string]any{"category": "git"},
		Semantics: domain.ToolSemantics{
			Class:           domain.ToolClassObserve,
			ReusePolicy:     domain.ToolReuseOnSuccess,
			DuplicatePolicy: domain.ToolDuplicateSuppressInflight,
			Freshness:       domain.ToolFreshnessPolicy{Strategy: domain.ToolFreshnessReadSet},
			SideEffectClass: domain.SideEffectNone,
			Source:          "git",
			ReadPathArgs:    []string{"repo_path"},
			IdentityArgs:    []string{"repo_path", "path", "limit", "target", "line_start", "line_end"},
			SourceLimit:     4,
		},
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
	execctx.FillPermissionRequest(ctx, &request)
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

func intArg(args map[string]any, key string, fallback int) int {
	value, ok := args[key]
	if !ok {
		return fallback
	}
	switch n := value.(type) {
	case int:
		return n
	case int64:
		return int(n)
	case float64:
		return int(n)
	default:
		return fallback
	}
}

func failure(call domain.ToolCall, output string) domain.ToolResult {
	return domain.ToolResult{CallID: call.ID, Name: call.Name, Success: false, Output: "エラー: " + output}
}
