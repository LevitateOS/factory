import {check, object} from '../api/models.js';
import {parseReviewAnchor} from '../api/collaboration-response.js';
import type {PullRef, ReviewAnchor} from '../api/collaboration.js';

export interface HumanAnchor {
  base: string;
  head: string;
  path: string;
  side: 'old' | 'new';
  scope: 'file' | 'lines';
  start_line: number;
  end_line: number;
}
export function parseHumanAnchor(value: unknown): HumanAnchor {
  const row = object(value);
  check(Object.keys(row).length === 7);
  check(typeof row.base === 'string' && /^(?:[a-f0-9]{40}|[a-f0-9]{64})$/.test(row.base));
  check(typeof row.head === 'string' && /^(?:[a-f0-9]{40}|[a-f0-9]{64})$/.test(row.head));
  check(typeof row.path === 'string' && row.path.length <= 4096 &&
    row.path.split('/').every(segment => !!segment && segment !== '.' && segment !== '..') && !row.path.includes('\0'));
  check(row.side === 'old' || row.side === 'new');
  check(row.scope === 'file' || row.scope === 'lines');
  check(Number.isSafeInteger(row.start_line) && Number.isSafeInteger(row.end_line));
  check(row.scope === 'file' ? row.start_line === 0 && row.end_line === 0
    : (row.start_line as number) > 0 && (row.end_line as number) >= (row.start_line as number));
  return {base: row.base, head: row.head, path: row.path, side: row.side,
    scope: row.scope, start_line: row.start_line as number, end_line: row.end_line as number};
}
/** The human command uses one side's path; the immutable read keeps both paths. */
export function humanAnchor(value: ReviewAnchor, parent: PullRef): HumanAnchor {
  const anchor = parseReviewAnchor(value);
  check(anchor.diff.pull.repository === parent.repository && anchor.diff.pull.number === parent.number &&
    anchor.diff.pull.external_id === parent.external_id);
  const path = anchor.side === 'old' ? anchor.old_path : anchor.new_path;
  check(!!path && path.split('/').every(segment => !!segment && segment !== '.' && segment !== '..'));
  check(/^(?:[a-f0-9]{40}|[a-f0-9]{64})$/.test(anchor.diff.base) &&
    /^(?:[a-f0-9]{40}|[a-f0-9]{64})$/.test(anchor.diff.head));
  return {base: anchor.diff.base, head: anchor.diff.head, path, side: anchor.side, scope: anchor.scope,
    start_line: anchor.lines?.start ?? 0, end_line: anchor.lines?.end ?? 0};
}
