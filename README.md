# yagent

LM Studio と連携するための CLI エージェントで、ローカルの AI モデルとのインタラクションを簡単に行うためのインターフェースを提供します。

## 機能

- LM Studio との CLI インターフェース
- AI インタラクションのためのシンプルなコマンドラインインターフェース
- インタラクティブな TUI モードでの対話
- **Function Calling 対応** - OpenAI API の tools 機能に対応
- **ファイル操作機能** - LLM の指示に応じてローカルファイルの読み書きを実行可能

## 特徴

### Function Calling (ツール機能)

yagent は OpenAI API の Function Calling に対応しています。LLM が自動的にツールを呼び出し、ファイル操作を実行できます：

- **ツール定義**: LLM に利用可能なツールを定義可能
- **自動呼び出し**: LLM の判断でツールが自動的に呼び出される
- **拡張可能**: クリーンアーキテクチャに基づき、簡単に新規ツールを追加可能

### ファイル操作ツール

ファイル操作は専用のツールとして実装されています：

- **file_reader**: ファイル読み取りツール
  - 指定したファイルの内容を読み取り、LLM に送信
  - LLM の応答に基づいて追加の処理を実行
  
- **file_writer**: ファイル書き込みツール
  - LLM が生成した内容を Markdown コードブロックから抽出し、ファイルに保存
  - ユーザーの確認を得た後に実行

### セキュリティ機能

- **ユーザー確認**: ファイル操作前に必ずユーザーの確認を取得
- **パス制限**: `config.toml` で許可するディレクトリを指定可能
- **トラバーサル防止**: ディレクトリトラバーサル攻撃を防ぐための正規化処理

## インストール

```bash
go install .
```

## 使用方法

### TUI モード（対話モード）

デフォルトで TUI モードが起動します：

```bash
yagent
```

設定ファイルを使用する場合：

```bash
yagent --config config.toml
```

### LLM コマンド（単発クエリ）

```bash
yagent exec --prompt "質問内容" --config config.toml
```

### ファイル操作の例

TUI モードで以下のように LLM に依頼すると、自動的にファイルが読み書きされます：

```
LLM: この内容を /Users/yappo/Projects/yagent-tmp/test.txt に書き込んでください：
```
```
これはテストファイルです
```

### 設定ファイルのフォーマット

TOML 形式で記述します：

```toml
[server]
default = "lmstudio"

[[server.servers]]
name = "lmstudio"
url = "http://127.0.0.1:1234"
token = "your-api-token"

# ファイル操作の制限（オプション）
[file]
allowed_paths = ["/Users/yappo/Projects/yagent-tmp"]
```

## 開発

### テストを実行

```bash
go test ./...
```

### ビルド方法

```bash
./build.sh
# または
go build -o yagent .
```

### 新規ツールの追加方法

```go
// ツールの実装
type MyTool struct {}

func (t *MyTool) Name() string { return "my_tool" }
func (t *MyTool) Description() string { return "説明" }
func (t *MyTool) Parameters() map[string]interface{} { return params }
func (t *MyTool) Execute(ctx context.Context, args map[string]interface{}) *ToolOutput {
    // 実装
}

// LLMClient に登録
client := llm.NewLLMClient(baseURL, token)
client.WithTools(&MyTool{})
```

### 実行例

```bash
# エージェントを実行 (TUI モード)
yagent

# LLM サーバーに質問を送信 (設定ファイルを使用)
yagent exec --prompt "こんにちは" --config config.toml

# インタラクティブな対話 (TUI モード)
yagent --config config.toml
```

## 依存関係

- [Cobra](https://github.com/spf13/cobra) - CLI フレームワーク
- [Viper](https://github.com/spf13/viper) - 設定管理
- [TOML](https://toml.io/) - 設定ファイル形式

## ライセンス

MIT License
