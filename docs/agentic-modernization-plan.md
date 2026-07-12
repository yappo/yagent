# Agentic Modernization Plan

作成日: 2026-06-13

この文書は yagent を local LLM first の coding agent / harness engineering tool として作り直すための設計メモです。後方互換性は維持しません。

durable workflow、transactional state root、recovery、security eval、measured routing の次段設計は [Durable Agent Runtime Design](./durable-agent-runtime-design.md) に分離した。

## Primary Sources Checked

- Anthropic, "Building effective agents" (2024-12-19): simple, composable patterns, orchestrator-workers, evaluator-optimizer, sandboxed testing, transparent planning, ACI/tool documentation.
  <https://www.anthropic.com/engineering/building-effective-agents>
- OpenAI API docs, "Using GPT-5.5": GPT-5.5 の coding workflow では planning, tool use, verification, subagent delegation, success criteria, structured outputs, Responses API を明示する方針。
  <https://developers.openai.com/api/docs/guides/latest-model>
- OpenAI API docs, "Migrate to the Responses API": Responses は typed item を基本単位にし、tool call / function_call_output を message から分離する。
  <https://developers.openai.com/api/docs/guides/migrate-to-responses>
- OpenAI API docs, "Function calling": Responses API では function_call_output item を input に戻す。tool_search や allowed_tools は large tool surface の context 削減に使う。
  <https://developers.openai.com/api/docs/guides/function-calling>
- OpenAI API docs, "Structured model outputs": Chat Completions は `response_format.json_schema`、Responses は `text.format` で JSON schema を渡せる。strict schema では required fields と `additionalProperties:false` を前提にする。
  <https://developers.openai.com/api/docs/guides/structured-outputs>
- OpenAI Agents SDK docs: primitive は agents, handoffs/agents-as-tools, guardrails, sessions, tracing, sandbox agents。yagent では SDK を採用せず、同等の runtime 境界を Go 側に持つ。
  <https://openai.github.io/openai-agents-python/>
- OpenAI, "A practical guide to building AI agents": agent は失敗時の停止・escalation、層状 guardrail、実運用上の human intervention を設計に含める。
  <https://openai.com/business/guides-and-resources/a-practical-guide-to-building-ai-agents/>
- OpenAI, "The next evolution of the Agents SDK": durable execution は外部化した state を snapshot/rehydrate する設計で実現する。
  <https://openai.com/index/the-next-evolution-of-the-agents-sdk/>
- Anthropic, "Demystifying evals for AI agents": multi-turn agent の eval は state 変化と実環境の outcome を確認する。
  <https://www.anthropic.com/engineering/demystifying-evals-for-ai-agents>
- MCP specification 2025-06-18: JSON-RPC 2.0, stateful connection, capability negotiation, resources/prompts/tools, roots/sampling/elicitation, explicit user consent and tool safety。
  <https://modelcontextprotocol.io/specification/2025-06-18>
- Qwen/Qwen3.6-35B-A3B model card: Apache-2.0, 35B total / 3B activated, 262,144 native context, OpenAI-compatible Chat Completions via vLLM/SGLang/Transformers, Qwen reasoning/tool parsers, and Qwen3.6 sampling parameter recommendations。
  <https://huggingface.co/Qwen/Qwen3.6-35B-A3B>
- Google Gemma 4 model card: E2B/E4B/12B/26B A4B/31B、native system instructions、function calling、structured tool use、128k/256k context、推奨 sampling `temperature=1.0`, `top_p=0.95`, `top_k=64`。
  <https://ai.google.dev/gemma/docs/core/model_card_4>
- Google Gemma 4 announcement: agentic workflow / coding / local deployment を主要用途とし、26B A4B は約 3.8B active parameters、LM Studio day-one support。
  <https://blog.google/innovation-and-ai/technology/developers-tools/gemma-4/>
- LM Studio Gemma 4 model page: Gemma 4 の tool use / vision / reasoning 対応と、26B A4B package 約 15.6GB、31B package 約 19GB。
  <https://lmstudio.ai/models/gemma-4>
- LM Studio Developer Docs: local server can be started from Developer tab or `lms server start`, and OpenAI-compatible endpoints include `/v1/models`, `/v1/chat/completions`, and `/v1/responses`.
  <https://lmstudio.ai/docs/developer/core/server>
  <https://lmstudio.ai/docs/developer/openai-compat>
