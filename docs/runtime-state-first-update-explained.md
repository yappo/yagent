# yagent の今回の更新内容を人間向けにやさしく説明する文書

## この文書の目的

この文書は、今回 yagent に入れた大きな設計変更を、

- なるべく専門用語を噛み砕いて
- 何が変わったのかを順番に
- コードを読まない人でもイメージしやすいように

説明するためのものです。

対象読者は、次のような人です。

- 「runtime」「artifact」「WorkUnit」と言われてもピンとこない人
- 変更内容をあとから追いたい人
- 将来このコードを保守する人
- 設計の意図を知ってからコードを読みたい人

難しい言葉が出てきたときは、まず「何のためのものか」を先に説明し、そのあとで「コードでは何になっているか」を説明します。

---

## まず一言でいうと何が変わったのか

今回の更新で、yagent は

**「LLM が会話の流れでなんとなく進める agent」**

から、

**「runtime が状態を管理し、LLM は必要な判断だけを担当する system」**

へ大きく寄せました。

もう少しやさしく言うと、以前は

- 会話の流れ
- その場のメッセージ
- 直前の summary

に強く依存して動いていました。

今は、

- 何をやるべきか
- 何を観測したか
- 何を変更したか
- 検証結果はどうだったか
- どの情報を次の agent に渡すべきか

を、できるだけ**構造化された状態**として runtime が持つように変えています。

つまり、

- LLM は「考える」
- runtime は「覚える」「整理する」「重複を防ぐ」「並べて実行する」

という役割分担に近づけた、というのが今回の本質です。

---

## この変更が必要だった理由

以前のやり方には、次のような根本問題がありました。

### 1. 会話に依存しすぎていた

以前は「前にどんな会話をしたか」が強く残りやすく、
必要な事実と、ただの会話の残り香が混ざりやすい状態でした。

その結果、

- 前回の雑な summary が次回に強く影響する
- planner の仮説が coder にそのまま流れ込む
- 本当は不要な情報まで agent が背負う

という汚染が起きやすくなっていました。

### 2. tool call が使い捨てだった

たとえば `fs_read` で読んだ結果や、あるコマンドの出力があっても、
それが「runtime の資産」としてきちんと残っていませんでした。

そのため、

- 同じファイルを何度も読む
- 同じ command を何度も打つ
- 変更の前後関係を追いにくい

というムダが起きやすい構造でした。

### 3. 並列化が弱かった

「この 2 つは同時にやってよい」「これは衝突するから順番にやるべき」という判断を、
runtime ではなく LLM 側の偶然の出力にかなり頼っていました。

これでは、

- たまたま複数 tool call を返した時しか並列化しづらい
- 安全な並列化かどうかを runtime が判断しきれない

という問題がありました。

### 4. phase はあるのに、主役が phase だった

`plan -> execute -> verify -> recover -> finalize` という段階はありましたが、
実際の制御は「phase 関数を順番に呼ぶ」寄りでした。

本来やりたかったのは、

- 依存関係を持つ作業単位を並べる
- 終わったものを見て次の作業を追加する
- verify が fail なら recover を足す
- recover が終わったら verify をもう一度積む

という、**状態駆動の実行**です。

今回の更新は、この差を埋めるためのものです。

---

## 今回の変更で何を目指したのか

目標は次の 3 つです。

### 1. artifact 種別を計画どおり埋め切る

自然言語の summary ではなく、
できるだけ型のある artifact で phase 間をつなぐことです。

今回特に重要だった artifact は次の 3 つです。

- `repo_map`
- `change_set`
- `test_report`

### 2. WorkUnit を本当に主役にする

「今やるべき作業」を phase 名ではなく WorkUnit として持ち、
runtime がそれを見て実行順を決める形に近づけることです。

### 3. packet と memory を summary 依存から外す

agent に渡す情報や長期記憶を、
「最近の会話を雑に要約したもの」ではなく、
なるべく typed artifact や typed fact から作ることです。

---

## 重要な用語をやさしく説明する

