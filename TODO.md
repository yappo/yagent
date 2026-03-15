# TODO

## Next

- 会話ログの永続化
- モデル切り替え UI
- ストリーミング応答対応
- permission policy の設定ファイル対応
- permission card に差分プレビューや変更件数を出す
- tool call card を複数同時表示・履歴表示できるようにする
- `exec` コマンドでも tool call 内容を標準出力に表示する
- search / git / patch tool のユニットテストを追加する
- TUI の focus mode と keymap カスタマイズ
- fake approver / fake tool を使った統合テストの拡充

## Tech Debt

- TUI の model は `composer`, `conversation`, `tool_call`, `permission` component に分割できる
- tool observer と approver bridge の責務整理
- path policy は将来的に glob / rule ベースへ拡張したい
- task catalog の schema validation と分かりやすいエラーメッセージ整備
- README の task 例を sample file として実ファイル化してもよい

## Nice To Have

- ログ検索
- markdown 表示改善
- セッション再開
- 複数サーバープロファイルの即時切り替え
- テーマ切り替え
- Git の読み取り Tool に branch / blame / file history を追加
