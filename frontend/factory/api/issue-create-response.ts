import {check, id, object} from './models.js';
import {issueActionAllowed} from './capability-response.js';
import {commandOutcome, outcomeCommitted} from './command-response.js';
import type {CommandOutcome} from './command-response.js';
import type {IssueRef} from './collaboration.js';

export function issueCreateAllowed(value: unknown, repository: string, actor: string): boolean {
  return issueActionAllowed(value, repository, actor, null, 'issue.create');
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
): CommandOutcome & {issue: IssueRef | null} {
  const row = object(value),
    {issue, ...outcome} = row;
  check(Object.hasOwn(row, 'issue'));
  const result = commandOutcome(outcome, command);
  check(issue === null || outcomeCommitted(result));
  return {...result, issue: issue === null ? null : createdIssueRef(issue, repository)};
}
