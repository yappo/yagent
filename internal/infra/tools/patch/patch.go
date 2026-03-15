package patch

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"yagent/internal/domain"
	"yagent/internal/infra/policy"
)

type Tool struct {
	paths    *policy.PathPolicy
	engine   domain.PolicyEngine
	approver domain.Approver
}

type operation struct {
	Path    string `json:"path"`
	OldText string `json:"old_text"`
	NewText string `json:"new_text"`
}

func New(paths *policy.PathPolicy, engine domain.PolicyEngine, approver domain.Approver) domain.Tool {
	return &Tool{paths: paths, engine: engine, approver: approver}
}

func (t *Tool) Definition() domain.ToolDefinition {
	return domain.ToolDefinition{
		Name:        "patch_apply",
		Description: "構造化テキストパッチを適用します。各 operation は old_text を new_text に置換します。",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"operations": map[string]any{
					"type": "array",
					"items": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"path":     map[string]any{"type": "string"},
							"old_text": map[string]any{"type": "string"},
							"new_text": map[string]any{"type": "string"},
						},
						"required": []string{"path", "old_text", "new_text"},
					},
				},
			},
			"required": []string{"operations"},
		},
		Metadata: map[string]any{"category": "patch"},
	}
}

func (t *Tool) Execute(ctx context.Context, call domain.ToolCall) domain.ToolResult {
	if err := authorize(ctx, t.engine, t.approver, call); err != nil {
		return failure(call, err.Error())
	}
	rawOps, ok := call.Arguments["operations"].([]any)
	if !ok || len(rawOps) == 0 {
		return failure(call, "operations パラメータが必要です")
	}
	ops := make([]operation, 0, len(rawOps))
	for _, raw := range rawOps {
		m, ok := raw.(map[string]any)
		if !ok {
			return failure(call, "operation の形式が不正です")
		}
		path, ok := m["path"].(string)
		if !ok || path == "" {
			return failure(call, "operation.path が必要です")
		}
		oldText, ok := m["old_text"].(string)
		if !ok {
			return failure(call, "operation.old_text が必要です")
		}
		newText, ok := m["new_text"].(string)
		if !ok {
			return failure(call, "operation.new_text が必要です")
		}
		ops = append(ops, operation{Path: path, OldText: oldText, NewText: newText})
	}

	applied := make([]string, 0, len(ops))
	for _, op := range ops {
		resolved, err := t.paths.ResolveWritableFile(op.Path)
		if err != nil {
			return failure(call, err.Error())
		}
		data, err := os.ReadFile(resolved)
		if err != nil {
			return failure(call, err.Error())
		}
		content := string(data)
		if !strings.Contains(content, op.OldText) {
			return failure(call, fmt.Sprintf("old_text が見つかりません: %s", resolved))
		}
		next := strings.Replace(content, op.OldText, op.NewText, 1)
		if err := os.WriteFile(resolved, []byte(next), 0o644); err != nil {
			return failure(call, err.Error())
		}
		applied = append(applied, resolved)
	}
	return marshalSuccess(call, map[string]any{"applied": applied})
}

func authorize(ctx context.Context, engine domain.PolicyEngine, approver domain.Approver, call domain.ToolCall) error {
	if engine == nil || approver == nil {
		return nil
	}
	decision, request, err := engine.Evaluate(ctx, call)
	if err != nil {
		return err
	}
	if rawOps, ok := call.Arguments["operations"].([]any); ok {
		request.Scope = fmt.Sprintf("%d patch operations", len(rawOps))
		request.Resource = request.Scope
	}
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

func marshalSuccess(call domain.ToolCall, value any) domain.ToolResult {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return failure(call, err.Error())
	}
	return domain.ToolResult{CallID: call.ID, Name: call.Name, Success: true, Output: string(data)}
}

func failure(call domain.ToolCall, output string) domain.ToolResult {
	return domain.ToolResult{CallID: call.ID, Name: call.Name, Success: false, Output: "エラー: " + output}
}
