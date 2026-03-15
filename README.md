# yagent

yagent は OpenAI 互換 API を使ってローカルまたはリモートの LLM と対話する、TUI 中心の AI coding agent です。  
Bubble Tea / Bubbles を使った対話 UI、ツール呼び出し、ファイル操作の許可 UI、単発実行 CLI を備えています。

## 特徴

- `yagent --config <file>` でそのまま TUI 起動
- `yagent exec --prompt ...` で単発実行
- Bubble Tea / Bubbles ベースの TUI
- OpenAI 互換の `chat/completions` API に対応
- ツールレジストリ経由で file read / write を実行
- Claude Code 風の permission UI
- `internal/` ベースで責務分離した構成

## コマンド

```bash
# TUI を起動
yagent

# 設定ファイル付きで TUI を起動
yagent --config ~/.yagent.toml

# 明示的に TUI を起動
yagent tui --config ~/.yagent.toml

# 単発実行
yagent exec --config ~/.yagent.toml --prompt "こんにちは"
```

## 設定ファイル

```toml
[server]
default = "lmstudio"

[[server.servers]]
name = "lmstudio"
url = "http://127.0.0.1:1234"
token = ""

[file]
allow_paths = ["/Users/you/Projects"]
```

### 設定項目

- `server.default`: 使用するサーバー名
- `server.servers`: 接続先一覧
- `file.allow_paths`: ツールからアクセス可能なパス一覧

起動時のカレントディレクトリは自動で許可パスに追加されます。

## TUI 操作

- `Enter`: 送信
- `Ctrl+J`: 改行
- `PgUp` / `PgDn`: ログスクロール
- `Alt+↑` / `Alt+↓`: 1 行ずつログスクロール
- `/` 入力中: 候補コマンドを表示
- `Tab`: 先頭の候補コマンドを補完
- `/help`: ヘルプ表示
- `/clear`: 会話ログをクリア
- `/exit`: 終了

### Permission UI

ファイル操作が必要なときは、下部に permission card を表示します。

- `←/→` または `Tab`: 選択移動
- `Enter`: 確定
- `Esc`: 拒否

選択肢:

- 今回だけ許可
- このセッションで許可
- 拒否

`このセッションで許可` は、同じツール・同じファイルに対してのみ再利用されます。

## アーキテクチャ

主要コードは `internal/` 配下にあります。

```text
internal/
  app/       起動 wiring と依存解決
  cli/       Cobra command 定義
  config/    TOML 設定読込
  domain/    中核型と interface
  infra/     LLM client / tools 実装
  tui/       Bubble Tea / Bubbles の UI
  usecase/   会話実行ロジック
```

### ツール拡張

ツールは `domain.Tool` を実装し、registry に登録します。

現在の file read / write は `internal/infra/tools/file` にあります。  
新しいツールを増やすときは、具体実装を `internal/infra/tools/<toolname>` に置き、`internal/app` の bootstrap で registry に登録してください。

## 開発

```bash
# ビルド
go build -o yagent .

# テスト
go test ./...

# 実行
./yagent --config ~/.yagent.toml
```

## テスト方針

- `internal/config`: 設定読込テスト
- `internal/infra/llm`: fake server を使った HTTP クライアントテスト
- `internal/infra/tools`: file tool / registry のユニットテスト
- `internal/usecase/chat`: tool loop を含む会話実行テスト
- `internal/tui`: state transition と viewport / permission UI のテスト

## ライセンス

MIT