ここはとても大事です。
今回の変更は用語が多いので、まず言葉の意味をきちんと整理します。

### runtime

runtime は、実行を管理する本体です。

「どの agent を呼ぶか」
「どの tool を使うか」
「何を覚えておくか」
「何を並列に走らせるか」
を決める側です。

LLM そのものではありません。

### LLM

文章を考えたり、計画を作ったり、ローカルな判断をするモデルです。

今回の設計では、
LLM は万能な司令塔ではなく、
runtime が整理した情報を受け取って必要な判断をする部品、
という位置づけに近づけています。

### WorkUnit

WorkUnit は「1個の作業単位」です。

たとえば、

- planner が計画を作る
- coder が実装する
- tester が検証する
- reviewer がレビューする

のような単位が WorkUnit です。

大事なのは、WorkUnit が単なる名前ではなく、

- 何に依存するか
- 何を読むか
- 何を書きそうか
- どの artifact を見るべきか
- どの失敗情報を持っているか

も一緒に持てることです。

### artifact

artifact は「後から参照できる成果物」です。

会話の一部ではなく、
ちゃんと名前と種類を持った保存物だと考えるとわかりやすいです。

今回よく出る artifact は次です。

- `execution_plan`: 実行計画
- `repo_map`: どのファイルやパスが重要そうか
- `evidence_bundle`: 調査結果の束
- `execution`: 実装や実行結果
- `change_set`: どのファイルがどう変わったか
- `review_findings`: レビュー指摘
- `test_report`: 検証結果
- `final_response`: 最終回答

### typed artifact

typed artifact は、
中身がただの文章ではなく、
JSON として意味のある形を持つ artifact です。

たとえば `change_set` なら、

- どのファイルが変わったか
- どの mutation に対応しているか
- どの tool 実行がその変更を生んだか

を項目ごとに保存できます。

### packet

packet は、agent に渡す「必要な情報のセット」です。

以前は会話履歴を広く渡しがちでしたが、
今は role ごとに packet を組み替えます。

たとえば coder には、

- 実行計画
- repo_map
- review findings
- test report
- change set

のような、実装に必要なものだけを中心に渡します。

### scratch / ephemeral

scratch は、一時的な保存場所です。

「今この run の途中で便利だから残すが、
長期記憶として強く残すべきではないもの」を置きます。

今回で、packet digest や agent packet 記録は scratch に入るようになりました。

### stable fact

stable fact は、会話の雰囲気ではなく、
比較的安定した事実として残したい情報です。

たとえば、

- この run で重要だったファイル
- 最近変更されたファイル
- 繰り返し出てくる検証失敗

のようなものです。

今回の更新では、artifact summary をそのまま昇格するやり方から一歩進めて、
typed artifact から stable fact を抽出するようにしました。

---

## 今回の更新の中身を順番に説明する

ここからが本題です。

---

## 1. run 全体を WorkUnit graph で回すようにした

### 以前

以前は、概ねこういう発想でした。

1. plan phase を呼ぶ
2. execute phase を呼ぶ
3. verify phase を呼ぶ
4. fail なら recover phase を呼ぶ
5. 最後に finalize phase を呼ぶ

これは人間にはわかりやすいですが、
runtime にとっては柔軟性が低いです。

なぜなら、

- verify が複数人いる
- verify が fail した
- recover 後に verify を再実行したい

という動きを、if 文でつなぐだけになりやすいからです。

### 今回

今回の更新では、run 全体を `runWorkGraph` が回す形に変えました。

考え方はこうです。

1. `ExecutionPlan` から初期 `WorkUnit` を作る
2. scheduler が「今実行できる unit」を選ぶ
3. 実行結果を state に反映する
4. verify が fail なら recovery unit を追加する
5. recovery 後に verify unit をもう一度追加する
6. 最後に finalize unit を追加する

つまり、**phase を順番に呼ぶのではなく、状態を見て次の作業を積む**形です。

### 何が嬉しいのか

