# yagent

「AIに bash 渡しとけば賢くやるでしょ」をやめた yagent は Bash tool を置かず task catalog/file permission/MCP wrappingでコンテキスト問題対策/subagent orchestration を最初から設計に入れた、*LLMを信用しない* 前提の coding agent。でも task や agent 追加は簡単!
https://github.com/yappo/yagent/

## yagent のユニークなポイント

yagent は、「LLM に bash を渡しておけば勝手に賢くやる」という発想ではなく、「LLM は便利だが、そのまま広い権限を与えるには信用しきれない」という前提から設計した coding agent です。そのため、あえて汎用の Bash tool は持たせていません。自由な shell 実行は強力ですが、モデルの誤解や暴走がそのまま危険操作につながりやすく、実運用では不安要素にもなります。yagent はそこを便利さで押し切らず、最初から危険な操作を簡単には通さない構造を選んでいます。

ただし、bash を消すだけでは不便になります。そこで yagent では、必要な作業を `tasks.toml` の task catalog として追加できるようにし、ビルド、テスト、検証、補助コマンド、MCP server の bind などを、安全性を意識した独自ツール経由で扱えるようにしています。つまり「何でも shell でやらせる」のではなく、「このリポジトリでは何を許可するか」を宣言的に定義して実行する形です。安全側に倒しつつ、運用上必要な task は素直に足せるので、窮屈な固定製品ではなく、現場仕様に育てやすい構成になっています。

ファイルアクセスも同じ思想です。広く自由に読める・書ける方が短期的には便利ですが、実際にはそれがそのまま不安につながります。yagent ではアクセス可能なパスを明示的に絞り込みつつ、作業の流れを止めすぎないよう permission UI を通じて問題ない範囲だけ段階的に緩められます。厳しく閉じるだけでもなく、最初から全部開けるだけでもなく、現場で扱いやすい粒度で制御できることを重視しています。

MCP Server についても、そのまま無造作に露出させるのではなく、task catalog と bind フローの中で独自にラップしています。これは安全性のためだけではありません。MCP は便利な一方で、server や tool の情報を常時抱え込むとコンテキストを食いやすく、必要のない情報まで LLM に渡し続ける構造になりがちです。yagent では必要なときにだけ `task_bind` で server を bind し、必要な tool だけを公開できるので、コンテキスト消費を抑えながら拡張性も確保しやすくなっています。

さらに yagent は、単一 agent に全部背負わせるのではなく、subagent / multiagent 前提で設計されています。`manager` `planner` `researcher` `coder` `tester` `reviewer` のような役割分担をベースに、`intake -> plan -> execute -> verify -> recover -> finalize` の harness で仕事を分け、必要なら ephemeral agent もその場で生成できます。最近の runtime では会話 summary ではなく typed artifact / reusable observation / workspace snapshot を主記憶に寄せているので、長めの run でも次の agent に必要な事実だけを scoped packet として渡しやすくなっています。

要するに yagent は、単なる「TUI から LLM を叩くツール」でも、単なる「自律実行が派手な agent」でもありません。Bash を安易に渡さず、task catalog・file permission・MCP wrapping・subagent orchestration を最初から設計に入れたうえで、それでも task や agent は簡単に拡張できるようにした、**“LLM を信用しない” 前提の実運用志向 coding agent** です。

## 特徴

- `yagent --config <file>` でそのまま TUI 起動
- `yagent setup` で repo-local `.yagent/config.toml` と `.yagent/tasks.toml` を生成
- `yagent schema agent` / `yagent schema tasks` で Agent DSL / task catalog の JSON Schema を出力
- `yagent exec --prompt ...` で単発実行
- `yagent exec --show-events` / `--format json` で単発実行の run / tool / model / failure summary を出力
- `yagent exec --stream` で OpenAI-compatible streaming transport を使う
- `yagent exec --resume latest --profile strong` のような resume / routing 指定
- `yagent benchmark --case ... --routing-candidate ...` で harness eval / local-remote 比較
- `yagent benchmark report --input ...` で保存済み eval record の集計・baseline 差分・回帰 gate
- Orchestrator-first のサブエージェント実行
- planner-driven execution plan で必要な agent だけを選択
- planner / verification / final response の runtime structured output
- typed artifact payload validation at the state-store boundary
- `intake -> plan -> execute -> verify -> recover -> finalize` の run harness
- adaptive packet compaction と state-first workspace memory
- relevance-ranked and budgeted context packets for artifacts / reusable observations
- role / phase ベースの model routing
- lazy capability discovery (`list_capabilities` / `enable_capability`)
- built-in agent catalog と user-defined agent DSL
- built-in agent に加えて ephemeral agent の即席生成
- Bubble Tea / Bubbles ベースの TUI
- Run Graph / Plan / Verification / Memory の切替 panel
- OpenAI 互換の `chat/completions` API に対応
- Agent Status viewport による実行状況モニター
- tool call card / tool log card による実行中ツール表示
- ツールレジストリ経由で `fs` `search` `git` `task` `patch` を実行
- Claude Code 風の permission UI
- `--log <path>` による JSON Lines イベントログ
- `yagent audit bundle` / `yagent audit trace` / `yagent audit search` / `yagent audit runs` / `yagent audit artifacts` / `yagent audit conversations` / `yagent audit observations` / `yagent audit mutations` / `yagent audit permissions` / `yagent audit executions` / `yagent audit models` による audit export
- `execution.max_parallel_agents` による並列実行制御 (`min = 1`)
- `task_list` / `task_run` / `task_bind` による安全寄りの Task Catalog
- `task_run` / `task_bind` failure の structured error output
- `tasks.toml` の `[[mcpservers]]` と遅延 bind による MCP 連携
- `internal/` ベースで責務分離した構成

## コマンド

