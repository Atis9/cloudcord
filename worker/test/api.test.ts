import test from 'node:test';
import assert from 'node:assert/strict';
import coreWorker, { decodeAudio } from '../src/index.ts';
import { spellWord as convertWord } from '../src/kana.ts';
const worker = { fetch: (request: Request, env: Env) => coreWorker.fetch(request, env, convertWord) };
import type { Env } from '../src/index.ts';
const audio = new Uint8Array([0x49, 0x44, 0x33, 1, 2, 3]);
function request(body: unknown = { guild_id: '123456789012345678', text: 'こんにちは', language: 'ja' }, token = 'secret'): Request {
  return new Request('https://example.com/v1/tts', { method: 'POST', headers: { authorization: `Bearer ${token}`, 'content-type': 'application/json' }, body: JSON.stringify(body) });
}
function environment(run: Env['AI']['run'] = async () => audio): Env { return { BOT_API_TOKEN: 'secret', MELOTTS_LANG: 'jp', AI: { run } }; }
test('authenticated Japanese request returns binary MP3 and exact binding parameters', async () => {
  let calls = 0;
  const env = environment(async (model, input) => { calls++; assert.equal(model, '@cf/myshell-ai/melotts'); assert.deepEqual(input, { prompt: 'こんにちは', lang: 'jp' }); return new Response(audio, { headers: { 'Content-Type': 'audio/mpeg' } }); });
  const response = await worker.fetch(request(), env);
  assert.equal(response.status, 200); assert.equal(response.headers.get('content-type'), 'audio/mpeg');
  assert.deepEqual(new Uint8Array(await response.arrayBuffer()), audio); assert.equal(calls, 1);
});
test('mixed post is converted in the Worker and synthesized exactly once', async () => {
  let calls = 0;
  const response = await worker.fetch(request({guild_id: '12', text: '読み上げの test を行います', language: 'ja'}), environment(async (_, input) => {
    calls++;
    assert.deepEqual(input, {prompt: '読み上げの ティーイーエスティー を行います', lang: 'jp'});
    return audio;
  }));
  assert.equal(response.status, 200);
  assert.equal(calls, 1);
  await response.body?.cancel();
});
test('real binding base64 WAV is served with its actual format and Japanese adapter language', async () => {
  const wav = new Uint8Array(48);
  wav.set(new TextEncoder().encode('RIFF'), 0);
  wav.set(new TextEncoder().encode('WAVEfmt '), 8);
  const response = await worker.fetch(request(), environment(async (_, input) => {
    assert.equal(input.lang, 'jp');
    return { audio: btoa(String.fromCharCode(...wav)) };
  }));
  assert.equal(response.status, 200);
  assert.equal(response.headers.get('content-type'), 'audio/wav');
  assert.deepEqual(new Uint8Array(await response.arrayBuffer()), wav);
});
test('English-only posts use British Draco while Japanese, mixed text and numbers retain MeloTTS', async () => {
  for (const [text, lang] of [
    ['Hello, this is a test.', 'en'],
    ['For workflows on GitHub Actions to authenticate with k8s.chitoku.jp, it trusts GitHub Actions through OpenID Connect.', 'en'],
    ['GitHub Actionsを使います。', 'jp'], ['こんにちは。', 'jp'], ['12345', 'jp'],
  ]) {
    let calls = 0;
    const response = await worker.fetch(request({guild_id: '12', text, language: 'ja'}), environment(async (model, input) => {
      calls++;
      if (lang === 'en') {
        assert.equal(model, '@cf/deepgram/aura-2-en');
        assert.deepEqual(input, {text, speaker:'draco', encoding:'mp3'});
      } else {
        assert.equal(model, '@cf/myshell-ai/melotts');
        assert.deepEqual(input, {lang, prompt: text.replace('GitHub Actions', convertWord('GitHub') + ' ' + convertWord('Actions'))});
      }
      return audio;
    }));
    assert.equal(response.status, 200);
    assert.equal(calls, 1);
    await response.body?.cancel();
  }
});
test('fragmented WAV header is validated without changing or buffering the audio body', async () => {
  const wav = new TextEncoder().encode('RIFF1234WAVEaudio');
  let source: ReadableStreamDefaultController<Uint8Array>;
  let cancelled = false;
  const upstream = new ReadableStream<Uint8Array>({ start(c) {
    source = c;
    c.enqueue(wav.slice(0, 2)); c.enqueue(wav.slice(2, 7)); c.enqueue(wav.slice(7));
  }, cancel() { cancelled = true; } });
  const response = await worker.fetch(request(), environment(async () => upstream));
  assert.equal(response.status, 200);
  assert.equal(response.headers.get('content-type'), 'audio/wav');
  const reader = response.body!.getReader();
  const chunks: number[] = [];
  while (chunks.length < wav.length) chunks.push(...(await reader.read()).value!);
  assert.deepEqual(chunks, [...wav]);
  await reader.cancel();
  assert.equal(cancelled, true);
  for (const invalid of ['RIFF1234AVI ', 'RIFF']) {
    const failed = await worker.fetch(request(), environment(async () => new Response(invalid)));
    assert.equal(failed.status, 502);
  }
});
test('authentication, invalid language, numeric IDs and body limits reject before AI', async () => {
  const env = environment(async () => { throw new Error('must not call AI'); });
  assert.equal((await worker.fetch(request(undefined, 'wrong'), env)).status, 401);
  for (const body of [null, { guild_id: 123, text: 'a', language: 'ja' }, { guild_id: '12', text: 'a', language: 'en' }, { guild_id: '12', text: '😀'.repeat(201), language: 'ja' }, { guild_id: '18446744073709551616', text: 'a', language: 'ja' }, { guild_id: '12', text: ' ', language: 'ja' }, { text: 'a'.repeat(5000) }]) {
    assert.equal((await worker.fetch(request(body), env)).status, 400);
  }
});
test('stream and base64 JSON output variants normalize and reject malformed or oversized audio', async () => {
  assert.deepEqual(await decodeAudio({ audio: btoa(String.fromCharCode(...audio)) }), audio);
  assert.deepEqual(await decodeAudio(new Response(JSON.stringify({ audio: btoa(String.fromCharCode(...audio)) }), { headers: { 'content-type': 'application/json' } })), audio);
  assert.deepEqual(await decodeAudio(new Response(audio).body), audio);
  await assert.rejects(decodeAudio({})); await assert.rejects(decodeAudio(new Uint8Array([1, 2, 3])));
  let cancelled = false;
  await assert.rejects(decodeAudio(new ReadableStream({ start(c) { c.enqueue(new Uint8Array(5 * 1024 * 1024 + 1)); }, cancel() { cancelled = true; } })));
  assert.equal(cancelled, true);
});
test('upstream failures never expose sensitive errors and do not retry', async () => {
  let calls = 0;
  const response = await worker.fetch(request(), environment(async () => { calls++; throw new Error('sensitive prompt/token'); }));
  assert.equal(response.status, 502); assert.equal(calls, 1); assert.ok(!(await response.text()).includes('sensitive'));
  const limited = await worker.fetch(request(), environment(async () => { throw { status: 429 }; }));
  assert.equal(limited.status, 429); assert.equal(limited.headers.get('retry-after'), '60');
});
test('astral Unicode characters count as code points', async () => {
  assert.equal((await worker.fetch(request({ guild_id: '12', text: '😀'.repeat(200), language: 'ja' }), environment())).status, 200);
});
test('binding timeout returns 504 without a second inference', async t => {
  t.mock.timers.enable({ apis: ['setTimeout'] });
  let signal: () => void = () => {};
  const started = new Promise<void>(resolve => { signal = resolve; });
  let calls = 0;
  const pending = worker.fetch(request(), environment(async () => { calls++; signal(); return new Promise(() => {}); }));
  await started;
  t.mock.timers.tick(25_000);
  const response = await pending;
  assert.equal(response.status, 504);
  assert.equal(calls, 1);
});

