import test from 'node:test';
import assert from 'node:assert/strict';
import worker, { decodeAudio, speechText } from '../src/index.ts';
import type { Env } from '../src/index.ts';
const audio = new Uint8Array([0x49, 0x44, 0x33, 1, 2, 3]);
function request(body: unknown = { guild_id: '123456789012345678', text: 'こんにちは', language: 'ja' }, token = 'secret'): Request {
  return new Request('https://example.com/v1/tts', { method: 'POST', headers: { authorization: `Bearer ${token}`, 'content-type': 'application/json' }, body: JSON.stringify(body) });
}
function environment(run: Env['AI']['run'] = async () => audio): Env { return { BOT_API_TOKEN: 'secret', AI_GATEWAY_ID: 'default', TTS_VOICE: 'Hana', AI: { run } }; }
test('all languages and mixed posts use one Inworld synthesis with original English', async () => {
  for (const [text, language] of [
    ['こんにちは', 'ja'], ['Hello, this is a test.', 'en'],
    ['読み上げの test を行います', 'ja'], ['你好，这是中文语音测试。', 'zh'],
    ['这 API を試します。', 'zh'], ['12345', 'ja'],
  ]) {
    let calls = 0;
    const response = await worker.fetch(request({guild_id:'12',text,language:'ja'}), environment(async (model,input,options) => {
      calls++;
      assert.equal(model,'inworld/tts-2');
      assert.deepEqual(input,{text:speechText(text,language),voice_id:'Hana',output_format:'mp3',temperature:1,timestamp_type:'none'});
      assert.deepEqual(options,{gateway:{id:'default',skipCache:true,collectLog:false}});
      return audio;
    }));
    assert.equal(response.status,200);
    assert.equal(response.headers.get('x-speech-language'),language);
    assert.equal(response.headers.get('x-kana-source'),null);
    assert.deepEqual(new Uint8Array(await response.arrayBuffer()),audio);
    assert.equal(calls,1);
  }
});
test('Han hints preserve simplified priority and English while escaping user markup', () => {
  assert.equal(speechText('这是 test です','zh'),'<lang lang="zh-CN">这是</lang> test です');
  assert.equal(speechText('東京 API を使います','ja'),'<lang lang="ja-JP">東京</lang> API を<lang lang="ja-JP">使</lang>います');
  assert.equal(speechText('<lang lang="en">東京</lang> &','ja'),'&lt;lang lang="en"&gt;<lang lang="ja-JP">東京</lang>&lt;/lang&gt; &amp;');
  const alternating = '汉a'.repeat(100);
  assert.equal(speechText(alternating, 'zh'), `<lang lang="zh-CN">${alternating}</lang>`);
  assert.ok(speechText(alternating, 'zh').length <= 2000);
});
const mixedText = 'みんなで「Hello」「你好」「안녕하세요」と挨拶しました。';
for (const [engine, voice, modelName, expected] of [
  ['minimax', 'Japanese_CalmLady', 'minimax/speech-2.8-turbo', { text: mixedText, voice_id: 'Japanese_CalmLady', format: 'mp3', pitch: 0, speed: 1, volume: 1 }],
  ['gemini', 'Kore', 'google/gemini-3.8-flash-tts', { text: mixedText, voice: 'Kore' }],
] as const) {
  test(`${engine} submits four languages as one unmodified request using the configured voice`, async () => {
    let calls = 0;
    const env = environment(async (model, input, options) => {
      calls++;
      assert.equal(model, modelName);
      assert.deepEqual(input, expected);
      assert.deepEqual(options, { gateway: { id: 'default', skipCache: true, collectLog: false } });
      return audio;
    });
    env.TTS_ENGINE = engine;
    env.TTS_VOICE = voice;
    const response = await worker.fetch(request({ guild_id: '12', text: mixedText, language: 'ja' }), env);
    assert.equal(response.status, 200);
    assert.equal(response.headers.get('x-speech-engine'), engine);
    assert.deepEqual(new Uint8Array(await response.arrayBuffer()), audio);
    assert.equal(calls, 1);
  });
}
test('base64 WAV remains supported', async () => {
  const wav=new TextEncoder().encode('RIFF1234WAVEaudio');
  const response=await worker.fetch(request(),environment(async () => ({audio:btoa(String.fromCharCode(...wav))})));
  assert.equal(response.status,200);
  assert.equal(response.headers.get('content-type'),'audio/wav');
  assert.deepEqual(new Uint8Array(await response.arrayBuffer()),wav);
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

test('ambiguous Han still uses Clef, then the same multilingual TTS', async () => {
  const calls:string[]=[];
  const response=await worker.fetch(request({guild_id:'12',text:'東京',language:'ja'}),environment(async(model,input) => {
    calls.push(model);
    if(model.includes('clef'))return {answers:{language:{choice:'ja',confidence:0.95}}};
    assert.equal(input.text,'<lang lang="ja-JP">東京</lang>');
    return audio;
  }));
  assert.equal(response.status,200);
  assert.deepEqual(calls,['@cf/cloudflare/clef','inworld/tts-2']);
  await response.body?.cancel();
});
test('language detection consumes the shared synthesis budget', async t => {
  t.mock.timers.enable({apis:['Date','setTimeout'],now:0});
  let detection!:()=>void, speech!:()=>void;
  const detecting=new Promise<void>(resolve=>{detection=resolve;});
  const speaking=new Promise<void>(resolve=>{speech=resolve;});
  const pending=worker.fetch(request({guild_id:'12',text:'中文共享時限。',language:'ja'}),environment(async model=>{
    if(model.includes('clef')){detection();return new Promise(resolve=>setTimeout(()=>resolve({answers:{language:{choice:'zh',confidence:0.99}}}),2000));}
    speech();return new Promise(()=>{});
  }));
  await detecting;t.mock.timers.tick(2000);await speaking;
  t.mock.timers.tick(23000);assert.equal((await pending).status,504);
});
test('signed R2 audio downloads stream without forwarding credentials', async t => {
  let cancelled=false;
  t.mock.method(globalThis,'fetch',async (url:URL,init:RequestInit)=>{
    assert.equal(url.hostname,'audio.r2.dev');
    assert.equal(init.redirect,'manual');assert.equal(init.credentials,'omit');
    assert.equal(init.headers,undefined);assert.ok(init.signal);
    return new Response(new ReadableStream({start(c){c.enqueue(audio);},cancel(){cancelled=true;}}));
  });
  const response=await worker.fetch(request(),environment(async()=>({state:'Completed',result:{audio:'https://audio.r2.dev/speech.mp3?signature=private'}})));
  assert.equal(response.status,200);
  const reader=response.body!.getReader();assert.deepEqual((await reader.read()).value,audio);
  await reader.cancel();assert.equal(cancelled,true);
});
test('untrusted URLs, failed jobs and redirected downloads are rejected', async t => {
  let downloads=0;
  t.mock.method(globalThis,'fetch',async()=>{downloads++;return new Response(null,{status:302,headers:{location:'http://127.0.0.1/'}});});
  for(const output of [
    {audio:'https://127.0.0.1/audio'}, {audio:'http://audio.r2.dev/audio'},
    {audio:'https://audio.r2.dev.evil.example/audio'}, {audio:'https://user:pass@audio.r2.dev/audio'},
    {state:'Failed',result:{audio:'https://audio.r2.dev/audio'}},
  ]) assert.equal((await worker.fetch(request(),environment(async()=>output))).status,502);
  assert.equal(downloads,0);
  assert.equal((await worker.fetch(request(),environment(async()=>({audio:'https://audio.r2.dev/audio'})))).status,502);
  assert.equal(downloads,1);
});
test('catalog JSON envelopes and download failures are bounded and sanitized', async t => {
  let mode='ok';
  t.mock.method(globalThis,'fetch',async()=>{
    if(mode==='large')return new Response(audio,{headers:{'content-length':String(6*1024*1024)}});
    if(mode==='json')return Response.json({audio:'https://audio.r2.dev/loop'});
    return new Response(audio);
  });
  const env=environment(async()=>Response.json({state:'Completed',result:{audio:'https://audio.r2.dev/signed?secret=private'}}));
  assert.equal((await worker.fetch(request(),env)).status,200);
  for(mode of ['large','json']){
    const response=await worker.fetch(request(),env);
    assert.equal(response.status,502);assert.ok(!(await response.text()).includes('private'));
  }
});
test('audio download timeout aborts fetch without retrying inference', async t => {
  t.mock.timers.enable({apis:['setTimeout']});
  let downloading!:()=>void;
  const started=new Promise<void>(resolve=>{downloading=resolve;});
  let signal:AbortSignal|undefined;
  t.mock.method(globalThis,'fetch',async(_:URL,init:RequestInit)=>{signal=init.signal!;downloading();return new Promise(()=>{});});
  const pending=worker.fetch(request(),environment(async()=>({audio:'https://audio.r2.dev/audio'})));
  await started;t.mock.timers.tick(25000);
  assert.equal((await pending).status,504);assert.equal(signal!.aborted,true);
});
