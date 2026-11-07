// Run an opt-in voice compatibility check without modifying the Worker.
const account = process.env.CLOUDFLARE_ACCOUNT_ID;
const token = process.env.CLOUDFLARE_API_TOKEN;
const voice = process.env.PROBE_VOICE;
if (!account || !/^[0-9a-f]{32}$/i.test(account) || !token || !voice || !/^[a-z0-9_-]{1,64}$/i.test(voice)) {
  throw new Error('Cloudflare account, token and valid voice ID required');
}
const response = await fetch(`https://api.cloudflare.com/client/v4/accounts/${account}/ai/run`, {
  method: 'POST',
  redirect: 'error',
  signal: AbortSignal.timeout(30_000),
  headers: {
    authorization: `Bearer ${token}`,
    'content-type': 'application/json',
    'cf-aig-gateway-id': 'default',
    'cf-aig-collect-log': 'false',
    'cf-aig-skip-cache': 'true',
  },
  body: JSON.stringify({ model: 'inworld/tts-2', input: {
    text: 'こんにちは。音声の確認です。', voice_id: voice,
    output_format: 'mp3', temperature: 1, timestamp_type: 'none',
  } }),
});
const payload = await response.json().catch(() => null);
const result = payload?.result?.result ?? payload?.result ?? payload;
const generated = response.ok && typeof result?.audio === 'string' && result.audio.length > 0;
const errors = [payload?.errors, payload?.result?.errors].flat().filter(error => typeof error?.code === 'number');
console.log(JSON.stringify({ voice, status: response.status, generated, errors: errors.map(error => ({
  code: error.code,
  // Report whether the voice field was rejected without printing raw provider output.
  voice_field: typeof error.message === 'string' && /voice[_ ]id|speaker/i.test(error.message),
})) }));
if (!generated) throw new Error('AI Gateway did not generate audio for the requested voice');
