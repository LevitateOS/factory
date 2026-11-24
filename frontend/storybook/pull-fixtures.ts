import type {PullListContext} from '../factory/pulls/list.js';
import type {PullDetailContext, PullTab} from '../factory/pulls/detail.js';
import {issueListHost, issueListRepository} from './issues-fixtures.js';

export type PullListStoryState = 'ready' | 'empty' | 'loading' | 'offline' | 'denied' | 'paged';
export type PullDetailStoryState = 'ready' | 'loading' | 'offline' | 'denied';

/** Declared pull request read peers shared by stories. They are not provider integrations. */
const observation = {state: 'available', revision: 'declared-pull-read', observed_at: '2026-11-03T10:00:00Z'};
const repositoryRef = {id: issueListRepository, host: issueListHost, external_id: 'declared-team-project', name: 'team/project'};
const alex = {id: '9007199254740995', host: issueListHost, external_id: '2', login: 'alex'};
const sam = {id: '9007199254740996', host: issueListHost, external_id: '3', login: 'sam'};
const commitSha = (byte: number) => byte.toString(16).padStart(2, '0').repeat(20);
const baseCommit = commitSha(0x0f);
const headCommit = commitSha(0xa1);
const pullRef = (number: string) => ({
  kind: 'pull',
  repository: issueListRepository,
  external_id: 'declared-pull-' + number,
  number,
});
const branch = (name: string, commit: string) => ({repository: repositoryRef, name, commit});
const page = <T>(items: T[], next = '') => ({observation, items, next, complete: next === ''});

function pullRecord(number: string, title: string, head: string, draft = false) {
  return {
    ref: pullRef(number),
    observation,
    title,
    body: 'Adds an index for library items so search can find them without a full scan.\n\n- [x] Unit tests\n- [ ] Documentation',
    state: 'open',
    draft,
    author: alex,
    base: branch('main', baseCommit),
    head: branch(head, headCommit),
    created_at: '2026-11-01T09:00:00Z',
    updated_at: '2026-11-03T09:00:00Z',
    merged_at: null,
    capabilities: null,
  };
}
const pullRows = [
  pullRecord('42', 'Index library items for search', 'index-library-search'),
  pullRecord('41', 'Draft: sketch search filters', 'search-filters', true),
  pullRecord('40', 'Fix tag ordering in library list', 'fix-tag-order'),
];

/** Pull list peer: the same read the list component makes, served from fixtures. */
export function pullListStoryPeer(state: PullListStoryState = 'ready') {
  const calls: {path: string; method: string}[] = [];
  const opened: {number: string}[] = [];
  const status = state === 'offline' ? 503 : state === 'denied' ? 403 : 200;
  const listPath = `/factory/collaboration/repositories/${issueListRepository}/pulls`;
  const context: PullListContext = {
    host: issueListHost,
    repository: issueListRepository,
    actor: alex.id,
    scope: 'declared:pulls:' + issueListRepository,
    transport: {
      get generation() {
        return 'declared-pull-list';
      },
      assetBase: '/',
      async request(path, init) {
        const url = new URL(path, 'https://declared.invalid/');
        calls.push({path, method: init?.method ?? 'GET'});
        if (init?.method !== 'GET' || url.pathname !== listPath || url.searchParams.get('limit') !== '20')
          throw Error('Unexpected pull-list request');
        if (state === 'loading') await new Promise<void>(() => undefined);
        if (status !== 200) return Response.json({error: {code: 'pulls_unavailable'}}, {status});
        const cursor = url.searchParams.get('cursor') ?? '';
        const rows = state === 'empty' ? [] : pullRows;
        const items = state === 'paged' ? (cursor ? rows.slice(1) : rows.slice(0, 1)) : rows;
        const next = state === 'paged' && !cursor ? 'opaque:/pulls?cursor=page-2&scope=declared' : '';
        return Response.json(page(items, next));
      },
    },
    openPull(pull) {
      opened.push({number: pull.number});
    },
  };
  return {context, calls, opened};
}

const indexPatch =
  'diff --git a/src/search/index.ts b/src/search/index.ts\n--- a/src/search/index.ts\n+++ b/src/search/index.ts\n@@ -10,6 +10,7 @@ export function indexItems(items: Item[]) {\n   const terms = [];\n-  for (const item of items) {\n+  for (const item of items.filter(isPublished)) {\n     terms.push(item.title);\n+    terms.push(...item.tags);\n   }\n   return terms;\n }\n';
const docsPatch =
  'diff --git a/docs/search.md b/docs/search.md\nnew file mode 100644\n--- /dev/null\n+++ b/docs/search.md\n@@ -0,0 +1,3 @@\n+# Search index\n+\n+Library items are indexed when they are published.\n';
const diff = {pull: pullRef('42'), base: baseCommit, head: headCommit};
const file = (path: string, change: string, patch: string, additions: number, deletions: number) => ({
  diff,
  old_path: path,
  new_path: path,
  change,
  patch_state: 'complete',
  patch,
  additions,
  deletions,
});
const lineAnchor = {diff, old_path: 'src/search/index.ts', new_path: 'src/search/index.ts', side: 'new', scope: 'lines', lines: {start: 12, end: 13}};
const comment = (external: string, author: typeof alex, body: string) => ({
  anchor: null,
  comment: {external_id: external, observation, author, body, created_at: '2026-11-03T11:00:00Z', updated_at: '2026-11-03T11:00:00Z'},
});
const statusRow = (name: string, conclusion: string, required: boolean, description: string) => ({
  observation,
  repository: issueListRepository,
  commit: headCommit,
  kind: 'status',
  name,
  state: 'completed',
  conclusion,
  required,
  description,
  job: null,
});

