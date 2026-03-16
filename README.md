# yagent

yagent は OpenAI 互換 API を使ってローカルまたはリモートの LLM と対話する、TUI 中心の AI coding agent です。  
Bubble Tea / Bubbles を使った対話 UI、permission UI、tool call UI、単発実行 CLI に加えて、宣言的 Agent DSL による動的サブエージェント実行を備えています。

## 特徴

- `yagent --config <file>` でそのまま TUI 起動
- `yagent exec --prompt ...` で単発実行
- Orchestrator-first のサブエージェント実行
- built-in agent catalog と user-defined agent DSL
- built-in agent に加えて ephemeral agent の即席生成
- Bubble Tea / Bubbles ベースの TUI
- OpenAI 互換の `chat/completions` API に対応
- Agent Status viewport による実行状況モニター
- tool call card / tool log card による実行中ツール表示
- ツールレジストリ経由で `file` `fs` `search` `git` `task` `patch` を実行
- Claude Code 風の permission UI
- `--log <path>` による JSON Lines イベントログ
- `execution.max_parallel_agents` による並列実行制御 (`min = 1`)
- `task_list` / `task_run` による安全寄りの Task Catalog
- `internal/` ベースで責務分離した構成

## コマンド

```bash
# TUI を起動
yagent

# イベントログをファイルに保存
yagent --log yagent.log

# 設定ファイル付きで TUI を起動
yagent --config ~/.yagent.toml

# 明示的に TUI を起動
yagent tui --config ~/.yagent.toml

# 単発実行
yagent exec --config ~/.yagent.toml --prompt "こんにちは"

# 単発実行 + イベントログ
yagent exec --log yagent.log --prompt "こんにちは"
```

## 設定ファイル

```toml
[server]
default = "lmstudio"

[[server.servers]]
name = "lmstudio"
url = "http://127.0.0.1:1234"
token = ""
model = "gpt-5"
timeout = "20m"

[file]
allow_paths = ["/Users/you/Projects"]

[execution]
max_parallel_agents = 2
max_handoff_depth = 2
default_timeout = "120s"

[agent_catalog]
paths = ["~/.config/yagent/agents"]

[agents.coder]
model = "gpt-5"

[agents.reviewer]
instruction = "Review for regressions first."
```

### 設定項目

- `server.default`: 使用するサーバー名
- `server.servers`: 接続先一覧
- `server.servers[].model`: そのサーバーを使うときの既定 model。CLI の `--model` や agent override がなければこれが使われます
- `server.servers[].timeout`: LLM client の HTTP timeout。未設定時は `20m`
- `file.allow_paths`: ツールからアクセス可能なパス一覧
- `execution.max_parallel_agents`: 並列実行する subagent 数の上限。`1` で逐次実行
- `execution.max_handoff_depth`: handoff の最大深度
- `execution.default_timeout`: agent ごとの LLM 呼び出し timeout
- `agent_catalog.paths`: user-defined agent DSL のファイルまたはディレクトリ一覧
- `agents.<id>.instruction`: built-in agent の instruction 上書き
- `agents.<id>.model`: built-in agent のモデル上書き
- `agents.<id>.allowed_tools`: built-in agent の tool allowlist 上書き
- `agents.<id>.disabled`: built-in agent の無効化

model の優先順は次です。

- `agents.<id>.model`
- リクエスト時の `--model`
- `server.servers[].model`

起動時のカレントディレクトリは自動で許可パスに追加されます。

## Agent DSL

yagent には最初からいくつかの built-in agent が入っていますが、リポジトリ固有の役割は TOML で追加できます。  
この TOML を README では `Agent DSL` と呼んでいます。

Agent DSL を使うと、たとえば次のような専用 agent を追加できます。

- ドキュメント更新専用 agent
- テスト観点の洗い出し専用 agent
- 特定ディレクトリだけを読む調査 agent

Agent DSL のファイルは `agent_catalog.paths` に指定したファイルまたはディレクトリから読み込みます。  
ディレクトリを指定した場合は、その中の `.toml` ファイルをすべて読み込みます。

まず built-in agent の基本セットがあり、その上に user-defined agent を追加するイメージです。  
`agents.<id>` は built-in agent の上書き設定で、Agent DSL は新しい agent を追加するための仕組みです。

