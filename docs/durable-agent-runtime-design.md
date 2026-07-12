# Durable Agent Runtime Design

更新日: 2026-07-12

この文書は、yagent を local LLM first の coding agent / harness として次の段階へ進めるための実装設計です。後方互換性は維持しません。

実装状況: durable domain contract、immutable generation snapshot store、cross-process lock、revision CAS、planner 前の workflow intent commit、validated plan attachment、production graph の `WorkflowSnapshot` authority 接続、CAS coordinator、lease/fencing、heartbeat renewal、期限切れ lease reconciliation、Mac sleep 後の stale outcome discard と retryable takeover、blocked propagation、tool action lifecycle、projection、typed `workflow_input` による snapshot-only cold recovery、source-typed security scenario、candidate 別 exact preflight、fallback transport-attempt accounting、usage/LM Studio telemetry、tool-free `planner_decision` contract、conversation/recovery operation 分離、旧 `RunState` scheduler fallback path の削除、benchmark workspace/state isolation、dispatch 直前の durable lease guard、mutating MCP tool の fencing acknowledgement contract、owned adapter 向け `pkg/durablefence`、grounded final claims gate、process-isolation proxy protocol と macOS fallback は実装済み。Gemma 4 の structured probe と read-only multi-tool eval gate は通過した。Qwen は structured probe と durable completion までは確認したが read-only eval gate は未通過。third-party provider 自身への fencing adoption、VM/container runner の provisioning と host-escape E2E、Qwen の探索効率改善、verify/recover/`needs_attention` の live benchmark は未完了。

## Decision Summary

次の改修は、個別の保存エラー修正や prompt 調整ではなく、runtime の中心を以下の 4 aggregate に分離する。

1. `Conversation`
   - user-facing history と immutable turn を保持する。
   - conversation continuation は新しい workflow を開始し、過去 workflow を再実行しない。
2. `Workflow`
   - root goal、immutable graph definition、generation、terminal status を保持する。
3. `WorkUnit`
   - frozen input artifact、read/write set、attempt、outcome、lease を保持する。
4. `Action`
   - model/tool 呼び出しの durable intent、idempotency key、precondition、result、mutation reconciliation を保持する。

`--resume` が会話継続と中断 workflow の再実行を兼ねる設計は廃止する。会話継続は conversation operation、workflow recovery は lease と reconciliation を伴う別 operation にする。

## Problems To Replace

- planning scratch と UI/audit projection には `RunState` が残るが、production graph は durable store を必須とし、scheduler authority は `WorkflowSnapshot` だけに置く。
- `WorkUnit.Status` は自由文字列で、transition validation、owner、lease、fencing token がない。
- graph の `latestExecution`、merged verification、finalize flag が process memory にしかない。
- unit の read/write set が実行中の artifact から再計算され、retry 時に scope が変わり得る。
- tool side effect より先に durable intent が保存されない。
- tool outcome が artifact、observation、snapshot、mutation、execution へ別々に保存され、partial state が可視になる。
- `sync.Mutex` は同一 process しか保護せず、複数 CLI process の lost update を防げない。
- tool/MCP output が raw `RoleTool` content のまま model に戻り、`runtime_evidence` provenance が一貫していない。
- model invocation に token usage がなく、local/remote routing の費用と context 浪費を測定できない。

## Runtime State Model

### Conversation

`Conversation` は対話の continuity だけを表す。

- `ConversationID`
- immutable `ConversationTurn[]`
- turn が開始した `WorkflowID`
- user/assistant message artifact references

`continue conversation` は過去の verified artifact を evidence として新規 workflow に渡す。過去 workflow の `running` unit を claim しない。

### Workflow

`Workflow` は一回の root goal 実行を表す。

- globally unique `WorkflowID`
- `ConversationID` と `TurnID`
- root goal
- immutable graph definition
- monotonically increasing `Revision`
- status: `pending | running | succeeded | failed | needs_attention`
- work-unit references
- final outcome references

graph definition の変更は既存 unit の mutation ではなく、新しい unit を追加する revision として commit する。

