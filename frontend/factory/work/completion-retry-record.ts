import {check, id} from '../api/models.js';
import {PendingRecord} from '../shared/retained-record.js';
import type {RetainedPending} from '../shared/retained-record.js';

export interface CompletionRetryAddress {
  readonly scope: string;
  readonly repository: string;
  readonly issue: string;
}
export interface PendingCompletionRetry {
  readonly command: string;
  readonly event: string;
  readonly cycle: string;
}

function key(address: CompletionRetryAddress): string {
  check(
    typeof address.scope === 'string' &&
      address.scope.trim().length > 0 &&
      address.scope.length <= 1024 &&
      !/[\u0000-\u001f\u007f]/.test(address.scope) &&
      id(address.repository) &&
      id(address.issue)
  );
  return `soda-work-completion-retry:v1:${encodeURIComponent(address.scope)}:${address.repository}:${address.issue}`;
}
function valid(value: unknown): value is PendingCompletionRetry {
  if (!value || typeof value !== 'object' || Array.isArray(value)) return false;
  const row = value as Record<string, unknown>;
  return [row.command, row.event, row.cycle].every(
    (value) => typeof value === 'string' && /^[a-f0-9]{32}$/.test(value)
  );
}
export function sameCompletionRetry(a: PendingCompletionRetry, b: PendingCompletionRetry): boolean {
  return a.command === b.command && a.event === b.event && a.cycle === b.cycle;
}

function record(address: CompletionRetryAddress): PendingRecord<PendingCompletionRetry> {
  return new PendingRecord<PendingCompletionRetry>(
    key(address),
    4096,
    (raw) => {
      const value: unknown = JSON.parse(raw);
      if (!valid(value)) return;
      const row = value as PendingCompletionRetry & Record<string, unknown>;
      if (
        Object.keys(row).length === 6 &&
        row.scope === address.scope &&
        row.repository === address.repository &&
        row.issue === address.issue
      )
        return {command: row.command, event: row.event, cycle: row.cycle};
    },
    (pending) => JSON.stringify({...address, command: pending.command, event: pending.event, cycle: pending.cycle}),
    sameCompletionRetry
  );
}
export function readCompletionRetry(address: CompletionRetryAddress): RetainedPending<PendingCompletionRetry> {
  return record(address).read();
}
export function retainCompletionRetry(
  address: CompletionRetryAddress,
  pending: PendingCompletionRetry
): 'saved' | 'unavailable' | 'conflict' {
  check(valid(pending));
  return record(address).retain(pending);
}
export function settleCompletionRetry(address: CompletionRetryAddress, pending: PendingCompletionRetry): boolean {
  return record(address).settle(pending);
}
