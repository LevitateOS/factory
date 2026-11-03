import {check, id} from '../api/models.js';
import {factoryRunID} from '../api/run-response.js';
import {inspectStored, writeStored} from '../shared/drafts.js';

export interface ContextChoiceAddress {
  readonly scope: string;
  readonly repository: string;
  /** Empty only for a direct run with no issue binding. */
  readonly issue: string;
}
/** The backend's semantic replay identity; no extra client command ID. */
export interface PendingContextChoice {
  readonly run: string;
  readonly fingerprint: string;
  readonly scope_digest: string;
  readonly required_digest: string;
}
const limit = 32;
function key(address: ContextChoiceAddress): string {
  check(
    typeof address.scope === 'string' &&
      address.scope.length > 0 &&
      address.scope.length <= 1024 &&
      !/[\u0000-\u0020\u007f]/.test(address.scope) &&
      id(address.repository) &&
      (address.issue === '' || id(address.issue))
  );
  return `soda-work-context:v1:${encodeURIComponent(address.scope)}:${address.repository}:${address.issue}`;
}
function valid(value: unknown): value is PendingContextChoice {
  if (!value || typeof value !== 'object' || Array.isArray(value)) return false;
  const row = value as Record<string, unknown>;
  return (
    Object.keys(row).length === 4 &&
    factoryRunID(row.run) &&
    [row.fingerprint, row.scope_digest, row.required_digest].every(
      (value) => typeof value === 'string' && /^[0-9a-f]{64}$/.test(value)
    )
  );
}
export function sameContextChoice(a: PendingContextChoice, b: PendingContextChoice): boolean {
  return (
    a.run === b.run &&
    a.fingerprint === b.fingerprint &&
    a.scope_digest === b.scope_digest &&
    a.required_digest === b.required_digest
  );
}
/** Unknown, foreign and unavailable bytes are preserved, never treated as absence. */
export function readContextChoices(address: ContextChoiceAddress): {
  choices: readonly PendingContextChoice[];
  blocked: boolean;
  durable: boolean;
} {
  const stored = inspectStored(key(address));
  if (stored.value === '') return {choices: [], blocked: false, durable: stored.durable};
  if (stored.value !== undefined && stored.value.length <= 32768) {
    try {
      const row: unknown = JSON.parse(stored.value);
      if (row && typeof row === 'object' && !Array.isArray(row)) {
        const data = row as Record<string, unknown>;
        if (
          Object.keys(data).length === 4 &&
          data.scope === address.scope &&
          data.repository === address.repository &&
          data.issue === address.issue &&
          Array.isArray(data.choices) &&
          data.choices.length > 0 &&
          data.choices.length <= limit &&
          data.choices.every(valid) &&
          data.choices.every(
            (choice, index, choices) => !choices.slice(0, index).some((previous) => sameContextChoice(previous, choice))
          )
        )
          return {choices: data.choices, blocked: false, durable: stored.durable};
      }
    } catch {
      /* Preserve original bytes. */
    }
  }
  return {choices: [], blocked: true, durable: stored.durable};
}
export function retainContextChoice(
  address: ContextChoiceAddress,
  choice: PendingContextChoice
): 'saved' | 'unavailable' | 'conflict' | 'full' {
  check(valid(choice));
  const current = readContextChoices(address);
  if (current.blocked) return 'conflict';
  const existing = current.choices.some((row) => sameContextChoice(row, choice));
  if (existing && current.durable) return 'saved';
  if (!existing && current.choices.length >= limit) return 'full';
  const choices = existing ? current.choices : [...current.choices, choice];
  return writeStored(key(address), JSON.stringify({...address, choices})) ? 'saved' : 'unavailable';
}
/** Merge removal of just the confirmed tuple; retain other unknown choices. */
export function settleContextChoice(
  address: ContextChoiceAddress,
  choice: PendingContextChoice
): 'settled' | 'unavailable' | 'conflict' {
  const current = readContextChoices(address);
  if (current.blocked || !current.choices.some((row) => sameContextChoice(row, choice))) return 'conflict';
  const choices = current.choices.filter((row) => !sameContextChoice(row, choice));
  return writeStored(key(address), choices.length ? JSON.stringify({...address, choices}) : '')
    ? 'settled'
    : 'unavailable';
}