```bash
# TUI を起動
yagent

# イベントログをファイルに保存
yagent --log yagent.log

# 設定ファイル付きで TUI を起動
yagent --config ~/.yagent.toml

# local Qwen / LM Studio 用の repo-local starter files を生成
yagent setup

# 既存ファイルを上書きせずに生成予定を確認
yagent setup --dry-run

# Agent DSL / task catalog の JSON Schema を出力
yagent schema agent --out .yagent/agent.schema.json
yagent schema tasks --out .yagent/tasks.schema.json

# 明示的に TUI を起動
yagent tui --config ~/.yagent.toml

# 単発実行
yagent exec --config ~/.yagent.toml --prompt "こんにちは"

# 単発実行 + イベントログ
yagent exec --log yagent.log --prompt "こんにちは"

# 前回 run を復元して続ける
yagent exec --resume latest --prompt "続きの修正をして"

# routing profile を指定
yagent exec --profile strong --prompt "慎重にレビューして"

# 単発実行でも run / tool / model / failure の要約を表示
yagent exec --show-events --prompt "README を点検して"

# CI/harness から使いやすい JSON で message/run/event summary を出力
yagent exec --format json --prompt "README を点検して"

# feature flag の比較
yagent benchmark --feature-profile legacy --feature-profile current --prompt "この repo の改善点を提案して"

# local Qwen と strong profile を同じ eval case で比較
yagent benchmark --case repo-readonly --routing-candidate local=fast --routing-candidate strong=strong --write-jsonl .yagent/benchmarks/repo-readonly.jsonl

# local Qwen の runtime readiness を確認してから benchmark
yagent benchmark --preflight-doctor --preflight-doctor-probe-structured --preflight-fail-on-warning --case repo-readonly --routing-candidate local=fast

# 保存済み benchmark record を集計し、legacy からの回帰を gate
yagent benchmark report --input .yagent/benchmarks/repo-readonly.jsonl --baseline legacy --fail-on-regression --min-pass-rate 0.80

# CI で扱いやすい JUnit XML として保存済み eval を export
yagent benchmark report --input .yagent/benchmarks/repo-readonly.jsonl --format junit > .yagent/benchmarks/junit.xml

# 利用できる built-in eval case を表示
yagent benchmark --list-cases

# LM Studio / Qwen 接続を確認
yagent doctor

# model load / context / quantization / capability を確認
yagent doctor --runtime

# runtime metadata と JSON schema structured output まで確認
yagent doctor --runtime --probe-structured

# CI/harness 用に JSON 出力し、warning/recommendation を gate
yagent doctor --runtime --probe-structured --format json --fail-on-warning --fail-on-recommendation

# run / artifact audit を表示
yagent audit bundle --run latest
yagent audit bundle --run latest --format text
yagent audit bundle --run latest --full-run --include-output --include-artifact-payload
yagent audit trace --run latest
yagent audit conversations
yagent audit conversations --run latest --format json --include-messages
yagent audit trace --run latest --kind model --format json
yagent audit search "permission denied" --run latest
yagent audit search qwen --kind model --format json
yagent audit search README --include-output
yagent audit runs
yagent audit runs --run latest --format json
yagent audit runs --run latest --full --format json
yagent audit artifacts --run latest --kind final_response --include-payload
yagent audit observations --run <run-id>
yagent audit mutations --run <run-id>
yagent audit runtime
yagent audit models --run <run-id>
yagent audit models --server local --format json
yagent audit models --run <run-id> --summary

# permission decision audit を表示
yagent audit permissions
yagent audit permissions --format json --run <run-id>

# tool execution audit を表示
yagent audit executions
yagent audit executions --format json --run <run-id>
```

## 設定ファイル

`--config` を指定しない場合、カレントディレクトリに `.yagent/config.toml` があればそれを読みます。存在しなければ built-in default を使います。`yagent setup` はこの repo-local config と `.yagent/tasks.toml` を生成します。既存ファイルは既定では上書きせず、上書きする場合は `--force` を指定します。

`yagent setup` の主な option は `--local-url` `--local-model` `--openai-model` `--config-out` `--tasks-out` `--dry-run` `--force` です。task catalog は `go.mod` と `package.json` を見て、`go:test` / `go:build` / `npm:test` / `npm:build` / `npm:lint` / `npm:typecheck` のうち該当するものを生成します。

`yagent schema agent` と `yagent schema tasks` は、user-defined Agent DSL と `tasks.toml` の JSON Schema を stdout に出力します。`--out <path>` を付けると schema file として保存できます。エディタ補完や CI 側の schema validation では、この出力をそのまま使えます。

```toml
[server]
default = "local"

[[server.servers]]
name = "local"
url = "http://127.0.0.1:1234"
token = ""
model = "Qwen/Qwen3.6-35B-A3B"
api = "chat_completions"
timeout = "20m"

[server.servers.generation]
max_output_tokens = 8192
temperature = 1.0
top_p = 0.95
top_k = 20
min_p = 0.0
presence_penalty = 1.5
repetition_penalty = 1.0

[[server.servers]]
name = "openai"
url = "https://api.openai.com"
token = ""
token_env = "OPENAI_API_KEY"
model = "gpt-5.5"
api = "responses"
timeout = "20m"

[server.servers.generation]
reasoning_effort = "high"
text_verbosity = "low"
parallel_tool_calls = true
store = false

[file]
allow_paths = ["/Users/you/Projects"]
deny_paths = [".env", "*.pem"]

[[file.rules]]
decision = "deny"
paths = [".git", ".yagent/state/*"]

[execution]
max_parallel_agents = 2
max_handoff_depth = 2
default_timeout = "120s"

[features]
phase_harness = true
adaptive_compaction = true
role_routing = true
repo_memory = true

[routing.profiles.fast]
server = "local"
model = "Qwen/Qwen3.6-35B-A3B"

[routing.profiles.strong]
server = "local"
model = "Qwen/Qwen3.6-35B-A3B"
fallback_server = "openai"
fallback_model = "gpt-5.5"

[routing.profiles.strong.generation]
reasoning_effort = "high"

[harness]
max_verification_attempts = 2
force_planner = true
continuation_policy = "prompt"

[context]
max_recent_messages = 12
max_artifacts = 8
max_relevant_files = 16
compact_after_turns = 12
compact_after_tool_calls = 12
compact_after_est_tokens = 12000
compact_after_verify_cycles = 2

[memory]
enabled = true
state_dir = ".yagent/state"
max_runs = 20
max_facts = 50

[benchmark]
default_runs = 2

[agent_catalog]
paths = ["~/.config/yagent/agents"]

[agents.coder]
model = "Qwen/Qwen3.6-35B-A3B"
routing_profile = "strong"

[agents.reviewer]
instruction = "Review for regressions first."
```

