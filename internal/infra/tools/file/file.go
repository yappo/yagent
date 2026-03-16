package file

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"yagent/internal/domain"
)

type ReadTool struct {
	validator Validator
	approver  domain.Approver
}

type WriteTool struct {
	validator Validator
	approver  domain.Approver
}

type ListTool struct {
	validator Validator
	approver  domain.Approver
}

type Validator struct {
	allowedRoots []string
	baseDir      string
}

func NewValidator(baseDir string, allowedRoots []string) Validator {
	return Validator{
		baseDir:      baseDir,
		allowedRoots: append([]string(nil), allowedRoots...),
	}
}

func NewReadTool(validator Validator, approver domain.Approver) *ReadTool {
	return &ReadTool{validator: validator, approver: approver}
}

func NewWriteTool(validator Validator, approver domain.Approver) *WriteTool {
	return &WriteTool{validator: validator, approver: approver}
}

func NewListTool(validator Validator, approver domain.Approver) *ListTool {
	return &ListTool{validator: validator, approver: approver}
}

func (t *ReadTool) Definition() domain.ToolDefinition {
	return domain.ToolDefinition{
		Name:         "file_reader",
		Description:  "指定されたファイルの内容を読み取ります。",
		ReadOnly:     true,
		ParallelSafe: true,
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"file_path": map[string]any{
					"type":        "string",
					"description": "読み取るファイルのパス",
				},
			},
			"required": []string{"file_path"},
		},
	}
}

func (t *WriteTool) Definition() domain.ToolDefinition {
	return domain.ToolDefinition{
		Name:             "file_writer",
		Description:      "指定されたファイルへ内容を書き込みます。",
		MutatesWorkspace: true,
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"file_path": map[string]any{
					"type":        "string",
					"description": "書き込むファイルのパス",
				},
				"content": map[string]any{
					"type":        "string",
					"description": "書き込む内容",
				},
			},
			"required": []string{"file_path", "content"},
		},
	}
}

func (t *ListTool) Definition() domain.ToolDefinition {
	return domain.ToolDefinition{
		Name:         "directory_list",
		Description:  "指定されたディレクトリ配下のファイルやディレクトリ一覧を取得します。",
		ReadOnly:     true,
		ParallelSafe: true,
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"directory_path": map[string]any{
					"type":        "string",
					"description": "探索するディレクトリのパス",
				},
				"recursive": map[string]any{
					"type":        "boolean",
					"description": "再帰的に探索するかどうか",
				},
			},
			"required": []string{"directory_path"},
		},
	}
}

func (t *ReadTool) Execute(ctx context.Context, call domain.ToolCall) domain.ToolResult {
	filePath, ok := stringArg(call.Arguments, "file_path")
	if !ok {
		return failure(call, "file_path パラメータが必要です")
	}

	resolved, err := t.validator.Resolve(filePath)
	if err != nil {
		return failure(call, err.Error())
	}

	if err := t.approve(ctx, resolved); err != nil {
		return failure(call, err.Error())
	}

	content, err := os.ReadFile(resolved)
	if err != nil {
		return failure(call, fmt.Sprintf("failed to read file %s: %v", resolved, err))
	}

	return success(call, string(content))
}

func (t *WriteTool) Execute(ctx context.Context, call domain.ToolCall) domain.ToolResult {
	filePath, ok := stringArg(call.Arguments, "file_path")
	if !ok {
		return failure(call, "file_path パラメータが必要です")
	}

	content, ok := stringArg(call.Arguments, "content")
	if !ok {
		return failure(call, "content パラメータが必要です")
	}

	resolved, err := t.validator.Resolve(filePath)
	if err != nil {
		return failure(call, err.Error())
	}

	if err := t.approve(ctx, resolved); err != nil {
		return failure(call, err.Error())
	}

	if err := os.WriteFile(resolved, []byte(content), 0o644); err != nil {
		return failure(call, fmt.Sprintf("failed to write file %s: %v", resolved, err))
	}

	return success(call, fmt.Sprintf("ファイル %s に書き込みました", resolved))
}