- LM Studio REST API docs: `/api/v1/models` returns richer model/runtime metadata, including loaded instances, context length, max context length, quantization, and model capabilities.
  <https://lmstudio.ai/docs/developer/rest/list>
- LM Studio Developer Docs, "Structured Output": OpenAI-compatible `/v1/chat/completions` で `response_format` に JSON schema を渡すと schema 準拠 JSON を要求できる。
  <https://lmstudio.ai/docs/developer/openai-compat/structured-output>
- LM Studio API changelog: 0.3.18 で OpenAI-compatible endpoint の `stream_options.include_usage` が追加され、0.3.19 で streaming usage statistics が修正された。
  <https://lmstudio.ai/docs/developer/api-changelog>
- LM Studio Integrations docs, "Codex": Codex は OpenAI-compatible `POST /v1/responses` で接続でき、tool-heavy workflow では 25k 超 context length が目安。
  <https://lmstudio.ai/docs/integrations/codex>

## Direction

yagent の中核は「LLM に広い shell を渡す agent」ではなく、「LLM を信用しない harness」です。最新の実装知見と現在のコードを合わせると、全面リライトよりも次の 5 層を明確にするのが最短です。

1. Model runtime
   - local: Qwen 3.6 または Gemma 4 を短め context の quantized runner で使う。
   - remote: OpenAI `gpt-5.5` は `responses` API で使う。
   - provider 差分は `domain.ModelRequest` に閉じ、orchestrator からは Settings/Tools/Items の意味だけを見る。

2. Harness runtime
   - fixed workflow と autonomous loop を分ける。
   - `plan -> execute -> verify -> recover -> finalize` は維持し、各 phase の入出力 contract を schema 化する。
   - evaluator-optimizer は verifier/recover として扱う。
   - verification が pass を明示できない場合は fail closed とし、回復上限後も fail なら run は `needs_attention` にする。
   - tool/file/agent/model 由来の本文は `runtime_evidence` として扱い、root user goal と harness 固定命令だけを直接 instruction として渡す。
   - finalize は tool-free の synthesis phase に固定し、未検証の変更を finalize で追加しない。

3. Tool fabric
   - tool は capability, risk, side effect, read/write set, cacheability, source limit を持つ。
   - MCP は lazy bind を維持しつつ、server annotations は trusted server でだけ採用し、既定では yagent 側 policy / explicit tool list を優先する。
   - large tool surface は list/search/bind の二段階にし、常時全 tool を context に載せない。

4. Context and memory
   - 会話 summary を主記憶にしない。typed artifact, observation, mutation, workspace snapshot を primary state にする。
   - local model では context を浪費しない。tool output は artifact 化し、agent packet は role/scope ごとに小さくする。

5. Evals and telemetry
   - OpenAI hosted evals は external model の tool call に制約があるため、yagent 自前の benchmark/harness eval を主にする。
   - run graph, tool event, permission decision, mutation fingerprint を JSONL と state store に保存する。

## Local Model Profiles

MacBook Air M4 / 32GB では full precision や 262k context を前提にしません。実運用の初期 target は次です。

- runner: LM Studio の OpenAI-compatible server
- API: `chat_completions`
- Qwen profile: `Qwen/Qwen3.6-35B-A3B`
- Gemma profile: `google/gemma-4-26b-a4b`
- mode: text-first。vision は別 profile に分ける
- context: 16k-64k を初期上限にする。262k は外部 GPU server 用 profile
- Qwen generation: thinking mode の general tasks は `temperature=1.0`, `top_p=0.95`, `top_k=20`, `min_p=0.0`, `presence_penalty=1.5`, `repetition_penalty=1.0` を starter とする。
- Gemma generation: official model card に合わせて `temperature=1.0`, `top_p=0.95`, `top_k=64` を starter とし、Qwen 固有の sampling parameter は送らない。

Gemma 4 26B A4B を 32GB machine の既定候補にするのは、LM Studio 掲載 package が約 15.6GB、active parameter が約 3.8B であることからの実装上の推定であり、Google が MacBook Air M4 / 32GB を保証しているわけではない。実際の usable context と速度は quantization、KV cache、同時実行数で検証する。