- 実行順が明示的になる
- retry や recovery が自然に表現できる
- dependency を scheduler が見られる
- verify が複数人いても扱いやすい

実装の中心は次です。

- `internal/usecase/orchestrator/work_graph.go`

---

## 2. `repo_map` / `change_set` / `test_report` を本当に生成するようにした

これは今回かなり大きい変更です。

以前は、

- packet 側では「こういう artifact がある前提」で話している
- でも実際にはその artifact が十分に生成されていない

というギャップがありました。

それを埋めました。

### `repo_map` とは何か

`repo_map` は、
「今回の task で重要そうなファイルやパスはどこか」をまとめた artifact です。

元ネタは主に次です。

- ユーザーのメッセージで触れられたファイル
- observation に出てきた read path
- memory に残っている stable fact
- 直近の change_set

これにより、agent は広い会話ログを読む代わりに、
「今回関係が深そうなパス」を先に掴めます。

### `change_set` とは何か

`change_set` は、
「この run の中で、どのファイルがどう変わったか」をまとめた artifact です。

ここでは mutation record や execution record を見て、

- path
- operation
- tool name
- mutation id
- execution id

などを残します。

これにより、reviewer や tester や finalizer は、
LLM の説明文ではなく「実際に変わったもの」を見られます。

### `test_report` とは何か

`test_report` は、
verification の結果を typed artifact としてまとめたものです。

内容はたとえば、

- 何回目の verify か
- pass/fail
- tester や reviewer ごとの結果
- repair brief

です。

これにより、verify と recover の往復が artifact として残り、
後続 agent が会話を全部読まなくても状況を理解しやすくなりました。

中心実装は次です。

- `internal/usecase/orchestrator/runtime_artifacts.go`
- `internal/usecase/orchestrator/artifacts.go`

---

## 3. packet builder を role ごとの専用実装に分けた

### 以前

以前も role-aware ではありましたが、
大きく見ると「1つの builder が role に応じて分岐している」形でした。

これは最初は悪くありません。
ただし規模が大きくなると、

- planner に渡すべき情報
- coder に渡すべき情報
- tester に渡すべき情報

が1か所に集まりすぎて見通しが悪くなります。

### 今回

今回の更新では、builder を role ごとの型に分けました。

たとえば、

- `plannerPacketBuilder`
- `researcherPacketBuilder`
- `coderPacketBuilder`
- `testerPacketBuilder`
- `reviewerPacketBuilder`
- `finalizerPacketBuilder`

があります。

これにより、

- planner は何を受け取るべきか
- coder は何を受け取るべきか
- finalizer は何を受け取るべきか

がコード上ではっきり分かれました。

### これは何のためか

一番大事なのは、**agent ごとの責務境界を守ること**です。

planner は雑な仮説を作る立場です。
coder はそれを実装する立場です。
reviewer はバグやリスクを見る立場です。

この役割が違うのに、全員に似たような packet を配ると、
役割の混線が起きやすくなります。

今回の分離は、その混線を減らすためのものです。

中心実装は次です。

- `internal/usecase/contextengine/packet_builders.go`

---

## 4. WorkUnit の中身を「薄い箱」からもう少し実体のあるものにした

### 以前

WorkUnit という型はありましたが、
実際に埋まっているのは主に次くらいでした。

- ID
- Role
- Phase
- DependsOn
- Task

つまり、
「何をやるかの名前」はあるが、
「何を読むのか」「何を書くのか」が薄かったです。

### 今回

今回の追加改善では、WorkUnit に対して `hydrateWorkUnit` を入れました。

これは、

- 現在の `repo_map`
- 現在の `change_set`
- 現在の `KnownFailures`
- 現在の artifact 群

を見て、

- `ArtifactRefs`
- `KnownFailureRefs`
- `ReadSet`
- `WriteSet`

を補う処理です。

### どういう考え方で埋めるのか

ざっくり言うと次です。

#### preparation

準備役なので、
execution plan や repo_map や evidence bundle など、
調査に役立つ artifact を見ます。

