// Explicit opt-in deployment check. Submits short billable syntheses.
// Never stores audio, input text, tokens or signed URLs in Actions artifacts/logs.
const base = process.env.WORKER_URL;
const token = process.env.CLOUDFLARE_BOT_API_TOKEN;
if (!base || !token || new URL(base).protocol !== 'https:') throw new Error('HTTPS Worker URL and shared token required');
const url = new URL(`${base.replace(/\/$/, '')}/v1/tts`);
const engine = process.env.SPEECH_ENGINE || 'configured';
if (!['configured', 'inworld', 'grok'].includes(engine)) throw new Error('Unsupported speech engine');
if (engine !== 'configured') url.searchParams.set('engine', engine);
const samples = [
  ['Japanese', 'こんにちは。音声の確認です。'],
  ['Chinese', '你好，这是语音测试。'],
  ['English', 'Hello. This is a speech test.'],
  ['Mixed', process.env.PROBE_ENGINE === 'minimax'
    ? 'みんなで「Hello」「你好」「안녕하세요」と挨拶しました。'
    : '読み上げの test を行います。你好。Hello.'],
  ['Han with classification', '歡迎使用 Discord。'],
];
for (const [name, text] of samples) {
  const start = Date.now();
  const response = await fetch(url, {
    method: 'POST', redirect: 'error', signal: AbortSignal.timeout(55_000),
    headers: { authorization: `Bearer ${token}`, 'content-type': 'application/json' },
    body: JSON.stringify({ guild_id: '1', text, language: 'ja' }),
  });
  if (!response.ok) {
    const failure = await response.json().catch(() => null);
    const code = typeof failure?.error?.code === 'string' && /^(grok|tts)_[a-z_0-9]+$/.test(failure.error.code) ? failure.error.code : 'unknown';
    throw new Error(`${name}: HTTP ${response.status}, ${code}; request ID ${response.headers.get('x-request-id') || 'unknown'}`);
  }
  if (!response.body) throw new Error(`${name}: empty response`);
  const reader = response.body.getReader();
  let length = 0;
  let firstAudio = 0;
  const prefix = new Uint8Array(12);
  let count = 0;
  try {
    while (true) {
      const { done, value } = await reader.read();
      if (done) break;
      if (!firstAudio) firstAudio = Date.now() - start;
      length += value.byteLength;
      if (length > 5 * 1024 * 1024) throw new Error(`${name}: oversized audio`);
      const take = Math.min(prefix.length - count, value.length);
      prefix.set(value.subarray(0, take), count); count += take;
    }
  } finally { await reader.cancel().catch(() => {}); reader.releaseLock(); }
  const mp3 = count >= 3 && ((prefix[0] === 0x49 && prefix[1] === 0x44 && prefix[2] === 0x33) || (prefix[0] === 0xff && (prefix[1] & 0xe0) === 0xe0));
  const wav = count === 12 && new TextDecoder().decode(prefix.subarray(0, 4)) === 'RIFF' && new TextDecoder().decode(prefix.subarray(8)) === 'WAVE';
  if (!mp3 && !wav) throw new Error(`${name}: invalid audio signature`);
  const timings = {};
  for (const match of (response.headers.get('server-timing') || '').matchAll(/\b(language|synthesis|audio_start);dur=(\d+)\b/g)) {
    timings[`${match[1]}_ms`] = Number(match[2]);
  }
  console.log(JSON.stringify({ sample: name, engine: response.headers.get('x-speech-engine') || engine, bytes: length, first_audio_ms: firstAudio, duration_ms: Date.now() - start, format: mp3 ? 'mp3' : 'wav', ...timings }));
}
