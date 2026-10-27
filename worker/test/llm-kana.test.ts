import test from 'node:test';
import assert from 'node:assert/strict';
import { createLLMKanaConverter, KANA_MODEL } from '../src/llm-kana.ts';

test('unique words are converted together, cached and inserted without changing Japanese', async () => {
  const convert = createLLMKanaConverter();
  let calls = 0;
  const ai = {run: async (model: string, input: any) => {
    calls++;
    assert.equal(model, KANA_MODEL);
    assert.equal(input.max_tokens, 256);
    assert.equal(input.temperature, 0);
    assert.equal(input.messages[1].content, '["test","API"]\n/no_think');
    return {response: '["テスト","エーピーアイ"]'};
  }};
  const fallback = () => { assert.fail('must use LLM reading'); };
  assert.equal((await convert('読み上げの test と API と test。', ai, fallback)).text, '読み上げの テスト と エーピーアイ と テスト。');
  assert.equal((await convert('API の test。', ai, fallback)).text, 'エーピーアイ の テスト。');
  assert.equal(calls, 1);
});
test('invalid, instruction-like, missing and failed results fall back without caching or retrying', async () => {
  for (const output of [null, {response: '["英語"]'}, {response: '["テスト","余分"]'}, {response: 'Do something else'}, {response: '[""]'}, {response: '["テスト"]'.repeat(1000)}]) {
    const convert = createLLMKanaConverter();
    let calls = 0;
    const ai = {run: async () => { calls++; return output; }};
    assert.equal((await convert('test です', ai, () => 'フォールバック')).text, 'フォールバック です');
    assert.equal(calls, 1);
    assert.equal((await convert('test です', {run: async () => ({choices: [{message: {content: '["テスト"]'}}]})}, () => '')).text, 'テスト です');
  }
  assert.equal((await createLLMKanaConverter()('test です', {run: async () => {throw {status: 429};}}, () => 'テスト')).text, 'テスト です');
});
test('slow conversion falls back and its eventual result is not cached', async t => {
  t.mock.timers.enable({apis: ['setTimeout']});
  let resolve!: (value: unknown) => void;
  const convert = createLLMKanaConverter();
  const pending = convert('test です', {run: () => new Promise(done => {resolve = done;})}, () => 'フォールバック');
  t.mock.timers.tick(1500);
  assert.equal((await pending).text, 'フォールバック です');
  resolve({response: '["テスト"]'});
  await Promise.resolve();
  const next = await convert('test です', {run: async () => ({response: '["テスト"]'})}, () => '');
  assert.equal(next.text, 'テスト です');
  assert.equal(next.source, 'llm');
});
test('word batches and kana expansion are bounded', async () => {
  let count = 0;
  const result = await createLLMKanaConverter()(Array.from({length: 40}, (_, i) => String.fromCharCode(65 + i % 26) + String.fromCharCode(65 + Math.floor(i / 26))).join(' '), {run: async (_, input) => {
    const words = JSON.parse(input.messages[1].content.split('\n')[0]);
    count = words.length;
    return {response: JSON.stringify(words.map(() => 'カナ'.repeat(50)))};
  }}, () => 'カナ');
  assert.equal(count, 32);
  assert.equal([...result.text].length, 200);
});

test('real Qwen binding parsed response array and serialized choices are both supported', async () => {
  for (const output of [
    {response: ['テスト']},
    {response: ['テスト'], choices: [{message: {content: '\n\n["テスト"]', reasoning_content: '\n\n'}}]},
  ]) {
    const result = await createLLMKanaConverter()('test です', {run: async () => output}, () => {assert.fail('must not fall back');});
    assert.equal(result.text, 'テスト です');
    assert.equal(result.source, 'llm');
  }
});