#### primary

主実装役なので、

- execution plan
- repo_map
- evidence bundle
- review findings
- test report

を見つつ、workspace を変更しうる unit なら write set も持ちます。

#### verification / recovery / finalize

これらは「何が変わったか」が重要です。

そのため `change_set` を優先し、
最近変わった path を read set に使います。

これにより scheduler が
「この unit はどの path に関係するか」
を前よりまともに見られるようになります。

中心実装は次です。

- `internal/usecase/orchestrator/work_unit_scope.go`

---

## 5. memory を summary 昇格から typed fact 抽出に寄せた

### 以前

以前は、
「最近の artifact の summary を memory の stable fact に昇格する」
という色がまだ強く残っていました。

これはゼロよりは良いのですが、
summary は人間向けの文章なので、runtime の事実としては粗いです。

### 今回

今回の更新では、stable fact を typed artifact から作る処理を入れました。

例を挙げると、

- `repo_map` から「重要パス」
- `change_set` から「最近変更したパス」
- `test_report` / `review_findings` から「継続して注意すべき failure」

を抽出します。

これにより memory は、
会話の雰囲気ではなく、
より runtime の事実に近いものを持てるようになりました。

中心実装は次です。

- `internal/usecase/orchestrator/runtime_memory.go`

---

## 6. scratch を一時記録の専用領域として使うようにした

今回の変更では、scratch の意味もかなりはっきりしました。

scratch は、

- packet digest
- agent packet の記録

のように、
今の run の途中では便利だが、
そのまま強い long-term memory にしてはいけないもの

を置く場所です。

これにより、

- 何でも memory に入れてしまう
- 次回 session に不要なものまで bleed する

という問題を減らせます。

中心実装は次です。

- `internal/infra/state/store.go`
- `internal/usecase/contextengine/engine.go`

---

## state の保存場所はどう変わったのか

今回の設計では、`.yagent/state` 配下の意味を分ける方向に寄せています。

大きく分けると次です。

### `sessions/`

run 単位の保存です。

「その時の run はどう進んだか」を残します。

### `workspace/facts.json`

stable fact を保存します。

「この repo について、比較的安定して言えそうなこと」を残します。

### `workspace/snapshot.json`

workspace の観測状態です。

「どの path を、どの revision で見たか」に近い情報を持ちます。

### `observations/`

再利用可能な observation を保存します。

たとえば read 系 tool の結果がここに入ります。

### `artifacts/`

typed artifact を保存します。

### `executions/`

tool execution record を保存します。

「この tool を、この normalized args で実行した」という記録です。

### `mutations/`

workspace を変えた記録を保存します。

### `scratch/`

一時的な packet や digest を保存します。

---

## 今回の更新で実行の流れはどうなったのか

ここは具体例で見たほうが分かりやすいです。

ユーザーが、

`README.md を直して、最後に確認してほしい`

と頼んだとします。

すると runtime は概ねこう動きます。

1. planner が実行計画を作る  
   例: primary は coder、verify は tester、finalize は manager

2. runtime が `ExecutionPlan` から `WorkUnit` を作る

3. `repo_map` を作る  
   どのファイルが重要そうかをまとめる

4. preparation があれば並列実行する

5. primary の coder を実行する

6. coder が tool を呼ぶ  
   たとえば `fs_read`, `fs_write`

7. runtime が tool execution を記録する

8. mutation が起きたら `change_set` を作る

9. tester や reviewer が `change_set` を見ながら検証する

10. verify 結果から `test_report` を作る

11. fail なら recovery unit を積む

12. recovery が終わったら verify を再投入する

13. 最後に finalizer が結果をまとめる

このとき大事なのは、
「何が変わったか」
「何が失敗したか」
「次に誰に何を渡すべきか」
を、会話から再推測するのではなく、
runtime が state と artifact を通して持っていることです。

---

## 今回の更新でコードを読むなら、どこから見るとよいか

おすすめは次の順番です。