### 設定項目

- `server.default`: 使用するサーバー名
- `server.servers`: 接続先一覧
- `server.servers[].model`: そのサーバーを使うときの既定 model。CLI の `--model` や agent override がなければこれが使われます
- `server.servers[].token`: API token。指定時は `token_env` より優先します
- `server.servers[].token_env`: API token を読む環境変数名。OpenAI fallback では `OPENAI_API_KEY` などを指定します
- `server.servers[].api`: `chat_completions` または `responses`。local OpenAI-compatible runner は通常 `chat_completions`、OpenAI reasoning / tool-heavy workflow は `responses`
- `server.servers[].timeout`: LLM client の HTTP timeout。未設定時は `20m`
- `server.servers[].generation`: model request に渡す生成設定。`max_output_tokens` `temperature` `top_p` `top_k` `min_p` `presence_penalty` `repetition_penalty` `reasoning_effort` `text_verbosity` `parallel_tool_calls` `store` を指定できます
- `file.allow_paths`: ツールからアクセス可能な root path 一覧。カレントディレクトリは起動時に自動追加されます
- `file.deny_paths`: `allow_paths` より優先して拒否する path / glob
- `file.rules`: `allow` / `deny` の path / glob rule。deny rule は allowed root より優先し、allow rule は明示的な追加許可として使います
- `permission.rules`: tool call の静的 policy。上から順に評価し、最初に一致した rule の `decision` を使います
- `execution.max_parallel_agents`: 並列実行する subagent 数の上限。`1` で逐次実行
- `execution.max_handoff_depth`: handoff の最大深度
- `execution.default_timeout`: agent ごとの LLM 呼び出し timeout
- `features.phase_harness`: phased multi-agent harness を使うか
- `features.adaptive_compaction`: packet を軽くする compaction を使うか
- `features.role_routing`: role / phase ベースの router を使うか
- `features.repo_memory`: workspace memory / observation cache の読込と更新を使うか
- `routing.profiles.*`: role / phase ごとの model routing 先
- `routing.profiles.*.generation`: server default の生成設定に profile 固有の上書きを重ねます
- `harness.max_verification_attempts`: verify -> recover の最大試行回数
- `harness.force_planner`: 実装前に planner を強制するか
- `harness.continuation_policy`: agent が `max_turns` に到達したときの既定動作。`prompt` / `allow` / `deny`
- `context.*`: recent messages / artifacts / compaction 閾値
- `memory.*`: `./.yagent/state/` 配下の session state / workspace memory / observation cache 設定
- `benchmark.default_runs`: `yagent benchmark` の既定試行回数
- `agent_catalog.paths`: user-defined agent DSL のファイルまたはディレクトリ一覧
- `agents.<id>.instruction`: built-in agent の instruction 上書き
- `agents.<id>.model`: built-in agent のモデル上書き
- `agents.<id>.routing_profile`: built-in agent の routing profile 上書き
- `agents.<id>.allowed_tools`: built-in agent の tool allowlist 上書き
- `agents.<id>.disabled`: built-in agent の無効化

tool scheduler は `read_set` / `write_set` が分かる tool call を path conflict ベースで並列化します。`process` / `external` / workspace mutation のような side effect でも、task catalog の `read_paths` / `write_paths` や MCP roots / path 引数から scope を解決できる場合は、`execution.max_parallel_agents` と source limit の範囲で同時実行できます。scope が不明な process / external call は従来通り直列化します。

model の優先順は次です。

- `agents.<id>.model`
- リクエスト時の `--model`
- `server.servers[].model`

起動時のカレントディレクトリは自動で許可パスに追加されます。

`permission.rules` の `decision` は `allow` / `require_approval` / `deny` です。selector には `tool` `action` `resource_kind` `risk` `resource` `resources` `agent` `side_effect` `side_effects` を使えます。`resource` は exact match、basename glob、path glob を受け付けます。どの rule にも一致しない場合は、従来通り Permission UI に確認します。

```toml
[[permission.rules]]
tool = "fs_stat"
risk = "low"
decision = "allow"

[[permission.rules]]
tool = "task_run"
side_effect = "network_access"
decision = "deny"

[[permission.rules]]
tool = "fs_write"
agent = "researcher"
decision = "deny"
```

`memory.state_dir` 配下の主なレイアウトは次です。

- `sessions/`: 各 session の state
- `workspace/facts.json`: stable workspace facts / known failures / reusable observations の要約
- `workspace/snapshot.json`: 観測 freshness 判定に使う workspace snapshot
- `artifacts/`: typed artifact
- `conversations/`: turn ごとの request/context/output message と event summary
- `observations/`: reusable observation record
- `executions/`: tool execution record
- `mutations/`: mutation record。小さいファイルでは content SHA-256 を含む path state から `mutation_fingerprint` を作ります
- `scratch/`: permission decision など、run artifact へ集約する一時 record
- `latest_session`: `/resume` が参照する最新 session id

Context packet は artifact / reusable observation を relevance ranking したうえで、runtime store がある場合は observation の `read_set` と workspace snapshot の path state で freshness を確認します。`workspace/facts.json` の reusable observation summary は observation record の `integrity_sha256` と突き合わせ、不一致の summary は packet に載せません。Agent DSL の `token_budget` が指定されている場合は、その agent の context packet を概算 token 数で上限管理し、低優先の recent messages / artifacts / observations から削ります。

