import type {FactoryTransport} from '../factory/api/transport.js';
import type {WorkOverviewContext, WorkPlanContext, DirectionRevisionContext} from '../factory/work/progress.js';
import {presentationLinks} from './work-presentation-fixtures.js';

export type ProgressStoryState = 'ready' | 'loading' | 'missing' | 'denied' | 'offline' | 'partial';
export const progressRepository = '9007199254740993';
export const progressIntent = 'd' + 'a'.repeat(24);

/** Declared read peer only; no backend, credentials, provider, model or worker. */
export function progressStoryPeer(state: ProgressStoryState, repository = progressRepository) {
  let generation = `declared-progress:${state}`;
  let failure = 0;
  let hold = false;
  const pending: {signal: AbortSignal | null | undefined; resolve: () => void}[] = [];
  const calls: string[] = [];
  const transport: FactoryTransport = {
    get generation() {
      return generation;
    },
    assetBase: '/',
    async request(path, init) {
      init?.signal?.throwIfAborted();
      calls.push(path);
      if (init?.method !== 'GET') return Response.json({error: 'read_only'}, {status: 405});
      if (hold) await new Promise<void>((resolve) => pending.push({signal: init.signal, resolve}));
      if (state === 'loading') {
        const signal = init.signal;
        if (!signal) throw Error('A read lifetime is required.');
        signal.throwIfAborted();
        return new Promise<Response>((_, reject) =>
          signal.addEventListener('abort', () => reject(signal.reason), {once: true})
        );
      }
      const status = failure || (state === 'missing' ? 404 : state === 'denied' ? 403 : state === 'offline' ? 503 : 0);
      if (status) return Response.json({error: {message: 'Declared progress read unavailable.'}}, {status});
      if (path === `factory/intents/${progressIntent}/overview`)
        return Response.json({
          intent: progressIntent,
          revision: 'd' + 'b'.repeat(24),
          state: 'accepted',
          direction: {objective: 'Make release readiness visible.', tasks_total: 1},
          context: {repository: repository},
          setup: {state: 'ready'},
          generation: 'committed',
          work_items: {by_state: {waiting: 1}, total: 1, outcomes_total: 1, outcomes_evidenced: 0},
          mappings: [{task_key: 'release-checklist', issue_id: 'opaque', issue_index: '12', effective: true}],
          attention: [],
        });
      if (path === `factory/intents/${progressIntent}`)
        return Response.json({
          id: 'd' + 'b'.repeat(24),
          intent: progressIntent,
          repository: repository,
          objective: 'Make release readiness visible.',
          state: 'accepted',
          ideas: [],
          tasks: [],
          max_issues: 4,
        });
      if (path === `factory/intents/${progressIntent}/revisions?limit=2`)
        return Response.json({
          intent: progressIntent,
          revisions: [{revision: 'd' + 'b'.repeat(24), repository: repository, state: 'accepted'}],
        });
      if (path === `repositories/${repository}/factory`) return Response.json({repository: repository});
      const issuePath = `repositories/${repository}/factory/issues/`;
      if (path.startsWith(issuePath)) {
        if (path.slice(issuePath.length).startsWith('13')) return Response.json({error: 'forbidden'}, {status: 403});
        if (path.endsWith('/plan?limit=5')) return Response.json({comparisons: []});
        if (path === issuePath + '12')
          return Response.json({
            repository: repository,
            issue: '12',
            status: {valid: true},
            readiness: {readiness: 'queued', assessed_unix: 1720000300},
          });
      }
      return Response.json({error: 'not_found'}, {status: 404});
    },
  };
  const overview: WorkOverviewContext = {
    transport,
    repository: repository,
    intentID: progressIntent,
    links: presentationLinks(),
  };
  const plan: WorkPlanContext = {
    transport,
    repository: repository,
    issues: state === 'partial' ? ['12', '13'] : ['12'],
    links: presentationLinks(),
  };
  const direction: DirectionRevisionContext = {transport, intentID: progressIntent};
  return {
    overview,
    plan,
    direction,
    calls,
    hold() {
      hold = true;
    },
    release() {
      hold = false;
      for (const item of pending) item.resolve();
    },
    aborted() {
      return pending.length > 0 && pending.every((item) => item.signal?.aborted);
    },
    retireGeneration() {
      generation += ':retired';
    },
    fail(status: number) {
      failure = status;
    },
  };
}
