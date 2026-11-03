import {check, id} from '../api/models.js';
import {factoryRunID} from '../api/run-response.js';
import {inspectStored, writeStored} from '../shared/drafts.js';

export interface VerificationRequestAddress {
  readonly scope: string;
  readonly repository: string;
}
export interface PendingVerificationRequest {
  readonly run: string;
  readonly command: string;
  readonly action: 'retry' | 'repair_delivery';
}

function key(address: VerificationRequestAddress): string {
  check(
    typeof address.scope === 'string' &&
      address.scope.trim().length > 0 &&
      address.scope.length <= 1024 &&
      !/[\u0000-\u001f\u007f]/.test(address.scope) &&
      id(address.repository)
  );
  return `soda-work-verification:v1:${encodeURIComponent(address.scope)}:${address.repository}`;
}
function valid(value: unknown): value is PendingVerificationRequest {
  if (!value || typeof value !== 'object' || Array.isArray(value)) return false;
  const row = value as Record<string, unknown>;
  return (
    factoryRunID(row.run) &&
    (row.action === 'retry' ? factoryRunID(row.command) : row.action === 'repair_delivery' && row.command === '')
  );
}
export function sameVerificationRequest(a: PendingVerificationRequest, b: PendingVerificationRequest): boolean {
  return a.command === b.command && a.run === b.run && a.action === b.action;
}

/** Unknown bytes remain untouched; unavailable storage is never mistaken for absence. */
export function readVerificationRequest(address: VerificationRequestAddress): {
  pending: PendingVerificationRequest | undefined;
  blocked: boolean;
  durable: boolean;
} {
  const stored = inspectStored(key(address));
  if (stored.value === '') return {pending: undefined, blocked: false, durable: stored.durable};
  if (stored.value !== undefined && stored.value.length <= 4096) {
    try {
      const value: unknown = JSON.parse(stored.value);
      if (valid(value)) {
        const row = value as PendingVerificationRequest & Record<string, unknown>;
        if (Object.keys(row).length === 5 && row.scope === address.scope && row.repository === address.repository)
          return {
            pending: {run: row.run, command: row.command, action: row.action},
            blocked: false,
            durable: stored.durable,
          };
      }
    } catch {
      /* Preserve the original record. */
    }
  }
  return {pending: undefined, blocked: true, durable: stored.durable};
}

export function retainVerificationRequest(
  address: VerificationRequestAddress,
  pending: PendingVerificationRequest
): 'saved' | 'unavailable' | 'conflict' {
  check(valid(pending));
  const current = readVerificationRequest(address);
  if (current.blocked || (current.pending && !sameVerificationRequest(current.pending, pending))) return 'conflict';
  if (current.pending && current.durable) return 'saved';
  return writeStored(
    key(address),
    JSON.stringify({...address, run: pending.run, command: pending.command, action: pending.action})
  )
    ? 'saved'
    : 'unavailable';
}
/** Only the exact issued record may be settled by a recorded receipt. */
export function settleVerificationRequest(
  address: VerificationRequestAddress,
  pending: PendingVerificationRequest
): boolean {
  const current = readVerificationRequest(address);
  if (current.blocked || !current.pending || !sameVerificationRequest(current.pending, pending)) return false;
  writeStored(key(address), '');
  return true;
}
