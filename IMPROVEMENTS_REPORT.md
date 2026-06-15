# yagent 改善策レポート

## 1. 高優先度の機能追加

### 1.1 Permission UI の一括承認機能実装 (TODO.md: Next)
- **現状**: 権限要求が個別に処理される
- **改善案**: 同じリスクレベル・同じ action の権限要求をバッチ処理可能にする
- **実装場所**: `internal/tui/approver.go`
- **期待効果**: ユーザーの操作負荷削減、承認フローの効率化

### 1.2 Agent Status viewport の詳細情報開示 (TODO.md: Next)
- **現状**: 失敗理由が簡易的に表示される
- **改善案**: 失敗時の詳細ログやスタックトレースを `/memory` や `/status` で展開可能にする
- **実装場所**: `internal/tui/model.go`
- **期待効果**: デバッグの容易さ向上、問題解決時間の短縮

### 1.3 Conversation ログの永続化 (TODO.md: Next)
- **現状**: TUI の会話ログはセッション終了で消える
- **改善案**: `internal/infra/logging` で JSON Lines 形式で永続保存
- **実装場所**: `internal/infra/logging/logging.go`
- **期待効果**: 過去の対話の参照・分析、問題追跡の容易さ

### 1.4 exec モードの TUI 同等イベント要約 (TODO.md: Next)
- **現状**: exec モードでは簡易的なログのみ
- **改善案**: `internal/tui` のイベント基盤を再利用し、視覚的な要約表示
- **実装場所**: `internal/cli/exec.go`
- **期待効果**: 非 TUI ユーザーでも実行結果の理解度向上

### 1.5 Git Tool の拡張 (TODO.md: Nice To Have)
- **現状**: 基本的な git status/diff/log 機能のみ
- **改善案**: blame / file history / branch 操作を追加
- **実装場所**: `internal/infra/tools/git`
- **期待効果**: コードレビュー・リファクタリングの効率化

## 2. テクニカルデット解消

### 2.1 TUI model のコンポーネント分割 (TODO.md: Tech Debt)
- **現状**: `model.go` が単一の大規模ファイル（~80KB）
- **改善案**: 
  - `composer`: メッセージ合成ロジック
  - `conversation`: 対話履歴管理
  - `status`: Agent Status viewport
  - `tool_call`: ツール呼び出し表示
  - `permission`: 権限確認 UI
- **実装場所**: `internal/tui/model.go` → 分割
- **期待効果**: 保守性向上、テスト容易さの向上

### 2.2 Orchestrator の event payload 整理 (TODO.md: Tech Debt)
- **現状**: UI 表示用とログ用の detail が混在
- **改善案**: `internal/domain/event.go` で明示的な分割
- **実装場所**: `internal/usecase/orchestrator`
- **期待効果**: ログ可視化の明確化、UI 最適化

### 2.3 Scheduler の path 粒度向上 (TODO.md: Tech Debt)
- **現状**: coarse な read/write set 推定
- **改善案**: task/MCP の実 path を強く考慮した粒度管理
- **実装場所**: `internal/infra/policy`
- **期待効果**: 不必要なファイルアクセス削減、コンテキスト最適化

### 2.4 Task catalog の schema validation (TODO.md: Tech Debt)
- **現状**: TOML 構文エラーのみでエラー
- **改善案**: JSON Schema でバリデーション、分かりやすいエラーメッセージ
- **実装場所**: `internal/config` / `internal/usecase/taskcatalog`
- **期待効果**: 設定ミスの早期発見、ユーザーフレンドリーさ向上

## 3. テスト拡充 (TODO.md: Tech Debt)

### 3.1 search/git/patch tool のユニットテスト追加
- **実装場所**: `internal/infra/tools/search/*` / `internal/infra/tools/git/*` / `internal/infra/tools/patch/*`
- **期待効果**: ツール整合性の保証、リファクタリングの安全さ

### 3.2 Fake approver/fake tool を使った統合テスト拡充
- **実装場所**: `internal/tui/model_test.go`
- **期待効果**: 権限フロー・ツール呼び出しの end-to-end テスト

### 3.3 Typed artifact payload の schema validation 追加
- **実装場所**: `internal/domain/artifact.go`
- **期待効果**: Artifact 整合性の保証、データ腐敗防止

## 4. ドキュメント改善

### 4.1 README.md の拡張
- **現状**: 基本的な機能説明のみ
- **改善案**:
  - 設定項目のデフォルト値例示
  - Benchmark プロフィルの詳細説明
  - Agent DSL の追加サンプル
- **期待効果**: ユーザーオンボーディングの容易さ向上

