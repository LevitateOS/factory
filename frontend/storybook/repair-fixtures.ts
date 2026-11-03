import type {RepairChange} from '../factory/api/repair-response.js';
import type {RepairContext} from '../factory/work/repair-funding.js';
import {retainRepairChange} from '../factory/work/repair-record.js';
export type RepairStoryState =
  | 'unfunded'
  | 'active'
  | 'withdrawn'
  | 'loading'
  | 'offline'
  | 'denied'
  | 'unknown'
  | 'recovered'
  | 'stale';
export const repairRepository = '9007199254740993';
export function repairStoryPeer(state: RepairStoryState) {
  let generation = `declared-repair:${state}`,
    failure = 0,
    revision = state === 'active' || state === 'withdrawn' ? 1 : 0;
  let grant: (RepairChange & {id: string; repository: string}) | null = revision
    ? {
        id: 'a'.repeat(32),
        repository: repairRepository,
        command_id: 'a'.repeat(32),
        expected_revision: 0,
        investigator_runs: 1,
        proposal_judgments: 1,
        active_minutes: 30,
        active: state === 'active',
        effect: '',
      }
    : null;
  let hold: 'all' | 'write' | undefined,
    receipt: Record<string, unknown> = {},
    foreign = false;
  let afterWriteFailure = 0;
  const held: {signal: AbortSignal | null | undefined; release: () => void}[] = [];
  const commands = new Map<string, string>(),
    calls: {path: string; method: string; body?: unknown}[] = [];
  const scope = `storybook:repair:${state}`;
  if (state === 'recovered')
    retainRepairChange(
      {scope, repository: repairRepository},
      {
        command_id: 'b'.repeat(32),
        expected_revision: 0,
        investigator_runs: 1,
        proposal_judgments: 1,
        active_minutes: 30,
        active: true,
        effect: 'Investigate the recorded stall.',
      }
    );
  const context: RepairContext = {
    repository: repairRepository,
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
        const body = typeof init?.body === 'string' ? (JSON.parse(init.body) as RepairChange) : undefined;
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
        if (path !== `repositories/${repairRepository}/factory/repair`) return new Response(null, {status: 404});
        if (method === 'GET')
          return Response.json({
            revision,
            grant: grant && {...grant, repository: foreign ? '1' : repairRepository},
            charges: grant
              ? [{id: 'charge', kind: 'run', episode_id: 'episode', cycle: 1, active_minutes: 30, failed: true}]
              : [],
            episodes: grant
              ? [
                  {
                    episode_id: 'episode',
                    reason: 'missing_producer',
                    cycles: [{id: 'cycle', number: 1, state: 'attention', reason: 'no_change', run_id: 'run'}],
                  },
                ]
              : [],
          });
        if (method === 'PUT' && body) {
          const known = commands.get(body.command_id);
          if (
            state === 'stale' ||
            (known && known !== JSON.stringify(body)) ||
            (!known && body.expected_revision !== revision)
          )
            return new Response(null, {status: 409});
          if (!known) {
            commands.set(body.command_id, JSON.stringify(body));
            grant = {...body, id: body.command_id, repository: repairRepository};
            revision = body.expected_revision + 1;
            if (state === 'unknown') throw new TypeError('Declared lost reply');
          }
          failure = afterWriteFailure;
          return Response.json({command_id: body.command_id, revision: body.expected_revision + 1, ...receipt});
        }
        return new Response(null, {status: 405});
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
    failReadAfterWrite(code: number) {
      afterWriteFailure = code;
    },
    receipt(patch: Record<string, unknown>) {
      receipt = patch;
    },
    foreign(value: boolean) {
      foreign = value;
    },
    advance() {
      if (grant) {
        grant = {...grant, id: 'e'.repeat(32), command_id: 'e'.repeat(32), investigator_runs: 2};
        revision++;
      }
    },
  };
}
