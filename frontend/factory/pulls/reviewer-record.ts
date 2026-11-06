import {check, id, object} from '../api/models.js';
import {PendingRecord} from '../shared/retained-record.js';

export interface ReviewerAddress {scope: string; host: string; actor: string; repository: string; pull: string}
export interface ReviewerTarget {kind: 'user' | 'team'; id: string; label: string}
export interface PendingReviewer {command: string; target: ReviewerTarget; requested: boolean; external: string; cancel: boolean}
const issued = (value: unknown): value is string => typeof value === 'string' && /^[a-f0-9]{32}$/.test(value);
const fields = ({scope, host, actor, repository, pull}: ReviewerAddress) => ({scope, host, actor, repository, pull});
export const sameReviewer = (a: PendingReviewer, b: PendingReviewer) =>
  a.command === b.command && a.target.kind === b.target.kind && a.target.id === b.target.id &&
  a.requested === b.requested && a.external === b.external;
export function reviewerRecord(address: ReviewerAddress) {
  check(issued(address.host) && id(address.actor) && id(address.repository) && id(address.pull));
  check(!!address.scope.trim() && address.scope.length <= 1024 && !/[\u0000-\u001f\u007f]/.test(address.scope));
  const key = `soda-pull-reviewers:v1:${encodeURIComponent(address.scope)}:${address.host}:${address.actor}:${address.repository}:${address.pull}`;
  return new PendingRecord<PendingReviewer>(key, 64 * 1024, raw => {
    try {
      const row = object(JSON.parse(raw)), target = object(row.target);
      check(Object.keys(row).length === 10 && Object.entries(fields(address)).every(([key, value]) => row[key] === value));
      check(issued(row.command) && typeof row.requested === 'boolean' && typeof row.cancel === 'boolean');
      check(typeof row.external === 'string' && !!row.external.trim() && row.external.length <= 512);
      check(Object.keys(target).length === 3 && (target.kind === 'user' || target.kind === 'team') && id(target.id));
      check(typeof target.label === 'string' && !!target.label.trim() && target.label.length <= 512 && !target.label.includes('\0'));
      return {command: row.command, target: {kind: target.kind, id: target.id as string, label: target.label},
        requested: row.requested, external: row.external, cancel: row.cancel};
    } catch { return; }
  }, pending => JSON.stringify({...fields(address), ...pending}), sameReviewer);
}
