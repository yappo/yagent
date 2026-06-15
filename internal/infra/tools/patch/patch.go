package patch

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"yagent/internal/domain"
	"yagent/internal/infra/policy"
	"yagent/internal/infra/tools/diffpreview"
	"yagent/internal/infra/tools/execctx"
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

type preparedOperation struct {
	operation
	Resolved string
	Next     string
	Preview  string
	Stats    diffpreview.Stats
}

func New(paths *policy.PathPolicy, engine domain.PolicyEngine, approver domain.Approver) domain.Tool {
	return &Tool{paths: paths, engine: engine, approver: approver}
}

func (t *Tool) Definition() domain.ToolDefinition {
	return domain.ToolDefinition{
		Name:             "patch_apply",
		Description:      "構造化テキストパッチを適用します。各 operation は old_text を new_text に置換します。",
		CapabilityGroup:  "patch",
		Risk:             "high",
		RequiresApproval: true,
		MutatesWorkspace: true,
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
		Semantics: domain.ToolSemantics{
			Class:           domain.ToolClassMutate,
			ReusePolicy:     domain.ToolReuseNever,
			DuplicatePolicy: domain.ToolDuplicateAllow,
			Freshness:       domain.ToolFreshnessPolicy{Strategy: domain.ToolFreshnessNone},
			SideEffectClass: domain.SideEffectWorkspace,
			Source:          "patch",
			IdentityArgs:    []string{"operations"},
			SourceLimit:     1,
		},
	}
}

func (t *Tool) Execute(ctx context.Context, call domain.ToolCall) domain.ToolResult {
	ops, err := parseOperations(call)
	if err != nil {
		return failure(call, err.Error())
	}

	prepared := make([]preparedOperation, 0, len(ops))
	nextByPath := make(map[string]string)
	for _, op := range ops {
		resolved, err := t.paths.ResolveWritableFile(op.Path)
		if err != nil {
			return failure(call, err.Error())
		}
		content, ok := nextByPath[resolved]
		if !ok {
			data, err := os.ReadFile(resolved)
			if err != nil {
				return failure(call, err.Error())
			}
			content = string(data)
		}
		if !strings.Contains(content, op.OldText) {
			return failure(call, fmt.Sprintf("old_text が見つかりません: %s", resolved))
		}
		next := strings.Replace(content, op.OldText, op.NewText, 1)
		nextByPath[resolved] = next
		prepared = append(prepared, preparedOperation{
			operation: op,
			Resolved:  resolved,
			Next:      next,
			Preview:   diffpreview.Replacement(resolved, op.OldText, op.NewText),
			Stats:     diffpreview.ReplacementStats(op.OldText, op.NewText),
		})
	}

	if err := authorize(ctx, t.engine, t.approver, call, prepared); err != nil {
		return failure(call, err.Error())
	}

	applied := make([]string, 0, len(prepared))
	for _, op := range prepared {
		if err := os.WriteFile(op.Resolved, []byte(op.Next), 0o644); err != nil {
			return failure(call, err.Error())
		}
		applied = append(applied, op.Resolved)
	}
	return marshalSuccess(call, map[string]any{"applied": applied})
}

func parseOperations(call domain.ToolCall) ([]operation, error) {
	rawOps, ok := call.Arguments["operations"].([]any)
	if !ok || len(rawOps) == 0 {
		return nil, fmt.Errorf("operations パラメータが必要です")
	}
	ops := make([]operation, 0, len(rawOps))
	for _, raw := range rawOps {
		m, ok := raw.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("operation の形式が不正です")
		}
		path, ok := m["path"].(string)
		if !ok || path == "" {
			return nil, fmt.Errorf("operation.path が必要です")
		}
		oldText, ok := m["old_text"].(string)
		if !ok {
			return nil, fmt.Errorf("operation.old_text が必要です")
		}
		newText, ok := m["new_text"].(string)
		if !ok {
			return nil, fmt.Errorf("operation.new_text が必要です")
		}
		ops = append(ops, operation{Path: path, OldText: oldText, NewText: newText})
	}
	return ops, nil
}

func authorize(ctx context.Context, engine domain.PolicyEngine, approver domain.Approver, call domain.ToolCall, prepared []preparedOperation) error {
	if engine == nil || approver == nil {
		return nil
	}
	decision, request, err := engine.Evaluate(ctx, call)
	if err != nil {
		return err
	}
	execctx.FillPermissionRequest(ctx, &request)
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
	previews := make([]string, 0, len(prepared))
	changedFiles := make(map[string]struct{}, len(prepared))
	for _, op := range prepared {
		previews = append(previews, op.Preview)
		changedFiles[op.Resolved] = struct{}{}
		request.Additions += op.Stats.Additions
		request.Deletions += op.Stats.Deletions
	}
	request.PreviewKind = "patch"
	request.Preview = diffpreview.Combine(previews)
	request.ChangeFiles = len(changedFiles)
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
