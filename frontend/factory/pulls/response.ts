import {check, id, object} from '../api/models.js';
import type {PullRequest, PullRef} from '../api/collaboration.js';
import type {ChangedFile, ReviewAnchor, ReviewLocation} from '../api/collaboration.js';
import {parseChangedFile, parseReviewAnchor} from '../api/collaboration-response.js';

export type PullItem = Pick<
  PullRequest,
  'ref' | 'title' | 'body' | 'state' | 'draft' | 'author' | 'base' | 'head' | 'created_at' | 'updated_at' | 'merged_at'
>;
export type PullPage<T> = {items: T[]; next: string; complete: boolean};

const text = (value: unknown, max: number): string => {
  check(typeof value === 'string' && value.length <= max && !value.includes('\0'));
  return value;
};
const date = (value: unknown): string | null => {
  if (value === null) return null;
  const result = text(value, 64);
  check(/^\d{4}-\d\d-\d\dT/.test(result) && Number.isFinite(Date.parse(result)));
  return result;
};
const commit = (value: unknown): string | null => {
  if (value === null) return null;
  const result = text(value, 64);
  check(/^(?:[a-f0-9]{40}|[a-f0-9]{64})$/i.test(result));
  return result;
};
const available = (value: unknown): void => {
  const observation = object(value);
  check(observation.state === 'available' && text(observation.revision, 4096).trim());
};
export const pullRef = (value: unknown, repository: string, number?: string): PullRef => {
  const ref = object(value);
  check(ref.kind === 'pull' && ref.repository === repository && id(ref.number));
  if (number) check(ref.number === number);
  const external = text(ref.external_id, 512);
  check(external.trim());
  return {kind: 'pull', repository, number: ref.number, external_id: external};
};
function branch(value: unknown, host: string) {
  const ref = object(value);
  const repository = ref.repository === null ? null : object(ref.repository);
  if (repository) check(id(repository.id) && repository.host === host);
  return {
    repository: repository
      ? {id: repository.id as string, host, external_id: text(repository.external_id, 512), name: text(repository.name, 512)}
      : null,
    name: text(ref.name, 4096),
    commit: commit(ref.commit),
  };
}
export function pullItem(value: unknown, host: string, repository: string, number?: string): PullItem {
  const item = object(value);
  available(item.observation);
  const state = item.state;
  check(state === 'open' || state === 'closed' || state === 'merged' || state === 'unknown');
  check(item.draft === null || typeof item.draft === 'boolean');
  const author = item.author === null ? null : object(item.author);
  if (author) check(id(author.id) && author.host === host);
  return {
    ref: pullRef(item.ref, repository, number),
    title: text(item.title, 65536),
    body: text(item.body, 4 * 1024 * 1024),
    state,
    draft: item.draft,
    author: author
      ? {id: author.id as string, host, external_id: text(author.external_id, 512), login: text(author.login, 256)}
      : null,
    base: branch(item.base, host),
    head: branch(item.head, host),
    created_at: date(item.created_at),
    updated_at: date(item.updated_at),
    merged_at: date(item.merged_at),
  };
}
export function pullPage<T>(value: unknown, max: number, parse: (item: unknown) => T, cursor = ''): PullPage<T> {
  const page = object(value);
  available(page.observation);
  check(Array.isArray(page.items) && page.items.length <= max && typeof page.complete === 'boolean');
  const next = text(page.next, 4096);
  check(page.complete === (next === '') && (next === '' || next !== cursor));
  return {items: page.items.map(parse), next, complete: page.complete};
}
export function pullList(value: unknown, host: string, repository: string, state: string, cursor = '') {
  const seen = new Set<string>();
  return pullPage(value, 20, (item) => {
    const pull = pullItem(item, host, repository);
    check((state === 'all' || (state === 'closed' ? pull.state !== 'open' : pull.state === state)) && !seen.has(pull.ref.number));
    seen.add(pull.ref.number);
    return pull;
  }, cursor);
}

export function pullFiles(value: unknown, repository: string, number: string, base = '', head = '', cursor = '') {
  const result = object(value), diff = object(result.diff);
  pullRef(diff.pull, repository, number);
  const actualBase = commit(diff.base), actualHead = commit(diff.head);
  check(actualBase && actualHead && (!base || (actualBase === base && actualHead === head)));
  const page = pullPage(result.files, 10, (item): ChangedFile => {
    const file = parseChangedFile(item);
    pullRef(file.diff.pull, repository, number);
    check(file.diff.base === actualBase && file.diff.head === actualHead);
    return file;
  }, cursor);
  return {diff: {base: actualBase, head: actualHead}, ...page};
}

