# Cloudcord

Discordのテキスト投稿を日本語・英語・中国語・韓国語で読み上げるBot。音声生成にはCloudflare AI Gateway経由のGemini 3.8 Flash-Lite TTSを使う。

## 使い方

ボイスチャンネルに入り、テキストチャンネルで `/join` を実行する。以降の投稿を順番に読み上げる。`/leave` で停止・退出する。コマンドは一般メンバーも利用できる。Botの操作は本人が参加中のボイスチャンネルに限り、読み上げ対象には本人も閲覧できるテキストチャンネルを指定する。

| コマンド | 動作 |
| --- | --- |
| `/join [text_channel]` | 読み上げ開始。省略時は現在のテキストチャンネル |
| `/voice [speaker]` | 話者を Sulafat・Vindemiatrix・Kore から選ぶ。省略時は現在の設定を表示 |
| `/leave` | 読み上げを停止して退出 |

- 1サーバーにつき1接続。本文は先頭200文字、待機キューは最大20件。
- 1投稿をまとめて音声生成し、投稿順に再生する。再生中は次の1件だけ音声を先読みする。
- 日英中韓の混在文は原文をまとめて渡し、Geminiが発音を判断する。
- `/voice` は Bot と同じボイスチャンネルから実行する。設定はその読み上げセッションの新規投稿に適用され、`/leave` や Bot の再起動後は既定の Sulafat に戻る。
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
