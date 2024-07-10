#!/bin/bash

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

commit_message=$(echo -e "$user_prompt" | llm --system "$system_prompt" --model gpt-4o)

if [ -z "$commit_message" ]; then
  echo "Failed to generate commit message." >&2
  exit 1
fi

cleaned_commit_message=$(echo "$commit_message" | sed 's/^```//' | sed 's/```$//')

echo -e "$cleaned_commit_message"