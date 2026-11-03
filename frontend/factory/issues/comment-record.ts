import {check, id, object} from '../api/models.js';
import {inspectPersisted, writeStored} from '../shared/drafts.js';
import {factoryCommandId} from '../api/run-response.js';

export interface CommentAddress {
  scope: string;
  host: string;
  actor: string;
  repository: string;
  issue: string;
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
export interface CommentSaved {
  raw: string | undefined;
  writing: CommentWriting | undefined;
  blocked: boolean;
  durable: boolean;
}
const issued = (value: unknown): value is string => typeof value === 'string' && /^[a-f0-9]{32}$/.test(value);
const unsaved = new Map<string, {raw: string; baseline: string | undefined}>();
const fields = ({scope, host, actor, repository, issue}: CommentAddress): CommentAddress => ({
  scope,
  host,
  actor,
  repository,
  issue,
});
export function commentRecordKey(address: CommentAddress): string {
  check(issued(address.host) && id(address.actor) && id(address.repository) && id(address.issue));
  check(!!address.scope.trim() && address.scope.length <= 1024 && !/[\u0000-\u001f\u007f]/.test(address.scope));
  return `soda-issue-comment:v1:${encodeURIComponent(address.scope)}:${address.host}:${address.actor}:${address.repository}:${address.issue}`;
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
    check(Object.keys(row).length === 6 && Object.entries(fields(address)).every(([key, value]) => row[key] === value));
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
export function readCommentWriting(address: CommentAddress): CommentSaved {
  const key = commentRecordKey(address),
    persisted = inspectPersisted(key),
    local = unsaved.get(key);
  const raw = local?.raw ?? persisted.value,
    writing = decode(raw, address);
  return {
    raw,
    writing,
    durable: !local && persisted.durable,
    blocked: (raw !== '' && !writing) || (!!local && persisted.value !== local.baseline),
  };
}
/** Compare the exact observed bytes, retaining a failed write's persisted
 * baseline across remounts, as the request draft owner does. Never overwrite a
 * newer document's writing, unknown bytes or a submission with another intent. */
export function saveCommentWriting(
  address: CommentAddress,
  expected: string | undefined,
  writing: CommentWriting
): 'saved' | 'unavailable' | 'conflict' {
  const key = commentRecordKey(address),
    current = readCommentWriting(address);
  if (current.blocked || current.raw !== expected) return 'conflict';
  const raw = JSON.stringify({...fields(address), writing});
  check(decode(raw, address) !== undefined);
  const persisted = inspectPersisted(key),
    baseline = unsaved.get(key)?.baseline ?? expected;
  if (persisted.value !== baseline) return 'conflict';
  unsaved.set(key, {raw, baseline});
  // An unreadable persisted baseline cannot authorize overwriting unknown data.
  if (persisted.value === undefined || !writeStored(key, raw)) return 'unavailable';
  unsaved.delete(key);
  return 'saved';
}
export function emptyCommentWriting(): CommentWriting {
  return {draft: {revision: factoryCommandId(), body: ''}};
}

/** Explicitly recover the persisted draft while retaining this document's
 * unresolved submission. Two different submissions must never replace each other. */
export function reloadCommentWriting(address: CommentAddress): CommentSaved {
  const key = commentRecordKey(address),
    persisted = inspectPersisted(key),
    local = unsaved.get(key);
  if (!local || persisted.value === undefined) return readCommentWriting(address);
  const saved = decode(persisted.value, address),
    pending = decode(local.raw, address)?.pending;
  if (persisted.value !== '' && !saved) return readCommentWriting(address);
  if (pending) {
    if (saved?.pending && !samePendingComment(pending, saved.pending)) return readCommentWriting(address);
    const writing: CommentWriting = {
      draft: saved?.draft ?? emptyCommentWriting().draft,
      pending: {...pending, cancel: pending.cancel || !!saved?.pending?.cancel},
    };
    unsaved.set(key, {raw: JSON.stringify({...fields(address), writing}), baseline: persisted.value});
  } else unsaved.delete(key);
  return readCommentWriting(address);
}
