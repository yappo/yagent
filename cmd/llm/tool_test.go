package llm

import (
	"context"
	"os"
	"testing"
)

func TestToolRegistry(t *testing.T) {
	registry := NewToolRegistry()

	// テスト用のダミーツール
	tool := &testTool{
		name:        "test_tool",
		description: "テスト用のツール",
	}

	registry.Register(tool)

	// 登録されたツールが取得できるか確認
	retrieved := registry.Get("test_tool")
	if retrieved == nil {
		t.Fatal("登録されたツールが取得できませんでした")
	}

	if retrieved.Name() != "test_tool" {
		t.Errorf("期待されたツール名 'test_tool', 取得された '%s'", retrieved.Name())
	}

	// 存在しないツールが取得できないか確認
	nonExistent := registry.Get("non_existent")
	if nonExistent != nil {
		t.Error("存在しないツールが取得されました")
	}
}

func TestToolRegistryList(t *testing.T) {
	registry := NewToolRegistry()

	tool1 := &testTool{name: "tool1", description: "ツール 1"}
	tool2 := &testTool{name: "tool2", description: "ツール 2"}

	registry.Register(tool1)
	registry.Register(tool2)

	definitions := registry.List()

	if len(definitions) != 2 {
		t.Errorf("期待された定義数 2, 取得された %d", len(definitions))
	}
}

func TestToolHandler(t *testing.T) {
	registry := NewToolRegistry()
	tool := &testTool{name: "test_tool", description: "テスト用のツール"}
	registry.Register(tool)

	handler := NewToolHandler(registry)

	toolCalls := []ToolCall{
		{
			ID:   "call_1",
			Name: "test_tool",
			Arguments: map[string]interface{}{
				"arg1": "value1",
			},
		},
	}

	results := handler.HandleToolCalls(context.Background(), toolCalls)

	if len(results) != 1 {
		t.Errorf("期待された結果数 1, 取得された %d", len(results))
	}

	if !results[0].Success {
		t.Errorf("ツール実行が失敗しました：%s", results[0].Error)
	}
}

func TestToolHandlerNotFound(t *testing.T) {
	registry := NewToolRegistry()
	handler := NewToolHandler(registry)

	toolCalls := []ToolCall{
		{
			ID:        "call_1",
			Name:      "non_existent_tool",
			Arguments: map[string]interface{}{},
		},
	}

	results := handler.HandleToolCalls(context.Background(), toolCalls)

	if len(results) != 1 {
		t.Fatal("結果が取得できませんでした")
	}

	if results[0].Success {
		t.Error("存在しないツールの実行が成功しました")
	}
}

func TestFileReadTool(t *testing.T) {
	// 一時的なテストファイルを作成
	tmpFile, err := os.CreateTemp("", "testfile")
	if err != nil {
		t.Fatal(err)
	}
	tmpFilePath := tmpFile.Name()
	defer os.Remove(tmpFilePath)

	content := "テストファイルの内容"
	if _, err := tmpFile.WriteString(content); err != nil {
		t.Fatal(err)
	}
	tmpFile.Close()

	// FileReadTool を作成（許可パスに temp ディレクトリを追加）
	tool := NewFileReadTool([]string{tmpFilePath}, false)

	// 実行
	result := tool.Execute(context.Background(), map[string]interface{}{
		"file_path": tmpFilePath,
	})

	if !result.Success {
		t.Errorf("ファイル読み取りに失敗しました：%s", result.Error)
	}

	if result.Data != content {
		t.Errorf("期待された内容 '%s', 取得された '%s'", content, result.Data)
	}
}

func TestFileReadToolNotFound(t *testing.T) {
	tool := NewFileReadTool([]string{"/tmp"}, false)

	result := tool.Execute(context.Background(), map[string]interface{}{
		"file_path": "/non/existent/path.txt",
	})

	if result.Success {
		t.Error("存在しないファイルの読み取りが成功しました")
	}
}

func TestFileReadToolUnsafePath(t *testing.T) {
	tool := NewFileReadTool([]string{"/tmp"}, false)

	result := tool.Execute(context.Background(), map[string]interface{}{
		"file_path": "/tmp/../etc/passwd",
	})

	if result.Success {
		t.Error("安全でないパスのアクセスが許可されました")
	}
}

func TestFileWriterTool(t *testing.T) {
	tmpFile, err := os.CreateTemp("", "testfile")
	if err != nil {
		t.Fatal(err)
	}
	tmpFilePath := tmpFile.Name()
	tmpFile.Close()
	defer os.Remove(tmpFilePath)

	newContent := "新しいファイルの内容"
	tool := NewFileWriterTool([]string{tmpFilePath}, false)

	result := tool.Execute(context.Background(), map[string]interface{}{
		"file_path": tmpFilePath,
		"content":   newContent,
	})

	if !result.Success {
		t.Errorf("ファイル書き込みに失敗しました：%s", result.Error)
	}

	readContent, err := os.ReadFile(tmpFilePath)
	if err != nil {
		t.Fatal(err)
	}

	if string(readContent) != newContent {
		t.Errorf("期待された内容 '%s', 取得された '%s'", newContent, string(readContent))
	}
}

func TestFileWriterToolMissingArgs(t *testing.T) {
	tool := NewFileWriterTool([]string{"/tmp"}, false)

	// file_path がない場合
	result := tool.Execute(context.Background(), map[string]interface{}{
		"content": "content",
	})

	if result.Success {
		t.Error("file_path がない場合、エラーになるべきです")
	}

	// content がない場合
	result = tool.Execute(context.Background(), map[string]interface{}{
		"file_path": "/tmp/test.txt",
	})

	if result.Success {
		t.Error("content がない場合、エラーになるべきです")
	}
}

func TestToolDefinition(t *testing.T) {
	registry := NewToolRegistry()
	tool := &testTool{
		name:        "test_tool",
		description: "テスト用のツール",
	}
	registry.Register(tool)

	definitions := registry.List()

	if len(definitions) != 1 {
		t.Fatal("定義が取得できませんでした")
	}

	def := definitions[0]

	if def.Type != "function" {
		t.Errorf("期待されたタイプ 'function', 取得された '%s'", def.Type)
	}

	if def.Function.Name != "test_tool" {
		t.Errorf("期待された名前 'test_tool', 取得された '%s'", def.Function.Name)
	}

	if def.Function.Description != "テスト用のツール" {
		t.Errorf("期待された説明 'テスト用のツール', 取得された '%s'", def.Function.Description)
	}
}

func TestLLMClientWithTools(t *testing.T) {
	client := NewLLMClient("http://localhost:1234", "")

	// ツールを登録
	tool := NewFileReadTool([]string{"/tmp"}, false)
	client.WithTools(tool)

	// ツールハンドラが設定されているか確認
	if client.GetToolHandler() == nil {
		t.Fatal("ツールハンドラが設定されていません")
	}
}

// testTool テスト用のダミーツール
type testTool struct {
	name        string
	description string
}

func (t *testTool) Name() string {
	return t.name
}

func (t *testTool) Description() string {
	return t.description
}

func (t *testTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
	}
}

func (t *testTool) Execute(ctx context.Context, args map[string]interface{}) *ToolOutput {
	return &ToolOutput{
		Success: true,
		Data:    "test result",
	}
}