この repo の adapter は今回、`chat_completions` と `responses` を config で切り替えられるようにした。Responses では raw output item を assistant metadata に保持して、次の request input に再投入する。これは reasoning item / function_call item を落とすと tool-heavy reasoning workflow が壊れるため。

planner の execution plan と verifier の verification result は runtime structured output に接続した。Chat Completions では `response_format.json_schema`、Responses では `text.format` を使い、同じ schema を prompt の `ExpectedOutput` にも載せる。schema 非対応の local model に備え、verification parser は明示的な `VERIFICATION_STATUS: pass|fail` line format のみ fallback として受け付ける。status が欠ける、または未知なら pass と推定せず fail にする。

LM Studio 自体の install / 起動は yagent の責務にしない。yagent 側では `yagent doctor` で `/v1/models` を確認し、configured model と LM Studio 側の model identifier のズレを検出する。さらに `--runtime` で `/api/v1/models` から load 状態、loaded context、max context、quantization、tool-use/reasoning capability を確認し、local coding-agent workload に必要な context/capability の warning を出す。runtime metadata と config から、LM Studio 側の loaded context、`context.compact_after_est_tokens`、`generation.max_output_tokens`、Qwen 3.6 / Gemma 4 sampling parameter の推奨も出す。`--format json` では `problems` / `warnings` / `recommendations` / runtime metadata / probe result を machine-readable にし、`--fail-on-warning` / `--fail-on-recommendation` で benchmark 前の runtime gate に使える。`--probe` で lightweight generation、`--probe-structured` で JSON schema structured output の実行経路を確認できるようにした。

`doctor --write-recommended-config` は runtime 診断後に yagent 側で反映できる項目だけを complete config TOML として書き出す。対象は exact LM Studio model id、routing/agent model reference、`context.compact_after_est_tokens`、local server の model-specific generation parameters。Gemma 4 へ切り替えた config に Qwen 固有の `min_p` / penalty が残っていれば unset する。LM Studio 側でしか変えられない loaded context length は external action として残す。

`doctor --save-audit` は runtime 診断結果を state store の scratch record として保存し、`audit runtime` で server/model、loaded context、quantization、probe、warning/recommendation 数を後から確認できるようにする。local model の load 設定を変えながら benchmark record と突き合わせるための runtime evidence として扱う。

`benchmark report` は保存済み runtime audit を `--runtime-audit-server` で添付し、`--require-runtime-audit` / `--min-runtime-context` / `--max-runtime-warnings` / `--require-runtime-structured-probe` で eval regression gate と同じ report gate に載せる。

`yagent setup` は repo-local starter files として `.yagent/config.toml` と `.yagent/tasks.toml` を生成する。`--local-preset auto|qwen36|gemma4|generic` で local model family を選び、model id が指定されていれば `auto` で family を検出する。model 未指定の `auto` は従来通り Qwen 3.6 を使う。config は OpenAI `gpt-5.5` responses fallback、permission policy、context/memory/benchmark defaults を含む。OpenAI fallback の token は `token_env = "OPENAI_API_KEY"` で環境変数から読むため、repo-local config に API key を直書きしない。task catalog は `go.mod` / `package.json` を見て標準 harness task を生成する。`--config` 未指定時は `.yagent/config.toml` を自動検出するため、setup 後は `yagent doctor --runtime --probe-structured` をそのまま実行できる。

## Refactor Order

1. Model runtime split
   - `server.servers[].api`
   - `server.servers[].generation`
   - `routing.profiles.*.generation`
   - Chat Completions / Responses adapter
   - done: `yagent doctor --probe` / `--probe-structured` で LM Studio local model の生成・structured output 経路を確認
   - done: `yagent doctor --runtime` で LM Studio REST runtime metadata を読み、loaded context / quantization / capability を診断
   - done: runtime metadata と config から local model 向け context / output token / model-specific sampling parameter の recommendations を表示
   - done: `yagent doctor --format json` と gate flags で CI/harness から local runtime readiness を判定
   - done: `yagent doctor --write-recommended-config` で runtime 診断由来の yagent config 推奨値を complete TOML として書き出し
   - done: `yagent doctor --save-audit` / `yagent audit runtime` で local runtime 診断結果を state に保存・export
   - done: `yagent benchmark report --runtime-audit-server` で保存済み runtime audit を eval report に添付し、context/probe/warning を gate
   - done: `yagent benchmark --preflight-doctor` で harness eval 前に local runtime readiness を gate し、preflight summary を report / flat record に保存
   - done: `yagent setup` で repo-local starter config / task catalog を生成し、`.yagent/config.toml` を自動検出
   - done: local model profile を Qwen 3.6 / Gemma 4 / generic に分離し、`setup --local-preset` と model-specific doctor recommendations を追加

