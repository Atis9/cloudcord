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
4. Workers AIを有効にする。AI bindingを使用するため、外部APIキーやAI Gatewayの購入クレジットは不要。

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

`CI` の成功後、`Deploy Worker` を `main` から実行する。認証secretの登録、Workerデプロイ、認証・入力検証の確認を行う。疎通確認はAI推論を実行しない。

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

通常のボイスチャンネルで `/join` → 投稿 → `/leave` を確認する。日本語・英語・中国語と、連続投稿の順序・読み上げ中の停止を確認する。

障害時はBotのJSONログ、Workerのrequest ID、`X-Speech-Language` を確認する。401は共有token、429はWorkers AIの利用制限を確認する。
