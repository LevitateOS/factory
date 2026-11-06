import {check, id} from '../api/models.js';
import {factoryRunID} from '../api/run-response.js';
import {PendingRecord} from '../shared/retained-record.js';
import type {RetainedPending} from '../shared/retained-record.js';

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

function record(address: VerificationRequestAddress): PendingRecord<PendingVerificationRequest> {
  return new PendingRecord<PendingVerificationRequest>(
    key(address),
    4096,
    (raw) => {
      const value: unknown = JSON.parse(raw);
      if (!valid(value)) return;
      const row = value as PendingVerificationRequest & Record<string, unknown>;
      if (Object.keys(row).length === 5 && row.scope === address.scope && row.repository === address.repository)
        return {run: row.run, command: row.command, action: row.action};
    },
    (pending) => JSON.stringify({...address, run: pending.run, command: pending.command, action: pending.action}),
    sameVerificationRequest
  );
}
export function readVerificationRequest(
  address: VerificationRequestAddress
): RetainedPending<PendingVerificationRequest> {
  return record(address).read();
}
export function retainVerificationRequest(
  address: VerificationRequestAddress,
  pending: PendingVerificationRequest
): 'saved' | 'unavailable' | 'conflict' {
  check(valid(pending));
  return record(address).retain(pending);
}
export function settleVerificationRequest(
  address: VerificationRequestAddress,
  pending: PendingVerificationRequest
): boolean {
  return record(address).settle(pending);
}
