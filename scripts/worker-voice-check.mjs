// Upload a preview using the production binding and secrets. Do not route
// production traffic to the uploaded version.
import { spawnSync } from 'node:child_process';
import { mkdtemp, readFile, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';

const voice = process.env.PROBE_VOICE;
if (!voice || !/^[a-z0-9_-]{1,64}$/i.test(voice)) throw new Error('Valid voice ID required');
const engine = process.env.PROBE_ENGINE;
if (!['inworld', 'minimax', 'gemini', 'gemini-lite'].includes(engine)) throw new Error('Unsupported voice probe engine');
const base = new URL(process.env.WORKER_URL);
if (base.protocol !== 'https:' || !base.hostname.endsWith('.workers.dev')) {
  throw new Error('A workers.dev URL is required for voice previews');
}
const dir = await mkdtemp(join(tmpdir(), 'cloudcord-voice-check-'));
try {
  const output = join(dir, 'wrangler.jsonl');
  const upload = spawnSync(process.execPath, [
    'node_modules/wrangler/bin/wrangler.js', 'versions', 'upload',
    '--var', `TTS_VOICE:${voice}`, '--var', `TTS_ENGINE:${engine}`, '--var', 'TTS_PREVIEW_DIAGNOSTICS:true',
  ], { cwd: 'worker', stdio: 'inherit', env: { ...process.env, WRANGLER_OUTPUT_FILE_PATH: output } });
  if (upload.status !== 0) throw new Error('Worker preview upload failed');
  const entries = (await readFile(output, 'utf8')).trim().split('\n').map(line => JSON.parse(line));
  const version = entries.findLast(entry => entry.type === 'version-upload');
  if (!version?.preview_url) throw new Error('No Worker version URL was returned');
  const preview = new URL(version.preview_url);
  if (preview.protocol !== 'https:' || !preview.hostname.endsWith(`-${base.hostname}`) ||
      preview.username || preview.password || preview.port) throw new Error('Unexpected Worker version URL');
  console.log(`Checking ${engine} voice: ${voice}`);
  const check = spawnSync(process.execPath, ['scripts/worker-speech-check.mjs'], {
    stdio: 'inherit', env: { ...process.env, WORKER_URL: preview.origin, SPEECH_ENGINE: 'configured' },
  });
  if (check.status !== 0) throw new Error('Voice preview did not generate valid audio');
} finally {
  await rm(dir, { recursive: true, force: true });
}