test('upstream binary response rate limit preserves valid Retry-After', async () => {
  const response = await worker.fetch(request(), environment(async () => new Response(null, { status: 429, headers: { 'Retry-After': '120' } })));
  assert.equal(response.status, 429);
  assert.equal(response.headers.get('retry-after'), '120');
});
test('binary audio starts before the upstream stream ends and cancellation propagates', async () => {
  let source: ReadableStreamDefaultController<Uint8Array>;
  let cancelled = false;
  const upstream = new ReadableStream<Uint8Array>({ start(c) { source = c; c.enqueue(audio); }, cancel() { cancelled = true; } });
  const response = await worker.fetch(request(), environment(async () => upstream));
  assert.equal(response.status, 200);
  const reader = response.body!.getReader();
  assert.deepEqual((await reader.read()).value, audio);
  await reader.cancel();
  assert.equal(cancelled, true);
});
test('an oversized chunk after the first audio aborts the response stream', async () => {
  let source: ReadableStreamDefaultController<Uint8Array>;
  const upstream = new ReadableStream<Uint8Array>({ start(c) { source = c; c.enqueue(audio); } });
  const response = await worker.fetch(request(), environment(async () => upstream));
  const reader = response.body!.getReader();
  await reader.read();
  source!.enqueue(new Uint8Array(5 * 1024 * 1024));
  await assert.rejects(reader.read());
});
test('timeout before the first audio closes the upstream stream', async t => {
  t.mock.timers.enable({ apis: ['setTimeout'] });
  let cancelled = false;
  let signal: () => void = () => {};
  const began = new Promise<void>(resolve => { signal = resolve; });
  const pending = worker.fetch(request(), environment(async () => { signal(); return new ReadableStream({ cancel() { cancelled = true; } }); }));
  await began;
  // Let the binding return and register the stream cancellation callback.
  await Promise.resolve(); await Promise.resolve();
  t.mock.timers.tick(25_000);
  assert.equal((await pending).status, 504);
  assert.equal(cancelled, true);
});