## LM Studio / Qwen

Qwen は LM Studio 側でユーザが download / load / server start しておく前提です。yagent 側では次をサポートします。

- `yagent setup` による repo-local starter config / task catalog 生成
- `server.servers[].url = "http://127.0.0.1:1234"` の OpenAI-compatible endpoint
- `server.servers[].api = "chat_completions"` または `"responses"`
- Qwen thinking mode 向けの `server.servers[].generation`
- `yagent doctor` による `/v1/models` 接続確認と model identifier の確認
- `yagent doctor --runtime` による LM Studio REST API の load 状態 / context length / quantization / tool-use capability 確認と設定推奨
- `yagent doctor --probe` による lightweight generation 確認
- `yagent doctor --probe-structured` による JSON schema structured output 確認
- `yagent doctor --format json` / `--fail-on-warning` / `--fail-on-recommendation` による CI/harness gate
- `yagent doctor --write-recommended-config` による、runtime 診断結果を反映した complete config TOML の書き出し
- `yagent doctor --save-audit` と `yagent audit runtime` による runtime 診断結果の保存・比較

LM Studio 公式 docs では Developer tab の "Start server"、または `lms server start` で local server を起動できます。OpenAI-compatible endpoint は `/v1/models` `/v1/chat/completions` `/v1/responses` を提供します。LM Studio REST API の `/api/v1/models` では、download 済み model に加えて loaded instance の context length、max context、quantization、capability も確認できます。

```bash
# repo-local starter files を生成
yagent setup

# LM Studio を起動済みの状態で確認
yagent doctor

# loaded context / quantization / tool-use capability を確認
yagent doctor --runtime

# default 以外の server を確認
yagent doctor --server openai

# 軽い生成リクエストで model runtime を確認
yagent doctor --probe

# planner / verifier / finalizer と同じ JSON schema 経路を確認
yagent doctor --runtime --probe-structured

# machine-readable な診断結果を出して、未調整 runtime を gate
yagent doctor --runtime --probe-structured --format json --fail-on-warning --fail-on-recommendation

# runtime 診断から yagent 側で反映できる推奨値を complete config として書き出す
yagent doctor --runtime --write-recommended-config .yagent/config.recommended.toml

# runtime 診断を state に保存し、後から確認する
yagent doctor --runtime --probe-structured --save-audit
yagent audit runtime
yagent audit runtime --server local --format json
```

MacBook Air M4 / 32GB では、最初は Qwen3.6-35B-A3B の量子化モデルを text-only / 16k-32k context 程度で試し、必要に応じて context を上げてください。LM Studio の `/v1/models` に出る model identifier が `Qwen/Qwen3.6-35B-A3B` と違う場合は、`config.toml` の `model` を LM Studio 側の名前へ合わせます。`doctor --runtime` は loaded context が 25k 未満の場合に warning を出します。これは coding agent が tool/context を多く消費するためで、LM Studio の Codex integration docs でも 25k 超の context が目安として案内されています。

`doctor --runtime` の `Recommendations` は、LM Studio 側の loaded context、`context.compact_after_est_tokens`、`server.servers[].generation.max_output_tokens`、Qwen3.6 thinking mode 向け sampling parameter を、現在の runtime metadata と config から提案します。たとえば 8k context で model を load している場合は、LM Studio 側では 32k context への引き上げ、Yagent 側では output token を小さめに抑える提案が出ます。Qwen3.6 の starter config は、model card の agent/coding benchmark 設定に合わせて `temperature = 1.0` / `top_p = 0.95` / `top_k = 20` / `min_p = 0.0` / `presence_penalty = 1.5` / `repetition_penalty = 1.0` を入れています。

`--format json` は `problems` / `warnings` / `recommendations` / `runtime` / `probe` を stable key で出力します。既定では `problems` だけが exit status を失敗にします。`--fail-on-warning` と `--fail-on-recommendation` を付けると、local runtime の未調整状態も benchmark 前の gate として扱えます。

`--write-recommended-config <path>` は `--runtime` を兼ね、`/v1/models` の fuzzy match で見つかった exact model id、`context.compact_after_est_tokens`、local server の `generation.*` 推奨値を反映した complete config TOML を書き出します。LM Studio 側の loaded context length など yagent の config で変更できない項目は stderr の `external` に残します。既存ファイルは上書きせず、上書きする場合は `--force-recommended-config` を付けます。

`--save-audit` は `--runtime` を兼ね、doctor 結果を `memory.state_dir` の scratch record として保存します。`yagent audit runtime` は保存済みの server/model、loaded context、quantization、probe、warning/recommendation 数を text または JSON で出力します。local Qwen の quantization や context 設定を変えながら benchmark record と突き合わせるための証跡として使えます。

## TUI Panels

右側 pane は `Run Graph / Plan / Verification / Memory` を切り替えられます。`/graph` `/plan` `/verification` `/memory` で直接開けて、`Ctrl+←/→` でも順送りできます。`/resume` を使うと `latest_session` を読み込み、`/resume <run-id>` で保存済み run を指定して、Plan / Verification / Memory panel もその内容で更新できます。`/memory` panel には stable facts / known failures / reusable observations / recent artifacts を表示します。

TUI の header には現在の routing profile、model override、streaming 状態、theme を表示します。`/profile` は利用可能な routing profile を表示し、`/profile fast` や `/profile strong` で以後の turn に渡す profile を切り替えます。`/profile clear` で既定 routing に戻します。設定済み profile がある場合、未知の profile 名は拒否され、`/profile f` のような入力では profile 名を `Tab` 補完できます。`/model <name>` は全 agent に明示 model override を渡し、profile の model 解決に戻す場合は `/model clear` を使います。`/stream on` は text delta を assistant block に逐次表示し、`/stream off` で通常の完了後表示に戻します。`/theme` は利用可能な TUI theme を表示し、`/theme contrast` や `/theme mono` で表示 theme を切り替え、`/theme clear` で既定 theme に戻します。通常は local Qwen 用 profile を `/profile fast`、OpenAI fallback を含む profile を `/profile strong` のように切り替え、model override は一時的な検証にだけ使います。

