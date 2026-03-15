# TODO

## Next

- 会話ログの永続化
- モデル切り替え UI
- ツール実行ログの詳細表示
- ストリーミング応答対応
- ディレクトリ一覧やコマンド実行など追加ツール
- permission policy の設定ファイル対応
- TUI の focus mode と keymap カスタマイズ
- fake approver / fake tool を使った統合テストの拡充

## Tech Debt

- TUI の model はさらに `composer`, `conversation`, `permission` component に分割できる
- `internal/usecase/chat` のイベント通知を導入すると loading / tool 実行表示をより細かく制御しやすい
- file tool の path policy は将来的に glob / rule ベースへ拡張したい
- `exec` コマンドも将来的には TUI と同じイベント基盤へ寄せたい

## Nice To Have

- ログ検索
- markdown 表示改善
- セッション再開
- 複数サーバープロファイルの即時切り替え
- テーマ切り替え
