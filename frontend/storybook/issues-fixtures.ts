import type {Issue, IssueRef} from '../factory/api/collaboration.js';
import type {IssueListContext} from '../factory/issues/issues.js';

export type IssueListStoryState =
  | 'ready'
  | 'empty'
  | 'empty-filtered'
  | 'loading'
  | 'offline'
  | 'denied'
  | 'paged'
  | 'missing'
  | 'plain-text';
export const issueListRepository = '9007199254740993';
export const issueListHost = 'a'.repeat(32);
const observed = {state: 'available', revision: 'declared-issue-read', observed_at: '2026-11-03T10:00:00Z'} as const;

/** Declared read peer shared by stories/browser journeys. It is not a provider
 * integration or a source of mutation/automation authority. */
export function issuesStoryPeer(state: IssueListStoryState = 'ready', repository = issueListRepository) {
  const actor = {id: '9007199254740995', host: issueListHost, external_id: '2', login: 'alex'};
  const calls: {path: string; method: string}[] = [],
    signals: AbortSignal[] = [],
    gates: (() => void)[] = [];
  const opened: IssueRef[] = [],
    navigated: IssueRef[] = [],
    openSignals: AbortSignal[] = [],
    openGates: (() => void)[] = [];
  let held = state === 'loading',
    heldOpen = false,
    failedOpen = false,
    mismatch = '',
    generation = 'declared-issue-list';
  let status = state === 'offline' ? 503 : state === 'denied' ? 403 : 200;
  const first: Issue = {
    ref: {kind: 'issue', repository, external_id: '101', number: '9223372036854775806'},
    observation: observed,
    title: repository === issueListRepository ? 'Keep issue context across views' : 'Another repository issue',
    body: 'This description belongs to the later detail reader.',
    state: 'open',
    author: actor,
    assignees: [actor],
    labels: [{external_id: '7', name: 'design', color: '22aa66'}],
    created_at: '2026-11-01T09:00:00Z',
    updated_at: '2026-11-03T09:00:00Z',
    closed_at: null,
    capabilities: null,
  };
  const items: Issue[] = [
    first,
    {...first, ref: {...first.ref, number: '12', external_id: '102'}, title: 'Second open issue'},
    {...first, ref: {...first.ref, number: '11', external_id: '103'}, title: 'Closed issue', state: 'closed'},
    {...first, ref: {...first.ref, number: '10', external_id: '104'}, title: 'Unknown lifecycle', state: 'unknown'},
  ];
  if (state === 'missing')
    Object.assign(first, {author: null, assignees: null, labels: null, created_at: null, updated_at: null});
  if (state === 'plain-text') {
    first.title = '<img src=x onerror=alert(1)> literal issue title';
    first.labels = [{external_id: '7', name: '<script>alert(1)</script>', color: 'url(javascript:alert(1))'}];
  }
  const context: IssueListContext = {
    host: issueListHost,
    repository,
    actor: actor.id,
    scope: 'declared:issues:' + repository,
    transport: {
      get generation() {
        return generation;
      },
      assetBase: '/',
      async request(path, init) {
        const url = new URL(path, 'https://declared.invalid/');
        calls.push({path, method: init?.method ?? 'GET'});
        if (init?.signal) signals.push(init.signal);
        if (
          init?.method !== 'GET' ||
          url.pathname !== '/factory/collaboration/repositories/' + repository + '/issues' ||
          url.searchParams.get('limit') !== '20'
        )
          throw Error('Unexpected issue-list request');
        const selected = url.searchParams.get('state'),
          cursor = url.searchParams.get('cursor') ?? '';
        let rows = state === 'empty' ? [] : items.filter((item) => selected === 'all' || item.state === selected);
        let next = '';
        if (state === 'empty-filtered' && !cursor) {
          rows = [];
          next = 'filtered-next';
        }
        if (state === 'paged' && rows.length > 1) {
          rows = cursor ? rows.slice(1) : rows.slice(0, 1);
          if (!cursor) next = 'opaque:/next?scope=declared&v=1';
        }
        const snapshot = structuredClone({observation: observed, items: rows, next, complete: !next});
        if (mismatch === 'repository' && snapshot.items[0]) snapshot.items[0].ref.repository = '1';
        if (mismatch === 'actor-host' && snapshot.items[0]?.author) snapshot.items[0].author.host = 'b'.repeat(32);
        if (mismatch === 'numeric-id' && snapshot.items[0]) Object.assign(snapshot.items[0].ref, {number: 12});
        if (mismatch === 'duplicate' && snapshot.items[0]) snapshot.items.push(snapshot.items[0]);
        if (mismatch === 'filter' && snapshot.items[0]) snapshot.items[0].state = 'closed';
        if (mismatch === 'cursor') Object.assign(snapshot, {next: cursor, complete: false});
        if (mismatch === 'observation') Object.assign(snapshot.observation, {state: 'denied'});
        const code = status;
        // Intentionally ignore abort to qualify consumer retirement of late data.
        if (held) await new Promise<void>((resolve) => gates.push(resolve));
        return Response.json(code === 200 ? snapshot : {error: {code: 'issues_unavailable'}}, {status: code});
      },
    },
    async openIssue(issue, signal) {
      opened.push(issue);
      openSignals.push(signal);
      if (heldOpen) await new Promise<void>((resolve) => openGates.push(resolve));
      signal.throwIfAborted();
      if (failedOpen) throw Error('Declared host navigation unavailable');
      navigated.push(issue);
    },
  };
  return {
    context,
    calls,
    opened,
    navigated,
    hold() {
      held = true;
    },
    release() {
      held = false;
      gates.splice(0).forEach((resolve) => resolve());
    },
    held: () => gates.length,
    aborted: () => signals.some((signal) => signal.aborted),
    setStatus(value: number) {
      status = value;
    },
    mismatch(value: string) {
      mismatch = value;
    },
    retireGeneration() {
      generation += ':retired';
    },
    holdOpen() {
      heldOpen = true;
    },
    releaseOpen() {
      heldOpen = false;
      openGates.splice(0).forEach((resolve) => resolve());
    },
    openAborted: () => openSignals.some((signal) => signal.aborted),
    failOpen(value: boolean) {
      failedOpen = value;
    },
  };
}
