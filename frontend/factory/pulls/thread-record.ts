import {check, id, object} from '../api/models.js';
import {PendingRecord} from '../shared/retained-record.js';
import {parseHumanAnchor, type HumanAnchor} from './anchor.js';

export interface ThreadAddress {scope: string; host: string; actor: string; repository: string; pull: string; threadID: string}
export interface PendingThread {command: string; resolved: boolean; anchor: HumanAnchor | null; cancel: boolean}
const issued = (value: unknown): value is string => typeof value === 'string' && /^[a-f0-9]{32}$/.test(value);
const fields = ({scope, host, actor, repository, pull, threadID}: ThreadAddress) => ({scope, host, actor, repository, pull, threadID});
export function threadRecordKey(address: ThreadAddress) {
  check(issued(address.host) && id(address.actor) && id(address.repository) && id(address.pull));
  check(typeof address.scope === 'string' && !!address.scope.trim() && address.scope.length <= 1024 && !/[\u0000-\u001f\u007f]/.test(address.scope));
  check(typeof address.threadID === 'string' && !!address.threadID && address.threadID.length <= 1024 && !address.threadID.includes('\0'));
  return `soda-pull-thread:v1:${encodeURIComponent(address.scope)}:${address.host}:${address.actor}:${address.repository}:${address.pull}:${encodeURIComponent(address.threadID)}`;
}
function decode(raw: string, address: ThreadAddress): PendingThread | undefined {
  try {
    const row = object(JSON.parse(raw));
    check(row.cancel === undefined || typeof row.cancel === 'boolean');
    check(Object.keys(row).length === 9 + (row.cancel === undefined ? 0 : 1) && Object.entries(fields(address)).every(([key, value]) => row[key] === value));
    check(issued(row.command) && typeof row.resolved === 'boolean');
    return {command: row.command, resolved: row.resolved,
      anchor: row.anchor === null ? null : parseHumanAnchor(row.anchor), cancel: row.cancel === true};
  } catch { return; }
}
export const sameThread = (a: PendingThread, b: PendingThread) =>
  a.command === b.command && a.resolved === b.resolved && JSON.stringify(a.anchor) === JSON.stringify(b.anchor);
export const threadRecord = (address: ThreadAddress) => new PendingRecord(
  threadRecordKey(address), 64 * 1024, raw => decode(raw, address), pending => JSON.stringify({...fields(address), ...pending}), sameThread
);
