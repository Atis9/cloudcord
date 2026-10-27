import test from 'node:test';
import assert from 'node:assert/strict';
import { createLanguageDetector } from '../src/language.ts';

test('kana, English and non-Han input do not invoke the classifier', async () => {
  const detect = createLanguageDetector();
  const ai = {run: async () => {assert.fail('no classification needed');}};
  for (const [text, lang] of [['今日は test です。','jp'], ['カタカナ','jp'], ['ﾃｽﾄ','jp'], ['Hello world','en'], ['12345','jp'], ['😀','jp']]) {
    assert.equal(await detect(text, ai), lang);
  }
});
test('Han-only, simplified, traditional and Chinese-English posts use a cached classification', async () => {
  const detect = createLanguageDetector();
  for (const [text, classification, lang] of [['你好，世界。','zh','zh'], ['這是中文。','zh','zh'], ['今天使用 GitHub Actions。','zh','zh'], ['東京','ja','jp'], ['了解','ja','jp']]) {
    let calls = 0;
    const ai = {run: async (_: string, input: any) => {
      calls++;
      assert.equal(input.model, 'clef');
      assert.equal(input.state, text);
      return {answers: {language: {choice: classification, confidence: 0.95}}};
    }};
    assert.equal(await detect(text, ai), lang);
    assert.equal(await detect(text, ai), lang);
    assert.equal(calls, 1);
  }
});
test('invalid or failed classification defaults to Japanese and is not cached', async () => {
  for (const output of [null, {answers: {language: {choice:'en', confidence: 0.99}}}, ...[undefined, 0.6, NaN, 2, -1].map(confidence => ({answers: {language: {choice:'zh', confidence}}}))]) {
    const detect = createLanguageDetector();
    assert.equal(await detect('漢字', {run: async () => output}), 'jp');
    assert.equal(await detect('漢字', {run: async () => ({answers: {language: {choice:'zh',confidence:0.99}}})}), 'zh');
  }
  assert.equal(await createLanguageDetector()('中文', {run: async () => {throw new Error('unavailable');}}), 'jp');
});
test('slow language detection falls back without caching a late answer', async t => {
  t.mock.timers.enable({apis: ['setTimeout']});
  const detect = createLanguageDetector();
  let resolve!: (value: unknown) => void;
  const pending = detect('中文', {run: () => new Promise(done => {resolve=done;})});
  t.mock.timers.tick(3000);
  assert.equal(await pending, 'jp');
  resolve({answers: {language: {choice:'zh',confidence:0.99}}});
  await Promise.resolve();
  let calls=0;
  assert.equal(await detect('中文', {run: async () => {calls++; return {answers: {language: {choice:'zh',confidence:0.99}}};}}), 'zh');
  assert.equal(calls, 1);
});
