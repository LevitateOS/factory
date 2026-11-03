import type {VerificationAllowanceContext} from '../factory/work/verification-allowance.js';
import {retainAllowanceChange} from '../factory/work/allowance-record.js';
export type AllowanceStoryState =
  | 'ready'
  | 'read-only'
  | 'loading'
  | 'offline'
  | 'denied'
  | 'unknown'
  | 'recovered'
  | 'stale';
export const allowanceIntent = 'd' + '1'.repeat(24),
  allowanceRepository = '9007199254740993';
export function allowanceStoryPeer(state: AllowanceStoryState) {
  let generation = `declared-allowance:${state}`,
    revision = '',
    runs = 2,
    minutes = 60,
    failure = 0;
  let held: 'all' | 'action' | undefined,
    scopeRepository = allowanceRepository;
  let receipt: Record<string, unknown> = {};
  const commands = new Map<string, string>();
  const releases: {signal: AbortSignal | null | undefined; release: () => void}[] = [];
  const calls: {path: string; method: string; body?: unknown}[] = [];
  const view = () => ({
    intent: allowanceIntent,
    revision,
    limits: {runs, active_minutes: minutes},
    consumption: {runs: 2, active_seconds: 148},
    remaining: {runs: runs - 2, active_seconds: minutes * 60 - 148},
    can_increase: state !== 'read-only',
  });
  const scope = `storybook:allowance:${state}`;
  if (state === 'recovered')
    retainAllowanceChange(
      {scope, repository: allowanceRepository, intent: allowanceIntent},
      {command_id: 'c'.repeat(32), expected_revision: '', limits: {runs: 3, active_minutes: 60}}
    );
  const context: VerificationAllowanceContext = {
    repository: allowanceRepository,
    intent: allowanceIntent,
    commandScope: async (signal) => {
      signal.throwIfAborted();
      return scope;
    },
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
            ? (JSON.parse(init.body) as {
                command_id: string;
                expected_revision: string;
                limits: {runs: number; active_minutes: number};
              })
            : undefined;
        calls.push({path, method, ...(body ? {body} : {})});
        if (held === 'all' || (held === 'action' && method === 'POST'))
          await new Promise<void>((release) => releases.push({release, signal: init?.signal}));
        if (state === 'loading') {
          const signal = init?.signal;
          if (!signal) throw Error('Lifetime required');
          signal.throwIfAborted();
          return new Promise<Response>((_, reject) =>
            signal.addEventListener('abort', () => reject(signal.reason), {once: true})
          );
        }
        if (failure || state === 'denied' || state === 'offline')
          return Response.json({error: 'declared'}, {status: failure || (state === 'denied' ? 403 : 503)});
        if (method === 'GET' && path === `factory/intents/${allowanceIntent}`)
          return Response.json({intent: allowanceIntent, repository: scopeRepository, state: 'accepted'});
        if (path !== `factory/intents/${allowanceIntent}/verification-allowance`)
          return new Response(null, {status: 404});
        if (method === 'GET') return Response.json(view());
        if (method === 'POST' && body) {
          if (state === 'stale') return new Response(null, {status: 409});
          const prior = commands.get(body.command_id);
          if (prior && prior !== JSON.stringify(body)) return new Response(null, {status: 409});
          if (!prior) {
            if (body.expected_revision !== revision) return new Response(null, {status: 409});
            commands.set(body.command_id, JSON.stringify(body));
            revision = body.command_id;
            runs = body.limits.runs;
            minutes = body.limits.active_minutes;
            if (state === 'unknown') throw new TypeError('Declared lost reply');
          }
          return Response.json({...view(), command_id: body.command_id, ...receipt});
        }
        return new Response(null, {status: 405});
      },
    },
  };
  return {
    context,
    calls,
    hold(action = false) {
      held = action ? 'action' : 'all';
    },
    release() {
      held = undefined;
      for (const item of releases) item.release();
    },
    aborted() {
      return releases.length > 0 && releases.every((item) => item.signal?.aborted);
    },
    retireGeneration() {
      generation += ':retired';
    },
    fail(code: number) {
      failure = code;
    },
    receipt(patch: Record<string, unknown>) {
      receipt = patch;
    },
    scope(repository: string) {
      scopeRepository = repository;
    },
    advance() {
      revision = 'e'.repeat(32);
      runs = 4;
    },
  };
}
