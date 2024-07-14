#!/bin/bash

# OpenAI APIキーを環境変数から取得
API_KEY="${OPENAI_API_KEY}"

# APIキーが設定されていない場合、エラーメッセージを表示して終了
if [ -z "$API_KEY" ]; then
  echo "Error: OPENAI_API_KEY environment variable is not set." >&2
  exit 1
fi


# ステージされた変更の差分を取得
diff=$(git diff --cached)

# 差分がない場合、スクリプトを終了
if [ -z "$diff" ]; then
  echo "No staged changes found." >&2
  exit 1
fi

# システムプロンプトとユーザープロンプトを設定
system_prompt=$(cat << 'EOF'
# 命令

あなたは「gitのコミットメッセージを生成するAIアシスタント」です。
渡されたgitの変更点をもとに最適なコミットメッセージを作成してください。

# 条件

- 変更点の要約を簡潔に伝えること
- 変更の理由や目的が分かるようにすること
- コードの具体的な変更内容（ファイル名や機能）を含めること
- コミットメッセージは日本語で記述すること
- コードブロック(\`\`\`)は出力せず内容だけを出力すること

# 入力データ

```
diff --git a/src/main.js b/src/main.js
index 83db48d..bfef0a4 100644
--- a/src/main.js
+++ b/src/main.js
@@ -25,7 +25,7 @@ function updateUserProfile(user) {
     userProfile.name = user.name;
     userProfile.email = user.email;
     userProfile.age = user.age;
-    userProfile.location = user.location;
+    userProfile.address = user.address;
     return userProfile;
 }
```

# 出力例

```
updateUserProfile関数でlocationフィールドをaddressにリファクタリング

- `src/main.js` 内の `user.location` を `user.address` に変更
- データモデルの更新に伴う一貫性を確保
```
EOF
)

user_prompt="$diff"

# 文字列をJSON用にエスケープする関数
json_escape() {
    local s=$(echo "$1" | sed 's/\\/\\\\/g; s/"/\\"/g; s/$/\\n/g' | tr -d '\n')
    echo -n "$s"
}

# エスケープされたJSONデータを生成
json_data='{
  "model": "gpt-4o",
  "messages": [
    {"role": "system", "content": "'"$(json_escape "$system_prompt")"'"},
    {"role": "user", "content": "'"$(json_escape "$user_prompt")"'"}
  ]
}'

# OpenAI APIにリクエストを送信
response=$(curl -s https://api.openai.com/v1/chat/completions \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer $API_KEY" \
  -d "$json_data")

# レスポンスからコミットメッセージを抽出し、整形する
commit_message=$(echo "$response" | sed -n 's/.*"content"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' | sed 's/\\n/\n/g' | sed 's/\\"/"/g')

if [ -z "$commit_message" ]; then
  echo "Failed to generate commit message. API response:" >&2
  echo "$response" >&2
  exit 1
fi

# コミットメッセージから不要な文字を削除
trimmed=$(echo "$commit_message" | sed -e 's/^```//' -e 's/```$//' | sed -e '1s/^\s*//' -e '$s/\s*$//' | sed '/./,$!d')

# 生成されたコミットメッセージを表示
printf '%s\n' "$trimmed"


# echo "Raw commit_message:"
# echo "$commit_message" | od -c
# echo "Trimmed message:"
# echo "$trimmed" | od -c