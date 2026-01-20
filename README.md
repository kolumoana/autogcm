# autogcm (Auto Git Commit Message)

自動的に Git コミットメッセージを生成します。

## 依存

Groq の API キーが必要です（OpenAI 互換エンドポイントを使用）。

## インストール

```sh
go install github.com/kolumoana/autogcm@latest
```

## セットアップ

以下の環境変数を設定してください：

```
export GROQ_API_KEY='your_api_key_here'
```

任意で以下を上書きできます：

```
export GROQ_BASE_URL='https://api.groq.com/openai/v1'
export GROQ_MODEL='openai/gpt-oss-20b'
```

## 使用方法

```
autogcm | git commit --file=-
```

## カスタマイズ

システムプロンプトをカスタマイズする場合は、[systemPrompt.md](./systemPrompt.md) ファイルを編集してください。

## ライセンス

MIT ライセンス