### WorkUnit

`WorkUnit` は scheduler が claim する最小単位とする。

- globally unique `WorkUnitID`
- kind / phase / role / attempt
- frozen input artifact references
- frozen read/write set
- dependencies
- status: `pending | leased | executing | succeeded | skipped | blocked | failed | needs_attention`
- lease: `OwnerID`, random `Token`, monotonic `FencingToken`, `ExpiresAt`
- outcome/action references

許可する transition は domain function で検証する。completion は current lease token と workflow revision が一致する場合だけ commit できる。期限切れ lease は即座に retry せず、関連 action の reconciliation 後にだけ `pending` へ戻せる。

### Action

`Action` は model/tool I/O の durable protocol である。

- globally unique `ActionID`
- `WorkflowID`, `WorkUnitID`, attempt
- action kind、tool/model identity
- idempotency key
- lease token / fencing token
- normalized arguments
- declared read/write set
- precondition fingerprint
- status: `prepared | executing | succeeded | failed | ambiguous | abandoned`
- result artifact / execution / mutation references
- postcondition fingerprint

tool を呼ぶ前に `prepared` intent を commit する。tool 完了後は result artifact、execution、observation、mutation、workspace snapshot、cache invalidation、unit transition を 1 transaction で commit する。

filesystem、process、MCP、外部 API をまたぐ universal exactly-once は保証しない。runtime は stale fencing credential による durable commit を拒否するが、provider が idempotency key / fencing token を受け付けない場合、開始済みの外部 effect の遅延完了までは停止できない。現実装は期限切れ実行に completed / executing / ambiguous mutation があれば `needs_attention` に閉じ、blind retry しない。postcondition から success を自動再構築する adapter は今後個別に追加する。

tool 実行 context には action/workflow/work-unit ID、attempt、idempotency key、lease/fencing token を載せる。MCP adapter はこれを `tools/call.params._meta["dev.yagent/durable-action"]` へ arguments と分離して送る。`_meta` は標準化された transport だが yagent 固有 token の enforcement protocol ではないため、対応 server が明示的に fencing token を検証するまでは外部 effect の exactly-once を主張しない。

### Provider-side Fencing Adapter

yagent 所有の Go MCP/provider adapter には `pkg/durablefence` の reference gate を使える。adapter は request `_meta` を `ParseMCPMetadata` で decode し、resource ごとの stable scope に対して次を provider の永続化境界へ組み込む。

1. resource transaction の前に `Gate.Begin` で action identity と monotonic fencing token を比較する。
2. 同じ fencing token の prepared action は重複実行せず、completed action は保存済み result metadata を replay する。
3. resource 更新と `Gate.Complete` を同じ atomic transaction に含める。

`Gate.Invoke` は effect の呼び出し回数を制御する補助 API であり、任意の外部 API / process effect と fence record の atomicity を作るものではない。provider がその二つを同じ永続化 transaction に置けない場合、成功を自動確定せず ambiguous outcome として yagent の `needs_attention` に返す。第三者 server への採用と永続化方式の検証は yagent のコードだけでは完了しない。

## Transactional FileStore

FileStore は mutable JSON files の集合を source of truth にせず、immutable generation と manifest を source of truth にする。

commit protocol:

1. state root の cross-process advisory lock を取得する。
2. `HEAD` が指す generation と expected revision を確認する。
3. transaction に含まれる object を staging generation に書き、各 file を `fsync` する。
4. object hash と完全な logical index を含む manifest を書き、`fsync` する。
5. staging generation を immutable generation path へ rename し、parent directory を `fsync` する。
6. `HEAD` を atomic replacement し、state root を `fsync` する。
7. lock を解放する。

reader は operation 開始時に 1 度だけ `HEAD` を読み、同じ generation から全 record を読む。未到達 staging generation は restart 時に削除でき、`HEAD` から到達できない object は commit 済み state として扱わない。

