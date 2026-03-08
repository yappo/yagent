# yagent

LM Studioと連携するためのCLIエージェントで、ローカルのAIモデルとのインタラクションを簡単に行うためのインターフェースを提供します。

## 機能

- LM StudioとのCLIインターフェース
- AIインタラクションのためのシンプルなコマンドラインインターフェース

## インストール

```bash
go install .
```

## 使用方法

エージェントを実行:
```bash
yagent
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
# エージェントを実行
yagent
```

