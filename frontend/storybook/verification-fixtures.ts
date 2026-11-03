import type {FactoryTransport} from '../factory/api/transport.js';
import type {DeliveryRepairProgress} from '../factory/api/run-response.js';
import type {VerificationRetryContext} from '../factory/work/verification-retry.js';
import {retainVerificationRequest} from '../factory/work/verification-record.js';

export type VerificationStoryState =
  | 'retry'
  | 'repair'
  | 'investigating'
  | 'attention'
  | 'adopted'
  | 'empty'
  | 'loading'
  | 'offline'
  | 'denied'
  | 'unknown-retry'
  | 'unknown-repair'
  | 'recovered-retry'
  | 'recovered-repair';
export const verificationRun = 'a'.repeat(32);
export const verificationRepository = '9007199254740993';

/** Declared HTTP peer only: no credentials, backend, provider, model or worker. */
export function verificationStoryPeer(state: VerificationStoryState) {
  let generation = `declared-verification:${state}`;
  let hold: 'read' | 'action' | undefined;
  const held: {signal: AbortSignal | null | undefined; release: () => void}[] = [];
  let failure = 0;
  let reply: Record<string, unknown> = {};
  let readPatch: Record<string, unknown> = {};
  let progress: DeliveryRepairProgress['state'] | undefined =
    state === 'investigating' || state === 'attention' || state === 'adopted' ? state : undefined;
  let confirmed = false;
  const repair = state === 'repair' || state === 'unknown-repair' || state === 'recovered-repair';
  const calls: {path: string; method: string; body?: unknown}[] = [];
  const transport: FactoryTransport = {
    get generation() {
      return generation;
    },
    assetBase: '/',
    async request(path, init) {
      init?.signal?.throwIfAborted();
      const method = init?.method ?? 'GET';
      const body = typeof init?.body === 'string' ? (JSON.parse(init.body) as Record<string, unknown>) : undefined;
      calls.push({path, method, ...(body === undefined ? {} : {body})});
      if ((hold === 'read' && method === 'GET') || (hold === 'action' && method === 'POST'))
        // Ignore abort deliberately: the component must refuse late facts too.
        await new Promise<void>((release) => held.push({signal: init?.signal, release}));
      if (state === 'loading') {
        const signal = init?.signal;
        if (!signal) throw Error('A read lifetime is required');
        signal.throwIfAborted();
        return new Promise<Response>((_, reject) =>
          signal.addEventListener('abort', () => reject(signal.reason), {once: true})
        );
      }
      if (failure || state === 'denied' || state === 'offline')
        return Response.json({error: 'declared_unavailable'}, {status: failure || (state === 'denied' ? 403 : 503)});
      if (method === 'GET' && path === `factory/runs/${verificationRun}`) {
        return Response.json({
          run: {id: verificationRun, role: 'soda-reviewer', reconciled: true, outcome: 'succeeded'},
          view: {repository: verificationRepository},
          verification_retry: {available: !repair && !progress && !confirmed && state !== 'empty'},
          delivery_repair: {
            available: repair && !progress && !confirmed,
            ...(progress
              ? {
                  progress: {
                    cause_id: 'c'.repeat(32),
                    cycle_id: 'd'.repeat(32),
                    failed_run: verificationRun,
                    source_commit: 'e'.repeat(40),
                    state: progress,
                    failures: [{check_ref: 'visible-result', detail: 'The recorded result needs attention.'}],
                    failure_count: 1,
                    ...(['attention', 'adopted'].includes(progress)
                      ? {
                          judgment: {
                            option: 'accept',
                            accept_probability: 0.87,
                            confidence: progress === 'attention' ? 0.24 : 0.88,
                          },
                        }
                      : {}),
                  },
                }
              : {}),
          },
          ...readPatch,
        });
      }
      if (method === 'POST' && path === `factory/runs/${verificationRun}/actions`) {
        if (
          (state === 'unknown-retry' || state === 'unknown-repair') &&
          calls.filter((call) => call.method === 'POST').length === 1
        )
          throw new TypeError('Declared lost command reply.');
        if (body?.action === 'retry') {
          confirmed = true;
          return Response.json({command_id: body.command_id, prior_run: verificationRun, queued: true, ...reply});
        }
        if (body?.action === 'repair_delivery') {
          progress = 'ready';
          confirmed = true;
          return Response.json({
            run_id: verificationRun,
            cause_id: 'c'.repeat(32),
            cycle_id: 'd'.repeat(32),
            state: 'queued',
            ...reply,
          });
        }
      }
      return Response.json({error: 'not_found'}, {status: 404});
    },
  };
  const scope = `storybook:verification:${state}`;
  const recovered = state === 'recovered-retry' || state === 'recovered-repair';
  if (recovered)
    retainVerificationRequest(
      {scope, repository: verificationRepository},
      {run: verificationRun, action: repair ? 'repair_delivery' : 'retry', command: repair ? '' : 'c'.repeat(32)}
    );
  const context: VerificationRetryContext = {
    transport,
    repository: verificationRepository,
    runs: recovered ? [] : [verificationRun],
    commandScope: async (signal) => {
      signal.throwIfAborted();
      return scope;
    },
  };
  return {
    context,
    calls,
    hold(kind: 'read' | 'action') {
      hold = kind;
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
    fail(status: number) {
      failure = status;
    },
    receipt(patch: Record<string, unknown>) {
      reply = patch;
    },
    read(patch: Record<string, unknown>) {
      readPatch = patch;
    },
    progress(next: DeliveryRepairProgress['state']) {
      progress = next;
    },
  };
}
