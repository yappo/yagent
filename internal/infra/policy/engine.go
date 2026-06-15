package policy

import (
	"context"
	"fmt"
	"path"
	"path/filepath"
	"strings"

	"yagent/internal/domain"
)

type Engine struct {
	rules []Rule
}

type Rule struct {
	Decision     domain.PolicyDecision
	Tool         string
	Action       string
	ResourceKind string
	Risk         string
	Resources    []string
	Agent        string
	SideEffects  []string
}

func NewEngine(rules ...Rule) *Engine {
	return &Engine{rules: append([]Rule(nil), rules...)}
}

func (e *Engine) Evaluate(_ context.Context, call domain.ToolCall) (domain.PolicyDecision, domain.PermissionRequest, error) {
	req := domain.PermissionRequest{
		ToolName: call.Name,
		AgentID:  call.RequestedByAgentID,
		Purpose:  call.Purpose,
	}

	switch call.Name {
	case "fs_read":
		req.Operation = "ファイル読み取り"
		req.Action = "read"
		req.ResourceKind = "file"
		req.Risk = "medium"
		req.SideEffects = []string{"llm_disclosure"}
		req.Scope = stringValue(call.Arguments["path"])
		req.Resource = req.Scope
		req.Summary = "ファイル内容を読み取って LLM に渡します"
	case "fs_write":
		req.Operation = "ファイル書き込み"
		req.Action = "write"
		req.ResourceKind = "file"
		req.Risk = "high"
		req.SideEffects = []string{"filesystem_write"}
		req.Scope = stringValue(call.Arguments["path"])
		req.Resource = req.Scope
		req.Summary = "ファイルを新規作成または上書きします"
	case "fs_list":
		req.Operation = "ディレクトリ一覧"
		req.Action = "list"
		req.ResourceKind = "directory"
		req.Risk = "medium"
		req.SideEffects = []string{"llm_disclosure"}
		req.Scope = stringValue(call.Arguments["path"])
		req.Resource = req.Scope
		req.Summary = "ディレクトリ配下の一覧を取得して LLM に渡します"
	case "fs_stat":
		req.Operation = "ファイル情報取得"
		req.Action = "read_metadata"
		req.ResourceKind = "path"
		req.Risk = "low"
		req.SideEffects = []string{"llm_disclosure"}
		req.Scope = stringValue(call.Arguments["path"])
		req.Resource = req.Scope
		req.Summary = "パスのメタデータを取得して LLM に渡します"
	case "search_text":
		req.Operation = "テキスト検索"
		req.Action = "search"
		req.ResourceKind = "directory"
		req.Risk = "medium"
		req.SideEffects = []string{"llm_disclosure"}
		req.Scope = stringValue(call.Arguments["root"])
		req.Resource = req.Scope
		req.Summary = "基点ディレクトリ配下のテキストを検索して LLM に渡します"
	case "search_files":
		req.Operation = "ファイル探索"
		req.Action = "search"
		req.ResourceKind = "directory"
		req.Risk = "medium"
		req.SideEffects = []string{"llm_disclosure"}
		req.Scope = stringValue(call.Arguments["root"])
		req.Resource = req.Scope
		req.Summary = "基点ディレクトリ配下のファイル一覧を検索して LLM に渡します"
	case "fs_remove":
		req.Action = "remove"
		req.Scope = stringValue(call.Arguments["path"])
		req.Resource = req.Scope
		req.SideEffects = []string{"filesystem_delete"}
		if boolValue(call.Arguments["recursive"]) {
			req.Operation = "ディレクトリ再帰削除"
			req.ResourceKind = "directory"
			req.Risk = "high"
			req.Summary = "ディレクトリを再帰削除します"
		} else {
			req.Operation = "ファイル削除"
			req.ResourceKind = "file"
			req.Risk = "high"
			req.Summary = "ファイルを削除します"
		}
	case "fs_move":
		req.Operation = "ファイル移動"
		req.Action = "move"
		req.ResourceKind = "file"
		req.Risk = "high"
		req.SideEffects = []string{"filesystem_write"}
		req.Scope = fmt.Sprintf("%s -> %s", stringValue(call.Arguments["source_path"]), stringValue(call.Arguments["destination_path"]))
		req.Resource = req.Scope
		req.Summary = "ファイルを移動またはリネームします"
	case "patch_apply":
		req.Operation = "パッチ適用"
		req.Action = "patch"
		req.ResourceKind = "file"
		req.Risk = "high"
		req.SideEffects = []string{"filesystem_write"}
		req.Scope = "patch_operations"
		req.Resource = req.Scope
		req.Summary = "構造化パッチをファイルへ適用します"
	case "task_run":
		req.Operation = "タスク実行"
		req.Action = "execute"
		req.ResourceKind = "task"
		req.Risk = fallbackString(stringValue(call.Arguments["_policy_risk"]), "high")
		req.Scope = stringValue(call.Arguments["task_id"])
		req.Resource = req.Scope
		req.SideEffects = []string{"process_spawn"}
		if boolValue(call.Arguments["_policy_allow_network"]) {
			req.SideEffects = append(req.SideEffects, "network_access")
			req.Risk = "high"
		}
		req.Summary = "登録済みタスクを実行します"
	case "task_bind":
		req.Operation = "MCP server bind"
		req.Action = "spawn"
		req.ResourceKind = "mcp_server"
		req.Risk = fallbackString(stringValue(call.Arguments["_policy_risk"]), "high")
		req.Scope = stringValue(call.Arguments["task_id"])
		req.Resource = req.Scope
		req.SideEffects = []string{"process_spawn"}
		if boolValue(call.Arguments["_policy_allow_network"]) {
			req.SideEffects = append(req.SideEffects, "network_access")
			req.Risk = "high"
		}
		req.Summary = "登録済み MCP server を起動して bind します"
	case "git_status", "git_diff", "git_log", "git_show", "git_branch", "git_blame", "git_file_history":
		req.Operation = "Git 情報取得"
		req.Action = "git_read"
		req.ResourceKind = "repository"
		req.Risk = "medium"
		req.Scope = stringValue(call.Arguments["repo_path"])
		req.Resource = req.Scope
		req.SideEffects = []string{"llm_disclosure"}
		req.Summary = "Git リポジトリの情報を読み取って LLM に渡します"
	case "task_list":
		return domain.PolicyAllow, req, nil
	default:
		if strings.HasPrefix(call.Name, "mcp__") {
			req.Operation = "MCP tool 実行"
			req.Action = "mcp_call"
			if boolValue(call.Arguments["_policy_read_only"]) {
				req.Action = "mcp_read"
			}
			req.ResourceKind = "mcp_tool"
			req.Risk = fallbackString(stringValue(call.Arguments["_policy_risk"]), "high")
			taskID := stringValue(call.Arguments["_policy_task_id"])
			serverToolName := fallbackString(stringValue(call.Arguments["_policy_server_tool_name"]), call.Name)
			req.Scope = call.Name
			req.Resource = serverToolName
			if taskID != "" {
				req.Scope = taskID + ":" + serverToolName
				req.Resource = req.Scope
			}
			req.SideEffects = []string{"llm_disclosure", "external_tool_call"}
			if !boolValue(call.Arguments["_policy_read_only"]) {
				req.SideEffects = append(req.SideEffects, "external_mutation")
				req.Risk = "high"
			}
			if boolValue(call.Arguments["_policy_allow_network"]) {
				req.SideEffects = append(req.SideEffects, "network_access")
				req.Risk = "high"
			}
			req.Summary = "bind 済み MCP tool を実行します"
			break
		}
		return domain.PolicyDeny, req, fmt.Errorf("未対応の policy tool です: %s", call.Name)
	}

	if req.Scope == "" {
		return domain.PolicyDeny, req, fmt.Errorf("permission scope を解決できませんでした")
	}
	if decision, ok := e.matchRule(req); ok {
		return decision, req, nil
	}
	return domain.PolicyRequireApproval, req, nil
}

