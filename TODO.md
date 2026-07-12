# TODO

## Runtime Durability

- [x] durable workflow 用の typed aggregate / lease / fencing / action lifecycle contract と、immutable object / generation manifest / atomic `HEAD` / cross-process lock / revision CAS による workflow snapshot store を追加する。
- [x] `Config.WorkflowStore` が有効な production graph を `WorkflowSnapshot` authority に接続し、CAS coordinator、blocked propagation、tool Prepare/Start/Execute/Finish、projection、caller seed 付き既存 pending snapshot の再開を実装する。
- [x] active lease の heartbeat renewal、期限切れ `leased/executing` の aggregate reconciliation、read-only / 未開始 action の cross-process takeover、mutating ambiguity の `needs_attention` 化を実装する。再 claim は新しい fencing generation を使い、旧 credential の action/unit commit は拒否する。
- [x] Mac のスリープで heartbeat が lease expiry を越えた場合、stale outcome を commit せず snapshot を reload/reconcile する。read-only unit は新しい fencing generation で再開し、mutating ambiguity は既存の `needs_attention` policy に従う。
- [x] durable tool execution context（action/workflow/work-unit ID、attempt、idempotency key、lease/fencing token）を tool provider 境界へ伝播し、MCP `tools/call.params._meta` の `dev.yagent/durable-action` として arguments から分離して送信する。
- [x] tool registry の dispatch 直前に durable lease/action credential を再確認し、stale action を local provider / MCP provider へ到達させない。mutating MCP tool は yagent fencing extension の tool annotation と response `_meta` acknowledgement が一致した場合だけ成功として確定する。
- [x] owned Go MCP/provider adapter が server-side fencing を実装できる public `pkg/durablefence` を追加する。request `_meta` の decode、scope ごとの monotonic fence、同一 action の completed replay、prepared action の再実行拒否、response acknowledgement を提供する。
- [ ] third-party MCP server / provider adapter が idempotency key / fencing token を自身の永続化境界で比較して stale execution を拒否するよう採用する。MCP 標準の `_meta` は任意 metadata の transport であり、server-side enforcement 自体は保証しない。旧 workerが開始済みの外部 effect 自体の遅延完了は universal には停止できない。
- [x] conversation continuation と durable workflow recovery を別 operation / CLI / TUI command にし、production graph の旧 `RunState` scheduler fallback path を削除する。
- [x] revision 1 に明示参照された typed `workflow_input` artifact（context messages、model、profile、capabilities、stream setting）を保存し、別 service が `WorkflowID` だけから plan と request seed を復元する snapshot-only cold recovery を実装する。
- [x] `TurnRequest.ResumeID` を廃止する。`ContinueConversation` は同じ conversation に新 turn / 新 workflow を作り、`RecoverWorkflow` は `WorkflowID` だけで新しい user input なしに snapshot を復旧する。
- [x] planner 実行前に workflow identity と typed `workflow_input` を intent-only snapshot として commit し、planning 後は validated `AttachWorkflowPlan` transition で execution plan / work units を一度だけ追加する。intent-only snapshot は別 process の `RecoverWorkflow` が planning から再開できる。
- [x] planning を tool-free durable decision contract にし、durable work-unit lease 外の mutating/side-effect tool を実行前に拒否する。
- [x] durable workflow terminal 後の RunStore / conversation index 更新失敗を authoritative failure にしない。pending conversation record と `WorkflowID` を先に保存し、最終 index 更新失敗時は terminal snapshot の final artifact から継続履歴を復元して `projection_degraded` を記録する。

## Security And Evals