### 1. 全体の入口

- `internal/usecase/orchestrator/orchestrator.go`

`RunTurn` が全体の入口です。
今は plan のあとに `runWorkGraph` に入る、という流れが見えます。

### 2. 実行の中心

- `internal/usecase/orchestrator/work_graph.go`

ここが run 全体を WorkUnit graph として回す中心です。

### 3. WorkUnit の意味づけ

- `internal/usecase/orchestrator/harness.go`
- `internal/usecase/orchestrator/work_unit_scope.go`

ここで initial WorkUnit を作り、
さらに read/write/artifact/failure を補っています。

### 4. artifact 生成

- `internal/usecase/orchestrator/artifacts.go`
- `internal/usecase/orchestrator/runtime_artifacts.go`

`repo_map`, `change_set`, `test_report` の生成はここです。

### 5. memory と scratch

- `internal/usecase/orchestrator/runtime_memory.go`
- `internal/infra/state/store.go`

typed fact 抽出と scratch 保存はここです。

### 6. agent に渡す packet

- `internal/usecase/contextengine/engine.go`
- `internal/usecase/contextengine/packet_builders.go`

role ごとの packet の違いを見るならここです。

---

## 今回の更新で「何をしない設計」になったのか

これも重要です。

今回の変更は、何でも自動で賢くすることが目的ではありません。

むしろ次の方向を避けています。

### 1. LLM に全部覚えさせない

「前読んだよね？」
「さっきこう言ってたよね？」

を会話の記憶だけに頼らない方向です。

### 2. summary を真実扱いしない

summary は便利ですが、
それは人間向けの圧縮であって、
runtime の事実とは違います。

そのため、
summary は補助に下げ、
typed artifact や typed state を上に置きます。

### 3. parallelism を LLM 任せにしない

runtime が dependency や conflict を見て決める方向に寄せています。

---

## この変更で実際に何が良くなるのか

一言でいうと、
**やり直しや再利用に強くなる**ことです。

### 1. 同じ観測を繰り返しにくくなる

read 系 tool の結果が execution/observation として残るので、
条件が合えば再利用しやすくなります。

### 2. 変更点の説明責任が上がる

`change_set` があるので、
「何を変えたのか」が人間にも runtime にも追いやすくなります。

### 3. verify と recover の往復が管理しやすくなる

`test_report` と WorkUnit graph があるので、
pass/fail と retry の流れが整理されます。

### 4. agent 間の bleed が減る

planner の迷いや雑な会話履歴より、
必要な artifact を渡す方に寄るためです。

---

## まだ「改善余地」はあるのか

あります。

ただし、それは今回の更新が未完成という意味ではなく、
次にさらに良くする余地がある、という意味です。

たとえば次の余地があります。

### 1. WorkUnit の scope 推定をもっと正確にする

今は `repo_map` と `change_set` からかなりマシになりましたが、
完璧ではありません。

将来的には、

- tool metadata
- task 定義
- artifact の参照関係

をさらに使って細かくできる余地があります。

### 2. artifact の中身をさらに細かくする

たとえば `change_set` に、

- diff の要約
- 対応した review finding
- どの verify failure を直したか

まで入れる余地があります。

### 3. packet の relevance 選定をさらに賢くする

今は role ごとの専用 builder でかなり整理されましたが、
「この unit に本当に必要な artifact だけ選ぶ」精度は、
さらに上げていけます。

---

## 最後に

今回の更新を一言でまとめると、

**「会話中心の agent 実装」から「状態中心の runtime 実装」へ、さらに一段深く寄せた更新**

です。

とくに重要なのは次の 4 点です。

1. run 全体を WorkUnit graph で回すようにした  
2. `repo_map` / `change_set` / `test_report` を本当に使うようにした  
3. packet builder を role 専用に分けた  
4. memory と WorkUnit を summary 依存からさらに外した

この文書を読んだあとにコードを見るなら、
まずは `orchestrator.go` と `work_graph.go` を見るのが一番おすすめです。

