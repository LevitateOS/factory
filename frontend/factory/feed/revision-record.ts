import {check, id, object} from '../api/models.js';
import {PendingRecord} from '../shared/retained-record.js';
import type {RetainedPending} from '../shared/retained-record.js';

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
function record(address: RevisionAddress): PendingRecord<PendingRevision> {
  return new PendingRecord<PendingRevision>(
    key(address),
    131072,
    (raw) => {
      const row = object(JSON.parse(raw));
      check(
        Object.keys(row).length === 5 &&
          row.scope === address.scope &&
          row.repository === address.repository &&
          row.post === address.post &&
          row.author === address.author
      );
      return parse(row.pending);
    },
    (pending) => JSON.stringify({...address, pending}),
    samePendingRevision
  );
}
export function readRevision(address: RevisionAddress): RetainedPending<PendingRevision> {
  return record(address).read();
}
export function retainRevision(
  address: RevisionAddress,
  pending: PendingRevision
): 'saved' | 'unavailable' | 'conflict' {
  parse(pending);
  return record(address).retain(pending);
}
export function settleRevision(address: RevisionAddress, pending: PendingRevision): boolean {
  return record(address).settle(pending);
}
