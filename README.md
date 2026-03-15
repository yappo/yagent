# yagent

yagent は OpenAI 互換 API を使ってローカルまたはリモートの LLM と対話する、TUI 中心の AI coding agent です。  
Bubble Tea / Bubbles を使った対話 UI、専用 Tool 群、ファイル操作の許可 UI、単発実行 CLI を備えています。

## 特徴

- `yagent --config <file>` でそのまま TUI 起動
- `yagent exec --prompt ...` で単発実行
- Bubble Tea / Bubbles ベースの TUI
- OpenAI 互換の `chat/completions` API に対応
- ツールレジストリ経由で `fs` `search` `git` `task` `patch` を実行
- Claude Code 風の permission UI と tool call カード
- `internal/` ベースで責務分離した構成
- `task_list` / `task_run` による安全寄りの Task Catalog
- `read`, `list`, `search` を含む全 Tool 操作で明示的な permission

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
timeout = 900

[file]
allow_paths = ["/Users/you/Projects"]

[agent]
max_iterations = 100
```

### 設定項目

- `server.default`: 使用するサーバー名
- `server.servers`: 接続先一覧
- `server.servers[].timeout`: 秒単位の HTTP タイムアウト。デフォルトは `900` (15 分)
- `file.allow_paths`: ツールからアクセス可能なパス一覧
- `agent.max_iterations`: 1 回のユーザ入力に対して LLM/tool loop を繰り返す最大回数。デフォルトは `100`

起動時のカレントディレクトリは自動で許可パスに追加されます。

## TUI 操作

- `Enter`: 送信
- `Ctrl+J`: 改行
- `PgUp` / `PgDn`: ログスクロール
- `Alt+↑` / `Alt+↓`: 1 行ずつログスクロール
- `/` 入力中: 候補コマンドを表示
- `@` 入力中: `pwd` 基準の相対パス候補を表示
- `Tab`: 候補コマンドまたはファイルパスを補完
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

`このセッションで許可` は、同じツール・同じ action・同じ scope・同じ risk に対してのみ再利用されます。

### Tool Call UI

LLM が Tool を呼び出すと、TUI 下部に tool call card を表示します。

- 実行しようとしている `tool 名`
- 対象 path や task id などの `target`
- `limit_bytes`, `recursive`, `overwrite` などの主なオプション
- 実行状態 (`requesting`, `completed`, `failed`)

permission が必要な操作では、tool call card で内容を確認した上で permission card で許可できます。

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

現在のツール実装は `internal/infra/tools/fs`, `search`, `git`, `task`, `patch` にあります。  
新しいツールを増やすときは、具体実装を `internal/infra/tools/<toolname>` に置き、`internal/app` の bootstrap で registry に登録してください。

### 利用可能な Tool

- `fs_read`, `fs_write`, `fs_list`, `fs_stat`, `fs_remove`, `fs_move`
- `search_text`, `search_files`
- `git_status`, `git_diff`, `git_log`, `git_show`
- `task_list`, `task_run`
- `patch_apply`

`read`, `list`, `search` を含め、LLM に情報が渡る操作はすべて permission 対象です。

主な挙動:

- `fs_read`: テキストファイル読取。バイナリは拒否
- `fs_write`: 新規作成 / 上書きを明示フラグ付きで実行
- `fs_list`: ディレクトリ一覧
- `fs_stat`: path のメタデータ取得
- `fs_remove`: ファイル削除とディレクトリ再帰削除
- `fs_move`: ファイル移動 / rename
- `search_text`, `search_files`: 基点ディレクトリ配下の検索
- `git_*`: 読み取り専用の Git 情報取得
- `task_*`: 登録済み task の列挙と実行
- `patch_apply`: 構造化パッチの適用

すべての fs/search/patch/git 操作は許可 root 配下に制限され、symlink 解決後の実体 path でも再検証されます。

### Task Catalog

`task_run` は任意コマンドを受け付けず、`task_list` に出る登録済み task だけを実行します。

task の定義元は次の優先順位です。

- リポジトリ設定: `.yagent/tasks.toml`
- ユーザ設定: `~/.config/yagent/tasks.toml`
- 組み込みテンプレート: Go / Node / Python / Rust

#### `.yagent/tasks.toml` に書くもの

`.yagent/tasks.toml` には「LLM が実行してよい定義済みコマンド」を書きます。

- LLM は `task_list` で task の一覧を見て、`task_run` では `id` を指定して実行します
- 任意の shell 文字列は渡せません
- 依存取得やネットワークアクセスが必要な task は `allow_network = true` にします
- 危険度は `risk = "low" | "medium" | "high"` で表します

最小例:

```toml
[[tasks]]
id = "go:test"
description = "go test を実行"
command = "go"
args = ["test", "./..."]
risk = "medium"
allow_network = false
timeout = 300
```

各フィールドの意味:

- `id`: task の識別子です。`task_run` でこの値を使います。`go:test`, `npm:build` のように用途が分かる名前を推奨します
- `description`: 人と LLM に見せる説明です。何をする task か短く書きます
- `command`: 実行するコマンド本体です。例: `go`, `npm`, `cargo`
- `args`: 固定引数の配列です。例: `["test", "./..."]`
- `cwd`: 実行ディレクトリです。省略時はリポジトリ root を使います
- `risk`: `low`, `medium`, `high` のいずれかです。迷ったらテスト・ビルドは `medium`、依存取得や外部変更があるものは `high` にしてください
- `allow_network`: ネットワークアクセスの可能性がある task は `true` にします。例: `npm install`, `go mod download`
- `timeout`: 秒単位のタイムアウトです。例: `300`

#### よくある例

Go:

```toml
[[tasks]]
id = "go:test"
description = "Go のテストを実行"
command = "go"
args = ["test", "./..."]
risk = "medium"
allow_network = false
timeout = 300

