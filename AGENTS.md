# AGENTS.md

このリポジトリで Codex が作業するときの最低限の方針です。

## 目的

- ユーザ指示の UX を壊さずに保守しやすいコードを維持する
- 主要実装は `internal/` 配下に置く
- `cmd/` 配下にビジネスロジックを戻さない

## 構成

- `internal/cli`: Cobra command 定義
- `internal/tui`: Bubble Tea / Bubbles の UI
- `internal/usecase`: アプリケーションロジック
- `internal/domain`: 中核型と interface
- `internal/infra`: 外部 I/O と具体実装
- `internal/config`: 設定読込
- `internal/app`: wiring / bootstrap

## 実装ルール

- UX 変更は明示的な依頼があるときだけ行う
- TUI は Bubbles の既存 component を優先して使う
- ツール追加時は `internal/infra/tools/<name>` を基本にする
- 依存方向は `cli/tui -> usecase -> domain <- infra` を崩さない
- 重複したロジックは helper または小さな package に寄せる

## テスト

- 変更後は基本的に `go test ./...` を実行する
- usecase と infra にはユニットテストを追加する
- TUI 変更では state transition を最低限テストする

## ドキュメント

- CLI や設定項目を変えたら `README.md` を更新する
- 今後の課題は `TODO.md` に整理する
