// Called only by the manually dispatched deployment workflow. The shared
// token reaches Wrangler via a mode-0600 temporary file, never an argv value.
import { mkdtemp, writeFile, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { spawnSync } from 'node:child_process';
const token = process.env.CLOUDFLARE_BOT_API_TOKEN;
if (!token) throw new Error('CLOUDFLARE_BOT_API_TOKEN secret is required');
const dir = await mkdtemp(join(tmpdir(), 'cloudcord-secrets-'));
try {
  const file = join(dir, 'secrets.json');
  await writeFile(file, JSON.stringify({ BOT_API_TOKEN: token }), { mode: 0o600 });
  const result = spawnSync(process.execPath, ['node_modules/wrangler/bin/wrangler.js', 'secret', 'bulk', file], { cwd: 'worker', stdio: 'inherit', env: process.env });
  if (result.status !== 0) throw new Error('Worker secret update failed');
} finally { await rm(dir, { recursive: true, force: true }); }
