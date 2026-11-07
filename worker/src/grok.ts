export type GrokInput = ({ websocket: true } | { text: string }) & {
  language: 'auto'; voice_id: string;
  output_format: { codec: 'mp3'; sample_rate: 24000; bit_rate: 128000 };
};
export type GrokOptions = {
  gateway: { id: string; skipCache: true; collectLog: false };
  returnRawResponse: true;
};
export type GrokAI = { run(model: string, input: GrokInput, options: GrokOptions): Promise<unknown> };
export class GrokError extends Error {
  code: string;
  constructor(code: string) { super(code); this.code = code; }
}
export interface VoiceSocket {
  accept(): void;
  send(data: string): void;
  close(code?: number, reason?: string): void;
  addEventListener(type: string, listener: (event: Event) => void): void;
  removeEventListener(type: string, listener: (event: Event) => void): void;
}
async function socketFrom(output: unknown, signal: AbortSignal): Promise<VoiceSocket> {
  if (signal.aborted) throw new GrokError('grok_timeout');
  if (output && typeof output === 'object' && 'webSocket' in output && output.webSocket) {
    return output.webSocket as VoiceSocket;
  }
  if (output instanceof Response) {
    // Upgrade metadata is small; never parse or log an unbounded error body.
    if (!output.body) throw new GrokError('grok_invalid_handshake');
    const reader = output.body.getReader();
    const abort = () => { void reader.cancel().catch(() => {}); };
    signal.addEventListener('abort', abort, { once: true });
    const chunks: Uint8Array[] = [];
    let size = 0;
    try {
      while (true) {
        const { value, done } = await reader.read();
        if (signal.aborted) throw new GrokError('grok_timeout');
        if (done) break;
        size += value.length;
        if (size > 16384) throw new GrokError('grok_invalid_handshake');
        chunks.push(value);
      }
    } finally { signal.removeEventListener('abort', abort); await reader.cancel().catch(() => {}); reader.releaseLock(); }
    const data = new Uint8Array(size);
    let offset = 0;
    for (const chunk of chunks) { data.set(chunk, offset); offset += chunk.length; }
    const text = new TextDecoder().decode(data);
    if (!output.ok) {
      let upstreamCode: unknown;
      try { const failure = JSON.parse(text); upstreamCode = failure.internalCode ?? failure.errors?.[0]?.code; } catch { /* Non-JSON gateway error. */ }
      const suffix = typeof upstreamCode === 'number' && Number.isSafeInteger(upstreamCode) ? `_${upstreamCode}` : '';
      throw new GrokError(`grok_gateway_rejected_${output.status}${suffix}`);
    }
    return socketFrom(JSON.parse(text), signal);
  }
  const result = output as { result?: unknown; websocket?: {url?: unknown; headers?: unknown} } | null;
  if (result?.result) return socketFrom(result.result, signal);
  // Some catalog endpoints supply an ephemeral connection descriptor instead
  // of an upgraded Response. The credential stays inside the Worker.
  const descriptor = result?.websocket;
  if (!descriptor || typeof descriptor.url !== 'string') throw new GrokError('grok_websocket_unavailable');
  const url = new URL(descriptor.url);
  if (url.protocol !== 'wss:' || url.hostname !== 'api.x.ai' || url.port || url.username || url.password || url.pathname !== '/v1/tts') {
    throw new GrokError('grok_invalid_handshake');
  }
  const headers = new Headers({ Upgrade: 'websocket' });
  if (descriptor.headers && typeof descriptor.headers === 'object') {
    for (const [name, value] of Object.entries(descriptor.headers)) {
      if (name.toLowerCase() !== 'authorization' || typeof value !== 'string' || !value.startsWith('Bearer ')) {
        throw new GrokError('grok_invalid_handshake');
      }
      headers.set(name, value);
    }
  }
  url.protocol = 'https:';
  const response = await fetch(url, { headers, signal, redirect: 'manual' });
  if (!('webSocket' in response) || !response.webSocket) {
    await response.body?.cancel(); throw new GrokError('grok_upgrade_failed');
  }
  return response.webSocket as VoiceSocket;
}

export function socketAudio(socket: VoiceSocket, text: string, signal: AbortSignal): ReadableStream<Uint8Array> {
  let finished = false;
  let bytes = 0;
  let timer: ReturnType<typeof setTimeout>;
  let controller: ReadableStreamDefaultController<Uint8Array>;
  const cleanup = () => {
    clearTimeout(timer);
    signal.removeEventListener('abort', abort);
    for (const [type, listener] of listeners) socket.removeEventListener(type, listener);
    try { socket.close(1000, 'Speech complete'); } catch { /* Already closed. */ }
  };
  const fail = () => {
    if (finished) return;
    finished = true; cleanup(); controller.error(new GrokError('grok_stream_failed'));
  };
  const abort = () => fail();
  const message = (event: Event) => {
    if (finished) return;
    try {
      const raw = (event as MessageEvent).data;
      if (typeof raw !== 'string' || raw.length > 7 * 1024 * 1024) throw new Error('invalid frame');
      const data = JSON.parse(raw) as { type?: string; delta?: unknown };
      if (data.type === 'audio.delta') {
        if (typeof data.delta !== 'string') throw new Error('missing audio');
        const decoded = atob(data.delta);
        bytes += decoded.length;
        if (bytes > 5 * 1024 * 1024) throw new Error('oversized audio');
        if (decoded.length) controller.enqueue(Uint8Array.from(decoded, c => c.charCodeAt(0)));
      } else if (data.type === 'audio.done') {
        finished = true; cleanup(); controller.close();
      } else if (data.type === 'error') fail();
    } catch { fail(); }
  };
  const listeners: [string, (event: Event) => void][] = [['message', message], ['error', fail], ['close', fail]];
  return new ReadableStream<Uint8Array>({
    start(c) {
      controller = c;
      for (const [type, listener] of listeners) socket.addEventListener(type, listener);
      signal.addEventListener('abort', abort, { once: true });
      timer = setTimeout(fail, 25_000);
      if (signal.aborted) { fail(); return; }
      try {
        socket.accept();
        socket.send(JSON.stringify({ type: 'text.delta', delta: text }));
        socket.send(JSON.stringify({ type: 'text.done' }));
      } catch { fail(); }
    },
    cancel() { if (!finished) { finished = true; cleanup(); } },
  });
}

export async function grokAudio(ai: GrokAI, text: string, gateway: string, voice: string, signal: AbortSignal): Promise<ReadableStream<Uint8Array>> {
  const output = await ai.run('xai/grok-tts', {
    websocket: true, language: 'auto', voice_id: voice,
    output_format: { codec: 'mp3', sample_rate: 24000, bit_rate: 128000 },
  // The binding's transport-level websocket option routes to Workers AI's
  // native endpoint. Catalog models must use the Gateway POST path instead;
  // websocket:true in the model input requests its connection descriptor.
  }, { returnRawResponse: true, gateway: { id: gateway, skipCache: true, collectLog: false } });
  const socket = await socketFrom(output, signal);
  return socketAudio(socket, text, signal);
}
