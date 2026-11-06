import {check, object} from './models.js';
import type {IssueRef, PullRef, Action} from './collaboration.js';

/** Display availability never grants mutation authority. */
export function issueActionAllowed(
  value: unknown,
  repository: string,
  actor: string,
  parent: IssueRef | null,
  action: 'issue.create' | 'issue.comment' | 'issue.edit'
): boolean {
  const row = object(value),
    observation = object(row.observation);
  check(
    row.repository === repository && row.actor === actor && row.pull === null && row.head === null && row.base === null
  );
  if (parent) {
    const issue = object(row.issue);
    check(
      issue.kind === 'issue' &&
        issue.repository === parent.repository &&
        issue.number === parent.number &&
        issue.external_id === parent.external_id
    );
  } else check(row.issue === null);
  check(observation.state === 'available' && typeof observation.revision === 'string' && !!observation.revision.trim());
  check(typeof observation.observed_at === 'string' && Number.isFinite(Date.parse(observation.observed_at)));
  const capability = object(object(row.actions)[action]);
  check(['allowed', 'denied', 'unsupported', 'unavailable', 'unknown'].includes(String(capability.state)));
  return capability.state === 'allowed';
}

export function pullActionAllowed(
  value: unknown,
  repository: string,
  actor: string,
  parent: PullRef,
  head: string | null,
  action: Extract<Action, 'pull.comment' | 'pull.review' | 'pull.review.comment' | 'pull.review.approve' | 'pull.review.request_changes' | 'thread.resolve' | 'pull.merge'>
): boolean {
  const row = object(value), observation = object(row.observation), pull = object(row.pull);
  check(row.repository === repository && row.actor === actor && row.issue === null);
  check(pull.kind === 'pull' && pull.repository === parent.repository && pull.number === parent.number && pull.external_id === parent.external_id);
  check(row.head === head && typeof row.connection === 'string' && !!row.connection);
  check(observation.state === 'available' && typeof observation.revision === 'string' && !!observation.revision.trim());
  check(typeof observation.observed_at === 'string' && Number.isFinite(Date.parse(observation.observed_at)));
  const capability = object(object(row.actions)[action]);
  check(['allowed', 'denied', 'unsupported', 'unavailable', 'unknown'].includes(String(capability.state)));
  return capability.state === 'allowed';
}
