import {check, id, object} from '../api/models.js';
import {PendingRecord} from '../shared/retained-record.js';

export interface IssueStateAddress {
  scope: string;
  host: string;
  actor: string;
  repository: string;
  issue: string;
}
export interface PendingIssueState {
  command: string;
  external: string;
  close: boolean;
}
const issued = (value: unknown): value is string => typeof value === 'string' && /^[a-f0-9]{32}$/.test(value);
const fields = ({scope, host, actor, repository, issue}: IssueStateAddress) => ({scope, host, actor, repository, issue});
export function issueStateRecordKey(address: IssueStateAddress) {
  check(issued(address.host) && id(address.actor) && id(address.repository) && id(address.issue));
  check(!!address.scope.trim() && address.scope.length <= 1024 && !/[\u0000-\u001f\u007f]/.test(address.scope));
  return `soda-issue-state:v1:${encodeURIComponent(address.scope)}:${address.host}:${address.actor}:${address.repository}:${address.issue}`;
}
function decode(raw: string, address: IssueStateAddress): PendingIssueState | undefined {
  try {
    const row = object(JSON.parse(raw));
    check(Object.keys(row).length === 8);
    for (const [key, value] of Object.entries(fields(address))) check(row[key] === value);
    check(issued(row.command) && typeof row.close === 'boolean');
    check(typeof row.external === 'string' && !!row.external.trim() && row.external.length <= 512 && !row.external.includes('\0'));
    return {command: row.command, external: row.external, close: row.close};
  } catch { return; }
}
const same = (a: PendingIssueState, b: PendingIssueState) =>
  a.command === b.command && a.external === b.external && a.close === b.close;
export const issueStateRecord = (address: IssueStateAddress) => new PendingRecord(
  issueStateRecordKey(address), 16 * 1024, raw => decode(raw, address), pending => JSON.stringify({...fields(address), ...pending}), same
);