transaction は compare-and-swap revision を持つ。複数 process が同じ unit を claim しても一方だけが commit し、古い lease token による completion は拒否する。LLM/tool I/O 中は lock を保持しない。

これは generic event sourcing、distributed scheduler、database abstraction ではない。単一 state root の correctness boundary に限定する。

## Recovery Protocol

workflow recovery は次の順で行う。

1. workflow と action index を 1 generation から読む。
2. expired `leased/executing` unit を列挙する。
3. action ごとに side-effect class と postcondition evidence を確認する。
4. `prepared` action は external execution 前なので `abandoned` に閉じる。
5. read-only action は terminal history を保持し、新しい fencing generation と idempotency key で retry する。
6. completed / executing / ambiguous mutation があれば unit を `needs_attention` にし、人間へ evidence を提示する。
7. retryable unit は lifecycle timestamp と lease をクリアして `pending` に戻す。計画上の recovery attempt は変えず、execution generation は fencing token で分離する。
8. 新 worker は `LastFencingToken + 1` で claim し、旧 credential による action/unit completion は拒否する。

同一 process でも Mac のスリープ中は heartbeat ticker が lease を延長できない。復帰後の worker は期限切れ credential で outcome を finish せず、最新 snapshot を reload して上記 reconciliation を実行する。read-only unit だけを新しい fencing generation で再開し、外部 mutation の outcome が不明なら `needs_attention` に止める。

verify/recover/finalize に必要な execution result と merged verification は typed outcome artifact から reducer で再構築する。process-local map を authoritative state にしない。

## Authority Cutover And Projection

Wave 2 の authority cutover は planning 完了直後の revision 1 workflow snapshot commit とする。

- revision 1 より前の `RunState` は planning scratch として扱える。
- revision 1 commit が成功する前に model/tool execution を開始しない。
- revision 1 以降、ready unit、dependency、dynamic recovery/verification/finalize unit、terminal status は `WorkflowSnapshot` だけが決める。
- legacy `RunState.WorkUnits` や process-local map を scheduler input に戻さない。

`RunState` は `ProjectRunState(snapshot)` で生成する UI/audit projection に変更した。projection は workflow ID/revision を持ち、削除後も authoritative snapshot から再構築できる。projection 保存失敗は `projection_degraded` として報告するが、既に commit 済みの workflow outcome を rollback または failed にしない。conversation は実行前に pending turn index を保存し、最終 index 更新が失敗しても `WorkflowID` から terminal final artifact を復元できる。

parallel batch は次の protocol で扱う。

1. 1 snapshot から ready/non-conflicting unit を選ぶ。
2. batch 全体を claim し、workflow revision を 1 回だけ進めて commit する。
3. batch 全体を executing にし、さらに 1 revision だけ進めて commit する。
4. commit 成功後に worker goroutine を開始する。
5. expensive model/tool I/O は並列のまま、state mutation は per-workflow commit coordinator で短時間 serialise する。

external CAS conflict では pure state mutation だけを reload/replay する。terminal action が同じ result で既に commit 済みなら成功として収束できるが、lease/result が違えば fail closed とする。CAS 解決のために model/tool I/O を再実行しない。

## Evidence And Security Evals

すべての model/tool/agent/history 由来本文を同じ provenance rule で扱う。

- root user goal と harness 固定 instruction だけを trusted instruction とする。
- planner reason、delegation scope、tool/file output、MCP response、assistant/tool history は `runtime_evidence` とする。
- tool protocol の `RoleTool` と `ToolCallID` は維持し、content 自体は evidence envelope で囲む。
- embedded closing marker は構造化 encoding で無効化する。
- MCP server の trust metadata は tool capability policy にだけ使い、返却本文を trusted instruction に昇格させない。

final response は strict JSON の `claims` を持ち、各 claim は observed artifact の ID または kind を `evidence_refs` に列挙する。runtime は参照先の存在、final response 自身を evidence にしていないこと、repository path が `repo_map` / `change_set` で観測済みであることを deterministic に検証し、違反を `needs_attention` にする。これは claim と evidence の紐付けを強制する boundary であり、evidence の自然言語が claim の真偽を意味的に証明するものではない。

