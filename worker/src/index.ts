import { japaneseText } from './kana.ts';
import type { WordConverter } from './kana.ts';
import { createLLMKanaConverter } from './llm-kana.ts';
import type { KanaInput } from './llm-kana.ts';
import { createLanguageDetector } from './language.ts';
import type { LanguageInput } from './language.ts';

export interface Env {
  AI: { run(model: string, input: { prompt: string; lang: string } | KanaInput | LanguageInput): Promise<unknown> };
  BOT_API_TOKEN: string;
  MELOTTS_LANG: string;
  KANA_MODE?: string;
}
const llmKana = createLLMKanaConverter();
const detectLanguage = createLanguageDetector();
const MAX_AUDIO = 5 * 1024 * 1024;
const MODEL = '@cf/myshell-ai/melotts';
class APIError extends Error {
  status: number;
  code: string;
  retryAfter: string;
  constructor(status: number, code: string, message: string, retryAfter = "60") {
    super(message); this.status = status; this.code = code; this.retryAfter = retryAfter;
  }
}
async function readBounded(stream: ReadableStream<Uint8Array>, limit: number, signal?: AbortSignal): Promise<Uint8Array> {
  const reader = stream.getReader();
  const abort = () => { void reader.cancel().catch(() => {}); };
  signal?.addEventListener("abort", abort, { once: true });
  const chunks: Uint8Array[] = [];
  let length = 0;
  try {
    while (true) {
      const { done, value } = await reader.read();
      if (signal?.aborted) throw new APIError(504, "tts_timeout", "TTS timed out");
      if (done) break;
      length += value.byteLength;
      if (length > limit) throw new Error('size limit');
      chunks.push(value);
    }
  } catch (error) {
    await reader.cancel().catch(() => {});
    throw error;
  } finally { signal?.removeEventListener("abort", abort); reader.releaseLock(); }
  const data = new Uint8Array(length);
  let offset = 0;
  for (const chunk of chunks) { data.set(chunk, offset); offset += chunk.length; }
  return data;
}
// Decode binary / JSON variants; the real MeloTTS binding can return WAV.
export async function decodeAudio(output: unknown, signal?: AbortSignal): Promise<Uint8Array> {
  let data: Uint8Array;
  if (output instanceof Response) {
    if (output.status === 429) {
      const header = output.headers.get('retry-after');
      let retryAfter = '60';
      if (header && /^\d{1,5}$/.test(header) && Number(header) <= 86400) retryAfter = header;
      else if (header && Number.isFinite(Date.parse(header)) && Date.parse(header) > Date.now()) retryAfter = new Date(header).toUTCString();
      throw new APIError(429, 'tts_rate_limited', 'TTS temporarily rate limited', retryAfter);
    }
    if (!output.ok || !output.body) throw new Error('upstream response failed');
    if (output.headers.get('content-type')?.includes('application/json')) {
      const raw = await readBounded(output.body, Math.ceil(MAX_AUDIO * 4 / 3) + 4096, signal);
      return decodeAudio(JSON.parse(new TextDecoder().decode(raw)), signal);
    }
    data = await readBounded(output.body, MAX_AUDIO, signal);
  } else if (output instanceof ReadableStream) {
    data = await readBounded(output, MAX_AUDIO, signal);
  } else if (output instanceof ArrayBuffer) {
    data = new Uint8Array(output);
  } else if (output instanceof Uint8Array) {
    data = output;
  } else if (typeof output === 'object' && output !== null && 'audio' in output && typeof output.audio === 'string') {
    if (output.audio.length > Math.ceil(MAX_AUDIO * 4 / 3) + 4) throw new Error('size limit');
    const decoded = atob(output.audio);
    data = Uint8Array.from(decoded, c => c.charCodeAt(0));
  } else { throw new Error('unsupported AI output'); }
  if (data.length === 0 || data.length > MAX_AUDIO) throw new Error('invalid audio size');
  audioMIME(data);
  return data;
}
type AudioStream = { stream: ReadableStream<Uint8Array>; mime: 'audio/mpeg' | 'audio/wav'; kanaSource?: string; language?: string };
function isMP3(data: Uint8Array): boolean {
  return (data[0] === 0x49 && data[1] === 0x44 && data[2] === 0x33) ||
    (data[0] === 0xff && (data[1] & 0xe0) === 0xe0);
}
function audioMIME(data: Uint8Array): AudioStream['mime'] {
  if (isMP3(data)) return 'audio/mpeg';
  if (data.length >= 12 && data[0] === 0x52 && data[1] === 0x49 && data[2] === 0x46 && data[3] === 0x46 &&
      data[8] === 0x57 && data[9] === 0x41 && data[10] === 0x56 && data[11] === 0x45) return 'audio/wav';
  throw new Error('invalid audio signature');
}
// Stream binary binding output after validating the audio signature. JSON
// base64 output still requires decoding the complete upstream JSON document.
export async function audioStream(output: unknown, signal?: AbortSignal): Promise<AudioStream> {
  if (signal?.aborted) {
    const body = output instanceof Response ? output.body : output instanceof ReadableStream ? output : null;
    await body?.cancel().catch(() => {});
    throw new APIError(504, 'tts_timeout', 'TTS timed out');
  }
  if (output instanceof Response && (!output.ok || output.headers.get('content-type')?.includes('application/json'))) {
    const data = await decodeAudio(output, signal);
    return { stream: new ReadableStream({ start(c) { c.enqueue(data); c.close(); } }), mime: audioMIME(data) };
  }
  const source = output instanceof Response ? output.body : output instanceof ReadableStream ? output : null;
  if (!source) {
    const data = await decodeAudio(output, signal);
    return { stream: new ReadableStream({ start(c) { c.enqueue(data); c.close(); } }), mime: audioMIME(data) };
  }
  const reader = source.getReader();
  const abort = () => { void reader.cancel().catch(() => {}); };
  signal?.addEventListener("abort", abort, { once: true });
  const release = () => { signal?.removeEventListener("abort", abort); reader.releaseLock(); };
  const deadline = Date.now() + 25_000;
  let length = 0;
  let finished = false;
  async function next(): Promise<ReadableStreamReadResult<Uint8Array>> {
    let timer: ReturnType<typeof setTimeout> | undefined;
    try {
      const part = await Promise.race([
        reader.read(),
        new Promise<never>((_, reject) => {
          timer = setTimeout(() => {
            reject(new APIError(504, 'tts_timeout', 'TTS timed out'));
            void reader.cancel().catch(() => {});
          }, Math.max(0, deadline - Date.now()));
        }),
      ]);
      if (signal?.aborted) throw new APIError(504, 'tts_timeout', 'TTS timed out');
      if (!part.done) {
        length += part.value.byteLength;
        if (length > MAX_AUDIO) throw new Error('size limit');
      }
      return part;
    } finally { if (timer !== undefined) clearTimeout(timer); }
  }
  const initial: Uint8Array[] = [];
  const signature = new Uint8Array(12);
  let signatureLength = 0;
  let mime: AudioStream['mime'];
  try {
    while (signatureLength < 3 || (!isMP3(signature) && signatureLength < 12)) {
      const part = await next();
      if (part.done) throw new Error('empty or truncated audio');
      initial.push(part.value);
      const count = Math.min(12 - signatureLength, part.value.length);
      signature.set(part.value.subarray(0, count), signatureLength);
      signatureLength += count;
      if (signatureLength >= 3 && !isMP3(signature) &&
          !(signature[0] === 0x52 && signature[1] === 0x49 && signature[2] === 0x46)) throw new Error('invalid audio signature');
    }
    mime = audioMIME(signature.subarray(0, signatureLength));
  } catch (error) { await reader.cancel().catch(() => {}); release(); throw error; }
  const stream = new ReadableStream<Uint8Array>({
    start(c) { for (const chunk of initial) c.enqueue(chunk); },
    async pull(c) {
      if (finished) return;
      try {
        const part = await next();
        if (part.done) { finished = true; release(); c.close(); }
        else c.enqueue(part.value);
      } catch {
        finished = true;
        await reader.cancel().catch(() => {}); release();
        c.error(new Error('TTS audio stream failed'));
      }
    },
    async cancel() { if (!finished) { finished = true; await reader.cancel().catch(() => {}); release(); } },
  });
  return { stream, mime };
}
async function authorized(header: string | null, token: string): Promise<boolean> {
  if (!token || !header) return false;
  const encoder = new TextEncoder();
  const a = new Uint8Array(await crypto.subtle.digest('SHA-256', encoder.encode(header)));
  const b = new Uint8Array(await crypto.subtle.digest('SHA-256', encoder.encode(`Bearer ${token}`)));
  let difference = 0;
  for (let i = 0; i < a.length; i++) difference |= a[i] ^ b[i];
  return difference === 0;
}
async function synthesize(env: Env, text: string, convertWord: WordConverter): Promise<AudioStream> {
  let timer: ReturnType<typeof setTimeout> | undefined;
  const controller = new AbortController();
  const deadline = Date.now() + 25_000;
  try {
    // A binding call has no supported AbortSignal. The response is bounded;
    // an already submitted inference may still finish after this deadline.
    const lang = await detectLanguage(text, env.AI, env.MELOTTS_LANG || 'jp');
    const isJapanese = lang !== 'en' && lang !== 'zh';
    const kana = isJapanese && env.KANA_MODE === 'llm' && /[A-Za-z]/.test(text)
      ? await llmKana(text, env.AI, convertWord) : undefined;
    const prompt = isJapanese ? kana?.text ?? japaneseText(text, convertWord) : text;
    return await Promise.race([
      env.AI.run(MODEL, { prompt, lang }).then(async output => ({...await audioStream(output, controller.signal), kanaSource: kana?.source, language: lang === 'jp' ? 'ja' : lang})),
      new Promise<never>((_, reject) => {
        timer = setTimeout(() => { controller.abort(); reject(new APIError(504, 'tts_timeout', 'TTS timed out')); }, Math.max(0, deadline - Date.now()));
      }),
    ]);
  } catch (error) {
    if (error instanceof APIError) throw error;
    if (typeof error === 'object' && error !== null && 'status' in error && error.status === 429) {
      throw new APIError(429, 'tts_rate_limited', 'TTS temporarily rate limited');
    }
    throw new APIError(502, 'tts_failed', 'TTS generation failed');
  } finally { if (timer !== undefined) clearTimeout(timer); }
}
export default {
  async fetch(request: Request, env: Env, convertWord: WordConverter): Promise<Response> {
    const requestID = crypto.randomUUID();
    const started = Date.now();
    const headers = { 'X-Request-ID': requestID, 'Cache-Control': 'no-store' };
    try {
      if (!await authorized(request.headers.get('authorization'), env.BOT_API_TOKEN)) {
        throw new APIError(401, 'unauthorized', 'Authentication required');
      }
      if (new URL(request.url).pathname !== '/v1/tts') throw new APIError(404, 'not_found', 'Endpoint not found');
      if (request.method !== 'POST') throw new APIError(405, 'method_not_allowed', 'Use POST');
      if (request.headers.get('content-type')?.split(';')[0].trim().toLowerCase() !== 'application/json') {
        throw new APIError(400, 'invalid_input', 'JSON body required');
      }
      let value: unknown;
      try {
        if (!request.body) throw new Error('empty');
        value = JSON.parse(new TextDecoder('utf-8', { fatal: true }).decode(await readBounded(request.body, 4096)));
      } catch { throw new APIError(400, 'invalid_input', 'Invalid or oversized JSON body'); }
      if (typeof value !== 'object' || value === null || !('guild_id' in value) || !('text' in value) || !('language' in value) ||
          typeof value.guild_id !== 'string' || !/^[1-9][0-9]{0,19}$/.test(value.guild_id) || BigInt(value.guild_id) > 18446744073709551615n ||
          typeof value.text !== 'string' || value.text.trim().length === 0 || [...value.text].length > 200 || value.language !== 'ja') {
        throw new APIError(400, 'invalid_input', 'Require a snowflake string, 1–200 characters, and language ja');
      }
      const data = await synthesize(env, value.text, convertWord);
      console.log(JSON.stringify({ event: 'tts_stream_ready', request_id: requestID, duration_ms: Date.now() - started, kana_source: data.kanaSource, language: data.language }));
      return new Response(data.stream, { headers: { ...headers, 'Content-Type': data.mime, 'X-Speech-Language': data.language!, ...(data.kanaSource ? {'X-Kana-Source': data.kanaSource} : {}) } });
    } catch (error) {
      const failure = error instanceof APIError ? error : new APIError(502, 'tts_failed', 'TTS generation failed');
      console.log(JSON.stringify({ event: 'request_failed', request_id: requestID, code: failure.code, status: failure.status, duration_ms: Date.now() - started }));
      return Response.json({ error: { code: failure.code, message: failure.message }, request_id: requestID }, {
        status: failure.status,
        headers: { ...headers, ...(failure.status === 429 ? { 'Retry-After': failure.retryAfter } : {}), ...(failure.status === 405 ? { Allow: 'POST' } : {}) },
      });
    }
  },
};
