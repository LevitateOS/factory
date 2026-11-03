import {check, id, object} from './models.js';
import {commentOutcome} from './comment-response.js';
import type {CommentOutcome} from './comment-response.js';
import type {IssueRef} from './collaboration.js';

export function issueCreateAllowed(value: unknown, repository: string, actor: string): boolean {
  const row = object(value),
    observation = object(row.observation);
  check(row.repository === repository && row.actor === actor);
  check(row.issue === null && row.pull === null && row.head === null && row.base === null);
  check(observation.state === 'available' && typeof observation.revision === 'string' && !!observation.revision.trim());
  check(typeof observation.observed_at === 'string' && Number.isFinite(Date.parse(observation.observed_at)));
  const action = object(object(row.actions)['issue.create']);
  check(['allowed', 'denied', 'unsupported', 'unavailable', 'unknown'].includes(String(action.state)));
  return action.state === 'allowed';
}

export function createdIssueRef(value: unknown, repository: string): IssueRef {
  const row = object(value);
  check(Object.keys(row).length === 4 && row.kind === 'issue' && row.repository === repository && id(row.number));
  check(
    typeof row.external_id === 'string' &&
      !!row.external_id.trim() &&
      row.external_id.length <= 512 &&
      !row.external_id.includes('\0')
  );
  return {kind: 'issue', repository, number: row.number, external_id: row.external_id};
}

export function issueCreateOutcome(
  value: unknown,
  command: string,
  repository: string
): CommentOutcome & {issue: IssueRef | null} {
  const row = object(value),
    {issue, ...outcome} = row;
  check(Object.hasOwn(row, 'issue'));
  const result = commentOutcome(outcome, command);
  check(issue === null || result.effect === 'committed');
  return {...result, issue: issue === null ? null : createdIssueRef(issue, repository)};
}
