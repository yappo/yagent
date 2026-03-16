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
- ツールレジストリ経由で `fs` `search` `git` `task` `patch` を実行
- Claude Code 風の permission UI
- `--log <path>` による JSON Lines イベントログ
- `execution.max_parallel_agents` による並列実行制御 (`min = 1`)
- planning mode による `plan -> approve -> execute` の2段階実行
- `fs_read` / `fs_list` / `search_*` / `git_*` のセッションキャッシュ
- phase-driven gather / synthesize による no-new-information ループ防止
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
enable_planning = true

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
- `execution.enable_planning`: 実行前に計画を作成し、承認後に read-only batch を先に進める
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

user-defined agent は TOML で定義します。

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

標準では次の built-in agent を同梱しています。

- `manager`
- `planner`
- `researcher`
- `coder`
- `tester`
- `reviewer`

`agents.<id>` では built-in agent の instruction / model / allowed tools / disabled を上書きできます。  
built-in agent の基本セットは最初から使えますが、追加の DSL で repo 専用 agent を拡張する前提です。

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

## 実行効率化

### Planning Mode

`execution.enable_planning = true` のとき、manager は最初に実行計画を作り、承認後に実行へ進みます。

- 計画には `summary`, `target_files`, `exit_conditions`, `batches` が含まれます
- `batches` に入った read-only tool call は、実行前にまとめて先行実行されます
- 実行フェーズでは承認済み plan を instruction に渡し、compatible な read-only call をまとめて出すように誘導します

### Phase-Driven Execution

各 agent は内部的に `gather -> synthesize -> finish` の phase を持ちます。

- `gather` 中は追加取得を自動で許可します
- ただし、同じ capability / target の取得で新情報が増えない場合は `novelty_exhausted` とみなします
- `novelty_exhausted` が続くと `synthesize` に遷移し、追加 tool call を止めて回答生成を優先します
- `fs_list` / `search_*` の結果からは候補 path も working set に取り込み、次の focused read/search に進みやすくします
- cached result は working set に反映し、同じ tool chatter を会話履歴に積み増し続けないようにしています

### Session Cache

セッション中は次の結果をキャッシュします。

- `fs_read`
- `fs_list`
- `fs_stat`
- `search_text`
- `search_files`
- `git_status`
- `git_diff`
- `git_log`
- `git_show`

`fs_write`, `fs_remove`, `fs_move`, `patch_apply`, `task_run` が成功すると、関連キャッシュは無効化されます。  
`fs_read` の結果からは言語非依存の軽量 summary も生成し、次の agent 呼び出し時の context に再利用します。

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

選択肢:

- 今回だけ許可
- このセッションで許可
- 拒否

`このセッションで許可` は、同じツール・同じ action・同じ scope・同じ risk に対して再利用されます。  
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

`ctx` は、参照中の `message + unique file refs + artifact refs + findings + recent observations` の合計です。  
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
