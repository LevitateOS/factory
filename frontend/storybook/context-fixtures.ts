import type {FactoryTransport} from '../factory/api/transport.js';
import type {FactoryRunDetail} from '../factory/api/run-response.js';
import {retainContextChoice} from '../factory/work/context-record.js';
import type {PendingContextChoice} from '../factory/work/context-record.js';
import type {ResolutionContext} from '../factory/work/context-resolution.js';

export type ContextStoryState =
  | 'complete'
  | 'incomplete'
  | 'loading'
  | 'offline'
  | 'denied'
  | 'unknown'
  | 'stale'
  | 'account-unavailable'
  | 'recovered';
export const contextRepository = '9007199254740993';
export const contextRunId = 'a'.repeat(32);

/** Declared transport only: no login, backend, provider, model or worker. */
export function contextStoryPeer(state: ContextStoryState) {
  let generation = `declared-context:${state}`;
  let digest = 'a'.repeat(64);
  let required = digest;
  let omitted = false;
  let receiptPatch: Record<string, unknown> = {};
  let receiptStatus = 200;
  let scope =
    state === 'recovered' ? 'declared-profile:provider:recovered-account' : 'declared-profile:provider:account';
  let seeded = false;
  let scopeFailure = state === 'account-unavailable';
  let wireRepository = contextRepository,
    wireIssue = '12';
  let wireID: string | undefined;
  let latest = contextRunId;
  let recorded: PendingContextChoice | undefined;
  let hold: 'read' | 'action' | 'scope' | undefined;
  const held: {signal: AbortSignal | null | undefined; resolve: () => void}[] = [];
  const calls: {path: string; method: string; body?: unknown}[] = [];
  const run = (runID = latest): FactoryRunDetail => ({
    id: runID,
    project_id: 'p0123456789abcdef01234567',
    role: 'soda-coder',
    harness: 'declared-peer',
    model: 'declared-peer',
    reconciled: false,
    view: {repository: wireRepository, issue: wireIssue},
    ...(!omitted && {
      context_selection: {
        fingerprint: digest,
        scope_digest: digest,
        required_digest: required,
        action: 'hold',
        availability: state === 'incomplete' ? 'incomplete' : 'complete',
        reason: state === 'incomplete' ? 'Missing required input.' : 'Required context is complete.',
        can_use_current: state !== 'incomplete' && recorded?.run !== runID,
      },
    }),
  });
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
      const requestedRun = path.split('/')[2] ?? latest;
      const current = run(requestedRun);
      if ((hold === 'read' && method === 'GET') || (hold === 'action' && method === 'POST')) {
        // Deliberately ignore cancellation to exercise late-result refusal too.
        await new Promise<void>((resolve) => {
          held.push({signal: init?.signal, resolve});
        });
      }
      if (state === 'loading') {
        const signal = init?.signal;
        if (!signal) throw Error('A read lifetime is required.');
        signal.throwIfAborted();
        return new Promise<Response>((_, reject) => {
          signal.addEventListener('abort', () => reject(signal.reason), {once: true});
        });
      }
      if (state === 'offline') throw new TypeError('Declared offline peer.');
      if (state === 'denied') return Response.json({error: 'forbidden'}, {status: 403});
      if (method === 'GET' && path === `repositories/${contextRepository}/factory/issues/12`)
        return Response.json({journey: {lineage: {latest_run: latest}}});
      if (method === 'GET' && /^factory\/runs\/[a-f0-9]{32}$/.test(path)) {
        const {context_selection, view, ...wireRun} = current;
        return Response.json({
          run: {...wireRun, id: wireID ?? requestedRun},
          view,
          context_selection:
            recorded && recorded.run === requestedRun && recorded.required_digest === required
              ? {
                  ...context_selection,
                  action: 'serve',
                  can_use_current: false,
                  resolution: {run_id: requestedRun, bundle: 'current', ...recorded},
                }
              : context_selection,
        });
      }
      if (method === 'POST' && /^factory\/runs\/[a-f0-9]{32}\/actions$/.test(path)) {
        if (state === 'unknown' && calls.filter((call) => call.method === 'POST').length === 1) {
          recorded = {run: requestedRun, ...(body as {context: Omit<PendingContextChoice, 'run'>}).context};
          throw new TypeError('The declared action reply was lost.');
        }
        if (state === 'stale') return Response.json({error: 'stale'}, {status: 409});
        const choice = (body as {context: Omit<PendingContextChoice, 'run'>}).context;
        return Response.json(
          {run_id: requestedRun, bundle: 'current', ...choice, ...receiptPatch},
          {status: receiptStatus}
        );
      }
      return Response.json({error: 'not_found'}, {status: 404});
    },
  };
  return {
    calls,
    context(entry: 'issue' | 'run' = 'issue'): ResolutionContext {
      if (state === 'recovered' && !seeded) {
        recorded = {run: contextRunId, fingerprint: digest, scope_digest: digest, required_digest: required};
        retainContextChoice({scope, repository: contextRepository, issue: '12'}, recorded);
        seeded = true;
      }
      const commandScope = async (signal: AbortSignal) => {
        const current = scope;
        if (hold === 'scope') await new Promise<void>((resolve) => held.push({signal, resolve}));
        signal.throwIfAborted();
        if (scopeFailure) throw Error('Declared account unavailable');
        return current;
      };
      const context = {transport, repository: contextRepository, issue: '12', commandScope};
      return entry === 'run' ? {...context, run: run()} : context;
    },
    hold(kind: 'read' | 'action' | 'scope') {
      hold = kind;
    },
    release() {
      hold = undefined;
      for (const item of held) item.resolve();
    },
    heldCount: () => held.length,
    aborted() {
      return held.length > 0 && held.every((item) => item.signal?.aborted);
    },
    revise() {
      digest = required = 'b'.repeat(64);
    },
    latestRun(id: string) {
      latest = id;
    },
    advanceRequired() {
      required = 'b'.repeat(64);
    },
    omitSelection() {
      omitted = true;
    },
    recorded(choice: PendingContextChoice) {
      recorded = {...choice};
    },
    receipt(patch: Record<string, unknown>, status = 200) {
      receiptPatch = patch;
      receiptStatus = status;
    },
    scope: () => scope,
    clearCalls() {
      calls.length = 0;
    },
    changeScope(value: string) {
      scope = value;
    },
    failScope() {
      scopeFailure = true;
    },
    wireScope(repository: string, issue: string, id = contextRunId) {
      wireRepository = repository;
      wireIssue = issue;
      wireID = id;
    },
    retireGeneration() {
      generation += ':retired';
    },
  };
}
