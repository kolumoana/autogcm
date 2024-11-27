# autogcm (Auto Git Commit Message)

自動的に Git コミットメッセージを生成します。

## 依存

- [Groq API キー](https://groq.com/)

## インストール

```sh
go install github.com/kolumoana/autogcm@latest
```

## セットアップ

```
export GROQ_API_KEY='your_api_key_here'
```

## 使用方法

```
autogcm | git commit --file=-
```

## カスタマイズ

システムプロンプトをカスタマイズする場合は、[systemPrompt.md](./systemPrompt.md) ファイルを編集してください。

## ライセンス

MIT ライセンス
