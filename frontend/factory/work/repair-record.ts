import {check, id, object} from '../api/models.js';
import {factoryRunID} from '../api/run-response.js';
import type {RepairChange} from '../api/repair-response.js';
import {PendingRecord} from '../shared/retained-record.js';
import type {RetainedPending} from '../shared/retained-record.js';
export interface RepairAddress {
  readonly scope: string;
  readonly repository: string;
}
export function validRepairChange(value: unknown): asserts value is RepairChange {
  const row = object(value);
  check(
    Object.keys(row).length === 7 &&
      factoryRunID(row.command_id) &&
      typeof row.expected_revision === 'number' &&
      Number.isSafeInteger(row.expected_revision) &&
      row.expected_revision >= 0 &&
      row.expected_revision < Number.MAX_SAFE_INTEGER &&
      typeof row.active === 'boolean' &&
      typeof row.effect === 'string' &&
      new TextEncoder().encode(row.effect).length <= 4000
  );
  for (const [field, max] of [
    [row.investigator_runs, 4],
    [row.proposal_judgments, 4],
    [row.active_minutes, 120],
  ] as const)
    check(typeof field === 'number' && Number.isSafeInteger(field) && field >= 0 && field <= max);
}
const key = (address: RepairAddress) => {
  check(
    typeof address.scope === 'string' &&
      address.scope.trim().length > 0 &&
      address.scope.length <= 1024 &&
      !/[\u0000-\u001f\u007f]/.test(address.scope) &&
      id(address.repository)
  );
  return `soda-work-repair-funding:v1:${encodeURIComponent(address.scope)}:${address.repository}`;
};
export function sameRepairChange(a: RepairChange, b: RepairChange): boolean {
  return (
    a.command_id === b.command_id &&
    a.expected_revision === b.expected_revision &&
    a.active === b.active &&
    a.effect === b.effect &&
    a.investigator_runs === b.investigator_runs &&
    a.proposal_judgments === b.proposal_judgments &&
    a.active_minutes === b.active_minutes
  );
}
function record(address: RepairAddress): PendingRecord<RepairChange> {
  return new PendingRecord<RepairChange>(
    key(address),
    32768,
    (raw) => {
      const row = object(JSON.parse(raw));
      check(Object.keys(row).length === 3 && row.scope === address.scope && row.repository === address.repository);
      validRepairChange(row.change);
      return row.change;
    },
    (pending) => JSON.stringify({...address, change: pending}),
    sameRepairChange
  );
}
export function readRepairChange(address: RepairAddress): RetainedPending<RepairChange> {
  return record(address).read();
}
export function retainRepairChange(
  address: RepairAddress,
  pending: RepairChange
): 'saved' | 'unavailable' | 'conflict' {
  validRepairChange(pending);
  return record(address).retain(pending);
}
export function settleRepairChange(address: RepairAddress, pending: RepairChange): boolean {
  return record(address).settle(pending);
}
