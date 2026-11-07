import test from 'node:test';
import assert from 'node:assert/strict';
import { grokAudio, socketAudio } from '../src/grok.ts';
import worker from '../src/index.ts';
import type { Env } from '../src/index.ts';
class Socket extends EventTarget {
  accepted = false;
  closed = false;
  sent: unknown[] = [];
  accept() { this.accepted = true; }
  close() { this.closed = true; }
  send(data: string) { this.sent.push(JSON.parse(data)); }
  message(data: unknown) { this.dispatchEvent(new MessageEvent('message', { data: JSON.stringify(data) })); }
}
const mp3 = new Uint8Array([0x49,0x44,0x33,1,2,3]);
const delta = {type:'audio.delta',delta:btoa(String.fromCharCode(...mp3))};
test('Grok sends one original mixed utterance and streams before audio.done', async () => {
  const socket = new Socket();
  let calls = 0;
  const stream = await grokAudio({run:async(model,input,opts)=>{
    calls++;
    assert.equal(model,'xai/grok-tts');
    assert.deepEqual(input,{websocket:true,language:'auto',voice_id:'rex',output_format:{codec:'mp3',sample_rate:24000,bit_rate:128000}});
    assert.deepEqual(opts,{returnRawResponse:true,gateway:{id:'default',skipCache:true,collectLog:false}});
    return {webSocket:socket};
  }},'読み上げの test。你好。','default','rex',new AbortController().signal);
  assert.ok(socket.accepted);
  assert.deepEqual(socket.sent,[{type:'text.delta',delta:'読み上げの test。你好。'},{type:'text.done'}]);
  const reader=stream.getReader();
  socket.message(delta);
  assert.deepEqual((await reader.read()).value,mp3);
  assert.equal(socket.closed,false);
  socket.message({type:'audio.done'});
  assert.equal((await reader.read()).done,true);
  assert.ok(socket.closed);assert.equal(calls,1);
});
test('cancel, abort, provider error, malformed data and early close clean up the socket',async()=>{
  for(const event of ['cancel','abort','error','malformed','close']){
    const socket=new Socket();const abort=new AbortController();
    const reader=socketAudio(socket,'test',abort.signal).getReader();
    if(event==='cancel'){await reader.cancel();}
    else {
      if(event==='abort')abort.abort();
      else if(event==='close')socket.dispatchEvent(new Event('close'));
      else socket.message(event==='error'?{type:'error',message:'private token'}:{type:'audio.delta',delta:'invalid base64 %%'});
      await assert.rejects(reader.read(),/grok_stream_failed/);
    }
    assert.ok(socket.closed);
    // Late messages after cleanup must be ignored.
    socket.message(delta);
  }
});
test('socket audio enforces a total duration limit',async t=>{
  t.mock.timers.enable({apis:['setTimeout']});
  const socket=new Socket();const reader=socketAudio(socket,'test',new AbortController().signal).getReader();
  const pending=reader.read();t.mock.timers.tick(25000);
  await assert.rejects(pending);assert.ok(socket.closed);
});
test('catalog cannot redirect ephemeral credentials to an untrusted host',async()=>{
  let fetched=false;
  const ai={run:async()=>({websocket:{url:'wss://evil.example/v1/tts',headers:{Authorization:'Bearer private'}}})};
  await assert.rejects(grokAudio(ai,'test','default','rex',new AbortController().signal),/grok_invalid_handshake/);
});
test('comparison route selects Grok while the configured Inworld path is retained', async () => {
  const socket=new Socket();
  const env:Env={AI:{run:async(model)=>{assert.equal(model,'xai/grok-tts');queueMicrotask(()=>socket.message(delta));return {webSocket:socket};}},BOT_API_TOKEN:'secret',AI_GATEWAY_ID:'default',TTS_VOICE:'Dennis'};
  const req=new Request('https://example.com/v1/tts?engine=grok-ws',{method:'POST',headers:{authorization:'Bearer secret','content-type':'application/json'},body:JSON.stringify({guild_id:'1',text:'こんにちは test。你好。',language:'ja'})});
  // Emit after the async connection setup has installed its listeners.
  const pending=worker.fetch(req,env);
  for(let i=0;i<20;i++)await new Promise(resolve=>setImmediate(resolve));
  socket.message(delta);
  const response=await pending;
  assert.equal(response.status,200);assert.equal(response.headers.get('x-speech-language'),'auto');
  await response.body?.cancel();assert.ok(socket.closed);
});
test('Grok REST submits the mixed text once without forcing a language',async()=>{
  const text='こんにちは test。你好。';let calls=0;
  const env:Env={AI:{run:async(model,input)=>{
    calls++;assert.equal(model,'xai/grok-tts');
    assert.deepEqual(input,{text,language:'auto',voice_id:'rex',output_format:{codec:'mp3',sample_rate:24000,bit_rate:128000}});
    return mp3;
  }},BOT_API_TOKEN:'secret',AI_GATEWAY_ID:'default',TTS_VOICE:'Dennis',TTS_ENGINE:'grok'};
  const req=new Request('https://example.com/v1/tts',{method:'POST',headers:{authorization:'Bearer secret','content-type':'application/json'},body:JSON.stringify({guild_id:'1',text,language:'ja'})});
  const response=await worker.fetch(req,env);
  assert.equal(response.status,200);assert.equal(calls,1);
  assert.deepEqual(new Uint8Array(await response.arrayBuffer()),mp3);
});
