package search

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"yagent/internal/domain"
	"yagent/internal/infra/policy"
	"yagent/internal/infra/tools/execctx"
)

type textTool struct {
	paths    *policy.PathPolicy
	engine   domain.PolicyEngine
	approver domain.Approver
}

type filesTool struct {
	paths    *policy.PathPolicy
	engine   domain.PolicyEngine
	approver domain.Approver
}

func NewTextTool(paths *policy.PathPolicy, engine domain.PolicyEngine, approver domain.Approver) domain.Tool {
	return &textTool{paths: paths, engine: engine, approver: approver}
}

func NewFilesTool(paths *policy.PathPolicy, engine domain.PolicyEngine, approver domain.Approver) domain.Tool {
	return &filesTool{paths: paths, engine: engine, approver: approver}
}

func (t *textTool) Definition() domain.ToolDefinition {
	return domain.ToolDefinition{
		Name:             "search_text",
		Description:      "基点ディレクトリ配下のテキストを検索します。",
		CapabilityGroup:  "search",
		Risk:             "medium",
		RequiresApproval: true,
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"root":        map[string]any{"type": "string", "description": "検索基点ディレクトリ"},
				"query":       map[string]any{"type": "string", "description": "検索文字列"},
				"max_results": map[string]any{"type": "integer", "description": "最大件数"},
			},
			"required": []string{"root", "query"},
		},
		Metadata: map[string]any{"category": "search"},
		Semantics: domain.ToolSemantics{
			Class:           domain.ToolClassObserve,
			ReusePolicy:     domain.ToolReuseOnSuccess,
			DuplicatePolicy: domain.ToolDuplicateSuppressInflight,
			Freshness:       domain.ToolFreshnessPolicy{Strategy: domain.ToolFreshnessReadSet},
			SideEffectClass: domain.SideEffectNone,
			Source:          "search",
			ReadPathArgs:    []string{"root"},
			IdentityArgs:    []string{"root", "query", "max_results"},
			SourceLimit:     6,
		},
	}
}

func (t *filesTool) Definition() domain.ToolDefinition {
	return domain.ToolDefinition{
		Name:             "search_files",
		Description:      "基点ディレクトリ配下からファイル名パターンに一致するファイルを探します。",
		CapabilityGroup:  "search",
		Risk:             "medium",
		RequiresApproval: true,
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"root":         map[string]any{"type": "string", "description": "検索基点ディレクトリ"},
				"name_pattern": map[string]any{"type": "string", "description": "filepath.Match 互換パターン"},
				"max_results":  map[string]any{"type": "integer", "description": "最大件数"},
			},
			"required": []string{"root", "name_pattern"},
		},
		Metadata: map[string]any{"category": "search"},
		Semantics: domain.ToolSemantics{
			Class:           domain.ToolClassObserve,
			ReusePolicy:     domain.ToolReuseOnSuccess,
			DuplicatePolicy: domain.ToolDuplicateSuppressInflight,
			Freshness:       domain.ToolFreshnessPolicy{Strategy: domain.ToolFreshnessReadSet},
			SideEffectClass: domain.SideEffectNone,
			Source:          "search",
			ReadPathArgs:    []string{"root"},
			IdentityArgs:    []string{"root", "name_pattern", "max_results"},
			SourceLimit:     6,
		},
	}
}

func (t *textTool) Execute(ctx context.Context, call domain.ToolCall) domain.ToolResult {
	root, ok := stringArg(call.Arguments, "root")
	if !ok {
		return failure(call, "root パラメータが必要です")
	}
	query, ok := stringArg(call.Arguments, "query")
	if !ok {
		return failure(call, "query パラメータが必要です")
	}
	if err := authorize(ctx, t.engine, t.approver, call); err != nil {
		return failure(call, err.Error())
	}

	resolvedRoot, err := t.paths.ResolveSearchRoot(root)
	if err != nil {
		return failure(call, err.Error())
	}
	maxResults := intArg(call.Arguments, "max_results", 50)
	type match struct {
		Path   string `json:"path"`
		Line   int    `json:"line"`
		Column int    `json:"column"`
		Text   string `json:"text"`
	}
	results := make([]match, 0, maxResults)
	err = filepath.WalkDir(resolvedRoot, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			if strings.HasPrefix(d.Name(), ".git") {
				return filepath.SkipDir
			}
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil || !utf8.Valid(data) || strings.ContainsRune(string(data), '\x00') {
			return nil
		}
		lines := strings.Split(string(data), "\n")
		for i, line := range lines {
			idx := strings.Index(line, query)
			if idx < 0 {
				continue
			}
			results = append(results, match{Path: path, Line: i + 1, Column: idx + 1, Text: line})
			if len(results) >= maxResults {
				return errLimitReached
			}
		}
		return nil
	})
	if err != nil && !errors.Is(err, errLimitReached) {
		return failure(call, err.Error())
	}
	return marshalSuccess(call, results)
}

func (t *filesTool) Execute(ctx context.Context, call domain.ToolCall) domain.ToolResult {
	root, ok := stringArg(call.Arguments, "root")
	if !ok {
		return failure(call, "root パラメータが必要です")
	}
	pattern, ok := stringArg(call.Arguments, "name_pattern")
	if !ok {
		return failure(call, "name_pattern パラメータが必要です")
	}
	if err := authorize(ctx, t.engine, t.approver, call); err != nil {
		return failure(call, err.Error())
	}

	resolvedRoot, err := t.paths.ResolveSearchRoot(root)
	if err != nil {
		return failure(call, err.Error())
	}
	maxResults := intArg(call.Arguments, "max_results", 200)
	results := make([]string, 0, maxResults)
	err = filepath.WalkDir(resolvedRoot, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			if d.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		matched, err := filepath.Match(pattern, d.Name())
		if err != nil {
			return err
		}
		if matched {
			results = append(results, path)
		}
		if len(results) >= maxResults {
			return errLimitReached
		}
		return nil
	})
	if err != nil && !errors.Is(err, errLimitReached) {
		return failure(call, err.Error())
	}
	return marshalSuccess(call, results)
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

func stringArg(args map[string]any, key string) (string, bool) {
	value, ok := args[key].(string)
	return value, ok && value != ""
}

func intArg(args map[string]any, key string, fallback int) int {
	value, ok := args[key]
	if !ok {
		return fallback
	}
	switch n := value.(type) {
	case float64:
		return int(n)
	case int:
		return n
	default:
		return fallback
	}
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

var errLimitReached = errors.New("limit reached")
