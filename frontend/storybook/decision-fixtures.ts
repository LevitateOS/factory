import type {FactoryTransport} from '../factory/api/transport.js';
import type {FactoryAcceptanceLinks} from '../factory/app/navigation.js';
import type {RequirementsAcceptanceContext} from '../factory/work/acceptance.js';
import type {RetryContext} from '../factory/work/completion-retry.js';
import {retainCompletionRetry} from '../factory/work/completion-retry-record.js';
import {retainAcceptance} from '../factory/work/acceptance-record.js';

export type DecisionStoryState = 'ready' | 'denied' | 'offline' | 'loading' | 'unknown' | 'stale' | 'recovered';
export const decisionRepository = '9007199254740993';
export const decisionIssue = '12';
const base = `repositories/${decisionRepository}/factory`;

export function decisionLinks(prefix = '/desktop'): FactoryAcceptanceLinks {
  return {
    issue: (issue) => `${prefix}/issue/${issue}`,
    comment: (issue, comment) => `${prefix}/issue/${issue}/discussion/${comment}`,
  };
}

/** Declared read/command peer: no account, backend, provider, model or worker. */
export function decisionStoryPeer(state: DecisionStoryState) {
  let generation = `declared-decisions:${state}`;
  let held = false;
  let advanced = false;
  let omitGates = false;
  let receiptPatch: Record<string, unknown> = {};
  let postStatus = 201;
  let fullReview = false;
  const releases: (() => void)[] = [];
  let lastSignal: AbortSignal | null | undefined;
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
      lastSignal = init?.signal;
      if (held) {
        // Deliberately ignore abort: the production element must refuse a late result too.
        await new Promise<void>((resolve) => {
          releases.push(resolve);
        });
      }
      if (state === 'loading') {
        const signal = init?.signal;
        if (!signal) throw Error('A lifetime signal is required.');
        signal.throwIfAborted();
        return new Promise<Response>((_, reject) => {
          signal.addEventListener('abort', () => reject(signal.reason), {once: true});
        });
      }
      if (state === 'offline') throw new TypeError('Declared offline peer.');
      if (method === 'GET' && path === `${base}/access`) {
        return Response.json({
          repository: decisionRepository,
          viewer: {code_write: state !== 'denied', session_current: state !== 'denied'},
        });
      }
      if (method === 'GET' && path === `${base}/issues/${decisionIssue}`) {
        return Response.json({
          repository: decisionRepository,
          issue: decisionIssue,
          status: {valid: true, ...(fullReview ? {acceptance: {id: 'd' + '1'.repeat(24)}} : {})},
          journey: {
            repository: decisionRepository,
            issue: decisionIssue,
            gates: omitGates
              ? []
              : [
                  {
                    repository: decisionRepository,
                    completion_id: (advanced ? 'e' : 'c').repeat(32),
                    cycle_id: (advanced ? 'f' : 'b').repeat(32),
                    state: 'attention',
                    retryable: true,
                    generation: 1,
                  },
                ],
          },
        });
      }
      if (fullReview && method === 'GET' && path === `${base}/issues/13`)
        return Response.json({
          repository: decisionRepository,
          issue: '13',
          status: {valid: true, acceptance: {id: 'd' + '2'.repeat(24)}},
        });
      if (method === 'GET' && path.split('?')[0] === `${base}/issues/${decisionIssue}/acceptance-preview`) {
        const selected = new URLSearchParams(path.split('?')[1]).getAll('comment_id');
        return Response.json({
          repository: decisionRepository,
          issue: decisionIssue,
          title_digest: 'a'.repeat(64),
          title_occurrence: 'original',
          body_digest: 'b'.repeat(64),
          content_version: 3,
          native_revision: 41,
          comments: selected.map((id) => ({id, digest: 'c'.repeat(64), content_version: 2})),
          dependencies: fullReview ? [{occurrence: '23', depends_on: '90'}] : [],
        });
      }
      if (method === 'POST' && (path === `${base}/issues/${decisionIssue}/acceptances` || path === `${base}/actions`)) {
        if (state === 'denied')
          return Response.json({error: 'forbidden', message: 'Current authority is required.'}, {status: 403});
        if (state === 'unknown' && calls.filter((call) => call.method === 'POST').length === 1)
          throw new TypeError('The declared command reply was lost.');
        if (state === 'stale')
          return Response.json(
            {error: 'objective_changed', message: 'Requirements changed. Review the current evidence.'},
            {status: 409}
          );
        if (path === `${base}/actions`) {
          const command = body as {command_id: string; completion: {event_id: string; expected_prior_cycle: string}};
          return Response.json(
            {
              command_id: command.command_id,
              event_id: command.completion.event_id,
              prior_cycle: command.completion.expected_prior_cycle,
              successor_cycle: command.command_id,
              repository: decisionRepository,
              queued: true,
              ...receiptPatch,
            },
            {status: postStatus}
          );
        }
        const decision = body as {decision_id: string};
        return Response.json(
          {decision_id: decision.decision_id, head: decision.decision_id, depth: 1, ...receiptPatch},
          {status: postStatus}
        );
      }
      return Response.json({error: 'not_found'}, {status: 404});
    },
  };
  const scope = `declared-decisions:storybook:${state}`;
  const commandScope = async (signal: AbortSignal) => {
    signal.throwIfAborted();
    return scope;
  };
  const acceptance: RequirementsAcceptanceContext = {
    transport,
    repository: decisionRepository,
    issue: decisionIssue,
    links: decisionLinks(),
    commandScope,
  };
  const retry: RetryContext = {
    transport,
    repository: decisionRepository,
    issue: decisionIssue,
    commandScope,
  };
  if (state === 'recovered') {
    retainCompletionRetry(
      {scope, repository: decisionRepository, issue: decisionIssue},
      {command: 'd'.repeat(32), event: 'c'.repeat(32), cycle: 'b'.repeat(32)}
    );
    advanced = true;
    omitGates = true;
    retainAcceptance(
      {scope, repository: decisionRepository, issue: decisionIssue},
      {
        decision_id: 'd' + '3'.repeat(24),
        predecessor: 'd' + '1'.repeat(24),
        title_digest: 'a'.repeat(64),
        title_occurrence: 'original',
        content_digest: 'b'.repeat(64),
        content_version: 3,
        native_revision: 41,
        sources: [{id: '21', digest: 'c'.repeat(64), content_version: 2}],
        resolutions: [],
        prerequisites: [],
      }
    );
  }
  return {
    acceptance,
    retry,
    calls,
    fullReview() {
      fullReview = true;
    },
    advanceGate(omit = false) {
      advanced = true;
      omitGates = omit;
    },
    receipt(patch: Record<string, unknown>, status = 201) {
      receiptPatch = patch;
      postStatus = status;
    },
    hold() {
      held = true;
    },
    release() {
      held = false;
      for (const release of releases.splice(0)) release();
    },
    aborted() {
      return lastSignal?.aborted ?? false;
    },
    retireGeneration() {
      generation += ':retired';
    },
  };
}
