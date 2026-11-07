import test from 'node:test';
import assert from 'node:assert/strict';
import worker from '../src/index.ts';
import type { Env } from '../src/index.ts';
const mp3 = new Uint8Array([0x49,0x44,0x33,1,2,3]);
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
  assert.equal(response.headers.get('x-speech-engine'),'grok');
  assert.deepEqual(new Uint8Array(await response.arrayBuffer()),mp3);
});
