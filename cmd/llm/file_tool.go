package llm

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type ToolConfirmationDecision string

const (
	ConfirmAllowOnce    ToolConfirmationDecision = "allowOnce"
	ConfirmAllowSession ToolConfirmationDecision = "allowSession"
	ConfirmDeny         ToolConfirmationDecision = "deny"
)

type ToolConfirmationRequest struct {
	ToolName  string
	Operation string
	FilePath  string
}

// FileReadTool ファイル読み取りツール
type FileReadTool struct {
	allowPaths    []string
	confirm       bool
	confirmFunc   func(request ToolConfirmationRequest) ToolConfirmationDecision
	baseDirectory string
}

// NewFileReadTool ファイル読み取りツールを作成
func NewFileReadTool(allowPaths []string, confirm bool) *FileReadTool {
	baseDir, _ := os.Getwd()
	return &FileReadTool{
		allowPaths:    allowPaths,
		confirm:       confirm,
		baseDirectory: baseDir,
	}
}

func (t *FileReadTool) WithConfirmFunc(confirmFunc func(request ToolConfirmationRequest) ToolConfirmationDecision) *FileReadTool {
	t.confirmFunc = confirmFunc
	return t
}

// Name ツール名
func (t *FileReadTool) Name() string {
	return "file_reader"
}

// Description ツールの説明
func (t *FileReadTool) Description() string {
	return "ファイルを読み取るツールです。指定されたパスのファイル内容を読み取ります。"
}

// Parameters パラメータ定義
func (t *FileReadTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"file_path": map[string]interface{}{
				"type":        "string",
				"description": "読み取るファイルのパス",
				"required":    true,
			},
		},
		"required": []string{"file_path"},
	}
}

// Execute ファイル読み取りを実行
func (t *FileReadTool) Execute(ctx context.Context, args map[string]interface{}) *ToolOutput {
	filePath, ok := args["file_path"].(string)
	if !ok || filePath == "" {
		return &ToolOutput{
			Success: false,
			Error:   "file_path パラメータが必要です",
		}
	}

	// 相対パスを絶対パスに変換
	absPath, err := t.resolvePath(filePath)
	if err != nil {
		return &ToolOutput{
			Success: false,
			Error:   fmt.Sprintf("パスの変換に失敗しました：%v", err),
		}
	}
	filePath = absPath

	if !t.isPathSafe(filePath) {
		return &ToolOutput{
			Success: false,
			Error:   fmt.Sprintf("安全でないファイルパスです：%s", filePath),
		}
	}

	if !t.isPathAllowed(filePath) {
		return &ToolOutput{
			Success: false,
			Error:   fmt.Sprintf("アクセスが許可されていないファイルパスです：%s", filePath),
		}
	}

	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		return &ToolOutput{
			Success: false,
			Error:   fmt.Sprintf("ファイルが存在しません：%s", filePath),
		}
	}

	// ユーザー確認
	if t.confirm {
		if t.confirmOperation(filePath) == ConfirmDeny {
			return &ToolOutput{
				Success: false,
				Error:   "ユーザーによってキャンセルされました",
			}
		}
	}

	content, err := os.ReadFile(filePath)
	if err != nil {
		return &ToolOutput{
			Success: false,
			Error:   fmt.Sprintf("failed to read file %s: %v", filePath, err),
		}
	}

	return &ToolOutput{
		Success: true,
		Data:    string(content),
	}
}

// resolvePath 相対パスを絶対パスに変換
func (t *FileReadTool) resolvePath(filePath string) (string, error) {
	if filepath.IsAbs(filePath) {
		return filePath, nil
	}
	return filepath.Join(t.baseDirectory, filePath), nil
}

// isPathSafe パスセキュリティチェック
func (t *FileReadTool) isPathSafe(filePath string) bool {
	cleanPath := filepath.Clean(filePath)
	if strings.HasPrefix(cleanPath, "..") {
		return false
	}
	absPath, err := filepath.Abs(filePath)
	if err != nil {
		return false
	}
	cleanAbsPath := filepath.Clean(absPath)
	if strings.HasPrefix(cleanAbsPath, "..") {
		return false
	}
	return true
}

// isPathAllowed 許可パスチェック
func (t *FileReadTool) isPathAllowed(filePath string) bool {
	if len(t.allowPaths) == 0 {
		return true
	}

	absFilePath, err := filepath.Abs(filePath)
	if err != nil {
		return false
	}

	for _, allowedPath := range t.allowPaths {
		absAllowedPath, err := filepath.Abs(allowedPath)
		if err != nil {
			continue
		}
		if strings.HasPrefix(absFilePath, absAllowedPath) {
			return true
		}
	}

	return false
}

// FileWriterTool ファイル書き込みツール
type FileWriterTool struct {
	allowPaths    []string
	confirm       bool
	confirmFunc   func(request ToolConfirmationRequest) ToolConfirmationDecision
	baseDirectory string
}

// NewFileWriterTool ファイル書き込みツールを作成
func NewFileWriterTool(allowPaths []string, confirm bool) *FileWriterTool {
	baseDir, _ := os.Getwd()
	return &FileWriterTool{
		allowPaths:    allowPaths,
		confirm:       confirm,
		baseDirectory: baseDir,
	}
}

