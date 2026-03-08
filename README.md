# yagent

LM Studioと連携するためのCLIエージェントで、ローカルのAIモデルとのインタラクションを簡単に行うためのインターフェースを提供します。

## 機能

- LM StudioとのCLIインターフェース
- AIインタラクションのためのシンプルなコマンドラインインターフェース
- インタラクティブなTUIモードでの対話

## インストール

```bash
go install .
```

## 使用方法

エージェントを実行:
```bash
yagent
```

TUIモードを使用する場合:
```bash
yagent --config config.toml
```

LLMコマンドを使用する場合:
```bash
yagent llm --prompt "質問内容" --config config.toml
```

設定ファイルのフォーマットはTOMLです。以下のような形式で記述できます:

```toml
[server]
default = "lmstudio"

[[server.servers]]
name = "lmstudio"
url = "http://127.0.0.1:1234"
token = "your-api-token"
```

## 開発

テストを実行:
```bash
go test ./...
```

ビルド方法:
```bash
./build.sh
# または
go build -o yagent .
```

## 使用例

```bash
# エージェントを実行 (TUIモード)
yagent

# LLMサーバーに質問を送信 (設定ファイルを使用)
yagent llm --prompt "こんにちは" --config config.toml

# インタラクティブな対話 (TUIモード)
yagent --config config.toml
```