/** Pull detail peer: the PR read plus one fixture response per tab. */
export function pullDetailStoryPeer(tab: PullTab = 'conversation', state: PullDetailStoryState = 'ready') {
  const calls: {path: string; method: string}[] = [];
  const links: string[] = [];
  const status = state === 'offline' ? 503 : state === 'denied' ? 403 : 200;
  const base = `/factory/collaboration/repositories/${issueListRepository}/pulls/42`;
  const pull = pullRecord('42', 'Index library items for search', 'index-library-search');
  const routes: Record<string, unknown> = {
    [base]: pull,
    [base + '/comments']: page([
      {pull: pullRef('42'), comment: {external_id: 'comment-201', observation, author: alex, body: 'Looks good **once** the index migration is split out.', created_at: '2026-11-03T11:00:00Z', updated_at: '2026-11-03T11:00:00Z'}},
      {pull: pullRef('42'), comment: {external_id: 'comment-202', observation, author: sam, body: 'Agreed, I will split it.', created_at: '2026-11-03T11:30:00Z', updated_at: '2026-11-03T11:30:00Z'}},
    ]),
    [base + '/commits']: {
      pull: pullRef('42'),
      head: headCommit,
      commits: page([
        {commit: headCommit, summary: 'Index library items for search', author: 'alex', authored_at: '2026-11-03T08:00:00Z'},
        {commit: commitSha(0xb2), summary: 'Add tag ordering to the index', author: 'alex', authored_at: '2026-11-02T16:00:00Z'},
        {commit: commitSha(0xc3), summary: 'Document the search fields', author: 'sam', authored_at: '2026-11-02T10:00:00Z'},
      ]),
    },
    [base + '/reviews']: page([
      {external_id: 'review-301', pull: pullRef('42'), observation, state: 'approved', author: sam, body: 'Approved. The index is in place.', stale: false, official: true, submitted_at: '2026-11-03T12:00:00Z'},
      {external_id: 'review-302', pull: pullRef('42'), observation, state: 'commented', author: alex, body: 'Please confirm the tag order.', stale: true, official: false, submitted_at: '2026-11-02T18:00:00Z'},
    ]),
    [base + '/threads']: page([
      {
        external_id: 'thread-501',
        pull: pullRef('42'),
        observation,
        anchor: lineAnchor,
        current_anchor: lineAnchor,
        recorded_location: {commit: headCommit, path: 'src/search/index.ts', side: 'new', line: 12},
        outdated: false,
        resolution: 'unresolved',
        comments: page([comment('comment-501', alex, 'Should unpublished items be skipped here?')]),
      },
      {
        external_id: 'thread-502',
        pull: pullRef('42'),
        observation,
        anchor: {...lineAnchor, lines: {start: 4, end: 4}},
        current_anchor: null,
        recorded_location: {commit: baseCommit, path: 'src/search/index.ts', side: 'old', line: 4},
        outdated: true,
        resolution: 'resolved',
        comments: page([comment('comment-502', sam, 'Resolved after the tag fix.')]),
      },
    ]),
    [base + '/files']: {
      diff: {pull: pullRef('42'), base: baseCommit, head: headCommit},
      files: page([file('src/search/index.ts', 'modified', indexPatch, 2, 1), file('docs/search.md', 'added', docsPatch, 3, 0)]),
    },
    [base + '/checks']: {
      pull: pullRef('42'),
      commit: headCommit,
      kind: 'status',
      status_gate: 'success',
      evaluation_commit: headCommit,
      protection: {repository: issueListRepository, requirements_complete: true, classic_visible: true, rules_visible: true, unknown_rule_kinds: []},
      checks: page([
        statusRow('ci/build', 'success', true, 'Build passed'),
        statusRow('ci/lint', 'neutral', false, 'Lint skipped for docs-only changes'),
      ]),
    },
  };
  const context: PullDetailContext = {
    host: issueListHost,
    repository: issueListRepository,
    pull: '42',
    actor: alex.id,
    scope: 'declared:pull-detail:' + issueListRepository,
    initialTab: tab,
    resolveLink(href) {
      return new URL(href, 'https://provider.invalid/team/project/pull/42').href;
    },
    openLink(url) {
      links.push(url);
    },
    transport: {
      get generation() {
        return 'declared-pull-detail';
      },
      assetBase: '/',
      async request(path, init) {
        const url = new URL(path, 'https://declared.invalid/');
        calls.push({path, method: init?.method ?? 'GET'});
        if (init?.method !== 'GET' || !(url.pathname in routes)) throw Error('Unexpected pull-detail request');
        if (state === 'loading') await new Promise<void>(() => undefined);
        if (status !== 200) return Response.json({error: {code: 'unavailable'}}, {status});
        return Response.json(routes[url.pathname]);
      },
    },
  };
  return {context, calls, links};
}
