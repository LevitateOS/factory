import type {ActivityContext} from '../factory/work/activity.js';
import type {ActivitySettings} from '../factory/api/activity-response.js';
import {saveActivityDraft} from '../factory/work/activity-draft.js';
export type ActivityStoryState =
  | 'recorded'
  | 'empty'
  | 'loading'
  | 'offline'
  | 'denied'
  | 'read-only'
  | 'unknown'
  | 'recovered'
  | 'truncated';
export const activityRepository = '9007199254740993';
export function activityStoryPeer(state: ActivityStoryState) {
  let generation = `declared-activity:${state}`,
    revision = 3,
    failure = 0;
  let settings: ActivitySettings = {issue_created: true, pr_created: true, verified_merge: true};
  let hold: 'all' | 'write' | undefined,
    mismatch = false;
  const held: {signal: AbortSignal | null | undefined; release: () => void}[] = [];
  const calls: {path: string; method: string; body?: unknown}[] = [];
  const scope = `storybook:activity:${state}`;
  if (state === 'recovered')
    saveActivityDraft(
      {scope, repository: activityRepository},
      {expected_revision: 3, settings: {...settings, pr_created: false}, submitted: true}
    );
  const view = () => ({
    repository: mismatch ? '1' : activityRepository,
    repository_path: '/declared/provider',
    revision,
    settings,
    observed_unix: 0,
    truncated: state === 'truncated',
    items:
      state === 'empty'
        ? []
        : [
            {
              id: 'issue',
              kind: 'issue_created',
              issue: '11',
              summary: 'Original accepted issue created',
              created_unix: 1,
              url: 'https://provider.invalid/issue/11',
            },
            {
              id: 'pull',
              kind: 'pr_created',
              issue: '11',
              pr: '12',
              summary: 'Pull request awaiting review',
              created_unix: 2,
              url: 'javascript:unsafeHint()',
            },
            {
              id: 'merge',
              kind: 'verified_merge',
              issue: '11',
              pr: '12',
              summary: 'Verified merge and original issue closed',
              created_unix: 0,
              url: '/native/route/not-used',
            },
          ],
  });
  const context: ActivityContext = {
    repository: activityRepository,
    editable: state !== 'read-only',
    commandScope: async (signal) => {
      signal.throwIfAborted();
      return scope;
    },
    presentItem: (item) => ({
      href: item.kind === 'pr_created' ? `/desktop/pull/${item.pr}` : `/desktop/issue/${item.issue}`,
    }),
    transport: {
      get generation() {
        return generation;
      },
      assetBase: '/',
      async request(path, init) {
        init?.signal?.throwIfAborted();
        const method = init?.method ?? 'GET';
        const body =
          typeof init?.body === 'string'
            ? (JSON.parse(init.body) as {expected_revision: number; settings: ActivitySettings})
            : undefined;
        calls.push({path, method, ...(body ? {body} : {})});
        if (hold === 'all' || (hold === 'write' && method === 'PUT'))
          await new Promise<void>((release) => held.push({signal: init?.signal, release}));
        if (state === 'loading') {
          const signal = init?.signal;
          if (!signal) throw Error('Lifetime required');
          signal.throwIfAborted();
          return new Promise<Response>((_, reject) =>
            signal.addEventListener('abort', () => reject(signal.reason), {once: true})
          );
        }
        if (failure || state === 'offline' || state === 'denied')
          return Response.json({error: 'declared'}, {status: failure || (state === 'denied' ? 403 : 503)});
        if (path !== `repositories/${activityRepository}/factory/activity`) return new Response(null, {status: 404});
        if (method === 'PUT' && body) {
          if (body.expected_revision !== revision) return new Response(null, {status: 412});
          revision++;
          settings = body.settings;
          if (state === 'unknown') throw new TypeError('Declared lost preference reply');
        }
        return Response.json(view());
      },
    },
  };
  return {
    context,
    calls,
    hold(write = false) {
      hold = write ? 'write' : 'all';
    },
    release() {
      hold = undefined;
      for (const item of held) item.release();
    },
    aborted() {
      return held.length > 0 && held.every((item) => item.signal?.aborted);
    },
    retireGeneration() {
      generation += ':retired';
    },
    fail(code: number) {
      failure = code;
    },
    mismatch(value: boolean) {
      mismatch = value;
    },
    advance() {
      revision++;
    },
  };
}