### 4.2 PLAN.md の更新
- **現状**: ファイル処理機能実装計画のみ
- **改善案**: yagent の全体機能と今後のロードマップを記述
- **期待効果**: プロジェクトビジョンの明確化

## 5. パフォーマンス最適化

### 5.1 fs_list の探索粒度最適化 (TODO.md: Next)
- **現状**: 深さ指定で再帰的探索が複数回発生
- **改善案**: 一度の結果をキャッシュし、重複探索を削減
- **実装場所**: `internal/infra/tools/fs`
- **期待効果**: ファイルシステム I/O の削減、レスポンス高速化

### 5.2 Observation relevance ranking (TODO.md: Next)
- **現状**: 基本的な関連性判定
- **改善案**: TF-IDF や BM25 を使ったスコアリング
- **実装場所**: `internal/usecase/contextengine`
- **期待効果**: Packet サイズ削減、コンテキスト最適化

## 6. ユーザーエクスペリエンス向上

### 6.1 Model 切り替え UI (TODO.md: Nice To Have)
- **現状**: CLI で `--model` を指定する必要がある
- **改善案**: TUI から即座にモデルを切り替えられる UI
- **実装場所**: `internal/tui/model.go`
- **期待効果**: モデル試行の容易さ向上

### 6.2 テーマ切り替え (TODO.md: Nice To Have)
- **現状**: 固定テーマ
- **改善案**: TUI の配色を切り替える機能
- **実装場所**: `internal/tui/completion.go`
- **期待効果**: ユーザーの好みに合わせた利用環境

### 6.3 セッション再開機能 (TODO.md: Nice To Have)
- **現状**: `/resume` は latest_session のみ
- **改善案**: 過去のセッション一覧から選択して再開
- **実装場所**: `internal/infra/state`
- **期待効果**: 断続的な作業環境でも継続性の確保

## 7. ビルド・デプロイメント

### 7.1 Makefile の拡張
- **現状**: 簡易な build/test 命令のみ
- **改善案**:
  - `make lint` (golangci-lint)
  - `make doc` (godoc generation)
  - `make test-cov` (coverage report)
  - `make benchmark` (benchmark suite)
- **期待効果**: CI/CD の簡素化、開発ワークフローの標準化

### 7.2 Benchmark 結果の CSV/artifact 保存 (TODO.md: Next)
- **現状**: コンソール出力のみ
- **改善案**: JSONL / CSV 形式で結果を保存
- **実装場所**: `internal/usecase/benchmark`
- **期待効果**: 結果の可視化・比較分析の容易さ

## 8. セキュリティ向上

### 8.1 Permission policy の設定ファイル対応 (TODO.md: Next)
- **現状**: コードでの判定のみ
- **改善案**: `.yagent/permission.toml` で許可パス・リスクレベルを定義
- **実装場所**: `internal/config` / `internal/infra/policy`
- **期待効果**: セキュリティポリシーの明示的宣言、チーム共有の容易さ

### 8.2 MCP Server の risk レベル追加 (README.md)
- **現状**: task catalog に risk = medium/default
- **改善案**: MCP server にも risk/allow_network を追加
- **期待効果**: 外部連携の適切な制限管理

## 9. まとめ

| カテゴリ | 優先度 | 実装期間目安 |
|---------|--------|-------------|
| Permission UI バッチ処理 | High | 1-2 日 |
| Agent Status 詳細表示 | High | 0.5-1 日 |
| Conversation ログ永続化 | High | 1-2 日 |
| exec モード TUI 要約 | Medium | 1-2 日 |
| Git Tool 拡張 | Medium | 2-3 日 |
| TUI model 分割 | Medium | 2-3 日 |
| Orchestrator payload 整理 | Medium | 0.5-1 日 |
| Scheduler path 粒度向上 | Low | 2-3 日 |
| Task catalog validation | Medium | 1-2 日 |
| search/git/patch テスト追加 | Low | 2-3 日 |
| README.md 拡張 | Low | 0.5-1 日 |

## 10. 実装ロードマップ

### Phase 1 (優先度: High) - ユーザー体験向上
1. Permission UI の一括承認機能
2. Agent Status の詳細情報表示
3. Conversation ログ永続化
4. exec モードの TUI 同等要約

### Phase 2 (優先度: Medium) - テクニカルデット解消
1. Git Tool 拡張
2. TUI model コンポーネント分割
3. Orchestrator event payload 整理
4. Task catalog schema validation

### Phase 3 (優先度: Low) - 機能拡充・テスト拡充
1. Scheduler path 粒度向上
2. Observation relevance ranking
3. Model/テーマ切り替え UI
4. セッション再開機能
5. Benchmark 結果保存
6. search/git/patch テスト追加
7. README.md 拡張

---

**作成日**: 2024
**バージョン**: v1.0.0