## Benchmark

`yagent benchmark` は feature flag / routing profile / harness eval case を同じ形式で比較するためのコマンドです。`--prompt` だけを渡すと、既定では `legacy` と `current` を比較します。

- `legacy`: phase harness / compaction / role routing / repo memory をすべて無効化した single-manager baseline
- `current`: `features.*` に設定された現在の構成
- `no-harness`, `no-routing`, `no-memory`, `no-compaction`: 個別 flag を切って比較する補助 profile

`--routing-candidate` を指定すると、同じ prompt / case を複数の routing profile で実行できます。たとえば local Qwen 用の `fast` と、OpenAI fallback を含む `strong` を比較できます。

```bash
yagent benchmark \
  --case repo-readonly \
  --routing-candidate local=fast \
  --routing-candidate strong=strong \
  --runs 1 \
  --write-jsonl .yagent/benchmarks/repo-readonly.jsonl \
  --write-csv .yagent/benchmarks/repo-readonly.csv \
  --save-artifact
```

local Qwen / LM Studio を対象にするときは、`--preflight-doctor` で benchmark 開始前に `doctor` gate を走らせられます。preflight の要約は stderr に出るため、`--output json` / `--output csv` の stdout は壊しません。`--preflight-doctor-probe-structured` は JSON schema structured output まで確認し、`--preflight-fail-on-warning` / `--preflight-fail-on-recommendation` は runtime warning や tuning recommendation を benchmark failure として扱います。preflight summary は `--output json` の report と `--write-jsonl` / `--write-csv` の flat record にも保存されるため、後から eval 結果、実際の model call / fallback metadata、runtime context / quantization / warning 数を同じ record で比較できます。

```bash
yagent benchmark \
  --preflight-doctor \
  --preflight-doctor-probe-structured \
  --preflight-fail-on-warning \
  --case repo-readonly \
  --routing-candidate local=fast \
  --runs 1
```

組み込み eval case は `yagent benchmark --list-cases` で確認できます。現在の built-in は `repo-readonly` `swe-like` `terminal-like` `permission-gate` です。`swe-like` は repo mutation を含むため、捨て worktree で実行する前提です。

独自 case は TOML で追加できます。

```toml
[[cases]]
id = "readme-audit"
name = "README audit"
prompt = "README.md を読み、CLI と設定の説明が実装と一致するか確認してください。ファイルは変更しないでください。"
tags = ["readonly", "docs"]

[cases.expectations]
status = "completed"
contains = ["README"]
required_tools = ["fs_read"]
min_tool_calls = 1
max_failed_events = 0
max_verification_failures = 0
```

```bash
yagent benchmark --case-file ./benchmarks/cases.toml --case readme-audit --output table
```

出力は各 profile ごとの成功数、eval pass 数、平均時間、平均イベント数、verification failure 数、各 run の phase / attempt / tool call 数をまとめます。`--output jsonl` / `--output csv` で stdout へ flat record を出せます。`--write-jsonl` / `--write-csv` は同じ flat record をファイルに保存します。`--save-artifact` は report と flat records を `memory.state_dir` の `benchmark_report` typed artifact として保存します。保存後は `yagent audit artifacts --run latest --kind benchmark_report --include-payload` で取り出せます。

保存済み record は `benchmark report` で再集計できます。JSONL/CSV を複数 `--input` で渡すと profile/case 単位に pass rate、成功率、平均時間、平均 tool call 数、平均 model call 数、model fallback 数、verification failure 数をまとめます。record には実際に使われた server / model / API / routing profile / LLM duration も保存されるため、local Qwen と OpenAI fallback の差を audit store なしでも比較できます。`--baseline <profile>` を付けると同じ case の baseline との差分を出し、`--fail-on-regression` は pass rate 低下または verification failure 増加を command failure にします。`--min-pass-rate` は CI や日次比較で最低 pass rate を gate するための閾値です。`--format junit` は group ごとの pass rate と runtime gate を JUnit XML testcase として出すため、既存 CI の test report UI に載せられます。

```bash
yagent benchmark report \
  --input .yagent/benchmarks/repo-readonly.jsonl \
  --input .yagent/benchmarks/terminal-like.csv \
  --baseline legacy \
  --fail-on-regression \
  --min-pass-rate 0.80
```

保存済み runtime audit を report に添付して、local Qwen の loaded context / warning / structured probe を同じ gate で確認できます。これは `yagent doctor --runtime --probe-structured --save-audit` で保存した最新 record を使います。

```bash
yagent benchmark report \
  --input .yagent/benchmarks/repo-readonly.jsonl \
  --runtime-audit-server local \
  --require-runtime-audit \
  --require-runtime-loaded \
  --require-runtime-structured-probe \
  --min-runtime-context 32768 \
  --max-runtime-warnings 0
```

`--format json` / `--format csv` / `--format junit` を指定すると、保存済み eval の集計結果をさらに別の harness や dashboard に渡せます。

## Agent DSL

yagent には最初からいくつかの built-in agent が入っていますが、リポジトリ固有の役割は TOML で追加できます。  
この TOML を README では `Agent DSL` と呼んでいます。

planner-driven execution plan では、この Agent DSL で定義した metadata も agent inventory に入ります。`task_kinds` `capabilities` `preferred_phases` `scope_hints` を明示しておくと、planner が built-in agent と同じ基準で user-defined agent を選びやすくなります。

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

built-in agent には、tool discovery workflow に関する共通 instruction も入っています。  
特に次の前提を明示しています。