- [x] planner reason、tool/file output、delegation、MCP-equivalent response、過去 assistant/tool/system history を対象にした deterministic `runtime_evidence` regression を追加し、tool protocol metadata を維持したまま本文を evidence envelope に隔離する。
- [x] benchmark に source-typed security scenario driver を追加する。trusted root prompt と分離した typed provenance として planner reason、RoleTool/file output、delegation、MCP response、prior assistant/tool/system history を注入し、tool protocol metadata と `runtime_evidence` fencing を維持する。
- [x] benchmark expectation に mutation、permission request、delegation、handoff の action-level upper bound gate を追加し、permission request は typed audit artifact から数える。
- [x] Chat Completions / Responses の model usage を non-streaming / streaming / fallback で取得し、audit event と benchmark record/report に input/output/total/cached/reasoning token と usage coverage を保存する。
- [x] usage と LM Studio runtime metadata を parallel telemetry として audit / benchmark record/report に伝播する。usage unavailable は 0 と推定せず区別する。
- [x] benchmark candidate ごとの exact server/model preflight と fallback transport-attempt accounting を追加する。論理 model call と transport attempt を分離し、一次失敗を含む duration・usage・failure を profile/case record に保存する。
- [x] planner の model output を runtime-owned `ExecutionPlan` 全体から、inventory-derived enum を持つ最小 `planner_decision`（task kind、primary、read-only preparation）へ縮小する。verify/recover/finalize/steps は runtime が決定し、invalid output は追加 model repair をせず rejected event と deterministic fallback にする。
- [ ] **明示指示待ち**: real LM Studio runtime で Qwen 3.6 / Gemma 4 の tool use、verify/recover、`needs_attention` を実測し、context length・quantization・parallelism を添えた benchmark record を保存する。ユーザーが明確に開始を指示し、かつこの task の token 余裕がある場合だけ実施する。通常の自動継続、offline 実装、検証では LM Studio を起動・接続しない。Gemma 4 26B A4B QAT は structured probe と `repo-readonly` gate を通過済み。Qwen3.6-35B-A3B IQ4_XS は 32k context / parallel 2 で structured probe と durable completion を確認したが、read-only run は 649秒、21 tool calls、7 failed events、約36.9万 total tokensで gate 不合格。read error からの自己修正は成立したが、探索効率と事実精度を改善して再計測する。verify/recover と `needs_attention` は未実測。
- [x] final response の `internal/...` / `cmd/...` / `pkg/...` / `docs/...` など repository path claim を `repo_map` / `change_set` の観測済み path と照合し、未観測 path は `needs_attention` にする deterministic grounding gate を追加する。read-only exploration の role-specific `max_tool_calls`（researcher 6、manager/tester/reviewer 8）と tool-free synthesis は実装済み。
- [x] final response に grounded `claims` と `evidence_refs` を要求し、artifact ID/kind の存在、claim ごとの evidence、repository path の観測根拠を検証する deterministic grounding gate を追加する。これは自然言語の真偽を完全に証明する semantic truth oracle ではない。
- [x] agent の暗黙 `max_turns = 200` を default 12 と役割別の有限 budget（planner 8、read-only/manager 12、coder 24）へ置き換え、未観測 path/package/symbol を事実として扱わず speculative path retry を止める共通指示を追加する。
- [x] benchmark の profile/case/run/candidate ごとに runtime cache と session state を隔離する。通常 runtime の cross-workflow read-only cache は維持しつつ、比較対象間の cache reuse で tool-call metric と latency が汚染されないようにする。
- [x] benchmark cell ごとに runtime state と workspace copy を隔離し、profile/candidate/run 間で前の変更が次の評価へ漏れないようにする。isolated cell は global user task catalog と外部 task/MCP path を拒否する。
- [x] process-isolated backend がない benchmark cell では `task_run` / `task_bind` を fail closed にし、command / external MCP process を起動させない。workspace copy と declared path validation だけで固定 command argument による外部アクセスを防げるとは扱わない。
- [x] trusted VM/container proxy runner を `execution.process_isolation.runner` として接続し、isolated benchmark の `task_run` と MCP stdio を disposable backend へ委譲できる protocol を追加する。backend 未設定時は process launch を fail closed にする。
- [x] macOS の明示 opt-in `macos-sandbox-exec` backend と declared write path の E2E deny test を追加する。deprecated API のため VM/container proxy を優先する。
- [ ] VM/container proxy runner の実装・provisioning と、backend が host escape できないことの integration/e2e verification を追加する。