2. Structured contracts
   - done: execution plan と verification を JSON schema 化
   - done: prompt 内 contract と runtime structured output を同じ schema から生成
   - done: final response を JSON schema 化し、`response` をユーザー向け本文、remaining_risks / next_steps を artifact payload に保存
   - done: typed artifact payload を state store 保存境界で strict decode / required field validation
   - done: task catalog tool の failure output を `ok:false` / `error.code` / `error.task_id` 付き JSON に構造化
   - done: `tasks.toml` / `[[mcpservers]]` の strict schema validation と path/index 付きエラーで設定ミスを早期検出
   - done: `yagent schema agent` / `yagent schema tasks` で Agent DSL と task catalog の JSON Schema を出力し、Agent DSL loader も strict validation に統一

3. Tool and permission runtime
   - done: permission policy を設定ファイル化し、tool/action/risk/resource/agent/side_effect の ordered rule で allow/deny/approval を制御
   - done: MCP server/tool の trust boundary を `trust`, `read_only_tools`, `mutating_tools`, `parallel_safe_tools` と runtime hint により詳細化
   - done: MCP `roots/list` に `roots` / `cwd` の file URI を返し、server-initiated `sampling/createMessage` は明示的に拒否
   - done: bound MCP tool に `roots` を保持し、relative path 引数や path 引数なし read/write fallback を実 filesystem read/write set に解決
   - done: `go.mod` / `package.json` から task catalog の標準 harness task を自動生成し、user/repo task で上書き可能にした
   - done: `fs_write` / `patch_apply` approval に差分 preview と変更件数を追加
   - done: queued permission requests に session approval / pattern approval を batch 適用
   - done: permission card で `Ctrl+A` / `Ctrl+D` による active + queued request の一括許可/拒否 UI を追加
   - done: content-state based mutation fingerprint を mutation record と tool event metrics に保存
   - done: `/approvals` に session/pattern/pending queue の scope, risk, requester, side effects を表示
   - done: permission decision を scratch に保存し、turn 完了時に `permission_audit` artifact へ集約
   - done: `yagent audit permissions` で permission decision audit を text / JSON export
   - done: `yagent audit executions` で tool execution audit を text / JSON export
   - done: `yagent audit runs` / `yagent audit artifacts` で saved run state と typed artifact を text / JSON export
   - done: `yagent audit observations` / `yagent audit mutations` で reusable observation cache と workspace mutation record を text / JSON export
   - done: model invocation metadata を prompt/response 本文なしで scratch に保存し、`yagent audit models` で server/model/API/profile/fallback/duration/status を export
   - done: `yagent audit models --summary` で server/model/API/profile ごとの call 数、success/failure、fallback、duration、agent/phase を集計
   - done: `llm_called` event と benchmark flat record に実際の server/model/API/profile/fallback/duration metadata を転記し、保存済み eval record だけで local/remote model 利用差分を比較
   - done: Chat Completions / Responses の non-streaming / streaming usage を共通型へ正規化し、audit、`llm_called`、benchmark record/report に input/output/total/cached/reasoning token と usage coverage を保存
   - done: `yagent exec --show-events` / `--format json` で単発実行でも run/tool/model/failure summary を確認可能
   - done: `yagent audit bundle` で run, typed artifacts, executions, model invocations, observations, mutations, permission decisions を 1 つの調査 bundle として export
   - done: `yagent audit trace` で run に紐づく agent/work_unit/tool/model/permission/mutation/artifact を時系列 span として text / JSON export
   - done: `yagent audit search` で run / conversation / tool / model / permission / runtime / observation / mutation / artifact の metadata と summary を横断検索
   - done: `harness.continuation_policy` で max turn 到達時の継続確認を `prompt` / `allow` / `deny` に固定できるようにした
   - done: `fs_list` を summary 付き bounded JSON にし、既定値を semantic key へ補完して同じ探索の duplicate suppression / reusable observation を効きやすくした
   - done: turn ごとの request/context/output message と event summary を conversation log として保存し、`yagent audit conversations` で export
   - done: Agent Status viewport で最新失敗の raw detail / artifact / ctx / metrics を開けるようにし、失敗履歴を選択可能にした
   - done: Agent Status viewport に `/status-filter` / `/status-fold` / `/status-search` を追加し、tree 絞り込み・完了 node fold・node/event/failure 検索を可能にした
   - done: TUI Agent Status / Run Graph state, event application, failure detail, search/filter/fold, status pruning を `internal/tui/status.go` に分離し、`model.go` から status component を切り出した
   - done: TUI の `/continue [conversation-id|latest]` で保存済み会話を選び、次の入力を新しい turn / workflow として継続する。`/recover <workflow-id>` は user message を追加せず durable snapshot を復旧する
   - done: TUI tool call card を複数 active tool 対応にし、完了 tool を Tool Logs と会話ログ履歴へ移すようにした
   - done: TUI active tool / Tool Logs state, rendering, cache, argument formatting を `internal/tui/tool_call.go` に分離し、`model.go` から tool_call component を切り出した
   - done: Permission UI で同一 approval unit かつ preview が同じ request を same-kind batch として集約し、1 回の承認・拒否で batch 全体へ decision を返すようにした
   - done: TUI permission state / key handling / batch aggregation / card rendering を `internal/tui/permission.go` に分離し、`model.go` から permission component を切り出した
   - done: TUI composer layout / input key handling / prompt submit / composer view cache を `internal/tui/composer.go` に分離し、`model.go` から composer component を切り出した
   - done: TUI conversation blocks / streaming output block / chat log rendering cache を `internal/tui/conversation.go` に分離し、`model.go` から conversation component を切り出した
   - done: TUI slash command 定義 / dispatch / runtime selector / catalog listing / run resume を `internal/tui/slash.go` に分離し、`model.go` から command surface を切り出した
   - done: TUI side panel state / navigation / Plan / Verification / Memory rendering を `internal/tui/panels.go` に分離し、`model.go` を core state / update / layout / cache orchestration に限定した
   - done: TUI root view composition / header / panel / permission / completion cache wrapper を `internal/tui/view.go` に分離した
   - done: TUI style schema と default / contrast / mono palette を `internal/tui/theme.go` に集約した
   - done: TUI 会話ログの軽量 Markdown 表示を追加し、見出し、箇条書き、番号付きリスト、引用、コードフェンス、基本 inline marker を読みやすく整形
   - done: TUI `/theme` で `default` / `contrast` / `mono` theme を切り替え、header と表示 cache を theme 変更に追随させた
   - done: TUI の permission approver と tool observer bridge を `RuntimeBridge` に統合し、Bubble Tea program への runtime message dispatch 責務を 1 か所に寄せた
   - done: TUI の `/profile` / `/model` と header 表示で routing profile と model override を切り替え可能にした。`/profile` は設定済み profile の表示・Tab 補完・未知 profile 拒否にも対応。
   - done: scheduler を path-aware side-effect conflict にし、read/write scope が分かる task/MCP/process/external/workspace mutation は source limit と path conflict の範囲で並列実行できるようにした。不明 scope の side effect は直列のまま。
   - done: file path policy に `deny_paths` と `[[file.rules]]` を追加し、allowed root より優先する deny glob/rule と明示 allow rule を扱えるようにした
   - done: Chat Completions / Responses の SSE streaming を `ModelStreamEvent` に正規化し、`exec --stream` と TUI `/stream on` から使えるようにした。structured JSON phase の token delta はチャットに出さず、stream delta は永続 audit event に混ぜない。
   - done: Git read tool surface に `git_branch` / `git_blame` / `git_file_history` を追加し、branch 状態・行履歴・file history を agent が直接確認可能にした
   - done: execution event の raw `detail` と UI/CLI 用 `display` を分離し、ログ・audit には詳細を残しつつ status/exec summary は短い表示文を使うようにした

