# autogcm (Auto Git Commit Message)

自動的に Git コミットメッセージを生成します。

## 必要条件

- OpenAI API キー

## インストール

```sh
go install github.com/kolumoana/autogcm@latest
```

## セットアップ

```
export OPENAI_API_KEY='your_api_key_here'
```

## 使用方法

```
autogcm | git commit --file=-
```

## カスタマイズ

システムプロンプトをカスタマイズする場合は、[systemPrompt.md](./systemPrompt.md) ファイルを編集してください。

## ライセンス

MIT ライセンス
