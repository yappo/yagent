package llm

import (
	"testing"
)

func TestFileReadToolPathAllowed(t *testing.T) {
	// 許可されたパス
	allowedPaths := []string{"/tmp/test.txt", "/home/user/project/src/main.go"}
	tool := NewFileReadTool(allowedPaths, false)

	for _, path := range allowedPaths {
		result := tool.Execute(nil, map[string]interface{}{"file_path": path})
		// アクセスが許可されている（ファイルが存在しないエラーは許容）
		if result.Error != "" && result.Error != "ファイルが存在しません："+path {
			t.Errorf("パス %s: 予期しないエラー %s", path, result.Error)
		}
	}

	// 許可されていないパス
	disallowedPaths := []string{"/etc/passwd"}
	tool2 := NewFileReadTool(allowedPaths, false)
	for _, path := range disallowedPaths {
		result := tool2.Execute(nil, map[string]interface{}{"file_path": path})
		if result.Success {
			t.Errorf("パス %s: 許可されるべきではない", path)
		}
	}
}

func TestFileReadToolPathSafe(t *testing.T) {
	tool := NewFileReadTool([]string{"/tmp"}, false)

	// セーフなパス
	safePaths := []string{"/tmp/test.txt"}
	for _, path := range safePaths {
		result := tool.Execute(nil, map[string]interface{}{"file_path": path})
		// セーフなパスはエラーになってもよい（ファイルが存在しないなど）
		// 重要なのは "安全でないファイルパス" エラーにならないこと
		if result.Error != "" && result.Error == "安全でないファイルパスです" {
			t.Errorf("パス %s: セーフなパスが安全でないとして拒否された", path)
		}
	}

	// セーフでないパス
	unSafePaths := []string{"/tmp/../etc/passwd"}
	for _, path := range unSafePaths {
		result := tool.Execute(nil, map[string]interface{}{"file_path": path})
		if result.Success {
			t.Errorf("パス %s: セーフでないはずが許可された", path)
		}
	}
}

func TestConfirmFileOperation(t *testing.T) {
	// このテストはユーザー入力が必要なので、手動で確認する必要があります
	t.Log("ConfirmFileOperation テストは手動で確認してください")
}
