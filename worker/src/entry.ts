import worker from './index.ts';
import type { Env } from './index.ts';
import { spellWord } from './kana.ts';

export default {
  fetch(request: Request, env: Env): Promise<Response> {
    return worker.fetch(request, env, spellWord);
  },
};
