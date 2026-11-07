import worker from './index.ts';
import type { Env } from './index.ts';

export default {
  fetch(request: Request, env: Env): Promise<Response> {
    return worker.fetch(request, env);
  },
};
