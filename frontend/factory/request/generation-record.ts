import {check, id, object} from '../api/models.js';
import type {IntentRevision} from '../api/request-response.js';
import {requestDraftKey, validRequestDraft} from './draft.js';
import type {RequestDraft, RequestDraftAddress} from './draft.js';
import {revisionInput} from './save-record.js';
import {inspectPersisted} from '../shared/drafts.js';

export interface PendingGeneration {
  host: string;
  revision: IntentRevision;
  operation: string;
  writing: RequestDraft;
}
export function generationKey(address: RequestDraftAddress): string {
  check(address.subject !== 'new');
  return requestDraftKey(address).replace('soda-intent-draft:', 'soda-intent-generation:');
}
const exact = (row: Record<string, unknown>, keys: string[]) =>
  check(Object.keys(row).length === keys.length && keys.every((key) => Object.hasOwn(row, key)));
function parse(raw: string, address: RequestDraftAddress): PendingGeneration {
  check(raw.length <= 32 * 1024 * 1024);
  const row = object(JSON.parse(raw));
  exact(row, ['scope', 'actor', 'subject', 'pending']);
  check(row.scope === address.scope && row.actor === address.actor && row.subject === address.subject);
  const pending = object(row.pending);
  exact(pending, ['host', 'revision', 'operation', 'writing']);
  check(typeof pending.host === 'string' && /^[a-f0-9]{32}$/.test(pending.host));
  const revision = revisionInput(pending.revision);
  check(revision.intent === address.subject && revision.state === 'accepted' && id(revision.repository));
  check(
    typeof pending.operation === 'string' &&
      pending.operation.length > 0 &&
      pending.operation.length <= 256 &&
      !/[\u0000-\u001f\u007f]/.test(pending.operation)
  );
  check(
    validRequestDraft(address, pending.writing) &&
      pending.writing.base === revision.id &&
      pending.writing.repository === revision.repository
  );
  return {
    host: pending.host,
    revision: pending.revision as IntentRevision,
    operation: pending.operation,
    writing: pending.writing,
  };
}
export function readGeneration(address: RequestDraftAddress): {pending?: PendingGeneration; blocked: boolean} {
  try {
    const stored = inspectPersisted(generationKey(address));
    if (stored.value === '') return {blocked: false};
    if (stored.value !== undefined) return {pending: parse(stored.value, address), blocked: false};
  } catch {
    /* Preserve unknown bytes. */
  }
  return {blocked: true};
}
const same = (a: PendingGeneration, b: PendingGeneration) => JSON.stringify(a) === JSON.stringify(b);
async function locked<T>(address: RequestDraftAddress, body: () => T): Promise<T> {
  check(!!navigator.locks);
  return navigator.locks.request(generationKey(address), body);
}
export async function retainGeneration(address: RequestDraftAddress, pending: PendingGeneration): Promise<boolean> {
  const raw = JSON.stringify({...address, pending});
  parse(raw, address);
  try {
    return await locked(address, () => {
      const current = readGeneration(address);
      if (current.blocked || current.pending) return false;
      window.localStorage.setItem(generationKey(address), raw);
      const stored = readGeneration(address);
      return !!stored.pending && same(stored.pending, pending);
    });
  } catch {
    return false;
  }
}
/** Preserve captured writing before removing only the matching command. */
export async function settleGeneration(address: RequestDraftAddress, pending: PendingGeneration): Promise<boolean> {
  try {
    return await locked(address, () => {
      const current = readGeneration(address);
      if (current.blocked || !current.pending || !same(current.pending, pending)) return false;
      const key = generationKey({...address, subject: pending.revision.id}).replace(
        'soda-intent-generation:',
        'soda-intent-generation-receipt:'
      );
      const raw = JSON.stringify({...address, pending}),
        archived = inspectPersisted(key);
      if (archived.value === undefined || (archived.value !== '' && archived.value !== raw)) return false;
      window.localStorage.setItem(key, raw);
      window.localStorage.removeItem(generationKey(address));
      return inspectPersisted(generationKey(address)).value === '';
    });
  } catch {
    return false;
  }
}
export interface GenerationReceipt {
  state: 'not_requested' | 'pending' | 'committed' | 'completed' | 'refused';
  operation: string;
  raw: Record<string, unknown>;
}
/** Validate identity independently of the lenient display/status projection. */
export function generationReceipt(
  value: unknown,
  revision: IntentRevision,
  expectedOperation?: string
): GenerationReceipt {
  const row = object(value);
  check(row.intent_id === revision.intent && row.revision_id === revision.id);
  check(['not_requested', 'pending', 'committed', 'completed', 'refused'].includes(String(row.state)));
  if (row.state === 'not_requested') {
    exact(row, ['intent_id', 'revision_id', 'state']);
    return {state: 'not_requested', operation: 'intent-' + revision.id, raw: row};
  }
  check(
    typeof row.operation_id === 'string' &&
      row.operation_id.length <= 256 &&
      !/[\u0000-\u001f\u007f]/.test(row.operation_id)
  );
  const cycle = row.reassessment_cycle || row.repair_cycle;
  check(!(row.reassessment_cycle && row.repair_cycle));
  const local =
    row.local_only === true &&
    row.state === 'completed' &&
    row.operation_id === '' &&
    typeof cycle === 'string' &&
    /^[a-f0-9]{32}$/.test(cycle);
  check(row.operation_id !== '' || local);
  const operation = local ? 'local:' + cycle : String(row.operation_id);
  check(expectedOperation === undefined || operation === expectedOperation);
  check(id(row.actor_id) && Number.isSafeInteger(row.native_revision) && Number(row.native_revision) > 0);
  check(Number.isSafeInteger(row.not_after) && Number(row.not_after) > 0);
  check(typeof row.intent_digest === 'string' && /^[a-f0-9]{64}$/.test(row.intent_digest));
  check(row.authorization_revision === revision.id + ':' + row.intent_digest);
  check(
    Array.isArray(row.submitted_tasks) &&
      row.submitted_tasks.length <= 64 &&
      !!row.payload &&
      typeof row.payload === 'object'
  );
  object(row.payload);
  if (row.task_issue_ids !== undefined) object(row.task_issue_ids);
  return {state: row.state as GenerationReceipt['state'], operation, raw: row};
}