eval は二層に分ける。

1. deterministic runtime regression
   - fake model/tool を使い、trusted instruction、visible tools、permission、delegation scope、mutation record を直接検査する。
   - planner reason、file/tool output、delegation、MCP response、prior history の corpus を持つ。
2. real-model robustness benchmark
   - Qwen 3.6、Gemma 4、remote profile で同じ payload を実行する。
   - final prose ではなく forbidden tool call、permission request、delegation/handoff、mutation、terminal status を gate にする。

fake model test は runtime enforcement を証明するが、実 model の instruction adherence は証明しない。real-model benchmark は別途必要である。

## Usage Telemetry Before Adaptive Routing

自動 model routing の前に、Chat Completions と Responses の usage を共通型へ正規化する。

- input/prompt tokens
- output/completion tokens
- total tokens
- cached input tokens when available
- reasoning tokens when available
- usage unavailable flag

これを model invocation audit、`llm_called` event、benchmark JSONL/CSV/report へ伝播する。LM Studio が usage を返さない場合は 0 を推定値として扱わず、unavailable として区別する。

routing の初期 policy は次とする。

- local cheap: read-only exploration、catalog discovery、短い summary、deterministic probe
- local with fallback: planner/researcher/tester、bounded single-scope edit、structured output が probe 済みの task
- remote: high-risk mutation、multi-module architecture、local verification failure、security review、release gate

RAM、temperature、dynamic context、parallel local generation に基づく自動 routing は、実測 record が揃うまで実装しない。

## Implementation Waves

### Wave 0: Immediate correctness and observability

- tool/MCP output を evidence envelope に統一し deterministic regression を追加する。
- model usage を transport layer で decode し common metadata へ載せる。
- durable workflow domain types と transition invariants を追加する。

### Wave 1: Transactional state root

- generation manifest、atomic `HEAD`、cross-process lock、CAS revision を実装する。
- crash injection と concurrent claim test を追加する。
- mutable projection files は source of truth から外す。

### Wave 2: Durable action protocol

- done: planner model call 前に intent-only snapshot を commit し、planning 結果は CAS-safe な `AttachWorkflowPlan` transition で一度だけ追加する。intent-only workflow は restart 後に planning から回復する。
- done: revision 1 snapshot commit を authority cutover にし、`Config.WorkflowStore` 有効時の production graph を durable snapshot に接続する。
- done: aggregate-level batch claim/start、per-workflow CAS coordinator、lease/fencing、blocked propagation を導入する。
- done: tool intent の Prepare、Start、実 tool Execute、Finish を durable action lifecycle として接続する。
- done: action/work-unit identity を artifact、execution、mutation に伝播し、`RunState` を再構築可能な projection にする。

### Wave 3: Graph rehydration

- done: reducer/projection で graph execution state を再構築し、既存 pending snapshot を再開する。
- done: active lease の heartbeat renewal、期限切れ lease reconciliation、retryable unit の cross-process takeover、mutating ambiguity の `needs_attention` 化を実装する。
- done: tool registry は dispatch 直前に durable snapshot の action/work-unit lease を再確認し、stale credential を local/MCP provider へ渡さない。mutating MCP tool は yagent fencing extension の declaration と result `_meta` acknowledgement が一致した場合だけ成功として確定する。
- done: yagent 所有 adapter 向けに `pkg/durablefence` の server-side fencing reference gate を追加し、request metadata、monotonic token、completed replay、prepared action の重複拒否を共通化した。
- pending: third-party MCP server / provider adapter が同じ fencing token を自身の永続化境界で比較し、古い token の effect を拒否するよう採用する。MCP `_meta` transport と yagent boundary check だけでは、既に開始された外部 effect の遅延完了は停止できない。
- done: revision 1 が typed `workflow_input` artifact を明示参照し、messages/model/profile/capabilities/stream と plan を snapshot だけから復元する。
- done: unit scope を creation 時に freeze する。

### Wave 4: User-facing operation split

