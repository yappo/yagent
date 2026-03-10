# yagent

LM Studio と連携するための CLI エージェントで、ローカルの AI モデルとのインタラクションを簡単に行うためのインターフェースを提供します。

## 機能

- LM Studio との CLI インターフェース
- AI インタラクションのためのシンプルなコマンドラインインターフェース
- インタラクティブな TUI モードでの対話
- **ファイル操作機能** - LLM の指示に応じてローカルファイルの読み書きを実行可能

## 特徴

### ファイル操作機能

yagent は、TUI モードで LLM にファイル操作を依頼できます。LLM が `/file-read` または `/file-write` コマンドを返すと、自動的に以下の処理が行われます：

- **ファイル読み込み** (`/file-read <パス>`)
  - 指定したファイルの内容を読み取り、LLM に送信
  - LLM の応答に基づいて追加の処理を実行
  
- **ファイル書き込み** (`/file-write <パス>`)
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
yagent llm --prompt "質問内容" --config config.toml
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

### 実行例

```bash
# エージェントを実行 (TUI モード)
yagent

# LLM サーバーに質問を送信 (設定ファイルを使用)
yagent llm --prompt "こんにちは" --config config.toml

# インタラクティブな対話 (TUI モード)
yagent --config config.toml
```

## 依存関係

- [Cobra](https://github.com/spf13/cobra) - CLI フレームワーク
- [Viper](https://github.com/spf13/viper) - 設定管理
- [TOML](https://toml.io/) - 設定ファイル形式

## ライセンス

MIT License
