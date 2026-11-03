import {check, id, object} from './models.js';
import type {ActorRef, Issue, IssueComment, IssueRef, Observation, Page} from './collaboration.js';

/** List projection of the canonical issue record. Description and mutation
 * capabilities belong to their separate detail/current-admission consumers. */
export type IssueListItem = Pick<
  Issue,
  'ref' | 'observation' | 'title' | 'state' | 'author' | 'assignees' | 'labels' | 'created_at' | 'updated_at'
>;
export type IssueFilter = 'open' | 'closed' | 'all';

function text(value: unknown, limit: number): string {
  check(typeof value === 'string' && value.length <= limit && !value.includes('\0'));
  return value;
}
function timestamp(value: unknown): string {
  const result = text(value, 64);
  check(/^\d{4}-\d\d-\d\dT/.test(result) && Number.isFinite(Date.parse(result)));
  return result;
}
function observation(value: unknown): Observation {
  const input = object(value);
  check(input.state === 'available');
  const revision = text(input.revision, 4096);
  check(revision.trim() !== '');
  return {state: 'available', revision, observed_at: timestamp(input.observed_at)};
}
function actor(value: unknown, host: string): ActorRef {
  const input = object(value);
  check(id(input.id) && input.host === host);
  const external = text(input.external_id, 512);
  check(external.trim() !== '');
  return {id: input.id, host, external_id: external, login: text(input.login, 256)};
}

export function issueListPage(
  value: unknown,
  host: string,
  repository: string,
  state: IssueFilter,
  cursor = ''
): Page<IssueListItem> {
  const page = object(value);
  check(/^[a-f0-9]{32}$/.test(host) && id(repository));
  check(Array.isArray(page.items) && page.items.length <= 20 && typeof page.complete === 'boolean');
  const seen = new Set<string>();
  const items = page.items.map((value): IssueListItem => {
    const item = issueListItem(value, host, repository);
    check(!seen.has(item.ref.number) && (state === 'all' || item.state === state));
    seen.add(item.ref.number);
    return item;
  });
  const next = text(page.next, 4096);
  check(page.complete === (next === '') && (next === '' || next !== cursor));
  check(page.complete || items.length > 0);
  return {observation: observation(page.observation), items, next, complete: page.complete};
}

function issueListItem(value: unknown, host: string, repository: string): IssueListItem {
  const input = object(value),
    ref = object(input.ref);
  check(ref.kind === 'issue' && ref.repository === repository && id(ref.number));
  const external = text(ref.external_id, 512);
  check(external.trim() !== '');
  const lifecycle = input.state === 'open' || input.state === 'closed' ? input.state : 'unknown';
  check(input.assignees === null || (Array.isArray(input.assignees) && input.assignees.length <= 1000));
  check(input.labels === null || (Array.isArray(input.labels) && input.labels.length <= 1000));
  return {
    ref: {kind: 'issue', repository, external_id: external, number: ref.number},
    observation: observation(input.observation),
    title: text(input.title, 65536),
    state: lifecycle,
    author: input.author === null ? null : actor(input.author, host),
    assignees: input.assignees === null ? null : input.assignees.map((value) => actor(value, host)),
    labels:
      input.labels === null
        ? null
        : input.labels.map((value) => {
            const label = object(value);
            return {
              external_id: text(label.external_id, 512),
              name: text(label.name, 1024),
              color: text(label.color, 64),
            };
          }),
    created_at: input.created_at === null ? null : timestamp(input.created_at),
    updated_at: input.updated_at === null ? null : timestamp(input.updated_at),
  };
}

export type IssueDetail = IssueListItem & Pick<Issue, 'body' | 'closed_at'>;
export function issueDetail(value: unknown, host: string, repository: string, number: string): IssueDetail {
  const item = issueListItem(value, host, repository),
    input = object(value);
  check(id(number) && item.ref.number === number);
  return {
    ...item,
    body: text(input.body, 4 * 1024 * 1024),
    closed_at: input.closed_at === null ? null : timestamp(input.closed_at),
  };
}

export function issueCommentPage(value: unknown, host: string, parent: IssueRef, cursor = ''): Page<IssueComment> {
  const page = object(value),
    seen = new Set<string>();
  check(Array.isArray(page.items) && page.items.length <= 20 && typeof page.complete === 'boolean');
  const items = page.items.map((value): IssueComment => {
    const input = object(value),
      ref = object(input.issue),
      comment = object(input.comment);
    check(
      ref.kind === 'issue' &&
        ref.repository === parent.repository &&
        ref.number === parent.number &&
        ref.external_id === parent.external_id
    );
    const external = text(comment.external_id, 512);
    check(!!external.trim() && !seen.has(external));
    seen.add(external);
    return {
      issue: {...parent},
      comment: {
        external_id: external,
        observation: observation(comment.observation),
        body: text(comment.body, 4 * 1024 * 1024),
        author: comment.author === null ? null : actor(comment.author, host),
        created_at: comment.created_at === null ? null : timestamp(comment.created_at),
        updated_at: comment.updated_at === null ? null : timestamp(comment.updated_at),
      },
    };
  });
  const next = text(page.next, 4096);
  check(page.complete === (next === '') && (next === '' || next !== cursor) && (page.complete || items.length > 0));
  return {observation: observation(page.observation), items, next, complete: page.complete};
}
