import {check, id, object} from '../api/models.js';
import {parseReviewAnchor} from '../api/collaboration-response.js';
import type {PullRef, ReviewAnchor} from '../api/collaboration.js';
import {factoryCommandId} from '../api/run-response.js';
import {WritingRecord, type RetainedWriting} from '../shared/retained-record.js';
import {humanAnchor} from './anchor.js';

export interface InlineAddress {scope: string; host: string; actor: string; repository: string; pull: string; parent: PullRef}
export interface InlineDraft {revision: string; anchor: ReviewAnchor; body: string}
export interface InlinePending {command: string; source: InlineDraft; draft: boolean; cancel: boolean}
export interface InlineWriting {draft?: InlineDraft; pending?: InlinePending}
export type InlineSaved = RetainedWriting<InlineWriting>;
const issued = (value: unknown): value is string => typeof value === 'string' && /^[a-f0-9]{32}$/.test(value);
const fields = ({scope, host, actor, repository, pull}: InlineAddress) => ({scope, host, actor, repository, pull});
export const validInlineBody = (body: string) =>
  !!body.trim() && !body.includes('\0') && new TextEncoder().encode(body).length <= 65536 &&
  [...body].every(char => { const code = char.codePointAt(0)!; return code < 0xd800 || code > 0xdfff; });
export const validInlineDraftBody = (body: string) =>
  !body.includes('\0') && new TextEncoder().encode(body).length <= 65536 &&
  [...body].every(char => { const code = char.codePointAt(0)!; return code < 0xd800 || code > 0xdfff; });
export function inlineRecordKey(address: InlineAddress) {
  check(issued(address.host) && id(address.actor) && id(address.repository) && id(address.pull));
  check(!!address.scope.trim() && address.scope.length <= 1024 && !/[\u0000-\u001f\u007f]/.test(address.scope));
  check(address.parent.kind === 'pull' && address.parent.repository === address.repository && address.parent.number === address.pull);
  return `soda-pull-inline:v1:${encodeURIComponent(address.scope)}:${address.host}:${address.actor}:${address.repository}:${address.pull}`;
}
function readDraft(value: unknown, parent: PullRef): InlineDraft {
  const row = object(value), anchor = parseReviewAnchor(row.anchor);
  check(Object.keys(row).length === 3 && issued(row.revision) && typeof row.body === 'string');
  humanAnchor(anchor, parent);
  check(validInlineDraftBody(row.body));
  return {revision: row.revision, anchor, body: row.body};
}
export const sameInlineDraft = (a: InlineDraft, b: InlineDraft) =>
  a.revision === b.revision && a.body === b.body && JSON.stringify(a.anchor) === JSON.stringify(b.anchor);
export const sameInlinePending = (a: InlinePending, b: InlinePending) =>
  a.command === b.command && a.draft === b.draft && sameInlineDraft(a.source, b.source);
function decode(raw: string | undefined, address: InlineAddress): InlineWriting | undefined {
  if (!raw) return;
  try {
    const row = object(JSON.parse(raw)), writing = object(row.writing);
    check(Object.keys(row).length === 6 && Object.entries(fields(address)).every(([key, value]) => row[key] === value));
    check(Object.keys(writing).every(key => key === 'draft' || key === 'pending'));
    const result: InlineWriting = {};
    if (writing.draft !== undefined) result.draft = readDraft(writing.draft, address.parent);
    if (writing.pending !== undefined) {
      const pending = object(writing.pending), source = readDraft(pending.source, address.parent);
      check(Object.keys(pending).length === 4 && issued(pending.command) && typeof pending.draft === 'boolean' && typeof pending.cancel === 'boolean');
      check(validInlineBody(source.body));
      result.pending = {command: pending.command, source, draft: pending.draft, cancel: pending.cancel};
    }
    return result;
  } catch { return; }
}
function record(address: InlineAddress) {
  return new WritingRecord(inlineRecordKey(address), raw => decode(raw, address), writing => JSON.stringify({...fields(address), writing}));
}
export const readInlineWriting = (address: InlineAddress): InlineSaved => record(address).read();
export const saveInlineWriting = (address: InlineAddress, expected: string | undefined, writing: InlineWriting) => record(address).save(expected, writing);
export const reloadInlineWriting = (address: InlineAddress): InlineSaved => record(address).reload((ours, saved) => {
  if (!ours?.pending) return 'persisted';
  if (saved?.pending && !sameInlinePending(ours.pending, saved.pending)) return 'conflict';
  const draft = saved?.draft ?? ours.draft;
  return {...(draft ? {draft} : {}), pending: {...ours.pending, cancel: ours.pending.cancel || !!saved?.pending?.cancel}};
});
export const newInlineDraft = (anchor: ReviewAnchor, body = ''): InlineDraft => ({revision: factoryCommandId(), anchor, body});
