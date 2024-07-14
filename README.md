# autogcm (Auto Git Commit Message)

AI を活用して自動的に Git コミットメッセージを生成するツール。

## 必要条件

- Bash, Git, curl
- OpenAI API キー

## セットアップ

1. OpenAI API キーを環境変数に設定：

   ```
   export OPENAI_API_KEY='your_api_key_here'
   ```

2. スクリプトを実行可能に：
   ```
   chmod +x autogcm.sh
   ```

## 使用方法

基本：

```
git add <files>
./autogcm.sh
git commit -m "$(./autogcm.sh)"
```

システム全体で利用：

```
sudo ln -s /path/to/your/autogcm.sh /usr/local/bin/autogcm
```

直接 Git にパイプ：

```
autogcm | git commit --file=-
```

## ライセンス

MIT ライセンス
