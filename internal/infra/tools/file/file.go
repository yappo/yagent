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

func (t *ReadTool) Definition() domain.ToolDefinition {
	return domain.ToolDefinition{
		Name:        "file_reader",
		Description: "指定されたファイルの内容を読み取ります。",
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
		Name:        "file_writer",
		Description: "指定されたファイルへ内容を書き込みます。",
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

func (t *ReadTool) approve(ctx context.Context, resource string) error {
	if t.approver == nil {
		return nil
	}

	decision, err := t.approver.Approve(ctx, domain.PermissionRequest{
		ToolName:  t.Definition().Name,
		Operation: "ファイル読み取り",
		Resource:  resource,
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
