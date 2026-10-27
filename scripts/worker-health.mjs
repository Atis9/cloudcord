// Deployment verification does not submit a billable inference.
const base = process.env.WORKER_URL;
const token = process.env.CLOUDFLARE_BOT_API_TOKEN;
if (!base || !token) throw new Error('Set CLOUDFLARE_API_BASE_URL variable and shared API token');
if (new URL(base).protocol !== 'https:') throw new Error('HTTPS required');
const url = `${base.replace(/\/$/, '')}/v1/tts`;
for (const authenticated of [false, true]) {
  const response = await fetch(url, {
    method: 'POST', redirect: 'error', signal: AbortSignal.timeout(10_000),
    headers: { 'content-type': 'application/json', ...(authenticated ? { authorization: `Bearer ${token}` } : {}) },
    body: JSON.stringify({ guild_id: '1', text: '', language: 'ja' }),
  });
  const expected = authenticated ? 400 : 401;
  if (response.status !== expected) throw new Error(`Worker check failed: HTTP ${response.status}, expected ${expected}`);
  await response.body?.cancel();
}
console.log('Worker reachable; authentication and validation passed. No inference submitted.');
