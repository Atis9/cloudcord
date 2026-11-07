# 音声処理

Botが整形した投稿をWorkerへ送り、WorkerがCloudflare AI Gateway経由でGemini 3.8 TTSを呼び出す。日本語・英語・中国語・韓国語の混在文は同じ話者で読む。BotのFFmpegが音声を約1.2倍速に調整し、Opusに変換してDiscordへ送る。

## 言語

読み上げる言語と発音はGeminiが原文から判断する。Workerは言語判定、言語タグの追加、カナ変換を行わない。日本語の文中にある短い外国語は、日本語の文脈に沿った発音になることがある。

## 制限

Botは読み上げ開始後に、次の待機投稿を1件だけ音声生成する。先読みした音声はBotのメモリに最大5 MiBまで保持し、待機上限20件にも含める。最初の投稿は音声が届き次第再生する。退出時は再生と先読みを止めるが、送信済みの音声生成には課金される場合がある。

Gemini Flash-Liteでは、120文字以上の投稿に自然な文の区切りがあれば、最大2つに分けて並行合成する。Workerは先頭の音声ができ次第、一続きのWAVとして配信を始め、後半を順番に追加する。短文や適切な区切りがない投稿は一括で合成する。

本文は200 Unicode文字まで。合成リクエストから音声の取得開始まで合計25秒、音声本体は最大5 MiB・25秒。推論の再試行は行わない。提出済みのAI binding推論はキャンセルできず、タイムアウトした推論にも料金がかかる場合がある。

Geminiが返すWAVはWorkerで検証してからBotに返す。AI Gatewayが署名付き音声URLを返した場合はWorker側で取得する。HTTPSのR2配信先だけを受け付け、リダイレクト・過大な応答・不正な音声形式を拒否する。音声URLや認証情報はBotへ渡さない。

## 話速と音量

BotのFFmpegで、声の高さを保ったまま約1.2倍速に調整する。同じ処理で、Opusへの変換前に自動音量調整・コンプレッサー・リミッターを適用する。言語ごとの入力音量差を抑え、突発的な大きい音を制限する。追加プロセスは使わず、音声フィルターも1スレッドで処理する。

自動音量調整は50ms単位・5フレームの窓を使用し、増幅は最大10倍（20 dB）、目標RMSは0.15。フレームのピークが0.015以下の区間は増幅を抑え、文の間の小さな息継ぎが目立ちにくくなるようにする。最後にOpusエンコード前のピークを約−1 dBFSへ制限する。音声全体の解析や2パス変換は行わない。

Opusは20msフレーム・64 kbps固定で送る。Botは先頭3フレームまでをバッファし、無音の2フレームを送ってから音声を再生する。

## 設定とログ

`worker/wrangler.toml` の `AI_GATEWAY_ID` は `default`、`TTS_VOICE` は `Sulafat` が既定。`TTS_ENGINE` の既定値はGemini 3.8 Flash-Lite TTSを使う `gemini-lite`。通常版のFlash TTSには `gemini` を指定する。既定のGatewayは最初の認証済み呼び出しで自動作成される。GoogleのAPIキーは不要。

内部APIは互換性のため `language=ja` を受け付ける。合成は自動判定で、`X-Speech-Language` は `auto` を返す。`/voice` で選択した場合、Botはキューに追加した時点の話者を `voice` に指定する。省略時は Worker の既定の話者を使う。

Workerのログには本文・音声URL・認証情報を含めない。Gateway呼び出しは `collectLog=false`・`skipCache=true` を指定する。読み上げ本文は音声生成のためGoogleへ送信される。

成功時のログと `Server-Timing` には、AI呼び出しと、AI応答後から音声の先頭を検証するまでの時間を記録する。AI呼び出しの時間にはGatewayでの待ち時間も含まれる。

## 費用

音声生成はAI GatewayのUnified Billingの前払いクレジットから支払う。テキスト入力と生成した音声に対してトークン単位で課金される。Bot側の話速変更は合成後に行う。最新料金と残高はCloudflareのダッシュボードで確認する。

- [Gemini 3.8 Flash TTSの料金](https://developers.cloudflare.com/ai/models/google/gemini-3.8-flash-tts/)
- [Gemini 3.8 Flash-Lite TTSの料金](https://developers.cloudflare.com/ai/models/google/gemini-3.8-flash-lite-tts/)

## 比較と疎通確認

`Deploy Worker` の `verify_speech` を指定すると、短い日英中韓の単独文・混在文を生成して疎通を確認できる。`probe_gemini_voice` と `probe_gemini_engine` を指定すると、本番を更新せず指定モデルと話者を確認できる。比較用に `probe_minimax_voice` と、認証済みの `/v1/tts?engine=grok` も使える。

音声生成の確認には推論料金がかかる。ログに音声・本文・署名付きURL・一時認証情報は保存しない。`first_audio_ms` と `duration_ms` で音声の到着開始と受信完了を区別する。