標準では次の built-in agent を同梱しています。

- `manager`: ユーザー窓口と全体の取り回しを担当
- `planner`: タスク分解を担当
- `researcher`: 関連ファイルの探索と要点抽出を担当
- `coder`: 実装ターンを担当
- `tester`: 検証を担当
- `reviewer`: レビューを担当

user-defined agent は 1 ファイルにつき 1 agent を定義します。最小例は次です。

```toml
id = "docs-writer"
name = "Docs Writer"
description = "README や設計メモの更新を担当"
instruction = "Write concise docs with concrete examples."
mode = "tool"
allowed_tools = ["fs_read"]
read_only = true
max_turns = 4
timeout = "30s"
tags = ["docs"]
```

主な項目の意味:

- `id`: agent の識別子。tool / handoff からこの ID で参照されます
- `name`: UI に表示する名前。省略時は `id`
- `description`: その agent が何を担当するかの短い説明
- `instruction`: その agent に常に与える追加指示
- `mode`: agent の使われ方。`tool` は補助役、`handoff` は実装の主担当、`manager` は窓口用です
- `allowed_tools`: その agent が使ってよい tool 一覧
- `read_only`: 読み取り専用 agent として扱いたいときに指定
- `model`: その agent だけ別 model を使いたいときに指定
- `max_turns`: その agent の最大継続ターン数
- `timeout`: その agent の LLM 呼び出し timeout
- `tags`: 人間向けの整理用ラベル

`agents.<id>` では built-in agent の instruction / model / allowed tools / disabled を上書きできます。  
built-in agent の基本セットは最初から使えますが、追加の DSL で repo 専用 agent を拡張する前提です。

## Task Catalog (`tasks.toml`)

`task_list` / `task_run` で見える task は、自由なシェルコマンドではなく、事前登録した task だけを実行する仕組みです。  
その登録ファイルが `tasks.toml` です。

置き場所は次の 2 つです。

- リポジトリごとの設定: `.yagent/tasks.toml`
- ユーザー共通の設定: `~/.config/yagent/tasks.toml`

これに加えて、`go.mod` や `package.json` などを見て自動で task テンプレートも追加されます。  
読み込み順は「自動検出テンプレート → ユーザー設定 → リポジトリ設定」で、同じ `id` があれば後から読んだ定義が上書きします。

最小例:

```toml
[[tasks]]
id = "go:test"
description = "Go のテストを実行"
command = "go"
args = ["test", "./..."]
risk = "medium"
allow_network = false
timeout = 300
```

主な項目の意味:

- `id`: task の識別子。`task_run` ではこの ID を指定します
- `description`: 人間や agent が `task_list` で読む説明
- `command`: 実行するコマンド本体
- `args`: コマンド引数の配列
- `cwd`: 実行ディレクトリ。省略時は現在のリポジトリ root
- `risk`: `low` / `medium` / `high`。未指定時は `medium`
- `allow_network`: ネットワーク利用を伴う task かどうか
- `timeout`: タイムアウト秒数

実用例:

```toml
[[tasks]]
id = "go:test"
description = "Go の全テストを実行"
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
id = "frontend:test"
description = "フロントエンドの単体テストを実行"
command = "npm"
args = ["test"]
cwd = "web"
risk = "medium"
allow_network = false
timeout = 600
```

`cwd` を相対パスで書いた場合は、現在の作業リポジトリを基準に解決されます。  
まずは `description` を人間が見て分かる文にしておくと、`task_list` の一覧がかなり使いやすくなります。

## 実行ログ

`--log <path>` を指定すると、実行イベントを JSON Lines 形式で出力します。

- `execution_event.agent_started`
- `execution_event.llm_called`
- `execution_event.tool_called`
- `execution_event.tool_failed`
- `execution_event.agent_failed`
- `execution_event.agent_completed`
- `execution_event.agent_continued`
- `permission.requested`
- `permission.resolved`

`tool_failed` と `agent_failed` には失敗理由も記録されるので、subagent の失敗調査に使えます。  
権限確認や継続確認が複数同時に発生した場合でも、TUI 側で順番に処理できます。

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