func (e *Engine) matchRule(request domain.PermissionRequest) (domain.PolicyDecision, bool) {
	for _, rule := range e.rules {
		if rule.matches(request) {
			return rule.Decision, true
		}
	}
	return "", false
}

func (r Rule) matches(request domain.PermissionRequest) bool {
	if r.Decision == "" {
		return false
	}
	if r.Tool != "" && !matchSelector(r.Tool, request.ToolName) {
		return false
	}
	if r.Action != "" && !matchSelector(r.Action, request.Action) {
		return false
	}
	if r.ResourceKind != "" && !matchSelector(r.ResourceKind, request.ResourceKind) {
		return false
	}
	if r.Risk != "" && !matchSelector(r.Risk, request.Risk) {
		return false
	}
	if r.Agent != "" && !matchSelector(r.Agent, request.AgentID) {
		return false
	}
	if len(r.Resources) > 0 && !matchAnyResource(r.Resources, request.Resource) {
		return false
	}
	if len(r.SideEffects) > 0 && !containsAll(request.SideEffects, r.SideEffects) {
		return false
	}
	return true
}

func matchAnyResource(patterns []string, resource string) bool {
	for _, patternValue := range patterns {
		if matchResource(patternValue, resource) {
			return true
		}
	}
	return false
}

func matchResource(patternValue string, resource string) bool {
	patternValue = strings.TrimSpace(patternValue)
	resource = strings.TrimSpace(resource)
	if patternValue == "" || resource == "" {
		return false
	}
	if patternValue == resource {
		return true
	}
	cleanResource := filepath.ToSlash(filepath.Clean(resource))
	cleanPattern := filepath.ToSlash(patternValue)
	if matched, err := path.Match(cleanPattern, cleanResource); err == nil && matched {
		return true
	}
	if base := path.Base(cleanResource); base != "." && base != "/" {
		if matched, err := path.Match(cleanPattern, base); err == nil && matched {
			return true
		}
	}
	return strings.HasPrefix(resource, patternValue)
}

func matchSelector(patternValue string, value string) bool {
	patternValue = strings.TrimSpace(patternValue)
	value = strings.TrimSpace(value)
	if patternValue == "" {
		return true
	}
	if patternValue == value {
		return true
	}
	matched, err := path.Match(patternValue, value)
	return err == nil && matched
}

func containsAll(values []string, required []string) bool {
	set := map[string]struct{}{}
	for _, value := range values {
		set[value] = struct{}{}
	}
	for _, value := range required {
		if _, ok := set[value]; !ok {
			return false
		}
	}
	return true
}

func stringValue(v any) string {
	s, _ := v.(string)
	return s
}

func boolValue(v any) bool {
	b, _ := v.(bool)
	return b
}

func fallbackString(value string, fallback string) string {
	if value != "" {
		return value
	}
	return fallback
}
