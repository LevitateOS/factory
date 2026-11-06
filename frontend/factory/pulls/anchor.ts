import {check} from '../api/models.js';
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
