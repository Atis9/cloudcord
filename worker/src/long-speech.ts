const SENTENCE_END = /[。！？!?]/u;
const OPEN_QUOTE = /[「『“]/u;
const CLOSE_QUOTE = /[」』”]/u;
const ABBREVIATION = /\b(?:[a-z]|mr|mrs|ms|dr|prof|st|vs|etc|e\.g|i\.e)$/iu;

// Split at most once, only where both sides contain substantial text. Skip
// quoted sentences and keep text with no suitable boundary in one request.
export function splitLongSpeech(text: string): [string, string] | undefined {
  const chars = [...text];
  if (chars.length < 120) return;
  const target = Math.min(72, Math.floor(chars.length / 2));
  const limit = Math.min(100, chars.length - 40);
  let depth = 0;
  let doubleQuote = false;
  let best = 0;
  for (let i = 0; i < limit; i++) {
    const char = chars[i];
    if (OPEN_QUOTE.test(char)) depth++;
    if (CLOSE_QUOTE.test(char) && depth > 0) depth--;
    if (char === '"') doubleQuote = !doubleQuote;
    if (depth !== 0 || doubleQuote || i + 1 < 40) continue;
    const next = chars[i + 1];
    const period = char === '.' && /\s/u.test(next) && !ABBREVIATION.test(chars.slice(Math.max(0, i - 6), i).join(''));
    if ((!SENTENCE_END.test(char) && !period) || SENTENCE_END.test(next)) continue;
    if (best === 0 || Math.abs(i + 1 - target) < Math.abs(best - target)) best = i + 1;
  }
  return best > 0 ? [chars.slice(0, best).join('').trimEnd(), chars.slice(best).join('').trimStart()] : undefined;
}

export type PCM = { channels: number; sampleRate: number; samples: Uint8Array };
const tag = new TextDecoder();

export function readPCM(wav: Uint8Array): PCM {
  const view = new DataView(wav.buffer, wav.byteOffset, wav.byteLength);
  if (wav.length < 12 || tag.decode(wav.subarray(0, 4)) !== 'RIFF' || tag.decode(wav.subarray(8, 12)) !== 'WAVE') {
    throw new Error('expected PCM WAV');
  }
  let channels = 0;
  let sampleRate = 0;
  for (let offset = 12; offset + 8 <= wav.length;) {
    const name = tag.decode(wav.subarray(offset, offset + 4));
    const size = view.getUint32(offset + 4, true);
    const start = offset + 8;
    const end = start + size;
    if (end > wav.length) break;
    if (name === 'fmt ' && size >= 16) {
      channels = view.getUint16(start + 2, true);
      sampleRate = view.getUint32(start + 4, true);
      if (view.getUint16(start, true) !== 1 || (channels !== 1 && channels !== 2) ||
          sampleRate < 8000 || sampleRate > 96000 || view.getUint16(start + 12, true) !== channels * 2 ||
          view.getUint16(start + 14, true) !== 16) break;
    }
    if (name === 'data' && channels > 0 && size > 0 && size % (channels * 2) === 0) {
      return { channels, sampleRate, samples: wav.subarray(start, end) };
    }
    offset = end + (size % 2);
  }
  throw new Error('expected PCM WAV');
}

export function streamingWAVHeader({ channels, sampleRate }: Pick<PCM, 'channels' | 'sampleRate'>): Uint8Array {
  // RIFF and data lengths are unknown until the continuation arrives. FFmpeg
  // can read these placeholders from a pipe and stop at the real EOF.
  const header = new Uint8Array(44);
  const encoder = new TextEncoder();
  header.set(encoder.encode('RIFF'), 0);
  header.set(encoder.encode('WAVEfmt '), 8);
  header.set(encoder.encode('data'), 36);
  const view = new DataView(header.buffer);
  view.setUint32(4, 0xffffffff, true);
  view.setUint32(16, 16, true);
  view.setUint16(20, 1, true);
  view.setUint16(22, channels, true);
  view.setUint32(24, sampleRate, true);
  view.setUint32(28, sampleRate * channels * 2, true);
  view.setUint16(32, channels * 2, true);
  view.setUint16(34, 16, true);
  view.setUint32(40, 0xffffffff, true);
  return header;
}