4. Context engine
   - done: artifact / reusable observation を role filter 後に relevance ranking
   - done: runtime observation の read_set/path state を使った freshness filter
   - done: reusable observation summary に `integrity_sha256` を持たせ、observation record と一致しない memory summary を packet から除外
   - done: Agent DSL `token_budget` による per-agent packet budget

5. Harness eval
   - done: `--routing-candidate` による local Qwen / OpenAI fallback の同一課題比較
   - done: benchmark preflight doctor により LM Studio/Qwen readiness と eval execution を同じ harness workflow と saved record に接続
   - done: routing candidate ごとに exact primary server/model の preflight を実行し、profile-specific record として保存
   - done: logical model call と fallback transport attempt を分離し、一次失敗を含む duration / usage / failure を JSONL / CSV / report に集計
   - done: built-in case として repo-readonly / SWE-like repo mutation / Terminal-like task / permission-gate task を追加
   - done: TOML `[[cases]]` で独自 case を読み込み、expectations を deterministic metrics で評価
   - done: flat result record を JSONL/CSV で stdout またはファイル保存
   - done: `yagent benchmark --save-artifact` で report と flat records を `benchmark_report` typed artifact として state store に保存
   - done: `yagent benchmark report` で保存済み JSONL/CSV record を profile/case 単位に再集計し、baseline 差分・pass rate gate・回帰 gate・model call/fallback 集計を実行
   - done: `yagent benchmark report --format junit` で保存済み eval/gate 結果を CI 向け JUnit XML として export
   - done: mutation / permission request / delegation / handoff の action-level upper bound gate を追加し、completed run でも unsafe action があれば eval failure にする
   - done: planner reason、tool/MCP-equivalent output、delegation scope、prior assistant/tool/system history の deterministic provenance regression を追加
   - done: trusted root prompt と分離した source-typed provenance を planner reason、RoleTool/file output、delegation、MCP response、prior assistant/tool/system history channel へ注入する benchmark scenario driver

