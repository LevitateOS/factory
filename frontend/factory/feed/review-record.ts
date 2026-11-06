import {check, id, object} from '../api/models.js';
import {decisionID} from '../api/request-response.js';
import {PendingRecord} from '../shared/retained-record.js';
import type {RetainedPending} from '../shared/retained-record.js';

export interface ReviewAddress {
  readonly scope: string;
  readonly repository: string;
  readonly post: string;
  readonly actor: string;
}
/** The backend's existing idempotency identity is the source post/revision,
 * product intent and accepted predecessor, rather than a new client nonce. */
export interface PendingReview {
  readonly revision: number;
  readonly intent: string;
  readonly acceptedRevision: string;
}
function key(address: ReviewAddress): string {
  check(
    typeof address.scope === 'string' &&
      address.scope.trim().length > 0 &&
      address.scope.length <= 1024 &&
      !/[\u0000-\u001f\u007f]/.test(address.scope)
  );
  check(id(address.repository) && id(address.actor) && /^[a-f0-9]{32}$/.test(address.post));
  return `soda-feed-review:v1:${encodeURIComponent(address.scope)}:${address.repository}:${address.post}:${address.actor}`;
}
function parse(value: unknown): PendingReview {
  const row = object(value);
  check(
    Object.keys(row).length === 3 &&
      typeof row.revision === 'number' &&
      Number.isSafeInteger(row.revision) &&
      row.revision >= 0 &&
      row.revision < Number.MAX_SAFE_INTEGER
  );
  check(decisionID(row.intent) && decisionID(row.acceptedRevision));
  return {revision: row.revision, intent: row.intent, acceptedRevision: row.acceptedRevision};
}
export function sameReview(a: PendingReview, b: PendingReview): boolean {
  return a.revision === b.revision && a.intent === b.intent && a.acceptedRevision === b.acceptedRevision;
}
function record(address: ReviewAddress): PendingRecord<PendingReview> {
  return new PendingRecord<PendingReview>(
    key(address),
    8192,
    (raw) => {
      const row = object(JSON.parse(raw));
      check(
        Object.keys(row).length === 5 &&
          row.scope === address.scope &&
          row.repository === address.repository &&
          row.post === address.post &&
          row.actor === address.actor
      );
      return parse(row.pending);
    },
    (pending) => JSON.stringify({...address, pending}),
    sameReview
  );
}
export function readReview(address: ReviewAddress): RetainedPending<PendingReview> {
  return record(address).read();
}
export function retainReview(address: ReviewAddress, pending: PendingReview): 'saved' | 'unavailable' | 'conflict' {
  parse(pending);
  return record(address).retain(pending);
}
export function settleReview(address: ReviewAddress, pending: PendingReview): boolean {
  return record(address).settle(pending);
}
