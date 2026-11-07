import { grokAudio, GrokError } from './grok.ts';
import type { GrokInput, GrokOptions } from './grok.ts';
import { createLanguageDetector } from './language.ts';
import type { LanguageInput } from './language.ts';

export type SpeechInput = {
  text: string; voice_id: string; output_format: 'mp3';
  temperature: number; timestamp_type: 'none';
};
export type GatewayOptions = { gateway: { id: string; skipCache: true; collectLog: false } };
type SpeechEngine = 'inworld' | 'grok' | 'grok-ws';
export interface Env {
  AI: { run(model: string, input: SpeechInput | LanguageInput | GrokInput, options?: GatewayOptions | GrokOptions): Promise<unknown> };
  BOT_API_TOKEN: string;
  AI_GATEWAY_ID: string;
  TTS_VOICE: string;
  TTS_ENGINE?: SpeechEngine;
  GROK_VOICE?: string;
}
const detectLanguage = createLanguageDetector();
const MAX_AUDIO = 5 * 1024 * 1024;
const MODEL = 'inworld/tts-2';
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
// Decode binary and base64 JSON audio variants.
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
type AudioStream = { stream: ReadableStream<Uint8Array>; mime: 'audio/mpeg' | 'audio/wav'; language?: string };
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
  // Catalog models return a signed audio URL, either directly or in result.
  if (typeof output === 'object' && output !== null && !(output instanceof Response)) {
    const envelope = output as { state?: unknown; result?: unknown; audio?: unknown };
    if (envelope.state !== undefined && envelope.state !== 'Completed') throw new Error('incomplete AI output');
    if (envelope.result !== undefined) return audioStream(envelope.result, signal);
    if (typeof envelope.audio === 'string' && /^https?:/i.test(envelope.audio)) {
      const url = new URL(envelope.audio);
      if (url.protocol !== 'https:' || url.username || url.password || url.port || url.hash ||
          !['.r2.dev', '.r2.cloudflarestorage.com'].some(suffix => url.hostname.endsWith(suffix))) {
        throw new Error('untrusted audio URL');
      }
      const response = await fetch(url, { signal, redirect: 'manual', credentials: 'omit' });
      // Do not recurse into JSON/redirect responses from the download origin.
      if (!response.ok || !response.body || response.headers.get('content-type')?.includes('json')) {
        await response.body?.cancel();
        throw new Error('audio download failed');
      }
      const size = Number(response.headers.get('content-length'));
      if (size > MAX_AUDIO) { await response.body.cancel(); throw new Error('size limit'); }
      return audioStream(response, signal);
    }
  }
  if (output instanceof Response && output.ok && output.headers.get('content-type')?.includes('application/json')) {
    if (!output.body) throw new Error('empty AI output');
    const raw = await readBounded(output.body, Math.ceil(MAX_AUDIO * 4 / 3) + 4096, signal);
    return audioStream(JSON.parse(new TextDecoder().decode(raw)), signal);
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
// Tags disambiguate Han characters without splitting the audio or converting
// English to kana. Escape input markup so it cannot override our language tags.
export function speechText(text: string, language: string): string {
  const escaped = text.replaceAll('&', '&amp;').replaceAll('<', '&lt;').replaceAll('>', '&gt;');
  if (language === 'en') return escaped;
  const code = language === 'zh' ? 'zh-CN' : 'ja-JP';
  const tagged = escaped.replace(/\p{Script=Han}+/gu, word => `<lang lang="${code}">${word}</lang>`);
  // Alternating scripts can otherwise expand a valid 200-character post past
  // the catalog endpoint's 2,000-character limit. Keep its language hint.
  return tagged.length <= 2000 ? tagged : `<lang lang="${code}">${escaped}</lang>`;
}
async function synthesize(env: Env, text: string, engine: SpeechEngine): Promise<AudioStream> {
  let timer: ReturnType<typeof setTimeout> | undefined;
  const controller = new AbortController();
  try {
    return await Promise.race([
      (async () => {
        if (engine === 'grok-ws') {
          const stream = await grokAudio(env.AI, text, env.AI_GATEWAY_ID || 'default', env.GROK_VOICE || 'rex', controller.signal);
          return { ...await audioStream(stream, controller.signal), language: 'auto' };
        }
        if (engine === 'grok') {
          const output = await env.AI.run('xai/grok-tts', {
            text, language: 'auto', voice_id: env.GROK_VOICE || 'rex',
            output_format: { codec: 'mp3', sample_rate: 24000, bit_rate: 128000 },
          }, { gateway: { id: env.AI_GATEWAY_ID || 'default', skipCache: true, collectLog: false } });
          return { ...await audioStream(output, controller.signal), language: 'auto' };
        }
        const lang = await detectLanguage(text, env.AI, 'ja');
        if (controller.signal.aborted) throw new APIError(504, 'tts_timeout', 'TTS timed out');
        const output = await env.AI.run(MODEL, {
          text: speechText(text, lang), voice_id: env.TTS_VOICE || 'Dennis',
          output_format: 'mp3', temperature: 1, timestamp_type: 'none',
        }, { gateway: { id: env.AI_GATEWAY_ID || 'default', skipCache: true, collectLog: false } });
        return { ...await audioStream(output, controller.signal), language: lang };
      })(),
      new Promise<never>((_, reject) => {
        timer = setTimeout(() => { controller.abort(); reject(new APIError(504, 'tts_timeout', 'TTS timed out')); }, 25_000);
      }),
    ]);
  } catch (error) {
    if (error instanceof APIError) throw error;
    if (error instanceof GrokError) throw new APIError(502, error.code, 'Grok speech unavailable');
    if (typeof error === 'object' && error !== null && 'status' in error && error.status === 429) {
      throw new APIError(429, 'tts_rate_limited', 'TTS temporarily rate limited');
    }
    throw new APIError(502, 'tts_failed', 'TTS generation failed');
  } finally { if (timer !== undefined) clearTimeout(timer); }
}
export default {
  async fetch(request: Request, env: Env): Promise<Response> {
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
      const selected = new URL(request.url).searchParams.get('engine') ?? env.TTS_ENGINE ?? 'inworld';
      if (selected !== 'inworld' && selected !== 'grok' && selected !== 'grok-ws') throw new APIError(400, 'invalid_input', 'Unsupported speech engine');
      const data = await synthesize(env, value.text, selected);
      console.log(JSON.stringify({ event: 'tts_stream_ready', request_id: requestID, duration_ms: Date.now() - started, language: data.language, engine: selected }));
      return new Response(data.stream, { headers: { ...headers, 'Content-Type': data.mime, 'X-Speech-Language': data.language! } });
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
