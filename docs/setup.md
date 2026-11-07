# DiscordとWorkerの設定

## Discord

[Discord Developer Portal](https://discord.com/developers/applications)でアプリケーションとBotを作成する。

- Message Content Intentを有効にする。
- 招待scopeは `bot` と `applications.commands`。
- テキストチャンネルのView Channel、ボイスチャンネルのView Channel・Connect・Speakを許可する。
- Application IDを控える。コマンドはグローバル登録され、Botを招待したサーバーで利用できる。
- Interactions Endpoint URLは不要。BotはGatewayへ接続する。

## Cloudflare

1. Workersの `workers.dev` サブドメインを設定し、Account IDを控える。Worker名は `cloudcord`。
2. 対象アカウントへWorkerをデプロイできるAPI tokenを作成する。初回作成時と既存Worker更新時の権限は[公式の権限表](https://developers.cloudflare.com/workers/authorization/workers/)で確認する。
3. BotとWorker間で共有するランダムな認証tokenを生成する（例: `openssl rand -hex 32`）。管理用API tokenとは別の値にする。
4. Workers AIを有効にし、AI GatewayのUnified Billing用クレジットを追加する。Googleの外部APIキーは不要。
5. `worker/wrangler.toml` の `AI_GATEWAY_ID`、`TTS_ENGINE`、`TTS_VOICE` を確認する。既定のGateway `default` は最初の認証済み呼び出しで自動作成される。Gatewayの「Require provider credentials」はオフにする。話者の既定値は `Sulafat`。

## GitHub

`production` Environmentに以下を保存する。

| 保存先 | 名前 | 用途 |
| --- | --- | --- |
| Secret | `CLOUDFLARE_API_TOKEN` | Workerデプロイ用 |
| Secret | `CLOUDFLARE_BOT_API_TOKEN` | BotとWorker間の認証 |
| Secret | `DISCORD_TOKEN` | BotのDiscord接続 |
| Variable | `CLOUDFLARE_ACCOUNT_ID` | Cloudflare Account ID |
| Variable | `CLOUDFLARE_API_BASE_URL` | WorkerのHTTPS URL（`/v1/tts` は含めない） |
| Variable | `DISCORD_APPLICATION_ID` | Discord Application ID |

通常のリリースは[タグで本番へデプロイ](kubernetes.md#タグで本番へデプロイ)する。Workerだけを手動で更新する場合は、`CI` の成功後に `Deploy Worker` を `main` から実行する。Workerと認証secretを同じバージョンでデプロイし、認証・入力検証を確認する。通常の疎通確認はAI推論を実行しない。

## ローカル実行

`.env.example` を `.env` にコピーして値を設定する。秘密情報を含むファイルは共有しない。

```sh
go build -o bin/cloudcord ./cmd/cloudcord
node --env-file=.env -e 'require("node:child_process").spawn("./bin/cloudcord", [], {stdio: "inherit"})'
```

FFmpegがPATHにない場合は `FFMPEG_PATH` を設定する。Dockerでの起動例:

```sh
docker run --rm --init --env-file .env ghcr.io/atis9/cloudcord@sha256:IMAGE_DIGEST
```

通常のボイスチャンネルで `/join` を実行し、日英中韓の単独文と混在文を投稿する。`/voice speaker:Kore` で話者を変え、次の投稿から切り替わることを確認する。連続投稿が順番どおりに再生され、読み上げ中に `/leave` で停止・退出できることも確認する。

障害時はBotのJSONログとWorkerのrequest IDを確認する。合成に使ったエンジンは `X-Speech-Engine`、所要時間は `Server-Timing` で確認できる。401は共有token、429は利用制限、音声生成の502はAI Gatewayのクレジット残高・Gateway設定も確認する。
