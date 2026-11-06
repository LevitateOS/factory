import {check, id, object} from '../api/models.js';
import {PendingRecord} from '../shared/retained-record.js';

export interface MergeAddress {scope: string; host: string; actor: string; repository: string; pull: string}
export type MergeMethod = 'merge' | 'squash' | 'rebase' | 'rebase-merge' | 'fast-forward-only';
export const noMergeMessage = (method: MergeMethod) => method === 'rebase' || method === 'fast-forward-only';
export const validMergeMessage = (method: MergeMethod, message: string) =>
  !message.includes('\0') && new TextEncoder().encode(message).length <= 65536 &&
  [...message].every(char => { const code = char.codePointAt(0)!; return code < 0xd800 || code > 0xdfff; }) &&
  (noMergeMessage(method) ? message === '' : !!message.trim());
export interface PendingMerge {
  command: string;
  method: MergeMethod;
  message: string;
  external: string;
  head: string;
  expectedHead?: true;
}
const issued = (value: unknown): value is string => typeof value === 'string' && /^[a-f0-9]{32}$/.test(value);
const fields = ({scope, host, actor, repository, pull}: MergeAddress) => ({scope, host, actor, repository, pull});
export function mergeRecordKey(address: MergeAddress) {
  check(issued(address.host) && id(address.actor) && id(address.repository) && id(address.pull));
  check(typeof address.scope === 'string' && !!address.scope.trim() && address.scope.length <= 1024 && !/[\u0000-\u001f\u007f]/.test(address.scope));
  return `soda-pull-merge:v1:${encodeURIComponent(address.scope)}:${address.host}:${address.actor}:${address.repository}:${address.pull}`;
}
function decode(raw: string, address: MergeAddress): PendingMerge | undefined {
  try {
    const row = object(JSON.parse(raw));
    check(Object.keys(row).length === (row.expectedHead === undefined ? 10 : 11));
    check(row.expectedHead === undefined || row.expectedHead === true);
    for (const [key, value] of Object.entries(fields(address))) check(row[key] === value);
    check(issued(row.command) && ['merge', 'squash', 'rebase', 'rebase-merge', 'fast-forward-only'].includes(String(row.method)));
    check(typeof row.message === 'string' && validMergeMessage(row.method as MergeMethod, row.message));
    check(typeof row.external === 'string' && !!row.external.trim() && row.external.length <= 512);
    check(typeof row.head === 'string' && /^[a-f0-9]{40}(?:[a-f0-9]{24})?$/i.test(row.head));
    return {command: row.command, method: row.method as MergeMethod, message: row.message,
      external: row.external, head: row.head, ...(row.expectedHead === true ? {expectedHead: true as const} : {})};
  } catch { return; }
}
export const sameMerge = (a: PendingMerge, b: PendingMerge) =>
  a.command === b.command && a.method === b.method && a.message === b.message && a.external === b.external && a.head === b.head && a.expectedHead === b.expectedHead;
export const mergeRecord = (address: MergeAddress) => new PendingRecord(
  mergeRecordKey(address), 512 * 1024, raw => decode(raw, address), pending => JSON.stringify({...fields(address), ...pending}), sameMerge
);