test('LLM mode changes only mixed posts, using one conversion and one TTS call', async () => {
  const calls: string[] = [];
  const env = environment(async (model, input) => {
    calls.push(model);
    if (model.includes('qwen')) return {response: '["ワーカー"]'};
    assert.deepEqual(input, {prompt: 'ワーカー を使います', lang: 'jp'});
    return audio;
  });
  env.KANA_MODE = 'llm';
  const response = await worker.fetch(request({guild_id: '12', text: 'Worker を使います', language: 'ja'}), env);
  assert.equal(response.status, 200);
  assert.equal(response.headers.get('x-kana-source'), 'llm');
  assert.deepEqual(calls, ['@cf/qwen/qwen3-30b-a3b-fp8', '@cf/myshell-ai/melotts']);
  await response.body?.cancel();
  for (const text of ['こんにちは', 'Hello world']) {
    let calls = 0;
    const env = environment(async (model) => { calls++; assert.equal(model, text === 'Hello world' ? '@cf/deepgram/aura-2-en' : '@cf/myshell-ai/melotts'); return audio; });
    env.KANA_MODE = 'llm';
    const response = await worker.fetch(request({guild_id: '12', text, language: 'ja'}), env);
    assert.equal(response.status, 200);
    assert.equal(calls, 1);
    await response.body?.cancel();
  }
});
test('LLM failure still sends the existing kana reading to TTS once', async () => {
  let calls = 0;
  const env = environment(async (model, input) => {
    calls++;
    if (model.includes('qwen')) throw new Error('failed');
    assert.deepEqual(input, {prompt: '読み上げの ティーイーエスティー を行います', lang: 'jp'});
    return audio;
  });
  env.KANA_MODE = 'llm';
  const response = await worker.fetch(request({guild_id: '12', text: '読み上げの test を行います', language: 'ja'}), env);
  assert.equal(response.status, 200);
  assert.equal(response.headers.get('x-kana-source'), 'fallback');
  assert.equal(calls, 2);
  await response.body?.cancel();
});

test('Chinese and Chinese-English text go unchanged to zh, without kana conversion', async () => {
  for (const text of ['你好，这是中文语音测试。', '歡迎使用 Discord。']) {
    const calls: string[] = [];
    const env = environment(async (model, input) => {
      calls.push(model);
      if (model.includes('clef')) return {answers: {language: {choice:'zh',confidence:0.99}}};
      assert.deepEqual(input, {prompt:text, lang:'zh'});
      return audio;
    });
    env.KANA_MODE='llm';
    const response=await coreWorker.fetch(request({guild_id:'12',text,language:'ja'}), env, () => {assert.fail('no kana for Chinese');});
    assert.equal(response.status,200);
    assert.equal(response.headers.get('x-speech-language'),'zh');
    assert.equal(response.headers.get('x-kana-source'),null);
    assert.equal(calls.length,text.includes('这') ? 1 : 2);
    await response.body?.cancel();
  }
});
test('ambiguous Japanese Han text retains Japanese after classification', async () => {
  let calls=0;
  const env=environment(async (model,input) => {
    calls++;
    if(model.includes('clef'))return {answers: {language: {choice:'ja',confidence:0.95}}};
    assert.deepEqual(input,{prompt:'東京',lang:'jp'});
    return audio;
  });
  const response=await worker.fetch(request({guild_id:'12',text:'東京',language:'ja'}),env);
  assert.equal(response.status,200);
  assert.equal(response.headers.get('x-speech-language'),'ja');
  assert.equal(calls,2);
  await response.body?.cancel();
});

test('language detection consumes the shared 25-second synthesis budget', async t => {
  t.mock.timers.enable({apis:['Date','setTimeout'],now:0});
  let beginDetection!: () => void;
  let beginSpeech!: () => void;
  const detecting=new Promise<void>(done => {beginDetection=done;});
  const speaking=new Promise<void>(done => {beginSpeech=done;});
  const env=environment(async model => {
    if(model.includes('clef')) {
      beginDetection();
      return new Promise(done => setTimeout(() => done({answers:{language:{choice:'zh',confidence:0.99}}}),2000));
    }
    beginSpeech();
    return new Promise(() => {});
  });
  let settled=false;
  const response=worker.fetch(request({guild_id:'12',text:'中文共享時限。',language:'ja'}),env);
  void response.then(() => {settled=true;});
  await detecting;
  t.mock.timers.tick(2000);
  await speaking;
  t.mock.timers.tick(22999);
  await Promise.resolve();
  assert.equal(settled,false);
  t.mock.timers.tick(1);
  assert.equal((await response).status,504);
});


test('simplified markers override kana and skip both language and kana inference', async () => {
  const text = '这 API を試します。';
  let calls=0;
  const env=environment(async (model,input) => {
    calls++;
    assert.equal(model,'@cf/myshell-ai/melotts');
    assert.deepEqual(input,{prompt:text,lang:'zh'});
    return audio;
  });
  env.KANA_MODE='llm';
  const response=await worker.fetch(request({guild_id:'12',text,language:'ja'}),env);
  assert.equal(response.status,200);
  assert.equal(response.headers.get('x-speech-language'),'zh');
  assert.equal(response.headers.get('x-kana-source'),null);
  assert.equal(calls,1);
  await response.body?.cancel();
});
