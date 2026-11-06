import {check, id, object} from '../api/models.js';
import {decisionID} from '../api/request-response.js';
import {factoryRunID} from '../api/run-response.js';
import type {VerificationAllowanceChange} from '../api/verification-allowance.js';
import {PendingRecord} from '../shared/retained-record.js';
import type {RetainedPending} from '../shared/retained-record.js';
export interface AllowanceAddress {
  readonly scope: string;
  readonly repository: string;
  readonly intent: string;
}
function key(address: AllowanceAddress): string {
  check(
    typeof address.scope === 'string' &&
      address.scope.trim().length > 0 &&
      address.scope.length <= 1024 &&
      !/[\u0000-\u001f\u007f]/.test(address.scope) &&
      id(address.repository) &&
      decisionID(address.intent)
  );
  return `soda-work-verification-allowance:v1:${encodeURIComponent(address.scope)}:${address.repository}:${address.intent}`;
}
function valid(value: unknown): asserts value is VerificationAllowanceChange {
  const row = object(value),
    limits = object(row.limits);
  check(
    Object.keys(row).length === 3 &&
      factoryRunID(row.command_id) &&
      (row.expected_revision === '' || factoryRunID(row.expected_revision)) &&
      Object.keys(limits).length === 2
  );
  for (const field of [limits.runs, limits.active_minutes])
    check(typeof field === 'number' && Number.isSafeInteger(field) && field > 0 && field <= 2147483647);
}
export function sameAllowanceChange(a: VerificationAllowanceChange, b: VerificationAllowanceChange): boolean {
  return (
    a.command_id === b.command_id &&
    a.expected_revision === b.expected_revision &&
    a.limits.runs === b.limits.runs &&
    a.limits.active_minutes === b.limits.active_minutes
  );
}
function record(address: AllowanceAddress): PendingRecord<VerificationAllowanceChange> {
  return new PendingRecord<VerificationAllowanceChange>(
    key(address),
    4096,
    (raw) => {
      const row = object(JSON.parse(raw));
      check(
        Object.keys(row).length === 4 &&
          row.scope === address.scope &&
          row.repository === address.repository &&
          row.intent === address.intent
      );
      valid(row.change);
      return row.change;
    },
    (pending) => JSON.stringify({...address, change: pending}),
    sameAllowanceChange
  );
}
export function readAllowanceChange(address: AllowanceAddress): RetainedPending<VerificationAllowanceChange> {
  return record(address).read();
}
export function retainAllowanceChange(
  address: AllowanceAddress,
  pending: VerificationAllowanceChange
): 'saved' | 'unavailable' | 'conflict' {
  valid(pending);
  return record(address).retain(pending);
}
export function settleAllowanceChange(address: AllowanceAddress, pending: VerificationAllowanceChange): boolean {
  return record(address).settle(pending);
}