ファイル操作や task 実行などで許可が必要なときは、下部に permission card を表示します。

- `←/→` または `Tab`: 選択移動
- `Enter`: 確定
- `Esc`: 拒否

非ファイル系の選択肢:

- 今回だけ許可
- 同じ操作を以後許可
- 拒否

ファイル系の選択肢:

- 今回だけ許可
- 同じ操作を以後許可
- ファイルパターン指定で以後許可
- 拒否

`同じ操作を以後許可` は、同じツール・同じ action・同じ scope・同じ risk に対して再利用されます。  
`ファイルパターン指定で以後許可` は、指定した glob パターンに一致する同種のファイル系リクエストを、そのセッション中は自動で許可します。  
パターンは `*.go` のような basename glob と `internal/*` のような path glob を使えます。  
サブエージェント導入後も permission の判定ルール自体は変わりません。  
permission card には、どの agent が要求したかも表示されます。

### Tool Call UI

LLM が tool を呼び出すと、TUI 下部に tool call card を表示します。

- 実行しようとしている `tool` 名
- 対象 path や task id などの `target`
- `limit_bytes`, `recursive`, `overwrite` などの主なオプション
- 実行状態 (`requesting`, `completed`, `failed`)

tool の出力は `Tool Logs` card に蓄積されます。

### Agent Status

TUI では会話ログとは別に `Agent Status` viewport を表示し、サブエージェントの状態を監視できます。

- 実行中 / 完了 / 失敗した agent 数
- 親子関係を含む agent ツリー
- handoff / delegate の流れ
- 直近イベント
- `elapsed`
- `ctx`

`ctx` は、参照中の `message + unique file refs + artifact refs` の合計です。  
`elapsed` は 1 秒単位で表示されます。

### 継続確認

built-in agent の既定 `max_turns` は `200` です。  
上限に達すると、permission UI と同じ導線で「継続実行するか」を確認します。

- 許可するとその agent の turn カウンタをリセットして続行
- 拒否すると `agent_failed` として終了

権限確認と継続確認の待ち時間は agent timeout に含まれません。

## アーキテクチャ

主要コードは `internal/` 配下にあります。

```text
internal/
  app/       起動 wiring と依存解決
  cli/       Cobra command 定義
  config/    TOML 設定読込
  domain/    中核型と interface
  infra/     LLM client / tools / policy / agent catalog 実装
  tui/       Bubble Tea / Bubbles の UI
  usecase/   orchestrator / task catalog / 実行ロジック
```

### 実行モデル

- `manager` がユーザー入力を受ける
- リポジトリ全体の調査や品質レポートのような広域タスクでは、`manager` は `planner` / `researcher` への委譲を優先する
- `delegate_to_<agent>` で bounded task を subagent に委譲
- `handoff_to_<agent>` で専門 agent に現在ターンを handoff
- `run_ephemeral_agent` で一時的な subagent を即席生成
- リポジトリ探索には `fs_list` を優先し、単純な一覧取得のためにスクリプト生成へ逃がさない
- `planner -> coder -> planner` のような再委譲ループは orchestrator で抑止
- 並列化は非破壊系 task のみ。書き込み系は常に直列化

### ツール

現在の主な tool は次です。

- `fs_read` / `fs_write` / `fs_list` / `fs_stat` / `fs_remove` / `fs_move`
- `search_text` / `search_files`
- `git_status` / `git_diff` / `git_log` / `git_show`
- `task_list` / `task_run`
- `patch_apply`

Task catalog は `.yagent/tasks.toml` と自動検出テンプレートから構築されます。

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
- `internal/infra/agents/catalog`: built-in catalog / user DSL 読込テスト
- `internal/infra/llm`: fake server を使った HTTP クライアントテスト
- `internal/infra/policy`: permission policy / path policy テスト
- `internal/infra/tools`: file/fs/task/registry のユニットテスト
- `internal/usecase/orchestrator`: delegation / handoff / ephemeral agent の実行テスト
- `internal/usecase/taskcatalog`: task catalog の読込テスト
- `internal/tui`: state transition と viewport / permission UI / status UI のテスト

## ライセンス

MIT