[[tasks]]
id = "go:build"
description = "Go のビルドを実行"
command = "go"
args = ["build", "./..."]
risk = "medium"
allow_network = false
timeout = 300

[[tasks]]
id = "go:mod-download"
description = "Go の依存関係を取得"
command = "go"
args = ["mod", "download"]
risk = "high"
allow_network = true
timeout = 300
```

Node:

```toml
[[tasks]]
id = "npm:test"
description = "npm test を実行"
command = "npm"
args = ["test"]
risk = "medium"
allow_network = false
timeout = 600

[[tasks]]
id = "npm:build"
description = "npm run build を実行"
command = "npm"
args = ["run", "build"]
risk = "medium"
allow_network = false
timeout = 600

[[tasks]]
id = "npm:install"
description = "依存関係をインストール"
command = "npm"
args = ["install"]
risk = "high"
allow_network = true
timeout = 900
```

Python:

```toml
[[tasks]]
id = "python:test"
description = "pytest を実行"
command = "pytest"
args = []
risk = "medium"
allow_network = false
timeout = 600

[[tasks]]
id = "python:install"
description = "requirements.txt から依存関係を取得"
command = "python3"
args = ["-m", "pip", "install", "-r", "requirements.txt"]
risk = "high"
allow_network = true
timeout = 900
```

Rust:

```toml
[[tasks]]
id = "cargo:test"
description = "cargo test を実行"
command = "cargo"
args = ["test"]
risk = "medium"
allow_network = false
timeout = 600

[[tasks]]
id = "cargo:build"
description = "cargo build を実行"
command = "cargo"
args = ["build"]
risk = "medium"
allow_network = false
timeout = 600
```

#### 複数ディレクトリがある repo の例

フロントエンドが `web/` 配下にある場合:

```toml
[[tasks]]
id = "web:test"
description = "web ディレクトリで npm test を実行"
command = "npm"
args = ["test"]
cwd = "web"
risk = "medium"
allow_network = false
timeout = 600
```

`cwd` は相対パスでも書けます。相対パスは repo root から解決されます。

#### 最初は何を書けばよいか

迷ったら、最初は次の 2 つか 3 つだけで十分です。

- テスト実行
- ビルド
- 依存取得

例:

- Go: `go:test`, `go:build`, `go:mod-download`
- Node: `npm:test`, `npm:build`, `npm:install`
- Python: `python:test`, `python:install`
- Rust: `cargo:test`, `cargo:build`

#### 安全のための注意

- `command` には `bash`, `sh`, `zsh` のような shell を直接指定しないことを推奨します
- `python -c`, `node -e` のように任意コード実行へ寄りやすい形は避けてください
- 削除系や外部変更系の task を置く場合は `risk = "high"` にしてください
- ネットワーク利用がある task は必ず `allow_network = true` にしてください
- まずは読みやすく単純な task を少数登録し、必要になったら増やすのが安全です

#### 組み込みテンプレートとの関係

- `go.mod`, `package.json`, `pyproject.toml`, `requirements.txt`, `Cargo.toml` があると、対応する標準 task が自動で候補に入ります
- `.yagent/tasks.toml` に同じ `id` を書くと、その定義で上書きされます
- プロジェクト固有の事情があるなら、明示的に `.yagent/tasks.toml` を置くのがおすすめです

### 安全モデル

yagent は任意 shell 実行を Tool として公開しません。  
その代わりに、目的別の専用 Tool と path/policy チェックで操作範囲を制御します。

- 任意の `bash`, `sh`, `zsh` コマンドは Tool 経由では実行しない
- `task_run` は `task_list` にある登録済み task だけを実行する
- `read`, `list`, `search` も情報漏洩リスクとして permission 対象
- permission は `tool + action + scope + risk` 単位でセッション記憶する
- 許可 root 外の path と symlink 経由の脱出を拒否する

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
- `internal/infra/policy`: path policy のユニットテスト
- `internal/infra/tools`: fs/task/registry のユニットテスト
- `internal/usecase/chat`: tool loop を含む会話実行テスト
- `internal/tui`: state transition と viewport / permission / tool call UI のテスト
- `internal/usecase/taskcatalog`: template と repo override のテスト

## ライセンス

MIT
