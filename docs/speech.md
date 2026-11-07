# 音声処理

Botが投稿を整形してWorkerへ送り、WorkerがCloudflare AI Gateway経由で `inworld/tts-2` を呼び出す。日本語・中国語・英語を同じ話者で1回の音声生成にまとめ、英単語はカナ化せず原文で渡す。BotのFFmpegがWAV / MP3をOpusに変換してDiscordへ送る。

## 言語

Inworld TTS 2が混在文の言語を切り替えて読む。漢字の読みを補助するため、Workerは漢字の連続部分だけに `<lang>` タグを付ける。英語・かなの部分は原文を保つ。入力に含まれるXMLの記号はエスケープする。タグの追加でモデルの2,000文字制限を超える場合だけ、文全体に1つの言語タグを付ける。

- 登録済みの中国語特有の簡体字（`这・语・说・们` など）を含む投稿: 漢字部分を `zh-CN` に指定する。判定モデルは呼び出さない。
- それ以外で、ひらがな・カタカナを含む投稿: 漢字部分を `ja-JP` に指定する。
- Latin文字だけの投稿: 言語タグなし。
- 上記に該当せず、漢字を含む投稿: Workers AIの `@cf/cloudflare/clef` が日中を判定する。
- 判定失敗・confidence 0.8未満・数字や絵文字のみ: 日本語を既定とする。

`国・学・体・会・机・写` など日本語と共通する字は簡体字の強制判定に含めない。短い共通表現は曖昧なので日本語を優先する。判定は最大3秒、高confidenceの結果をWorker isolate内に最大256件キャッシュする。confidenceは正解率の保証ではない。

## 制限

本文は200 Unicode文字まで。言語判定から音声ダウンロードの開始まで合計25秒、音声本体は最大5 MiB・25秒。推論の再試行は行わない。提出済みのAI binding推論はキャンセルできず、タイムアウトした推論にも料金がかかる場合がある。

AI Gatewayの返却する署名付き音声URLはWorker側で取得し、Botへ音声だけを返す。HTTPSのR2配信先だけを受け付け、リダイレクト・過大な応答・不正な音声形式を拒否する。音声URLや認証情報はBotへ渡さない。

## 音量調整

BotのFFmpegで、Opusへの変換前に自動音量調整・コンプレッサー・リミッターを適用する。言語ごとの入力音量差を抑え、突発的な大きい音を制限する。追加プロセスは使わず、音声フィルターも1スレッドで処理する。

自動音量調整は50ms単位・5フレームの窓を使用し、増幅は最大10倍（20 dB）、目標RMSは0.15。無音付近の増幅を抑え、最後にOpusエンコード前のピークを約−1 dBFSへ制限する。音声全体の解析や2パス変換は行わない。

## 設定とログ

`worker/wrangler.toml` の `AI_GATEWAY_ID` は `default`、`TTS_VOICE` は `Dennis` が既定。`default` Gatewayは最初の認証済み呼び出し時に自動作成される。AI bindingは認証済みなので、InworldのAPIキーは不要。

内部APIは互換性のため `language=ja` を受け付ける。`X-Speech-Language` の `ja` / `en` / `zh` は漢字の読みの補助言語であり、混在文全体を単一言語で読む指定ではない。

Workerのログには本文・音声URL・認証情報を含めない。Gateway呼び出しは `collectLog=false`・`skipCache=true` を指定する。読み上げ本文は音声生成のためInworldへ送信され、日中判定が必要な場合はClefにも送信される。

## 費用

[CloudflareのInworld TTS 2料金](https://developers.cloudflare.com/ai/models/inworld/tts-2/)は1文字$0.000025。10万文字で$2.50、30万文字で$7.50。言語タグも課金文字数に含まれるため、本文の文字数だけで計算した額より増える。$10で最大約40万の課金対象文字を生成できる。

音声生成はAI GatewayのUnified Billingの前払いクレジットから支払う。Workers AIの無料枠はこの音声生成には適用されない。Clefは従来のWorkers AI課金となる。Worker実行料・有料プラン基本料・税は別。最新料金と残高はCloudflareのDashboardで確認する。

## Grokとの比較

`TTS_ENGINE=inworld` を既定とし、`grok` へ変更するとCloudflare経由の `xai/grok-tts` WebSocketを使用する。`GROK_VOICE` の既定は `rex`。比較時だけ認証済みの `/v1/tts?engine=grok` または `?engine=inworld` で選択することもできる。

Grokでは原文を1回送り、言語は `auto` とする。Inworld専用の言語タグや日中判定は適用しない。`audio.delta` をMP3ストリームへ変換し、生成完了を待たずにBotへ返す。キャンセル・エラー・25秒経過・5 MiB超過時には接続を閉じる。外部APIキーは追加せず、AI Gatewayクレジットから支払う。Grokの料金は1文字$0.000015。

`Deploy Worker` の `verify_speech` と `speech_engine` を指定すると、短い日中英・混在文を生成して接続を確認できる。この確認には推論料金がかかる。ログに音声・本文・署名付きURL・一時認証情報は保存しない。`first_audio_ms` と `duration_ms` で音声の到着開始と受信完了を区別する。音質はDiscordで確認する。
