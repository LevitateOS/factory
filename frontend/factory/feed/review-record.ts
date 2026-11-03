import {check, id, object} from '../api/models.js';
import {decisionID} from '../api/request-response.js';
import {inspectStored, writeStored} from '../shared/drafts.js';

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
export function readReview(address: ReviewAddress): {
  pending: PendingReview | undefined;
  blocked: boolean;
  durable: boolean;
} {
  const stored = inspectStored(key(address));
  if (stored.value === '') return {pending: undefined, blocked: false, durable: stored.durable};
  if (stored.value !== undefined && stored.value.length <= 8192) {
    try {
      const row = object(JSON.parse(stored.value));
      check(
        Object.keys(row).length === 5 &&
          row.scope === address.scope &&
          row.repository === address.repository &&
          row.post === address.post &&
          row.actor === address.actor
      );
      return {pending: parse(row.pending), blocked: false, durable: stored.durable};
    } catch {
      /* Preserve unknown and foreign bytes. */
    }
  }
  return {pending: undefined, blocked: true, durable: stored.durable};
}
export function retainReview(address: ReviewAddress, pending: PendingReview): 'saved' | 'unavailable' | 'conflict' {
  parse(pending);
  const current = readReview(address);
  if (current.blocked || (current.pending && !sameReview(current.pending, pending))) return 'conflict';
  if (current.pending && current.durable) return 'saved';
  return writeStored(key(address), JSON.stringify({...address, pending})) ? 'saved' : 'unavailable';
}
export function settleReview(address: ReviewAddress, pending: PendingReview): boolean {
  const current = readReview(address);
  if (current.blocked || !current.pending || !sameReview(current.pending, pending)) return false;
  writeStored(key(address), '');
  return true;
}
