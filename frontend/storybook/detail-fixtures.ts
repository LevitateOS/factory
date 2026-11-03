import type {FactoryTransport} from '../factory/api/transport.js';
import type {WorkDetailContext} from '../factory/work/detail.js';
import {presentationLinks} from './work-presentation-fixtures.js';
import {retainRunRetry} from '../factory/work/retry-record.js';

export type DetailStoryState = 'ready' | 'read-only' | 'loading' | 'offline' | 'denied' | 'unknown' | 'recovered';
export const detailRepository = '9007199254740993';
export const detailRun = 'a'.repeat(32);
export const detailOldRun = 'e'.repeat(32);
export const detailEnvironment = 'p' + 'e'.repeat(24);

/** Declared browser peer only; no login, backend, provider, worker or model. */
export function detailStoryPeer(state: DetailStoryState, repository = detailRepository) {
  const scope = `storybook:detail:${state}`;
  if (state === 'recovered')
    retainRunRetry({scope, repository: repository, issue: '12'}, {command: 'c'.repeat(32), run: detailRun});
  let generation = `declared-detail:${state}`;
  let hold: 'read' | 'retry' | undefined;
  let failure = 0;
  const held: {signal: AbortSignal | null | undefined; resolve: () => void}[] = [];
  const calls: {path: string; method: string; body?: unknown}[] = [];
  const transport: FactoryTransport = {
    get generation() {
      return generation;
    },
    assetBase: '/',
    async request(path, init) {
      init?.signal?.throwIfAborted();
      const method = init?.method ?? 'GET';
      const body: unknown = typeof init?.body === 'string' ? JSON.parse(init.body) : undefined;
      calls.push({path, method, ...(body === undefined ? {} : {body})});
      if ((hold === 'read' && method === 'GET') || (hold === 'retry' && method === 'POST'))
        await new Promise<void>((resolve) => held.push({signal: init?.signal, resolve}));
      if (state === 'loading') {
        const signal = init?.signal;
        if (!signal) throw Error('A read lifetime is required.');
        signal.throwIfAborted();
        return new Promise<Response>((_, reject) =>
          signal.addEventListener('abort', () => reject(signal.reason), {once: true})
        );
      }
      const status = failure || (state === 'offline' ? 503 : state === 'denied' ? 403 : 0);
      if (status) return Response.json({error: {message: 'Declared detail read unavailable.'}}, {status});
      if (method === 'GET' && path === `repositories/${repository}/factory/issues/12`)
        return Response.json({
          repository: repository,
          issue: '12',
          status: {valid: true},
          readiness: {readiness: 'queued'},
          capabilities: Object.fromEntries(
            ['watch', 'pause', 'resume', 'stop', 'retry', 'takeover'].map((kind) => [
              kind,
              {available: state !== 'read-only', reason: 'read_only'},
            ])
          ),
          journey: {repository: repository, issue: '12', phases: [], tasks: [], gates: []},
          checks: {pr_number: '21', verdict: 'pending'},
          links: {issue_url: '/recorded-forge/issues/12', pull_url: '/recorded-forge/pulls/21'},
        });
      if (method === 'GET' && path === `repositories/${repository}/factory/access`)
        return Response.json({repository: repository, viewer: {code_write: false, session_current: true}});
      if (method === 'GET' && (path === `factory/runs/${detailRun}` || path === `factory/runs/${detailOldRun}`))
        return Response.json({
          run: {
            id: path.endsWith(detailOldRun) ? detailOldRun : detailRun,
            project_id: 'p0123456789abcdef01234567',
            role: 'soda-coder',
            harness: 'declared-peer',
            model: 'declared-peer',
            reconciled: true,
          },
          view: {repository: repository, issue: '12'},
        });
      if (
        method === 'POST' &&
        (path === `factory/runs/${detailRun}/actions` || path === `factory/runs/${detailOldRun}/actions`) &&
        (body as {action?: string})?.action === 'retry'
      ) {
        if (state === 'unknown' && calls.filter((call) => call.method === 'POST').length === 1)
          throw new TypeError('The declared action reply was lost.');
        return Response.json({
          queued: true,
          command_id: (body as {command_id: string}).command_id,
          prior_run: path.includes(detailOldRun) ? detailOldRun : detailRun,
        });
      }
      return Response.json({error: {message: 'No declared action adapter.'}}, {status: 404});
    },
  };
  const context: WorkDetailContext = {
    commandScope: async (signal) => {
      signal.throwIfAborted();
      return scope;
    },
    transport,
    repository: repository,
    issue: '12',
    runIDs: [detailRun],
    runID: detailRun,
    projectID: 'p0123456789abcdef01234567',
    environmentID: detailEnvironment,
    links: presentationLinks(),
    capabilities: {pause: true, resume: true, stop: true, retry: true, takeover: true, project_stop: false},
  };
  return {
    context,
    calls,
    hold(kind: 'read' | 'retry') {
      hold = kind;
    },
    release() {
      hold = undefined;
      for (const item of held) item.resolve();
    },
    aborted() {
      return held.length > 0 && held.every((item) => item.signal?.aborted);
    },
    retireGeneration() {
      generation += ':retired';
    },
    fail(status: number) {
      failure = status;
    },
  };
}
