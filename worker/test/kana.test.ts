import test from 'node:test';
import assert from 'node:assert/strict';
import { japaneseText, spellWord } from '../src/kana.ts';

test('fallback spells letters without dropping the word or Japanese text', () => {
  assert.equal(japaneseText('ABCを読む', spellWord), 'エービーシーを読む');
  assert.equal(japaneseText('testを読む', () => ''), 'ティーイーエスティーを読む');
  assert.equal(japaneseText('ABCを読む', () => {throw new Error('unavailable');}), 'エービーシーを読む');
});
test('kana expansion keeps the 200-code-point limit', () => {
  const text = japaneseText('a'.repeat(199) + 'あ', () => 'ア'.repeat(400));
  assert.equal([...text].length, 200);
  assert.ok(!/[A-Za-z]/.test(text));
});