- visible な `mcp__*` が無くても、ただちに「MCP は使えない」とは限らない
- MCP が必要そうなら、まず `task_list` で `kind = "mcp_server"` の entry を確認する
- relevant な server が未 bind なら `task_bind(task_id=...)` を呼ぶ
- bind 成功後は返却された `tool_names` をそのまま使う
- write-capable agent では `fs_write` / `patch_apply` を直接呼び、承認は会話ではなく approval dialog で行う
- write tool 実行時の承認は会話ではなく approval dialog で行われる
- 現在の agent が read-only で write が必要なときは、write-capable agent に delegate / handoff する

user-defined agent は 1 ファイルにつき 1 agent を定義します。最小例は次です。

```toml
id = "docs-writer"
name = "Docs Writer"
description = "README や設計メモの更新を担当"
instruction = "Write concise docs with concrete examples."
mode = "handoff"
allowed_tools = ["fs_read", "fs_write", "patch_apply"]
read_only = false
max_turns = 4
token_budget = 1200
timeout = "30s"
tags = ["docs"]
task_kinds = ["docs", "mutate"]
capabilities = ["documentation"]
preferred_phases = ["execute", "recover"]
scope_hints = ["README", "design docs"]
verification_required = true
verification_max_attempts = 2
```

Agent DSL は読み込み時に strict validation されます。未知キー、空の `id`、不正な `mode` / `task_kinds` / `preferred_phases`、負の `timeout` / `max_turns` / `token_budget` / `verification_max_attempts`、空文字の list item は source path と agent id 付きのエラーとして返します。schema は `yagent schema agent` で出力できます。

主な項目の意味:

- `id`: agent の識別子。tool / handoff からこの ID で参照されます
- `name`: UI に表示する名前。省略時は `id`
- `description`: その agent が何を担当するかの短い説明
- `instruction`: その agent に常に与える追加指示
- `mode`: agent の使われ方。`tool` は補助役、`handoff` は実装の主担当、`manager` は窓口用です
- `allowed_tools`: その agent が使ってよい tool 一覧
- `read_only`: 読み取り専用 agent として扱いたいときに指定
- `read_only = true` の agent は write tool を新たに得るわけではありません。ファイル変更が必要なときは write-capable agent へ委譲する前提です
- `model`: その agent だけ別 model を使いたいときに指定
- `max_turns`: その agent の最大継続ターン数
- `token_budget`: その agent の context packet 上限。概算 token 数で recent messages / artifacts / observations を抑制します
- `timeout`: その agent の LLM 呼び出し timeout
- `routing_profile`: その agent をどの routing profile で実行するか
- `tags`: 人間向けの整理用ラベル
- `task_kinds`: その agent が向いている要求種別。`question` `research` `docs` `review` `test` `mutate` を指定
- `capabilities`: planner に見せる能力ラベル
- `preferred_phases`: その agent が入りやすい phase。`plan` `execute` `verify` `recover` `finalize`
- `scope_hints`: planner に伝える担当範囲のヒント
- `verification_required` / `verification_max_attempts`: その agent を primary にしたときの verify policy

`agents.<id>` では built-in agent の instruction / model / allowed tools / disabled を上書きできます。  
built-in agent の基本セットは最初から使えますが、追加の DSL で repo 専用 agent を拡張する前提です。

## Task Catalog (`tasks.toml`)

`task_list` / `task_run` / `task_bind` で見える task は、自由なシェルコマンドではなく、事前登録した command task と MCP server task を扱う仕組みです。
その登録ファイルが `tasks.toml` です。

置き場所は次の 2 つです。

- リポジトリごとの設定: `.yagent/tasks.toml`
- ユーザー共通の設定: `~/.config/yagent/tasks.toml`

これに加えて、`go.mod` や `package.json` を見て自動で task テンプレートも追加されます。
読み込み順は「自動検出テンプレート → ユーザー設定 → リポジトリ設定」で、同じ `id` があれば後から読んだ定義が上書きします。
`tasks.toml` は読み込み時に strict validation されます。未知キー、空の `id`、command task の `command` 欠落、MCP server の未対応 `transport`、不正な `risk` / `trust`、負の `timeout`、同一ファイル内の重複 `id`、MCP tool filter の矛盾は source path と entry index 付きのエラーとして返します。
schema は `yagent schema tasks` で出力できます。

自動検出される task:

- `go.mod`: `go:test`, `go:build`
- `package.json`: `npm:test`, `npm:build`, `npm:lint`, `npm:typecheck` のうち `scripts` に存在するもの

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

### MCP Server Catalog (`[[mcpservers]]`)

同じ `tasks.toml` 内に、MCP server を `[[mcpservers]]` として登録できます。  
MCP server は `task_run` ではなく `task_bind` で起動・bind します。bind 後にだけ、その server の tool が `mcp__...` 形式で LLM に公開されます。

```toml
[[mcpservers]]
id = "docs"
description = "Docs search MCP"
transport = "stdio"
command = "npx"
args = ["-y", "@example/docs-mcp"]
roots = ["."]
tool_prefix = "docs"
trust = "untrusted"
read_only_tools = ["search_docs"]
parallel_safe_tools = ["search_docs"]
parallel_safe = false
include_tools = ["search_docs"]
```

主な項目の意味:

