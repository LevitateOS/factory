import {explorationKey} from '../factory/request/exploration-record.js';
import {readRequestDraft, retainRequestDraft} from '../factory/request/draft.js';
import type {RequestDraft} from '../factory/request/draft.js';
import {planningKey} from '../factory/request/planning-record.js';
import {generationKey} from '../factory/request/generation-record.js';
import {saveKey} from '../factory/request/save-record.js';
import {SodaRequestError} from '../factory/api/models.js';
import type {IntentRevision} from '../factory/api/request-response.js';
import type {IntentAPI} from '../factory/request/request.js';

export type RequestStoryState =
  | 'entry'
  | 'review'
  | 'loading'
  | 'denied'
  | 'offline'
  | 'stale'
  | 'draft-restored'
  | 'draft-stale'
  | 'save-uncertain'
  | 'generation-uncertain'
  | 'planning-uncertain'
  | 'exploration-uncertain';

const intent = 'd' + '1'.repeat(24);
const revision = 'd' + '2'.repeat(24);

/** Declared authored presentation only, never a model judgment. */
export function declaredExploration(basis: IntentRevision) {
  return {
    content: {
      producer: 'soda-authored-v1',
      supported: false,
      intent: basis.intent,
      revision: basis.id,
      objective: basis.objective,
      unknowns: [],
    },
    challenges: [],
    settled: [],
  };
}
/** Declared generation facts only; not a provider receipt or a native graph. */
export function declaredGeneration(revision: IntentRevision, state = 'completed') {
  const digest = 'a'.repeat(64);
  return {
    intent_id: revision.intent,
    revision_id: revision.id,
    operation_id: 'intent-' + revision.id,
    state,
    actor_id: '21',
    native_revision: 7,
    authorization_revision: revision.id + ':' + digest,
    not_after: 1700000000,
    intent_digest: digest,
    submitted_tasks: structuredClone(revision.tasks),
    payload: {creates: []},
    task_issue_ids: Object.fromEntries(revision.tasks.map((task, index) => [task.key, index + 1])),
    ...(state === 'completed' ? {receipt: {declared: true}} : {}),
  };
}
/** Declared in-memory responses only: no backend, provider, worker or login. */
export function requestStoryContext(state: RequestStoryState, profile = 'primary') {
  const actor = '9007199254740993';
  let saved: IntentRevision = {
    id: revision,
    intent,
    repository: '0',
    objective: 'Help a small team track release readiness.',
    state: 'draft',
    max_issues: 4,
    ideas: [
      {
        id: 'd' + '3'.repeat(24),
        description: 'Show which release tasks still need review.',
        consequence: 'The team can see the remaining work before release.',
        assumption: false,
        decision: 'pending',
        source: 'requested',
        provenance: 'human',
      },
    ],
    tasks: [
      {
        key: 'd' + '4'.repeat(24),
        objective: 'Show release task review state.',
        outcomes: ['Each task shows its review state.'],
        idea_ids: ['d' + '3'.repeat(24)],
      },
    ],
  };
  if (state === 'generation-uncertain' || state === 'planning-uncertain') {
    saved = {
      ...saved,
      repository: '7',
      state: state === 'generation-uncertain' ? 'accepted' : 'draft',
      ideas: saved.ideas.map((idea) => ({...idea, decision: 'accept', provenance: 'human', actor})),
    };
  }
  const draftScope = `storybook:request:${profile}:${state}`;
  const explored = {
    ...saved,
    id: 'd' + '7'.repeat(24),
    predecessor: saved.id,
    owner: actor,
    created_unix: 1,
    planning_calls: 0,
    exploration: declaredExploration(saved),
  };
  if (state === 'exploration-uncertain') {
    const address = {scope: draftScope, actor, subject: intent};
    if (window.localStorage.getItem(explorationKey(address)) === null)
      window.localStorage.setItem(
        explorationKey(address),
        JSON.stringify({
          ...address,
          pending: {
            host: 'a'.repeat(32),
            revision: saved,
            writing: {
              base: saved.id,
              repository: saved.repository,
              objective: saved.objective,
              department: '',
              maxIssues: saved.max_issues,
              changeText: '',
              ideas: saved.ideas.map(({id, decision}) => ({id, decision})),
              shelf: [],
              shelfDeps: {},
            },
          },
        })
      );
  }
  if (state === 'planning-uncertain') {
    const address = {scope: draftScope, actor, subject: intent};
    if (window.localStorage.getItem(planningKey(address)) === null)
      window.localStorage.setItem(
        planningKey(address),
        JSON.stringify({
          ...address,
          pending: {
            host: 'a'.repeat(32),
            revision: saved,
            operation: 'propose_intent',
            assignment: '',
            run: '',
            launch: 'unknown',
            writing: {
              base: saved.id,
              repository: saved.repository,
              objective: saved.objective,
              department: '',
              maxIssues: saved.max_issues,
              changeText: '',
              ideas: saved.ideas.map(({id, decision}) => ({id, decision})),
              shelf: [],
              shelfDeps: {},
            },
          },
        })
      );
  }
  if (state === 'generation-uncertain') {
    const address = {scope: draftScope, actor, subject: intent};
    if (window.localStorage.getItem(generationKey(address)) === null)
      window.localStorage.setItem(
        generationKey(address),
        JSON.stringify({
          ...address,
          pending: {
            host: 'a'.repeat(32),
            revision: saved,
            operation: 'intent-' + saved.id,
            writing: {
              base: saved.id,
              repository: saved.repository,
              objective: saved.objective,
              department: '',
              maxIssues: saved.max_issues,
              changeText: '',
              ideas: saved.ideas.map(({id, decision}) => ({id, decision})),
              shelf: [],
              shelfDeps: {},
            },
          },
        })
      );
  }
  if (state === 'save-uncertain') {
    const address = {scope: draftScope, actor, subject: intent};
    const before: RequestDraft = {
      base: saved.id,
      repository: saved.repository,
      objective: saved.objective,
      department: '',
      maxIssues: saved.max_issues,
      changeText: '',
      ideas: saved.ideas.map(({id, decision}) => ({id, decision})),
      shelf: [],
      shelfDeps: {},
    };
    if (window.localStorage.getItem(saveKey(address)) === null)
      window.localStorage.setItem(
        saveKey(address),
        JSON.stringify({
          ...address,
          pending: {
            host: 'a'.repeat(32),
            revision: {...saved, id: 'd' + '7'.repeat(24), predecessor: saved.id},
            before,
            after: {...before, base: 'd' + '7'.repeat(24)},
          },
        })
      );
  }
  if (state === 'draft-restored' || state === 'draft-stale') {
    const address = {scope: draftScope, actor, subject: intent};
    if (readRequestDraft(address).raw === '')
      retainRequestDraft(
        address,
        {
          base: state === 'draft-stale' ? 'd' + '9'.repeat(24) : revision,
          repository: '0',
          objective: 'Keep local release-review writing.',
          department: '',
          maxIssues: 5,
          changeText: 'Keep the human review notes.',
          ideas: saved.ideas.map(({id, decision}) => ({id, decision})),
          approach: 'incremental',
          shelf: [],
          shelfDeps: {},
        },
        ''
      );
  }
  const api: IntentAPI = async (path, body, signal) => {
    signal?.throwIfAborted();
    if (path === '/api/factory/identity' && !body) return {host: 'a'.repeat(32), actor};
    if (path === '/api/factory/departments' && !body) return [];
    if (
      state === 'planning-uncertain' &&
      path === `/api/factory/intents/${saved.intent}/revisions/${saved.id}/plan` &&
      !body
    )
      return {
        intent: saved.intent,
        revision: saved.id,
        operation: 'propose_intent',
        assignment_id: 'a'.repeat(32),
        run_id: 'b'.repeat(32),
        state: 'pending',
        candidates: 0,
      };
    if (state === 'generation-uncertain' && (path.endsWith('/generation') || path.endsWith('/generate')))
      return declaredGeneration(saved);
    if (state === 'exploration-uncertain') {
      if (path === `/api/factory/intents/${saved.intent}/revisions/${saved.id}/exploration` && !body)
        return {intent: saved.intent, basis_revision: saved.id, state: 'recorded', result: explored};
      if (path === `/api/factory/intents/${saved.intent}` && !body) return explored;
      if (path === `/api/factory/intents/${saved.intent}/review` && !body)
        return {intent: saved.intent, head_revision: explored.id, status: 'missing'};
    }
    if (state === 'loading') {
      if (!signal) throw Error('A request lifetime is required.');
      return new Promise((_, reject) => {
        signal.addEventListener('abort', () => reject(signal.reason), {once: true});
      });
    }
    if (state === 'denied') throw new SodaRequestError(403, 'repository_access_denied');
    if (state === 'offline') throw Error('Request unavailable.');
    if (path === '/api/factory/intents' && body) {
      if (state === 'stale') throw new SodaRequestError(412, 'stale_revision');
      saved = structuredClone(body) as unknown as IntentRevision;
      return {...structuredClone(saved), owner: actor, created_unix: 1, planning_calls: 0};
    }
    if (path === `/api/factory/intents/${saved.intent}` && !body) return structuredClone(saved);
    if (path === `/api/factory/intents/${saved.intent}/review` && !body)
      return {intent: saved.intent, head_revision: saved.id, status: 'missing'};
    if (path === `/api/factory/intents/${saved.intent}/plan` && !body)
      return {revision: saved.id, state: 'none', candidates: 0};
    // Unsupported fixture actions remain visibly unavailable, never empty success.
    throw new SodaRequestError(503, 'planning_unavailable');
  };
  return {
    api,
    actor,
    draftScope,
    request: state === 'entry' ? '' : intent,
    active: true,
    blocked: false,
  };
}
