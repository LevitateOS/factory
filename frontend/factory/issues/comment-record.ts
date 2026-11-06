import {check, id, object} from '../api/models.js';
import {WritingRecord} from '../shared/retained-record.js';
import type {RetainedWriting} from '../shared/retained-record.js';
import {factoryCommandId} from '../api/run-response.js';

export interface CommentAddress {
  scope: string;
  host: string;
  actor: string;
  repository: string;
  issue: string;
  kind?: 'pull';
}
export interface PendingComment {
  command: string;
  body: string;
  external: string;
  draft: string;
  cancel: boolean;
}
export interface CommentWriting {
  draft: {revision: string; body: string};
  pending?: PendingComment;
}
export type CommentSaved = RetainedWriting<CommentWriting>;
const issued = (value: unknown): value is string => typeof value === 'string' && /^[a-f0-9]{32}$/.test(value);
const fields = ({scope, host, actor, repository, issue, kind}: CommentAddress): CommentAddress => ({
  scope,
  host,
  actor,
  repository,
  issue,
  ...(kind === 'pull' ? {kind} : {}),
});
export function commentRecordKey(address: CommentAddress): string {
  check(issued(address.host) && id(address.actor) && id(address.repository) && id(address.issue));
  check(!!address.scope.trim() && address.scope.length <= 1024 && !/[\u0000-\u001f\u007f]/.test(address.scope));
  check(address.kind === undefined || address.kind === 'pull');
  return `soda-${address.kind === 'pull' ? 'pull' : 'issue'}-comment:v1:${encodeURIComponent(address.scope)}:${address.host}:${address.actor}:${address.repository}:${address.issue}`;
}
export function validCommentBody(body: string): boolean {
  return (
    /[^\p{White_Space}]/u.test(body) &&
    !body.includes('\0') &&
    new TextEncoder().encode(body).length <= 65536 &&
    [...body].every((char) => {
      const code = char.codePointAt(0)!;
      return code < 0xd800 || code > 0xdfff;
    })
  );
}
export function samePendingComment(a: PendingComment, b: PendingComment): boolean {
  return a.command === b.command && a.body === b.body && a.external === b.external && a.draft === b.draft;
}
function decode(raw: string | undefined, address: CommentAddress): CommentWriting | undefined {
  if (!raw) return;
  try {
    const row = object(JSON.parse(raw)),
      writing = object(row.writing),
      draft = object(writing.draft);
    check(Object.keys(row).length === (address.kind === 'pull' ? 7 : 6) && Object.entries(fields(address)).every(([key, value]) => row[key] === value));
    check(Object.keys(writing).length === (writing.pending === undefined ? 1 : 2));
    check(Object.keys(draft).length === 2 && issued(draft.revision) && typeof draft.body === 'string');
    const result: CommentWriting = {draft: {revision: draft.revision, body: draft.body}};
    if (writing.pending !== undefined) {
      const pending = object(writing.pending);
      check(Object.keys(pending).length === 5 && issued(pending.command) && issued(pending.draft));
      check(typeof pending.body === 'string' && validCommentBody(pending.body));
      check(
        typeof pending.external === 'string' &&
          !!pending.external.trim() &&
          pending.external.length <= 512 &&
          !pending.external.includes('\0')
      );
      check(typeof pending.cancel === 'boolean');
      result.pending = {
        command: pending.command,
        body: pending.body,
        external: pending.external,
        draft: pending.draft,
        cancel: pending.cancel,
      };
    }
    return result;
  } catch {
    return;
  }
}
function record(address: CommentAddress): WritingRecord<CommentWriting> {
  return new WritingRecord(
    commentRecordKey(address),
    (raw) => decode(raw, address),
    (writing) => JSON.stringify({...fields(address), writing})
  );
}
export function readCommentWriting(address: CommentAddress): CommentSaved {
  return record(address).read();
}
export function saveCommentWriting(
  address: CommentAddress,
  expected: string | undefined,
  writing: CommentWriting
): 'saved' | 'unavailable' | 'conflict' {
  return record(address).save(expected, writing);
}
export function emptyCommentWriting(): CommentWriting {
  return {draft: {revision: factoryCommandId(), body: ''}};
}

export function reloadCommentWriting(address: CommentAddress): CommentSaved {
  return record(address).reload((ours, saved) => {
    const pending = ours?.pending;
    if (!pending) return 'persisted';
    if (saved?.pending && !samePendingComment(pending, saved.pending)) return 'conflict';
    return {
      draft: saved?.draft ?? emptyCommentWriting().draft,
      pending: {...pending, cancel: pending.cancel || !!saved?.pending?.cancel},
    };
  });
}
