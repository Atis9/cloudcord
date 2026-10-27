export type WordConverter = (word: string) => string;
const letters = ['エー', 'ビー', 'シー', 'ディー', 'イー', 'エフ', 'ジー', 'エイチ', 'アイ', 'ジェー', 'ケー', 'エル', 'エム', 'エヌ', 'オー', 'ピー', 'キュー', 'アール', 'エス', 'ティー', 'ユー', 'ブイ', 'ダブリュー', 'エックス', 'ワイ', 'ゼット'];
export const spellWord: WordConverter = word => [...word.toLowerCase()].map(letter => letters[letter.charCodeAt(0) - 97] || '').join('');


export function japaneseText(text: string, convertWord: WordConverter): string {
  const converted = text.replace(/[A-Za-z]+/g, word => {
    try { return convertWord(word) || spellWord(word); }
    catch { return spellWord(word); }
  });
  return [...converted].slice(0, 200).join('');
}