- `id`: MCP server の識別子。`task_bind` ではこの ID を指定します
- `description`: 人間や agent が `task_list` で読む説明
- `transport`: 現在は `stdio` のみ対応
- `command`: MCP server を起動するコマンド本体
- `args`: コマンド引数の配列
- `cwd`: 実行ディレクトリ。省略時は現在のリポジトリ root
- `roots`: MCP server へ公開する filesystem root。未指定時は `cwd` を `roots/list` に返します
- `env`: 追加環境変数
- `risk`: `low` / `medium` / `high`
- `allow_network`: ネットワーク利用を伴う server かどうか
- `timeout`: タイムアウト秒数
- `tool_prefix`: bind 後の tool 名プレフィックス。未指定時は `id`
- `trust`: `untrusted` / `trusted`。既定は `untrusted`
- `trust_tool_annotations`: true のときだけ MCP server が返す tool annotation を安全判断に使います。`trust = "trusted"` でも有効になります
- `read_only_tools`: yagent 側で read-only とみなす server tool 名または glob
- `mutating_tools`: yagent 側で明示的に mutating とみなす server tool 名または glob
- `parallel_safe_tools`: yagent 側で並列実行可能とみなす server tool 名または glob
- `parallel_safe`: trusted annotation と組み合わせた並列化の上限フラグです。untrusted server では `parallel_safe_tools` が優先されます。mutating tool でも `roots` や path 引数から write scope が分かる場合は、scope が衝突しない範囲で scheduler が並列化できます
- `include_tools` / `exclude_tools`: 公開する MCP tool のフィルタ

MCP の `readOnlyHint` や `destructiveHint` のような annotation は、server が trusted でない限り yagent の安全判断には使いません。untrusted server で read-only tool を安全に扱いたい場合は、`read_only_tools` に明示してください。MCP server から `roots/list` が来た場合は `roots` / `cwd` の file URI だけを返し、`sampling/createMessage` は yagent 側では拒否します。server-initiated LLM call は Yagent の permission/audit 経路を迂回するため、明示的な tool として扱う方針です。

典型的な流れ:

1. `task_list` で `kind = "mcp_server"` の entry を確認
2. `task_bind` で対象 server を bind
3. bind 後に追加された `mcp__...` tool を agent が利用

補足:

- `task_list` の `mcp_server` entry には `bind_required`, `usage_hint`, `bind_hint`, `exposed_tool_prefix`, `exposed_tools` などの補助情報が含まれます
- `task_list` の `mcp_server` entry には `roots` も含まれ、bind 後の MCP tool runtime read/write set は relative path 引数をこの root 配下に解決します
- `task_bind` の返却には `tool_names` に加えて `server_tool_names`, `next_action_hint`, `exposed_tool_prefix` が含まれます
- `tool_names` は bind 後にそのまま呼べる qualified MCP tool 名です
- `server_tool_names` は MCP server 側の元の tool 名です
- `task_bind` と bind 後の `mcp__*` tool は既定で visible です。追加の capability 有効化を挟まずに `task_list` -> `task_bind` -> `mcp__*` の流れを取れます

## 実行ログ

`--log <path>` を指定すると、実行イベントを JSON Lines 形式で出力します。
execution event は調査用の raw `detail` と、TUI/CLI 表示用に短く整形した `display` を分けて保存します。`yagent exec --format json` でも同じく `detail` と `display` を分離して出力します。

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
workspace を変更する tool event には `mutation_id` / `mutation_fingerprint` / `workspace_revision` metrics も付きます。
`llm_called` event には実際に使われた `server_name` / `model` / `api` / `profile_name` / `duration_ms` / fallback metrics が付き、benchmark flat record にも転記されます。
permission decision と model invocation は `scratch/` に保存され、permission decision は turn 完了時に `permission_audit` artifact として run に集約されます。
保存済み run state は `yagent audit runs` で一覧・詳細を確認でき、`yagent audit runs --run latest --full --format json` で run state 全体を JSON export できます。turn ごとの request/context/output message と event summary は `yagent audit conversations` で確認でき、本文が必要な場合だけ `--include-messages --format json` を指定します。run に紐づく execution / model invocation / observation / mutation / permission decision までまとめて渡したい場合は `yagent audit bundle --run latest` を使います。bundle は既定では tool output や artifact 本文/payload を含めず、必要なときだけ `--include-output` / `--include-artifact-content` / `--include-artifact-payload` / `--full-run` を指定します。時系列で agent / tool / model / permission / mutation / artifact を確認したい場合は `yagent audit trace --run latest` を使います。`audit trace` は prompt / response や tool output 本文を含めず、span id、kind、status、phase、agent、duration、server/model/API/fallback などの metadata を text または JSON で出します。横断調査では `yagent audit search <query>` を使うと、run / conversation / tool execution / model invocation / permission / runtime / observation / mutation / artifact の metadata と summary をまとめて検索できます。既定では本文や tool output は検索せず、必要な場合だけ `--include-output` を付けます。run 内の typed artifact は state store 保存時に `schema_version` と payload shape を検証し、`yagent audit artifacts --run latest` で取り出せます。artifact 本文や payload は既定では省略し、必要なときだけ `--include-content` / `--include-payload` を指定します。reusable observation cache は `yagent audit observations`、workspace mutation record は `yagent audit mutations`、LLM 呼び出し metadata は `yagent audit models` で確認できます。model invocation audit は prompt / response 本文を保存せず、server / model / API / routing profile / agent / phase / duration / status / fallback metadata を保存します。`yagent audit models --summary` は server/model/API/profile ごとの call 数、success/failure、fallback 数、平均 duration、agent/phase を集計します。
権限確認や継続確認が複数同時に発生した場合でも、TUI 側で順番に処理できます。

## TUI 操作

会話ログは軽量 Markdown 表示に対応しています。見出し、箇条書き、番号付きリスト、引用、コードフェンス、インラインコードや太字の基本 marker を TUI 用に整形して表示します。

- `Enter`: 送信
- `Ctrl+J`: 改行
- `PgUp` / `PgDn`: ログスクロール
- `Alt+↑` / `Alt+↓`: 1 行ずつログスクロール
- `/` 入力中: 候補コマンドを表示
- `@` 入力中: `pwd` 基準の相対パス候補を表示
- `Tab`: 候補コマンドまたはファイルパスを補完
- `/help`: ヘルプ表示
- `/plan`: 直近 run の plan を表示
- `/artifacts`: 直近 run の artifacts を表示
- `/memory`: workspace memory を表示
- `/profile`: routing profile の表示・切替
- `/model`: model override の表示・切替
- `/stream`: streaming 応答表示の切替
- `/theme`: TUI theme の表示・切替
- `/failures`: Agent Status の失敗詳細を表示
- `/status-filter <text>|clear`: Agent Status tree を絞り込み
- `/status-fold on|off|toggle`: Agent Status の完了ノード折りたたみを切替
- `/status-search <text>|clear`: Agent Status の node / event / failure を検索
- `/resume [run-id|latest]`: 保存済み run を会話に復元
- `/approvals`: approval の状態を表示
- `/clear`: 会話ログをクリア
- `/exit`: 終了

