package llm

import (
	"os"
	"testing"
)

func TestReadFile(t *testing.T) {
	// 一時的なテストファイルを作成
	tmpFile, err := os.CreateTemp("", "testfile")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmpFile.Name())

	// テストファイルに内容を書き込む
	content := "テストファイルの内容"
	if _, err := tmpFile.WriteString(content); err != nil {
		t.Fatal(err)
	}
	tmpFile.Close()

	// ファイルを読み込む
	client := &LLMClient{}
	readContent, err := client.ReadFile(tmpFile.Name())
	if err != nil {
		t.Fatal(err)
	}

	// 読み込んだ内容が正しいか確認
	if readContent != content {
		t.Errorf("Expected %s, got %s", content, readContent)
	}
}

func TestWriteFile(t *testing.T) {
	// 一時的なテストファイルを作成
	tmpFile, err := os.CreateTemp("", "testfile")
	if err != nil {
		t.Fatal(err)
	}
	tmpFile.Close()
	defer os.Remove(tmpFile.Name())

	// ファイルに内容を書き込む
	client := &LLMClient{}
	newContent := "新しいファイルの内容"
	err = client.WriteFile(tmpFile.Name(), newContent)
	if err != nil {
		t.Fatal(err)
	}

	// 書き込まれた内容を確認
	readContent, err := os.ReadFile(tmpFile.Name())
	if err != nil {
		t.Fatal(err)
	}

	if string(readContent) != newContent {
		t.Errorf("Expected %s, got %s", newContent, string(readContent))
	}
}

func TestIsPathAllowed(t *testing.T) {
	// 許可パスの設定
	config := &Config{
		File: struct {
			AllowPaths []string `toml:"allow_paths"`
		}{
			AllowPaths: []string{"/tmp", "/home/user/project"},
		},
	}
	
	client := &LLMClient{
		config: config,
	}
	
	// 許可されたパス
	allowedPaths := []string{"/tmp/test.txt", "/home/user/project/src/main.go"}
	for _, path := range allowedPaths {
		if !client.isPathAllowed(path) {
			t.Errorf("Expected path %s to be allowed", path)
		}
	}
	
	// 許可されていないパス
	disallowedPaths := []string{"/etc/passwd", "/home/user/secret.txt"}
	for _, path := range disallowedPaths {
		if client.isPathAllowed(path) {
			t.Errorf("Expected path %s to be disallowed", path)
		}
	}
}

func TestConfirmFileOperation(t *testing.T) {
	// このテストはユーザー入力が必要なので、手動で確認する必要があります
	t.Log("ConfirmFileOperation テストは手動で確認してください")
}
	defer os.Remove(tmpFile.Name())

	// テストファイルに内容を書き込む
	content := "テストファイルの内容"
	if _, err := tmpFile.WriteString(content); err != nil {
		t.Fatal(err)
	}
	tmpFile.Close()

	// ファイルを読み込む
	client := &LLMClient{}
	readContent, err := client.ReadFile(tmpFile.Name())
	if err != nil {
		t.Fatal(err)
	}

	// 読み込んだ内容が正しいか確認
	if readContent != content {
		t.Errorf("Expected %s, got %s", content, readContent)
	}
}

func TestWriteFile(t *testing.T) {
	// 一時的なテストファイルを作成
	tmpFile, err := os.CreateTemp("", "testfile")
	if err != nil {
		t.Fatal(err)
	}
	tmpFile.Close()
	defer os.Remove(tmpFile.Name())

	// ファイルに内容を書き込む
	client := &LLMClient{}
	newContent := "新しいファイルの内容"
	err = client.WriteFile(tmpFile.Name(), newContent)
	if err != nil {
		t.Fatal(err)
	}

	// 書き込まれた内容を確認
	readContent, err := os.ReadFile(tmpFile.Name())
	if err != nil {
		t.Fatal(err)
	}

	if string(readContent) != newContent {
		t.Errorf("Expected %s, got %s", newContent, string(readContent))
	}
}

func TestIsPathAllowed(t *testing.T) {
	// 許可パスの設定
	config := &Config{
		File: struct {
			AllowPaths []string `toml:"allow_paths"`
		}{
			AllowPaths: []string{"/tmp", "/home/user/project"},
		},
	}

	client := &LLMClient{
		config: config,
	}

	// 許可されたパス
	allowedPaths := []string{"/tmp/test.txt", "/home/user/project/src/main.go"}
	for _, path := range allowedPaths {
		if !client.isPathAllowed(path) {
			t.Errorf("Expected path %s to be allowed", path)
		}
	}

	// 許可されていないパス
	disallowedPaths := []string{"/etc/passwd", "/home/user/secret.txt"}
	for _, path := range disallowedPaths {
		if client.isPathAllowed(path) {
			t.Errorf("Expected path %s to be disallowed", path)
		}
	}
}

func TestIsPathSafe(t *testing.T) {
	client := &LLMClient{}

	// セーフなパス
	safePaths := []string{"/tmp/test.txt", "test.txt", "./test.txt"}
	for _, path := range safePaths {
		if !client.isPathSafe(path) {
			t.Errorf("Expected path %s to be safe", path)
		}
	}

	// セーフでないパス
	unSafePaths := []string{"/tmp/../etc/passwd", "~/test.txt"}
	for _, path := range unSafePaths {
		if client.isPathSafe(path) {
			t.Errorf("Expected path %s to be unsafe", path)
		}
	}
}

func TestConfirmFileOperation(t *testing.T) {
	// このテストはユーザー入力が必要なので、手動で確認する必要があります
	t.Log("ConfirmFileOperation テストは手動で確認してください")
}
