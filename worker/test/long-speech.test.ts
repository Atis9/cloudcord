import assert from 'node:assert/strict';
import test from 'node:test';
import worker from '../src/index.ts';
import type { Env } from '../src/index.ts';
import { splitLongSpeech, streamingWAVHeader } from '../src/long-speech.ts';

const opening = '音声の確認を始めます。明日の朝は駅前の喫茶店に集合し、資料を確認してから展示会の会場へ向かう予定です。';
const continuation = '会場に着いたら、まず受付で参加証を受け取り、そのあと展示の内容を順番に確認してください。変更があれば、分かった時点でグループに連絡してください。帰りの時間は、全員の予定を確認してから決めましょう。';

function request(text: string): Request {
  return new Request('https://example.com/v1/tts', {
    method: 'POST', headers: { authorization: 'Bearer secret', 'content-type': 'application/json' },
    body: JSON.stringify({ guild_id: '1', text, language: 'ja', voice: 'Kore' }),
  });
}

function wav(samples: number[], sampleRate = 24000): Uint8Array {
  const pcm = Uint8Array.from(samples);
  const header = streamingWAVHeader({ channels: 1, sampleRate });
  const view = new DataView(header.buffer);
  view.setUint32(4, 36 + pcm.length, true);
  view.setUint32(40, pcm.length, true);
  return Uint8Array.from([...header, ...pcm]);
}

function env(run: Env['AI']['run']): Env {
  return { AI: { run }, AI_GATEWAY_ID: 'default', BOT_API_TOKEN: 'secret', TTS_VOICE: 'Sulafat' };
}

test('only long text with a natural boundary is split', () => {
  assert.deepEqual(splitLongSpeech(opening + continuation), [opening, continuation]);
  assert.equal(splitLongSpeech('短い投稿。もう一文。'), undefined);
  assert.equal(splitLongSpeech('あ'.repeat(160)), undefined);
  const quote = '「' + 'あ'.repeat(55) + '。' + 'い'.repeat(63) + '」と答えました。';
  assert.equal(splitLongSpeech(quote + continuation), undefined);
  const english = 'The first recording is scheduled for tomorrow morning. ' + 'Please wait in the main room and check your microphone before the rest of the team joins the call.';
  assert.equal(splitLongSpeech(english)?.[0], 'The first recording is scheduled for tomorrow morning.');
});

test('both Gemini requests start immediately and the first PCM is playable before the second finishes', async () => {
  let finish!: (audio: Uint8Array) => void;
  const pending = new Promise<Uint8Array>(resolve => { finish = resolve; });
  const inputTexts: string[] = [];
  const response = await worker.fetch(request(opening + continuation), env(async (model, input) => {
    assert.equal(model, 'google/gemini-3.8-flash-lite-tts');
    assert.ok('voice' in input);
    assert.equal(input.voice, 'Kore');
    inputTexts.push(input.text);
    return input.text === opening ? wav([1, 2, 3, 4]) : pending;
  }));
  assert.deepEqual(inputTexts, [opening, continuation]);
  assert.equal(response.status, 200);
  assert.equal(response.headers.get('x-speech-chunks'), '2');
  const reader = response.body!.getReader();
  const header = (await reader.read()).value!;
  assert.equal(new TextDecoder().decode(header.subarray(0, 4)), 'RIFF');
  assert.equal(new DataView(header.buffer).getUint32(40, true), 0xffffffff);
  assert.deepEqual((await reader.read()).value, Uint8Array.of(1, 2, 3, 4));
  finish(wav([5, 6]));
  assert.deepEqual((await reader.read()).value, Uint8Array.of(5, 6));
  assert.equal((await reader.read()).done, true);
});

test('mismatched continuations fail the audio stream', async () => {
  const response = await worker.fetch(request(opening + continuation), env(async (_model, input) =>
    input.text === opening ? wav([1, 2]) : wav([3, 4], 48000)));
  assert.equal(response.status, 200);
  await assert.rejects(response.arrayBuffer(), /TTS continuation failed/);
});

test('cancelling playback cancels a continuation download', async () => {
  let stopped!: () => void;
  const cancelled = new Promise<void>(resolve => { stopped = resolve; });
  const incomplete = new ReadableStream<Uint8Array>({
    start(controller) { controller.enqueue(wav([3, 4])); },
    cancel() { stopped(); },
  });
  const response = await worker.fetch(request(opening + continuation), env(async (_model, input) =>
    input.text === opening ? wav([1, 2]) : incomplete));
  assert.equal(response.status, 200);
  await response.body!.cancel();
  await cancelled;
});
