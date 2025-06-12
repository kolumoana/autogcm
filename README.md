# autogcm (Auto Git Commit Message)

自動的に Git コミットメッセージを生成します。

## 依存

以下のいずれかの API キーが必要です：

- [Gemini API キー](https://aistudio.google.com/app/apikey)
- [Groq API キー](https://groq.com/)
- [OpenAI API キー](https://platform.openai.com/api-keys)

## インストール

```sh
go install github.com/kolumoana/autogcm@latest
```

## セットアップ

以下の環境変数を設定してください（少なくとも1つは必須）：

```
export GEMINI_API_KEY='your_api_key_here'
export GROQ_API_KEY='your_api_key_here'
export OPENAI_API_KEY='your_api_key_here'
```

API の優先順位: Gemini → Groq → OpenAI

## 使用方法

```
autogcm | git commit --file=-
```

## カスタマイズ

システムプロンプトをカスタマイズする場合は、[systemPrompt.md](./systemPrompt.md) ファイルを編集してください。

## ライセンス

MIT ライセンス
