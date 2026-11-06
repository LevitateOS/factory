import {check, id} from '../api/models.js';
import {factoryRunID} from '../api/run-response.js';
import {PendingRecord} from '../shared/retained-record.js';
import type {RetainedPending} from '../shared/retained-record.js';

export interface RunRetryAddress {
  readonly scope: string;
  readonly repository: string;
  readonly issue: string;
}
export interface PendingRunRetry {
  readonly command: string;
  readonly run: string;
}

function key(address: RunRetryAddress): string {
  check(
    typeof address.scope === 'string' &&
      address.scope.trim().length > 0 &&
      address.scope.length <= 1024 &&
      !/[\u0000-\u001f\u007f]/.test(address.scope) &&
      id(address.repository) &&
      id(address.issue)
  );
  return `soda-work-run-retry:v1:${encodeURIComponent(address.scope)}:${address.repository}:${address.issue}`;
}
function valid(value: unknown): value is PendingRunRetry {
  if (!value || typeof value !== 'object' || Array.isArray(value)) return false;
  const record = value as Record<string, unknown>;
  return typeof record.command === 'string' && /^[a-f0-9]{32}$/.test(record.command) && factoryRunID(record.run);
}
function same(a: PendingRunRetry, b: PendingRunRetry): boolean {
  return a.command === b.command && a.run === b.run;
}

function record(address: RunRetryAddress): PendingRecord<PendingRunRetry> {
  return new PendingRecord<PendingRunRetry>(
    key(address),
    4096,
    (raw) => {
      const value: unknown = JSON.parse(raw);
      if (!valid(value)) return;
      const row = value as PendingRunRetry & Record<string, unknown>;
      if (
        Object.keys(row).length === 5 &&
        row.scope === address.scope &&
        row.repository === address.repository &&
        row.issue === address.issue
      )
        return {command: row.command, run: row.run};
    },
    (pending) => JSON.stringify({...address, ...pending}),
    same
  );
}
export function readRunRetry(address: RunRetryAddress): RetainedPending<PendingRunRetry> {
  return record(address).read();
}
export function retainRunRetry(
  address: RunRetryAddress,
  pending: PendingRunRetry
): 'saved' | 'unavailable' | 'conflict' {
  check(valid(pending));
  return record(address).retain(pending);
}
export function settleRunRetry(address: RunRetryAddress, pending: PendingRunRetry): void {
  record(address).settle(pending);
}
