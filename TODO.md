# TODO

## Next

- permission ダイアログの一括承認 UI
- `fs_list` の探索粒度最適化と重複要求削減
- Agent Status viewport から失敗理由の詳細を開けるようにする
- 会話ログの永続化
- exec モードでも TUI と同等のイベント要約を見やすく出す
- モデル切り替え UI
- ストリーミング応答対応
- benchmark 結果の CSV / artifact 保存
- permission policy の設定ファイル対応
- permission card に差分プレビューや変更件数を出す
- tool call card を複数同時表示・履歴表示できるようにする
- search / git / patch tool のユニットテストを追加する
- fake approver / fake tool を使った統合テストの拡充

## Tech Debt

- TUI の model は `composer`, `conversation`, `status`, `tool_call`, `permission` component に分割したい
- permission queue は大量件数でも動くが、同種 request の集約やバッチ承認がまだない
- tool observer と approver bridge の責務整理
- orchestrator の event payload は UI 表示用 detail とログ用 detail が混ざっている
- `fs_list` の結果をより強く再利用し、同じ探索を繰り返さないようにしたい
- path policy は将来的に glob / rule ベースへ拡張したい
- task catalog の schema validation と分かりやすいエラーメッセージ整備
- `exec` コマンドも TUI と同じイベント基盤へさらに寄せたい

## Nice To Have

- Agent DSL の schema / 補完支援
- Agent Status viewport の filter / fold / search
- 継続確認の既定ポリシー設定
- ログ検索
- markdown 表示改善
- セッション再開
- 複数サーバープロファイルの即時切り替え
- テーマ切り替え
- Git の読み取り Tool に branch / blame / file history を追加
