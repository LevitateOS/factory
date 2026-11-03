import {check, id} from '../api/models.js';
import {factoryRunID} from '../api/run-response.js';
import {inspectStored, writeStored} from '../shared/drafts.js';

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

/** Refuse unknown bytes without deleting or replacing them. */
export function readRunRetry(address: RunRetryAddress): {
  pending: PendingRunRetry | undefined;
  blocked: boolean;
  durable: boolean;
} {
  const stored = inspectStored(key(address));
  if (stored.value === '') return {pending: undefined, blocked: false, durable: stored.durable};
  if (stored.value !== undefined && stored.value.length <= 4096) {
    try {
      const value: unknown = JSON.parse(stored.value);
      if (valid(value)) {
        const record = value as PendingRunRetry & Record<string, unknown>;
        if (
          Object.keys(record).length === 5 &&
          record.scope === address.scope &&
          record.repository === address.repository &&
          record.issue === address.issue
        )
          return {pending: {command: record.command, run: record.run}, blocked: false, durable: stored.durable};
      }
    } catch {
      /* Keep the original record. */
    }
  }
  return {pending: undefined, blocked: true, durable: stored.durable};
}

/** A new effect is sent only after its exact identity reaches durable storage. */
export function retainRunRetry(
  address: RunRetryAddress,
  pending: PendingRunRetry
): 'saved' | 'unavailable' | 'conflict' {
  check(valid(pending));
  const current = readRunRetry(address);
  if (current.blocked || (current.pending && !same(current.pending, pending))) return 'conflict';
  if (current.pending && current.durable) return 'saved';
  return writeStored(key(address), JSON.stringify({...address, ...pending})) ? 'saved' : 'unavailable';
}
export function settleRunRetry(address: RunRetryAddress, pending: PendingRunRetry): void {
  const current = readRunRetry(address);
  if (!current.blocked && current.pending && same(current.pending, pending)) writeStored(key(address), '');
}
