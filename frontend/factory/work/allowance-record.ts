import {check, id, object} from '../api/models.js';
import {decisionID} from '../api/request-response.js';
import {factoryRunID} from '../api/run-response.js';
import type {VerificationAllowanceChange} from '../api/verification-allowance.js';
import {inspectStored, writeStored} from '../shared/drafts.js';
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
export function readAllowanceChange(address: AllowanceAddress): {
  pending: VerificationAllowanceChange | undefined;
  blocked: boolean;
  durable: boolean;
} {
  const stored = inspectStored(key(address));
  if (stored.value === '') return {pending: undefined, blocked: false, durable: stored.durable};
  if (stored.value !== undefined && stored.value.length <= 4096) {
    try {
      const row = object(JSON.parse(stored.value));
      check(
        Object.keys(row).length === 4 &&
          row.scope === address.scope &&
          row.repository === address.repository &&
          row.intent === address.intent
      );
      valid(row.change);
      return {pending: row.change, blocked: false, durable: stored.durable};
    } catch {
      /* Preserve the unsupported or foreign record. */
    }
  }
  return {pending: undefined, blocked: true, durable: stored.durable};
}
export function retainAllowanceChange(
  address: AllowanceAddress,
  change: VerificationAllowanceChange
): 'saved' | 'unavailable' | 'conflict' {
  valid(change);
  const stored = readAllowanceChange(address);
  if (stored.blocked || (stored.pending && !sameAllowanceChange(stored.pending, change))) return 'conflict';
  if (stored.pending && stored.durable) return 'saved';
  return writeStored(key(address), JSON.stringify({...address, change})) ? 'saved' : 'unavailable';
}
export function settleAllowanceChange(address: AllowanceAddress, change: VerificationAllowanceChange): boolean {
  const stored = readAllowanceChange(address);
  if (stored.blocked || !stored.pending || !sameAllowanceChange(stored.pending, change)) return false;
  writeStored(key(address), '');
  return true;
}