- done: conversation continuation と workflow recovery の domain/CLI/TUI operation を分離する。continuation は同じ conversation に新 turn / 新 workflow を作り、recovery は `WorkflowID` だけで snapshot を再開する。
- recovery は ambiguous action と必要な human decision を表示する。

### Wave 5: Measured routing and real-model eval

- done: token usage、latency、fallback、verification、mutation success と LM Studio runtime metadata を profile/candidate の benchmark record/report に集計する。
- done: candidate ごとに exact primary server/model の doctor preflight を実行し、profile record へ分離して保存する。
- done: logical model call と primary/fallback transport attempt を分離し、全 attempt の duration、usage availability/token、failure を record/report に集計する。
- partial: Gemma 4 は structured-output と read-only tool-use gate を通過した。Qwen 3.6 は structured probe と read error からの durable completion を確認したが、探索効率と事実精度の gate は未通過。
- done: benchmark profile/case/run/candidate 間の runtime cache/state を一時 state root で隔離し、通常 runtime の cross-workflow cache を保ったまま公平な比較 record を保存する。
- done: benchmark cell ごとに一時 state root と workspace copy を作り、profile/case/run/candidate 間の mutation と cache を隔離する。isolated cell は global user task catalog と workspace 外を指す declared task/MCP roots を拒否する。
- done: process-isolated backend がない benchmark cell では `task_run` と `task_bind` を fail closed にし、command / external MCP process を起動させない。workspace copy は process isolation の代替ではない。
- done: `task_run` と external MCP process を configured VM/container proxy へ委譲する protocol と、macOS の明示 opt-in `macos-sandbox-exec` backend を追加した。未設定時は fail closed。
- pending: VM/container proxy runner の provision と host escape を検証する integration/e2e。macOS fallback は deprecated API のため長期 backend にはしない。
- done: built-in agent の 200 turn 上限を role-specific `max_turns` と `max_tool_calls` budget に縮小し、未観測 repository fact と speculative path retry を禁止する grounding instruction を追加する。tool budget 到達後は tool-free synthesis に固定する。
- pending: Qwen 3.6 / Gemma 4 の verify/recover、`needs_attention` 実測 record を保存する。
- 十分な sample 数と labeled cases が揃った後に risk/capability routing を導入する。

## Subagent Allocation

現在利用可能な model catalog に基づき、次の基準で割り当てる。

| Task | Model | Effort | Reason |
|---|---|---|---|
| durable protocol / transaction design | GPT-5.6-Terra | high | cross-module invariant と failure semantics が中心 |
| security boundary implementation/review | GPT-5.6-Terra | high | false negative のコストが高い |
| bounded transport telemetry | GPT-5.6-Luna | medium | fixture-driven で write scope が明確 |
| benchmark cases/report fields | GPT-5.6-Luna | medium |既存 pattern に沿う反復実装 |
| docs / mechanical cleanup | GPT-5.6-Luna | low |低リスクで検証可能 |
| final cross-module integration review | GPT-5.6-Terra | high | aggregate 間の contract を再点検する |

`xhigh/max` は、通常の実装には使わない。crash semantics や reconciliation の矛盾がテストで解消できない場合だけ段階的に上げる。

## Pause And Resume Of This Refactor

ユーザから一時停止指示が来た場合、main agent は実行中 subagent へ interrupt を送り、次を要求する。

- 新しい変更を開始しない。
- workspace を buildable な境界へ戻す。ただし他 agent の変更を revert しない。
- changed files、completed work、remaining work、test result を返す。

その後 agent を close して ID と assignment を保持する。再開指示後は同じ ID を resume し、最新 workspace state と残作業を送る。agent が terminal completion 済みなら、新しい bounded worker を作る。

## Explicit Non-Goals

- distributed scheduler
- background daemon
- generic database backend abstraction
- arbitrary MCP/external API の universal exactly-once
- model prose を唯一の security oracle にする eval
- runtime data のない自動 RAM/thermal/context tuning
- measured corpus のない semantic model router
