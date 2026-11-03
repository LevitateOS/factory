import type {Issue, IssueComment} from '../factory/api/collaboration.js';
import type {IssueDetailContext} from '../factory/issues/detail.js';
import {issueListHost, issueListRepository} from './issues-fixtures.js';

export type IssueDetailStoryState =
  | 'ready'
  | 'paged'
  | 'empty'
  | 'loading'
  | 'offline'
  | 'denied'
  | 'closed'
  | 'missing'
  | 'hostile'
  | 'large';
export const detailIssueNumber = '9223372036854775806';
const observation = {
  state: 'available',
  revision: 'declared-detail-read',
  observed_at: '2026-11-03T12:00:00Z',
} as const;
export function issueDetailStoryPeer(
  state: IssueDetailStoryState = 'ready',
  repository = issueListRepository,
  number = detailIssueNumber
) {
  const actor = {id: '9007199254740995', host: issueListHost, external_id: '2', login: 'alex'};
  const issue: Issue = {
    ref: {kind: 'issue', repository, number, external_id: 'native-issue-101'},
    observation,
    title:
      repository === issueListRepository ? 'Keep context while reviewing an issue' : 'Another repository discussion',
    body: '# The desired behavior\n\nKeep **reviewed context** and *original writing*.\n\n- [x] Read the issue\n- [ ] Discuss the change\n\n> Keep the conversation together.\n\n```go\nfmt.Println("hello")\n```\n\n| State | Result |\n| --- | --- |\n| Read | Visible |\n\n[Reference](../docs/design.md) and ![Design image](../attachments/design.png)',
    state: state === 'closed' ? 'closed' : 'open',
    author: actor,
    assignees: [actor],
    labels: [{external_id: '7', name: 'design', color: 'green'}],
    created_at: '2026-11-01T09:00:00Z',
    updated_at: '2026-11-03T10:00:00Z',
    closed_at: state === 'closed' ? '2026-11-03T11:00:00Z' : null,
    capabilities: null,
  };
  const comment = (external: string, body: string): IssueComment => ({
    issue: {...issue.ref},
    comment: {
      external_id: external,
      observation,
      author: actor,
      body,
      created_at: '2026-11-03T11:00:00Z',
      updated_at: '2026-11-03T12:00:00Z',
    },
  });
  const comments = [
    comment('comment-201', 'First **discussion** note.'),
    comment('comment-202', 'Second conversation page.'),
  ];
  if (state === 'empty') {
    issue.body = '';
    comments.length = 0;
  }
  if (state === 'missing') {
    Object.assign(issue, {author: null, labels: null, assignees: null, created_at: null, updated_at: null});
    Object.assign(comments[0]!.comment, {author: null, created_at: null, updated_at: null});
  }
  if (state === 'hostile') {
    issue.body =
      '<script>window.issueExecuted=true</script>\n\n<svg onload="window.issueExecuted=true"></svg><img src="https://tracker.invalid/raw.png" onerror="window.issueExecuted=true">\n\n[Bad](javascript:alert(1)) [File](file:///etc/passwd) [Data](data:text/html,hi) [Credentials](https://u:p@example.invalid/) [Good](https://example.invalid/doc)\n\n<form><input name="location" value="bad"></form>\n\n<strong onclick="window.issueExecuted=true" style="color:red">Safe emphasis</strong>';
    comments[0]!.comment.body = issue.body;
  }
  if (state === 'large') {
    issue.body = '## Large description\n\n' + 'Original prose 🌋 '.repeat(8192);
    comments[0]!.comment.body = 'Original comment 🌋 '.repeat(4096);
  }
  const calls: {path: string; method: string}[] = [],
    signals: AbortSignal[] = [],
    gates: {path: string; release: () => void}[] = [];
  const links: string[] = [],
    opened: string[] = [],
    linkSignals: AbortSignal[] = [],
    linkGates: (() => void)[] = [];
  let held = state === 'loading',
    heldPath = '',
    code = state === 'offline' ? 503 : state === 'denied' ? 403 : 200,
    failPath = '',
    mismatch = '',
    generation = 'declared-issue-detail',
    heldLink = false,
    failedLink = false;
  const context: IssueDetailContext = {
    host: issueListHost,
    repository,
    issue: number,
    actor: actor.id,
    scope: 'declared:issue-detail:' + repository,
    resolveLink(href) {
      return new URL(href, 'https://provider.invalid/team/repo/issues/' + number).href;
    },
    async openLink(url, signal) {
      links.push(url);
      linkSignals.push(signal);
      if (heldLink) await new Promise<void>((resolve) => linkGates.push(resolve));
      signal.throwIfAborted();
      if (failedLink) throw Error('Declared link unavailable');
      opened.push(url);
    },
    transport: {
      get generation() {
        return generation;
      },
      assetBase: '/',
      async request(path, init) {
        calls.push({path, method: init?.method ?? 'GET'});
        if (init?.signal) signals.push(init.signal);
        const url = new URL(path, 'https://declared.invalid/'),
          base = '/factory/collaboration/repositories/' + repository + '/issues/' + number;
        if (init?.method !== 'GET' || ![base, base + '/comments'].includes(url.pathname))
          throw Error('Unexpected issue-detail request');
        const discussion = url.pathname.endsWith('/comments'),
          cursor = url.searchParams.get('cursor') ?? '';
        if (discussion && url.searchParams.get('limit') !== '20') throw Error('Unexpected comment window');
        const paged = state === 'paged' && !cursor,
          rows = state === 'paged' ? (cursor ? comments.slice(1) : comments.slice(0, 1)) : comments;
        const data = structuredClone(
          discussion
            ? {observation, items: rows, next: paged ? 'opaque:/comments?next=1&scope=declared' : '', complete: !paged}
            : issue
        );
        if (discussion) {
          const page = data as {items: IssueComment[]; next: string; complete: boolean};
          if (mismatch === 'comment-parent' && page.items[0]) page.items[0].issue.external_id = 'foreign-native-parent';
          if (mismatch === 'comment-kind' && page.items[0]) Object.assign(page.items[0].issue, {kind: 'pull'});
          if (mismatch === 'comment-author' && page.items[0]?.comment.author)
            page.items[0].comment.author.host = 'b'.repeat(32);
          if (mismatch === 'duplicate' && page.items[0]) page.items.push(page.items[0]);
          if (mismatch === 'cursor') {
            page.next = cursor;
            page.complete = false;
          }
        } else {
          const read = data as Issue;
          if (mismatch === 'repository') read.ref.repository = '1';
          if (mismatch === 'number') Object.assign(read.ref, {number: 12});
          if (mismatch === 'author' && read.author) read.author.host = 'b'.repeat(32);
          if (mismatch === 'parent-changed' && calls.filter((c) => !c.path.includes('/comments')).length > 1)
            read.ref.external_id = 'new-native-parent';
        }
        const status = !failPath || path.includes(failPath) ? code : 200;
        if (held || (heldPath && path.includes(heldPath)))
          await new Promise<void>((release) => gates.push({path, release}));
        return Response.json(status === 200 ? data : {error: {code: 'unavailable'}}, {status});
      },
    },
  };
  return {
    context,
    calls,
    links,
    opened,
    hold(path = '') {
      heldPath = path;
      held = !path;
    },
    release() {
      held = false;
      heldPath = '';
      gates.splice(0).forEach((gate) => gate.release());
    },
    held: () => gates.length,
    aborted: () => signals.some((signal) => signal.aborted),
    fail(status: number, path = '') {
      code = status;
      failPath = path;
    },
    mismatch(value: string) {
      mismatch = value;
    },
    retireGeneration() {
      generation += ':retired';
    },
    holdLink() {
      heldLink = true;
    },
    releaseLink() {
      heldLink = false;
      linkGates.splice(0).forEach((resolve) => resolve());
    },
    linkAborted: () => linkSignals.some((signal) => signal.aborted),
    failLink(value: boolean) {
      failedLink = value;
    },
  };
}