func (t *ListTool) Execute(ctx context.Context, call domain.ToolCall) domain.ToolResult {
	directoryPath, ok := stringArg(call.Arguments, "directory_path")
	if !ok {
		return failure(call, "directory_path パラメータが必要です")
	}

	resolved, err := t.validator.Resolve(directoryPath)
	if err != nil {
		return failure(call, err.Error())
	}

	info, err := os.Stat(resolved)
	if err != nil {
		return failure(call, fmt.Sprintf("failed to stat directory %s: %v", resolved, err))
	}
	if !info.IsDir() {
		return failure(call, fmt.Sprintf("ディレクトリではありません: %s", resolved))
	}

	if err := t.approve(ctx, resolved); err != nil {
		return failure(call, err.Error())
	}

	recursive, _ := call.Arguments["recursive"].(bool)
	entries := []string{}
	if recursive {
		err = filepath.WalkDir(resolved, func(path string, d os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			rel, err := filepath.Rel(resolved, path)
			if err != nil {
				return err
			}
			if rel == "." {
				return nil
			}
			if d.IsDir() {
				entries = append(entries, rel+"/")
				return nil
			}
			entries = append(entries, rel)
			return nil
		})
	} else {
		var dirEntries []os.DirEntry
		dirEntries, err = os.ReadDir(resolved)
		if err == nil {
			for _, entry := range dirEntries {
				name := entry.Name()
				if entry.IsDir() {
					name += "/"
				}
				entries = append(entries, name)
			}
		}
	}
	if err != nil {
		return failure(call, fmt.Sprintf("failed to list directory %s: %v", resolved, err))
	}

	if len(entries) == 0 {
		return success(call, "(empty)")
	}
	return success(call, strings.Join(entries, "\n"))
}

func (t *ReadTool) approve(ctx context.Context, resource string) error {
	if t.approver == nil {
		return nil
	}

	decision, err := t.approver.Approve(ctx, domain.PermissionRequest{
		ToolName:  t.Definition().Name,
		Operation: "ファイル読み取り",
		Resource:  resource,
		AgentID:   currentAgentID(ctx),
		Purpose:   currentPurpose(ctx),
	})
	if err != nil {
		return err
	}
	if decision == domain.PermissionDeny {
		return fmt.Errorf("ユーザーによってキャンセルされました")
	}
	return nil
}

func (t *ListTool) approve(ctx context.Context, resource string) error {
	if t.approver == nil {
		return nil
	}

	decision, err := t.approver.Approve(ctx, domain.PermissionRequest{
		ToolName:  t.Definition().Name,
		Operation: "ディレクトリ一覧取得",
		Resource:  resource,
		AgentID:   currentAgentID(ctx),
		Purpose:   currentPurpose(ctx),
	})
	if err != nil {
		return err
	}
	if decision == domain.PermissionDeny {
		return fmt.Errorf("ユーザーによってキャンセルされました")
	}
	return nil
}

func (t *WriteTool) approve(ctx context.Context, resource string) error {
	if t.approver == nil {
		return nil
	}

	decision, err := t.approver.Approve(ctx, domain.PermissionRequest{
		ToolName:  t.Definition().Name,
		Operation: "ファイル書き込み",
		Resource:  resource,
		AgentID:   currentAgentID(ctx),
		Purpose:   currentPurpose(ctx),
	})
	if err != nil {
		return err
	}
	if decision == domain.PermissionDeny {
		return fmt.Errorf("ユーザーによってキャンセルされました")
	}
	return nil
}

func (v Validator) Resolve(path string) (string, error) {
	resolved := path
	if !filepath.IsAbs(resolved) {
		resolved = filepath.Join(v.baseDir, resolved)
	}

	resolved = filepath.Clean(resolved)
	if strings.Contains(resolved, "..") && !filepath.IsAbs(path) {
		return "", fmt.Errorf("安全でないファイルパスです: %s", resolved)
	}

	absPath, err := filepath.Abs(resolved)
	if err != nil {
		return "", fmt.Errorf("パスの変換に失敗しました: %w", err)
	}

	if len(v.allowedRoots) == 0 {
		return absPath, nil
	}

	for _, root := range v.allowedRoots {
		absRoot, err := filepath.Abs(root)
		if err != nil {
			continue
		}
		if strings.HasPrefix(absPath, absRoot) {
			return absPath, nil
		}
	}

	return "", fmt.Errorf("アクセスが許可されていないファイルパスです: %s", absPath)
}

func stringArg(args map[string]any, key string) (string, bool) {
	value, ok := args[key].(string)
	return value, ok && value != ""
}

func success(call domain.ToolCall, output string) domain.ToolResult {
	return domain.ToolResult{
		CallID:  call.ID,
		Name:    call.Name,
		Success: true,
		Output:  output,
	}
}

func failure(call domain.ToolCall, output string) domain.ToolResult {
	return domain.ToolResult{
		CallID:  call.ID,
		Name:    call.Name,
		Success: false,
		Output:  "エラー: " + output,
	}
}

type contextKey string

const (
	agentIDContextKey contextKey = "agent_id"
	purposeContextKey contextKey = "purpose"
)

func WithExecutionContext(ctx context.Context, agentID, purpose string) context.Context {
	ctx = context.WithValue(ctx, agentIDContextKey, agentID)
	return context.WithValue(ctx, purposeContextKey, purpose)
}

func currentAgentID(ctx context.Context) string {
	value, _ := ctx.Value(agentIDContextKey).(string)
	return value
}

func currentPurpose(ctx context.Context) string {
	value, _ := ctx.Value(purposeContextKey).(string)
	return value
}
