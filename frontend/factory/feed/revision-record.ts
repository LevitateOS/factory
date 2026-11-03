import {check, id, object} from '../api/models.js';
import {inspectStored, writeStored} from '../shared/drafts.js';

export interface RevisionAddress {
  readonly scope: string;
  readonly repository: string;
  readonly post: string;
  readonly author: string;
}
export interface RevisionWriting {
  readonly body: string;
  readonly original?: string;
}
export interface PendingRevision {
  readonly command: string;
  readonly revision: number;
  readonly body: string;
  readonly baseBody: string;
  readonly writing: RevisionWriting;
}
const postID = /^[a-f0-9]{32}$/;
function key(address: RevisionAddress): string {
  check(
    typeof address.scope === 'string' &&
      address.scope.trim().length > 0 &&
      address.scope.length <= 1024 &&
      !/[\u0000-\u001f\u007f]/.test(address.scope) &&
      id(address.repository) &&
      id(address.author) &&
      postID.test(address.post)
  );
  return `soda-feed-retry:v1:${encodeURIComponent(address.scope)}:${address.repository}:${address.post}:${address.author}`;
}
export function revisionBody(writing: RevisionWriting): string {
  return writing.original === undefined ? writing.body : writing.original + '\n\nClarification: ' + writing.body;
}
export function sameRevisionWriting(a: RevisionWriting, b: RevisionWriting): boolean {
  return a.body === b.body && a.original === b.original;
}
export function samePendingRevision(a: PendingRevision, b: PendingRevision): boolean {
  return (
    a.command === b.command &&
    a.revision === b.revision &&
    a.body === b.body &&
    a.baseBody === b.baseBody &&
    sameRevisionWriting(a.writing, b.writing)
  );
}
function parse(value: unknown): PendingRevision {
  const row = object(value),
    writing = object(row.writing);
  check(Object.keys(row).length === 5 && typeof row.command === 'string' && postID.test(row.command));
  check(
    typeof row.revision === 'number' &&
      Number.isSafeInteger(row.revision) &&
      row.revision >= 0 &&
      row.revision < Number.MAX_SAFE_INTEGER
  );
  check(typeof row.body === 'string' && row.body.trim() && [...row.body].length <= 4000);
  check(typeof row.baseBody === 'string' && [...row.baseBody].length <= 4000);
  check(typeof writing.body === 'string' && (writing.original === undefined || typeof writing.original === 'string'));
  check(Object.keys(writing).every((key) => key === 'body' || key === 'original'));
  const result = {
    command: row.command,
    revision: row.revision,
    body: row.body,
    baseBody: row.baseBody,
    writing: {body: writing.body, ...(writing.original === undefined ? {} : {original: writing.original})},
  };
  check(revisionBody(result.writing) === result.body);
  return result;
}
/** Unknown, foreign or unreadable bytes are never treated as an empty slot. */
export function readRevision(address: RevisionAddress): {
  pending: PendingRevision | undefined;
  blocked: boolean;
  durable: boolean;
} {
  const stored = inspectStored(key(address));
  if (stored.value === '') return {pending: undefined, blocked: false, durable: stored.durable};
  if (stored.value !== undefined && stored.value.length <= 128 * 1024) {
    try {
      const row = object(JSON.parse(stored.value));
      check(
        Object.keys(row).length === 5 &&
          row.scope === address.scope &&
          row.repository === address.repository &&
          row.post === address.post &&
          row.author === address.author
      );
      return {pending: parse(row.pending), blocked: false, durable: stored.durable};
    } catch {
      /* Preserve the exact record. */
    }
  }
  return {pending: undefined, blocked: true, durable: stored.durable};
}
/** No new effect is dispatched before its exact intent is durably retained. */
export function retainRevision(
  address: RevisionAddress,
  pending: PendingRevision
): 'saved' | 'unavailable' | 'conflict' {
  parse(pending);
  const current = readRevision(address);
  if (current.blocked || (current.pending && !samePendingRevision(current.pending, pending))) return 'conflict';
  if (current.pending && current.durable) return 'saved';
  return writeStored(key(address), JSON.stringify({...address, pending})) ? 'saved' : 'unavailable';
}
/** A late receipt cannot remove a replacement or an unknown record. */
export function settleRevision(address: RevisionAddress, pending: PendingRevision): boolean {
  const current = readRevision(address);
  if (current.blocked || !current.pending || !samePendingRevision(current.pending, pending)) return false;
  writeStored(key(address), '');
  return true;
}
