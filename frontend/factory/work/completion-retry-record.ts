import {check, id} from '../api/models.js';
import {inspectStored, writeStored} from '../shared/drafts.js';

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

/** Unknown bytes remain untouched; unavailable storage is never mistaken for absence. */
export function readCompletionRetry(address: CompletionRetryAddress): {
  pending: PendingCompletionRetry | undefined;
  blocked: boolean;
  durable: boolean;
} {
  const stored = inspectStored(key(address));
  if (stored.value === '') return {pending: undefined, blocked: false, durable: stored.durable};
  if (stored.value !== undefined && stored.value.length <= 4096) {
    try {
      const value: unknown = JSON.parse(stored.value);
      if (valid(value)) {
        const row = value as PendingCompletionRetry & Record<string, unknown>;
        if (
          Object.keys(row).length === 6 &&
          row.scope === address.scope &&
          row.repository === address.repository &&
          row.issue === address.issue
        )
          return {
            pending: {command: row.command, event: row.event, cycle: row.cycle},
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

export function retainCompletionRetry(
  address: CompletionRetryAddress,
  pending: PendingCompletionRetry
): 'saved' | 'unavailable' | 'conflict' {
  check(valid(pending));
  const current = readCompletionRetry(address);
  if (current.blocked || (current.pending && !sameCompletionRetry(current.pending, pending))) return 'conflict';
  if (current.pending && current.durable) return 'saved';
  return writeStored(
    key(address),
    JSON.stringify({...address, command: pending.command, event: pending.event, cycle: pending.cycle})
  )
    ? 'saved'
    : 'unavailable';
}
/** Only the exact issued record may be settled by a recorded receipt. */
export function settleCompletionRetry(address: CompletionRetryAddress, pending: PendingCompletionRetry): boolean {
  const current = readCompletionRetry(address);
  if (current.blocked || !current.pending || !sameCompletionRetry(current.pending, pending)) return false;
  writeStored(key(address), '');
  return true;
}
