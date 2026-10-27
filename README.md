# Cloudcord

Discordのテキスト投稿を日本語・英語・中国語で読み上げるBot。音声生成と言語処理はCloudflare Workers AIで行い、BotはDiscordへの接続と音声再生を担当する。

## 使い方

ボイスチャンネルに入り、テキストチャンネルで `/join` を実行する。以降の投稿を順番に読み上げる。`/leave` で停止・退出する。コマンドにはサーバー管理またはAdministrator権限が必要。

| コマンド | 動作 |
| --- | --- |
| `/join [text_channel]` | 読み上げ開始。省略時は現在のテキストチャンネル |
| `/leave` | 読み上げを停止して退出 |

- 1サーバーにつき1接続。本文は先頭200文字、待機キューは最大20件。
- 1投稿をまとめて音声生成し、FIFOで再生する。
- 日本語の英単語はカナ化して読む。英語のみの投稿は英語音声。
- かなを含まない漢字の投稿は日中を判定し、中国語なら原文のまま中国語音声へ渡す。曖昧な場合は日本語を優先する。
- 再起動や切断の後は `/join` を再実行する。

## セットアップ・運用

1. [DiscordとWorkerの設定](docs/setup.md)
2. [Kubernetesへのデプロイ](docs/kubernetes.md)
3. [音声処理・制限・費用](docs/speech.md)

Botの設定例は [.env.example](.env.example)。秘密情報はGitHub Secrets・Kubernetes Secret・ローカルの `.env` に保存し、リポジトリには含めない。

## 開発

Go 1.25以上、Node.js 24、C compiler、pkg-config、FFmpeg（libopus対応）が必要。

```sh
./scripts/install-libdave.sh "$HOME/.local/cloudcord-libdave"
export PKG_CONFIG_PATH="$HOME/.local/cloudcord-libdave/lib/pkgconfig${PKG_CONFIG_PATH:+:$PKG_CONFIG_PATH}"
CGO_ENABLED=1 go test -race -timeout 60s ./...
go vet ./...
cd worker
npm ci
npm run typecheck
npm test
npm run build
```

`CI` はWorkerの型検査・テスト・ビルドと、amd64のネイティブコンテナビルド・テストを実行する。`Publish Bot` は検証済みのamd64イメージをGHCRへ配布する。WorkerとBotのデプロイは別の手動ワークフロー。

arm64など別のアーキテクチャで使う場合は、対象環境でローカルビルドする。

```sh
docker build -t cloudcord:local .
```