export function pullChecks(value: unknown, repository: string, number: string, head: string, kind: 'status' | 'job', cursor = '') {
  const result = object(value);
  pullRef(result.pull, repository, number);
  check(result.commit === head && result.kind === kind);
  const gate = result.status_gate;
  check(['not_required', 'pending', 'success', 'failure', 'error', 'warning', 'unknown'].includes(String(gate)));
  const page = pullPage(result.checks, 20, (value) => {
    const row = object(value);
    available(row.observation);
    check(row.repository === repository && row.commit === head && row.kind === kind);
    check(row.required === null || typeof row.required === 'boolean');
    const state = row.state, conclusion = row.conclusion;
    check(['pending', 'running', 'completed', 'unknown'].includes(String(state)));
    check(['success', 'failure', 'neutral', 'skipped', 'cancelled', 'timed_out', 'action_required', 'stale', 'unknown'].includes(String(conclusion)));
    const job = row.job === null ? null : object(row.job);
    return {name: text(row.name, 1024), state: state as string, conclusion: conclusion as string,
      required: row.required as boolean | null, description: text(row.description, 4096),
      workflow: job ? text(job.workflow, 1024) : null};
  }, cursor);
  return {gate: gate as string, ...page};
}

export function pullReviews(value: unknown, repository: string, number: string, cursor = '') {
  return pullPage(value, 20, value => pullReview(value, repository, number), cursor);
}

export function pullReview(value: unknown, repository: string, number: string) {
  const review = object(value), parent = pullRef(review.pull, repository, number);
  available(review.observation);
  const state = review.state;
  check(['pending', 'approved', 'changes_requested', 'commented', 'dismissed', 'unknown'].includes(String(state)));
  const author = review.author === null ? null : object(review.author);
  check(!author || id(author.id));
  return {externalID: text(review.external_id, 512), pull: parent, state: state as string,
    authorID: author ? author.id as string : null,
    author: author ? text(author.login, 256) : 'Unknown author', body: text(review.body, 4 * 1024 * 1024),
    stale: review.stale === true, official: review.official === true, submittedAt: date(review.submitted_at)};
}

export interface PullThreadView {
  externalID: string;
  anchor: ReviewAnchor | null;
  currentAnchor: ReviewAnchor | null;
  recordedLocation: ReviewLocation | null;
  outdated: boolean | null;
  resolution: string;
  comments: readonly PullThreadComment[];
  commentsNext: string;
}
export interface PullThreadComment {author: string; body: string; createdAt: string | null}
function threadComment(value: unknown, repository: string, number: string): PullThreadComment {
  const entry = object(value), comment = object(entry.comment);
  available(comment.observation);
  if (entry.anchor !== null) pullRef(parseReviewAnchor(entry.anchor).diff.pull, repository, number);
  const author = comment.author === null ? null : object(comment.author);
  return {author: author ? text(author.login, 256) : 'Unknown author', body: text(comment.body, 4 * 1024 * 1024), createdAt: date(comment.created_at)};
}
export function pullThreadComments(value: unknown, repository: string, number: string, cursor = '') {
  return pullPage(value, 20, item => threadComment(item, repository, number), cursor);
}
export function pullThreads(value: unknown, repository: string, number: string, cursor = '') {
  return pullPage(value, 20, (value): PullThreadView => {
    const thread = object(value), comments = object(thread.comments);
    pullRef(thread.pull, repository, number);
    available(thread.observation);
    check(thread.outdated === null || typeof thread.outdated === 'boolean');
    check(['resolved', 'unresolved', 'unknown'].includes(String(thread.resolution)));
    const anchor = thread.anchor === null ? null : parseReviewAnchor(thread.anchor);
    const currentAnchor = thread.current_anchor === null ? null : parseReviewAnchor(thread.current_anchor);
    const location = thread.recorded_location === null ? null : object(thread.recorded_location);
    if (location) check((location.side === 'old' || location.side === 'new') && Number.isSafeInteger(location.line) && (location.line as number) > 0);
    const recordedLocation: ReviewLocation | null = location ? {
      commit: commit(location.commit), path: text(location.path, 4096),
      side: location.side as 'old' | 'new', line: location.line as number,
    } : null;
    for (const a of [anchor, currentAnchor]) if (a) pullRef(a.diff.pull, repository, number);
    const page = pullPage(comments, 10, item => threadComment(item, repository, number));
    return {externalID: text(thread.external_id, 1024), anchor, currentAnchor, recordedLocation,
      outdated: thread.outdated as boolean | null, resolution: thread.resolution as string,
      comments: page.items, commentsNext: page.next};
  }, cursor);
}

export function pullComments(value: unknown, repository: string, number: string, cursor = '') {
  return pullPage(value, 20, value => {
    const entry = object(value), comment = object(entry.comment);
    pullRef(entry.pull, repository, number);
    available(comment.observation);
    const author = comment.author === null ? null : object(comment.author);
    return {externalID: text(comment.external_id, 1024), author: author ? text(author.login, 256) : 'Unknown author',
      body: text(comment.body, 4 * 1024 * 1024), createdAt: date(comment.created_at)};
  }, cursor);
}
