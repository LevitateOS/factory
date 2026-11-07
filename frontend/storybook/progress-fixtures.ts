import type {FactoryTransport} from '../factory/api/transport.js';
import type {WorkOverviewContext, WorkPlanContext, DirectionRevisionContext} from '../factory/work/progress.js';
import {presentationLinks} from './work-presentation-fixtures.js';

export type ProgressStoryState = 'ready' | 'loading' | 'missing' | 'denied' | 'offline' | 'partial';
export const progressRepository = '9007199254740993';
export const progressIntent = 'd' + 'a'.repeat(24);

/** Declared UI peer; optional policy edits remain in memory. No backend,
 * credentials, provider, model or worker. */
export function progressStoryPeer(
  state: ProgressStoryState,
  repository = progressRepository,
  editRoles: boolean | 'setup' = false
) {
  let generation = `declared-progress:${state}`;
  let failure = 0;
  let configured = editRoles !== 'setup';
  let hold = false;
  const pending: {signal: AbortSignal | null | undefined; resolve: () => void}[] = [];
  const calls: string[] = [];
  const policyChanges: Record<string, unknown>[] = [];
  const source = {
    connection: 'c'.repeat(32),
    label: 'This Muse login',
    native: {profile: 'a'.repeat(32), authentication: 'muse_login'},
    execution_available: false,
  };
  let funding: Record<string, unknown> | undefined;
  let policy = {
    repository,
    revision: 1,
    granted_by: '7',
    enabled: false,
    paused: true,
    target_branch: 'refs/heads/main',
    merge_method: 'squash',
    required_checks: ['build'],
    reassessment_minutes: 30,
    intent_minutes: 12,
    attempt_limits: {active_minutes: 30, correction_cycles: 2, review_rounds: 2, max_children: 1},
    roles: {
      'soda-coder': {
        harness: 'muse',
        model: 'declared-coder',
        account: '',
        profile: {effort: 'high', contributor: false},
      },
      'soda-reviewer': {
        harness: 'muse',
        model: 'declared-reviewer',
        account: '',
        profile: {effort: 'high', contributor: true},
      },
    },
    publish_actor: {token_id: '11', actor_id: '7', kind: 'ref_publish'},
    create_actor: {token_id: '11', actor_id: '7', kind: 'pr_create'},
    review_actor: {token_id: '11', actor_id: '7', kind: 'review_submit'},
    merge_actor: {token_id: '11', actor_id: '7', kind: 'merge'},
  };
  const accounts = [
    {repository, connection: 'coding-subscription', active: true, roles: ['soda-coder']},
    {repository, connection: 'review-subscription', active: true, roles: ['soda-reviewer']},
    {repository, connection: 'withdrawn-subscription', active: false, roles: ['soda-coder', 'soda-reviewer']},
  ];
  const transport: FactoryTransport = {
    get generation() {
      return generation;
    },
    assetBase: '/',
    async request(path, init) {
      init?.signal?.throwIfAborted();
      calls.push(path);
      if (hold) await new Promise<void>((resolve) => pending.push({signal: init?.signal, resolve}));
      if (state === 'loading') {
        const signal = init?.signal;
        if (!signal) throw Error('A read lifetime is required.');
        signal.throwIfAborted();
        return new Promise<Response>((_, reject) =>
          signal.addEventListener('abort', () => reject(signal.reason), {once: true})
        );
      }
      const status = failure || (state === 'missing' ? 404 : state === 'denied' ? 403 : state === 'offline' ? 503 : 0);
      if (status) return Response.json({error: {message: 'Declared progress read unavailable.'}}, {status});
      if (
        editRoles &&
        path === `repositories/${repository}/factory/sponsorships/${source.connection}` &&
        init?.method === 'PUT'
      ) {
        const change = JSON.parse(String(init.body));
        if (change.expected_revision !== (funding?.revision ?? 0)) return Response.json({}, {status: 409});
        funding = {
          ...change,
          revision: Number(funding?.revision ?? 0) + 1,
          repository,
          connection: source.connection,
          native: source.native,
        };
        return Response.json({state: 'applied'});
      }
      if (editRoles && path === `repositories/${repository}/factory/policy` && init?.method === 'PUT') {
        const change = JSON.parse(String(init.body));
        if (change.expected_revision !== (configured ? policy.revision : 0)) return Response.json({}, {status: 409});
        policyChanges.push(change);
        policy = configured
          ? {...policy, roles: change.roles, revision: policy.revision + 1}
          : {...policy, ...change, revision: 1};
        configured = true;
        return Response.json({state: 'applied'});
      }
      if (init?.method !== 'GET') return Response.json({error: 'read_only'}, {status: 405});
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
      if (path === `repositories/${repository}/factory`)
        return Response.json({
          repository,
          policy: editRoles && configured ? policy : null,
          sponsorships: [...(editRoles && editRoles !== 'setup' ? accounts : []), ...(funding ? [funding] : [])],
          spending_sources: editRoles ? [source] : [],
          policy_options: editRoles ? {target_branch: 'refs/heads/main', merge_methods: ['squash', 'rebase']} : null,
          native_bindings: editRoles
            ? ['git.ref.publish', 'pull_request.create', 'pull_request.review.submit', 'pull_request.merge'].map(
                (kind) => ({
                  kind,
                  actor_id: '7',
                  native_profile: 'a'.repeat(32),
                  native_connection: 'b'.repeat(32),
                  token_id: '0',
                })
              )
            : [],
        });
      if (path === `repositories/${repository}/factory/access`)
        return Response.json({repository, editable: {policy: {editable: !!editRoles}}});
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
    actor: '7',
    accountLabel: 'Declared owner',
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
    policyChanges,
    get policy() {
      return structuredClone(policy);
    },
    withdrawAccount(connection: string) {
      const account = accounts.find((entry) => entry.connection === connection);
      if (account) account.active = false;
    },
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