### Permission UI

ファイル操作や task 実行などで許可が必要なときは、下部に permission card を表示します。
`permission.rules` で `allow` / `deny` が決まった tool call は、card を出さずにその決定を使います。
`fs_write` と `patch_apply` の承認では、書き込み前の差分 preview と変更件数も表示します。

- `←/→` または `Tab`: 選択移動
- `Enter`: 確定
- `Esc`: 拒否
- `Ctrl+A`: 現在の request と待機列を今回だけ一括許可
- `Ctrl+D`: 現在の request と待機列を一括拒否

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
すでに待機列にある一致リクエストも、同じ session approval / pattern approval でまとめて自動許可されます。
同一 tool / action / resource kind / scope / risk で preview も同じ request は Permission UI 上で同種 request として集約され、1 回の承認・拒否で同じ decision を返します。異なる scope や preview の request は集約しません。
待機列に一致条件がない複数 request が溜まった場合でも、`Ctrl+A` / `Ctrl+D` で active request と queued request をまとめて処理できます。
パターンは `*.go` のような basename glob と `internal/*` のような path glob を使えます。
サブエージェント導入後も permission の判定ルール自体は変わりません。
permission card には、どの agent が要求したかも表示されます。
差分 preview と変更件数は approval UI と `exec` の標準入力確認に表示されます。log には preview 本文ではなく `preview_kind`、行数、変更件数だけを記録します。
`/approvals` では session approval / pattern approval / pending queue の scope, risk, requester, side effects を確認できます。

### Tool Call UI

LLM が tool を呼び出すと、TUI 下部に tool call card を表示します。並列実行中は active tool を同じ card に複数表示し、完了した tool は active list から外して Tool Logs と会話ログの履歴へ移します。

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
- 最新の失敗詳細。失敗が起きると自動表示され、`/failures` でも開けます
- 失敗詳細には raw detail、artifact ref、ctx、event metrics を表示します。Run Graph panel では `Ctrl+Up/Down` で失敗履歴を切り替え、`Esc` で詳細を閉じられます
- `/status-filter` で agent tree を絞り込み、`/status-fold` で完了済み leaf を隠し、`/status-search` で node / recent event / failure history の一致行を確認できます
- `elapsed`
- `ctx`

`ctx` は、参照中の `message + unique file refs + artifact refs` の合計です。  
`elapsed` は 1 秒単位で表示されます。

### 継続確認

built-in agent の既定 `max_turns` は `200` です。  
既定では上限に達すると、permission UI と同じ導線で「継続実行するか」を確認します。

- 許可するとその agent の turn カウンタをリセットして続行
- 拒否すると `agent_failed` として終了

headless eval や CI では `harness.continuation_policy` で既定動作を固定できます。`prompt` は従来通り確認、`allow` は確認なしで継続、`deny` は確認なしで終了します。

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
- `planner` が agent inventory を見て execution plan を作り、必要な agent を選ぶ
- user-defined agent も built-in agent と同じ inventory として plan に組み込まれる
- planner が無効な plan を返した場合は orchestrator が検証し、必要なら保守的な fallback に切り替える
- それ以外は `planner` が agent inventory を見て JSON schema 付きの execution plan を返す
- `delegate_to_<agent>` で bounded task を subagent に委譲
- `handoff_to_<agent>` で専門 agent に現在ターンを handoff
- `run_ephemeral_agent` で一時的な subagent を即席生成
- リポジトリ探索には `fs_list` を優先し、単純な一覧取得のためにスクリプト生成へ逃がさない
- `fs_list` は既定で hidden file を省き、直下のみを最大 80 件まで返す bounded JSON です。`summary` には matched/omitted/hidden 件数が入り、`depth` / `include_hidden` / `limit_entries` で粒度を上げられます。scan 上限に達した場合は `scan_truncated=true` と `omitted_entries_exact=false` で不完全な件数であることを示します
- `fs_list` の semantic key は既定値を正規化するため、同じ探索を省略形と明示形で繰り返しても runtime cache / duplicate suppression の対象になります
- planner の返した plan は orchestrator が validate し、無効なら 1 回だけ repair を要求して、それでもだめなら deterministic fallback に落とす
- 並列化は非破壊系 task のみ。書き込み系は常に直列化

### ツール

現在の主な tool は次です。

- `fs_read` / `fs_write` / `fs_list` / `fs_stat` / `fs_remove` / `fs_move`
- `search_text` / `search_files`
- `git_status` / `git_diff` / `git_log` / `git_show` / `git_branch` / `git_blame` / `git_file_history`
- `task_list` / `task_run` / `task_bind`
- `patch_apply`
- `list_capabilities` / `enable_capability`

Task catalog は `.yagent/tasks.toml` の `[[tasks]]` / `[[mcpservers]]` とユーザー共通の `~/.config/yagent/tasks.toml` から構築されます。
MCP まわりでは、visible な `mcp__*` が空でも `task_list` / `task_bind` による lazy-bind が使える場合があります。
write-capable agent では `fs_write` / `patch_apply` を直接実行でき、その後の書き込み承認は通常の assistant 返答ではなく approval dialog で行われます。
承認時には、`fs_write` は既存内容との差分、`patch_apply` は各 `old_text` / `new_text` 置換の preview を表示します。
一方で write tool が見えない理由が current agent の read-only 制約である場合は、「tool が存在しない」のではなく write-capable agent へ委譲すべきケースです。

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
