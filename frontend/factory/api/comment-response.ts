import {check, object} from './models.js';
import type {IssueRef} from './collaboration.js';

/** A capability is a current display/read condition, never mutation authority. */
export function issueCommentAllowed(value: unknown, repository: string, actor: string, parent: IssueRef): boolean {
  const row = object(value),
    issue = object(row.issue),
    observation = object(row.observation);
  check(
    row.repository === repository && row.actor === actor && row.pull === null && row.head === null && row.base === null
  );
  check(
    issue.kind === 'issue' &&
      issue.repository === parent.repository &&
      issue.number === parent.number &&
      issue.external_id === parent.external_id
  );
  check(observation.state === 'available' && typeof observation.revision === 'string' && !!observation.revision.trim());
  check(typeof observation.observed_at === 'string' && Number.isFinite(Date.parse(observation.observed_at)));
  const capability = object(object(row.actions)['issue.comment']);
  check(['allowed', 'denied', 'unsupported', 'unavailable', 'unknown'].includes(String(capability.state)));
  return capability.state === 'allowed';
}
export interface CommentOutcome {
  effect: string;
  cancellation: string;
  completion: string;
  finished: boolean;
}
export function commentOutcome(value: unknown, command: string): CommentOutcome {
  const row = object(value);
  check(row.command_id === command && typeof row.finished === 'boolean');
  check(
    Object.keys(row).every((key) =>
      ['command_id', 'observation', 'effect', 'cancellation', 'completion', 'finished'].includes(key)
    )
  );
  check(['found', 'not_observed', 'unavailable', 'unknown'].includes(String(row.observation)));
  if (row.observation !== 'found') {
    check(!row.finished && row.effect === undefined && row.cancellation === undefined && row.completion === undefined);
    return {effect: '', cancellation: '', completion: '', finished: false};
  }
  check(['pending', 'not_committed', 'committed', 'indeterminate'].includes(String(row.effect)));
  check(['none', 'pending', 'cancelled', 'too_late', 'indeterminate'].includes(String(row.cancellation)));
  check(['pending', 'complete', 'needs_intervention'].includes(String(row.completion)));
  const terminal = (row.effect === 'committed' || row.effect === 'not_committed') && row.completion === 'complete';
  check(row.finished === terminal && (row.completion !== 'complete' || terminal));
  check(row.effect !== 'committed' || row.cancellation !== 'cancelled');
  return {
    effect: row.effect as string,
    cancellation: row.cancellation as string,
    completion: row.completion as string,
    finished: row.finished,
  };
}