## Runtime Review (2026-07-11)

- done: run state の checkpoint error を無視せず、resume state が欠落・読込失敗なら新規 run に silently fallback しない。
- done: state file は temp file + sync + rename で個別ファイルを atomic replacement する。
- done: tool output artifact、observation、snapshot、mutation、execution record の保存失敗を実行失敗として伝播し、同一 process の snapshot 更新を serialise する。
- done: verification の malformed output は fail closed とし、最終 merged verification が fail の run は `completed` ではなく `needs_attention` にする。`yagent exec` text 出力もこの状態を明示する。
- done: planner/free-text assignment、delegation task、tool/file output、他 agent output、過去 assistant/tool history、context memory は untrusted `runtime_evidence` に隔離する。ephemeral agent は parent の read-only tool だけを使う分析 agent とし、再委譲・capability enable・workspace mutation を許可しない。
- done: tool/MCP result は `RoleTool` / tool call metadata を維持しながら本文を `runtime_evidence` envelope に一度だけ格納し、次の model call に raw content を渡さない。
- done: finalizer は tool list を受け取らず、tool call を返しても実行しない。
- done: durable `Workflow` / `DurableWorkUnit` / `DurableAction` / lease / fencing token の typed contract と、revision・lease を検証する pure lifecycle transition を追加した。
- done: FileStore に content-addressed immutable object、complete generation manifest、atomic `HEAD`、Darwin/Linux cross-process advisory lock、workflow revision CAS を使う durable workflow snapshot store を追加した。
- done: bootstrap は `memory.state_dir` の state store を常時作成し、`memory.enabled` は repo memory 利用だけを制御する。`Config.WorkflowStore` が有効な production graph は `WorkflowSnapshot` を authority とし、CAS coordinator、blocked propagation、tool Prepare/Start/Execute/Finish、projection、既存 pending snapshot の再開を使う。
- done: active lease の heartbeat renewal、期限切れ lease reconciliation、retryable read-only / 未開始 action の cross-process takeover、stale credential rejection、mutating ambiguity の `needs_attention` 化を実装した。Mac のスリープで heartbeat が止まった場合も stale outcome を破棄し、snapshot reconciliation を経て新しい fencing generation で read-only unit を再開する。
- done: model usage と LM Studio runtime metadata を parallel telemetry として audit / benchmark record/report に保存する。
- done: revision 1 の typed `workflow_input` artifact と execution-plan artifact だけから、別 service が request seed / plan を再構築する snapshot-only cold recovery を実装した。
- done: `TurnRequest.ResumeID` と旧 `/resume` を削除し、conversation continuation（新 turn / 新 workflow）と workflow recovery（入力なし）を domain/CLI/TUI の別 operation にした。
- done: production execution は durable workflow store を必須とし、旧 `RunState` scheduler fallback path を削除した。
- done: planner model call 前に workflow intent と typed input を durable commit し、planning 結果を validated CAS transition で既存 workflow へ attach する。intent-only snapshot は restart 後に planning から回復する。
- done: plan phase を tool-free decision contract にし、durable work-unit lease 外の mutating/side-effect tool を実行前に拒否する。
- done: terminal workflow 後の RunState/conversation projection 保存失敗を authoritative failure から分離し、pending conversation index と terminal snapshot から continuation history を再構築する。
- done: planner model の出力契約を full `ExecutionPlan` から inventory-derived enum 付きの最小 `planner_decision` へ置き換えた。plan ownership、verify/recover/finalize、steps は runtime が派生し、invalid output に同じ model を再度呼ぶ repair は行わない。
- done: durable action execution context を tool provider へ渡し、MCP 2025-11-25 の `tools/call.params._meta` に `dev.yagent/durable-action` として action identity、idempotency key、lease/fencing token を arguments と分離して送信する。
- done: tool registry の dispatch 直前に durable lease/action credential を再確認し、stale action を provider に渡さない。mutating MCP tool は yagent fencing extension の declaration と result `_meta` acknowledgement が一致した場合だけ成功として確定する。
- done: yagent 所有の Go MCP/provider adapter が `pkg/durablefence` を使って scope ごとの monotonic fencing、completed replay、prepared action の重複拒否を実装できるようにした。任意の外部 effect と fence record の atomicity は provider の永続化境界が担う。
- done: final response に `claims` と `evidence_refs` を要求し、artifact ID/kind の存在、claim ごとの evidence、観測済み repository path を deterministic に検証する。これは自然言語の真偽を証明する evaluator ではなく、未観測事実の成功確定を防ぐ gate である。
- partial: LM Studio の Gemma 4 26B A4B QAT (`Q4_K_XL`) で 256k loaded context、structured probe、`repo-readonly` の `fs_read` / `fs_list` multi-tool loop、sleep 後の fencing takeover を実測した。最小 `planner_decision` 導入後の live gate は `completed`、0 failed event、0 transport failure、2 tool calls、0 mutationで通過した。Qwen3.6-35B-A3B (`IQ4_XS`) は 32k context / parallel 2 で structured probe と durable completion を確認したが、read-only run は 649秒、21 tool calls、7 failed events、約36.9万 total tokens で gate 不合格だった。read-only tool failure を model に返して自己修正させる経路は成立した。
- done: benchmark の各 profile/case/run/candidate を一時 state root に隔離し、比較対象間の read-only cache 汚染を防止した。
- done: benchmark cell ごとに workspace copy と state root を作り、mutation / cache が次 profile/case/run/candidate へ漏れないようにした。isolated cell は global user task catalog と workspace 外 task/MCP declaration を拒否する。
- done: process-isolated backend がない benchmark cell は `task_run` / `task_bind` を fail closed にし、command / external MCP process を起動させない。workspace copy は process を OS 権限から隔離しない。
- done: benchmark の `task_run` / external MCP process を configured VM/container proxy へ委譲する protocol と、macOS の明示 opt-in `macos-sandbox-exec` backend を追加した。未設定時は fail closed。
- pending: VM/container proxy runner の provision と host escape を検証する integration/e2e。macOS fallback は deprecated API のため長期 backend にはしない。
- done: built-in agent の 200 turn 上限を role-specific budget（planner 8、manager/read-only 12、coder 24）に置き換え、未観測 repository fact と speculative path retry を禁止した。
- partial: yagent 所有 adapter 向けの server-side fencing reference implementation は完了した。third-party MCP server / provider adapter 自身への採用、Qwen の残る探索効率と事実精度、live verify/recover/`needs_attention` benchmark は外部 provider / 明示的な LM Studio 実測環境に依存するため未完了。

この境界は prompt-level provenance control であり、悪意ある model を sandbox 化するものではない。filesystem/process/MCP の実権限は既存 permission policy と tool implementation が最終的に制御する。