func (t *FileWriterTool) WithConfirmFunc(confirmFunc func(request ToolConfirmationRequest) ToolConfirmationDecision) *FileWriterTool {
	t.confirmFunc = confirmFunc
	return t
}

// Name ツール名
func (t *FileWriterTool) Name() string {
	return "file_writer"
}

// Description ツールの説明
func (t *FileWriterTool) Description() string {
	return "ファイルに書き込むツールです。指定されたパスに内容を記録します。"
}

// Parameters パラメータ定義
func (t *FileWriterTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"file_path": map[string]interface{}{
				"type":        "string",
				"description": "書き込むファイルのパス",
				"required":    true,
			},
			"content": map[string]interface{}{
				"type":        "string",
				"description": "書き込む内容",
				"required":    true,
			},
		},
		"required": []string{"file_path", "content"},
	}
}

// Execute ファイル書き込みを実行
func (t *FileWriterTool) Execute(ctx context.Context, args map[string]interface{}) *ToolOutput {
	filePath, ok := args["file_path"].(string)
	if !ok || filePath == "" {
		return &ToolOutput{
			Success: false,
			Error:   "file_path パラメータが必要です",
		}
	}

	content, ok := args["content"].(string)
	if !ok {
		return &ToolOutput{
			Success: false,
			Error:   "content パラメータが必要です",
		}
	}

	// 相対パスを絶対パスに変換
	absPath, err := t.resolvePath(filePath)
	if err != nil {
		return &ToolOutput{
			Success: false,
			Error:   fmt.Sprintf("パスの変換に失敗しました：%v", err),
		}
	}
	filePath = absPath

	if !t.isPathSafe(filePath) {
		return &ToolOutput{
			Success: false,
			Error:   fmt.Sprintf("安全でないファイルパスです：%s", filePath),
		}
	}

	if !t.isPathAllowed(filePath) {
		return &ToolOutput{
			Success: false,
			Error:   fmt.Sprintf("アクセスが許可されていないファイルパスです：%s", filePath),
		}
	}

	// ユーザー確認
	if t.confirm {
		if t.confirmOperation(filePath) == ConfirmDeny {
			return &ToolOutput{
				Success: false,
				Error:   "ユーザーによってキャンセルされました",
			}
		}
	}

	err = os.WriteFile(filePath, []byte(content), 0644)
	if err != nil {
		return &ToolOutput{
			Success: false,
			Error:   fmt.Sprintf("failed to write file %s: %v", filePath, err),
		}
	}

	return &ToolOutput{
		Success: true,
		Data:    fmt.Sprintf("ファイル %s に書き込みました", filePath),
	}
}

// resolvePath 相対パスを絶対パスに変換
func (t *FileWriterTool) resolvePath(filePath string) (string, error) {
	if filepath.IsAbs(filePath) {
		return filePath, nil
	}
	return filepath.Join(t.baseDirectory, filePath), nil
}

// isPathSafe パスセキュリティチェック
func (t *FileWriterTool) isPathSafe(filePath string) bool {
	cleanPath := filepath.Clean(filePath)
	if strings.HasPrefix(cleanPath, "..") {
		return false
	}
	absPath, err := filepath.Abs(filePath)
	if err != nil {
		return false
	}
	cleanAbsPath := filepath.Clean(absPath)
	if strings.HasPrefix(cleanAbsPath, "..") {
		return false
	}
	return true
}

// isPathAllowed 許可パスチェック
func (t *FileWriterTool) isPathAllowed(filePath string) bool {
	if len(t.allowPaths) == 0 {
		return true
	}

	absFilePath, err := filepath.Abs(filePath)
	if err != nil {
		return false
	}

	for _, allowedPath := range t.allowPaths {
		absAllowedPath, err := filepath.Abs(allowedPath)
		if err != nil {
			continue
		}
		if strings.HasPrefix(absFilePath, absAllowedPath) {
			return true
		}
	}

	return false
}

func (t *FileReadTool) confirmOperation(filePath string) ToolConfirmationDecision {
	request := ToolConfirmationRequest{
		ToolName:  t.Name(),
		Operation: "ファイル読み取り",
		FilePath:  filePath,
	}
	if t.confirmFunc != nil {
		return t.confirmFunc(request)
	}

	return confirmOperationFromStdin(request)
}

func (t *FileWriterTool) confirmOperation(filePath string) ToolConfirmationDecision {
	request := ToolConfirmationRequest{
		ToolName:  t.Name(),
		Operation: "ファイル書き込み",
		FilePath:  filePath,
	}
	if t.confirmFunc != nil {
		return t.confirmFunc(request)
	}

	return confirmOperationFromStdin(request)
}

func confirmOperationFromStdin(request ToolConfirmationRequest) ToolConfirmationDecision {
	fmt.Printf("%sを実行しますか？ファイル：%s\n", request.Operation, request.FilePath)
	fmt.Print("[1] 今回だけ許可  [2] このセッションで許可  [3] 拒否: ")
	var input string
	fmt.Scanln(&input)
	switch strings.TrimSpace(strings.ToLower(input)) {
	case "1":
		return ConfirmAllowOnce
	case "2":
		return ConfirmAllowSession
	default:
		return ConfirmDeny
	}
}
