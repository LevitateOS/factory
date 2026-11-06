import {check, id, object} from '../api/models.js';
import {factoryCommandId} from '../api/run-response.js';
import {WritingRecord, type RetainedWriting} from '../shared/retained-record.js';

export type ReviewEvent = 'draft' | 'comment' | 'approve' | 'request_changes';
export interface ReviewAddress {scope: string; host: string; actor: string; repository: string; pull: string}
export interface ReviewPending {
  command: string;
  event: ReviewEvent;
  body: string;
  external: string;
  head: string;
  draft: string;
  cancel: boolean;
  expectedHead?: true;
}
export interface ReviewWriting {
  draft: {revision: string; body: string};
  pending?: ReviewPending;
}
export type ReviewSaved = RetainedWriting<ReviewWriting>;
const issued = (value: unknown): value is string => typeof value === 'string' && /^[a-f0-9]{32}$/.test(value);
const fields = ({scope, host, actor, repository, pull}: ReviewAddress): ReviewAddress => ({scope, host, actor, repository, pull});
export const emptyReviewWriting = (): ReviewWriting => ({draft: {revision: factoryCommandId(), body: ''}});
export const validReviewBody = (body: string) =>
  !body.includes('\0') && new TextEncoder().encode(body).length <= 65536 &&
  [...body].every(char => { const code = char.codePointAt(0)!; return code < 0xd800 || code > 0xdfff; });

export function reviewRecordKey(address: ReviewAddress) {
  check(issued(address.host) && id(address.actor) && id(address.repository) && id(address.pull));
  check(typeof address.scope === 'string' && !!address.scope.trim() && address.scope.length <= 1024 && !/[\u0000-\u001f\u007f]/.test(address.scope));
  return `soda-pull-review:v1:${encodeURIComponent(address.scope)}:${address.host}:${address.actor}:${address.repository}:${address.pull}`;
}
function decode(raw: string | undefined, address: ReviewAddress): ReviewWriting | undefined {
  if (!raw) return;
  try {
    const row = object(JSON.parse(raw)), writing = object(row.writing), draft = object(writing.draft);
    check(Object.keys(row).length === 6 && Object.entries(fields(address)).every(([key, value]) => row[key] === value));
    check(Object.keys(writing).length === (writing.pending === undefined ? 1 : 2));
    check(Object.keys(draft).length === 2 && issued(draft.revision) && typeof draft.body === 'string' && validReviewBody(draft.body));
    const result: ReviewWriting = {draft: {revision: draft.revision, body: draft.body}};
    if (writing.pending !== undefined) {
      const pending = object(writing.pending);
      check(Object.keys(pending).length === (pending.expectedHead === undefined ? 7 : 8) && issued(pending.command) && issued(pending.draft));
      check(pending.expectedHead === undefined || pending.expectedHead === true);
      check(['draft', 'comment', 'approve', 'request_changes'].includes(String(pending.event)));
      check(typeof pending.body === 'string' && validReviewBody(pending.body));
      check(pending.event !== 'request_changes' || !!pending.body.trim());
      check(typeof pending.external === 'string' && !!pending.external.trim() && pending.external.length <= 512);
      check(typeof pending.head === 'string' && /^[a-f0-9]{40}(?:[a-f0-9]{24})?$/i.test(pending.head));
      check(typeof pending.cancel === 'boolean');
      result.pending = {
        command: pending.command, event: pending.event as ReviewEvent, body: pending.body,
        external: pending.external, head: pending.head, draft: pending.draft, cancel: pending.cancel,
        ...(pending.expectedHead === true ? {expectedHead: true as const} : {}),
      };
    }
    return result;
  } catch { return; }
}
function record(address: ReviewAddress) {
  return new WritingRecord(reviewRecordKey(address), raw => decode(raw, address), writing => JSON.stringify({...fields(address), writing}));
}
export const readReviewWriting = (address: ReviewAddress): ReviewSaved => record(address).read();
export const saveReviewWriting = (address: ReviewAddress, expected: string | undefined, writing: ReviewWriting) => record(address).save(expected, writing);
export const reloadReviewWriting = (address: ReviewAddress): ReviewSaved => record(address).reload((ours, saved) => {
  if (!ours?.pending) return 'persisted';
  if (saved?.pending && !sameReviewPending(ours.pending, saved.pending)) return 'conflict';
  return {draft: saved?.draft ?? emptyReviewWriting().draft, pending: {...ours.pending, cancel: ours.pending.cancel || !!saved?.pending?.cancel}};
});
export const sameReviewPending = (a: ReviewPending, b: ReviewPending) =>
  a.command === b.command && a.event === b.event && a.body === b.body && a.external === b.external && a.head === b.head && a.draft === b.draft && a.expectedHead === b.expectedHead;
