package policy

import (
	"context"
	"fmt"
	"strings"

	"yagent/internal/domain"
)

type Engine struct{}

func NewEngine() *Engine {
	return &Engine{}
}

func (e *Engine) Evaluate(_ context.Context, call domain.ToolCall) (domain.PolicyDecision, domain.PermissionRequest, error) {
	req := domain.PermissionRequest{
		ToolName: call.Name,
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
		req.Risk = "high"
		req.Scope = stringValue(call.Arguments["task_id"])
		req.Resource = req.Scope
		req.SideEffects = []string{"process_spawn"}
		req.Summary = "登録済みタスクを実行します"
	case "task_bind":
		req.Operation = "MCP server bind"
		req.Action = "spawn"
		req.ResourceKind = "mcp_server"
		req.Risk = "high"
		req.Scope = stringValue(call.Arguments["task_id"])
		req.Resource = req.Scope
		req.SideEffects = []string{"process_spawn"}
		req.Summary = "登録済み MCP server を起動して bind します"
	case "git_status", "git_diff", "git_log", "git_show":
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
			req.ResourceKind = "mcp_tool"
			req.Risk = "high"
			req.Scope = call.Name
			req.Resource = call.Name
			req.SideEffects = []string{"llm_disclosure", "external_tool_call"}
			req.Summary = "bind 済み MCP tool を実行します"
			break
		}
		return domain.PolicyDeny, req, fmt.Errorf("未対応の policy tool です: %s", call.Name)
	}

	if req.Scope == "" {
		return domain.PolicyDeny, req, fmt.Errorf("permission scope を解決できませんでした")
	}
	return domain.PolicyRequireApproval, req, nil
}

func stringValue(v any) string {
	s, _ := v.(string)
	return s
}

func boolValue(v any) bool {
	b, _ := v.(bool)
	return b
}
