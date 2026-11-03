import {check, id, object} from '../api/models.js';
import {intentPlanStatus} from '../api/request-response.js';
import type {IntentRevision, IntentPlanStatus} from '../api/request-response.js';
import {requestDraftKey, validRequestDraft} from './draft.js';
import type {RequestDraft, RequestDraftAddress} from './draft.js';
import {revisionInput} from './save-record.js';
import {inspectPersisted} from '../shared/drafts.js';

export interface PendingPlanning {
  host: string;
  revision: IntentRevision;
  operation: 'propose_intent' | 'decomposition_options';
  writing: RequestDraft;
  assignment: string;
  run: string;
  launch: 'unknown' | 'confirmed' | 'unconfirmed';
}
const nativeID = (value: unknown): value is string => typeof value === 'string' && /^[a-f0-9]{32}$/.test(value);
export function planningKey(address: RequestDraftAddress): string {
  check(address.subject !== 'new');
  return requestDraftKey(address).replace('soda-intent-draft:', 'soda-intent-planning:');
}
const exact = (row: Record<string, unknown>, keys: string[]) =>
  check(Object.keys(row).length === keys.length && keys.every((key) => Object.hasOwn(row, key)));
function parse(raw: string, address: RequestDraftAddress): PendingPlanning {
  check(raw.length <= 32 * 1024 * 1024);
  const row = object(JSON.parse(raw));
  exact(row, ['scope', 'actor', 'subject', 'pending']);
  check(row.scope === address.scope && row.actor === address.actor && row.subject === address.subject);
  const pending = object(row.pending);
  exact(pending, ['host', 'revision', 'operation', 'writing', 'assignment', 'run', 'launch']);
  check(nativeID(pending.host));
  const revision = revisionInput(pending.revision);
  check(revision.intent === address.subject && revision.state === 'draft' && id(revision.repository));
  check(pending.operation === 'propose_intent' || pending.operation === 'decomposition_options');
  check(
    validRequestDraft(address, pending.writing) &&
      pending.writing.base === revision.id &&
      pending.writing.repository === revision.repository
  );
  check(
    (pending.assignment === '' && pending.run === '' && pending.launch === 'unknown') ||
      (nativeID(pending.assignment) &&
        nativeID(pending.run) &&
        ['unknown', 'confirmed', 'unconfirmed'].includes(String(pending.launch)))
  );
  return pending as unknown as PendingPlanning;
}
const rawPlanning = (address: RequestDraftAddress, pending: PendingPlanning) => JSON.stringify({...address, pending});
const same = (a: PendingPlanning, b: PendingPlanning) => JSON.stringify(a) === JSON.stringify(b);
// Preserve observed reply identities through remounts if storage fails after dispatch.
const observed = new Map<string, {baseline: string | undefined; pending: PendingPlanning}>();
export function readPlanning(address: RequestDraftAddress): {
  pending?: PendingPlanning;
  blocked: boolean;
  durable: boolean;
} {
  try {
    const key = planningKey(address),
      stored = inspectPersisted(key),
      local = observed.get(key);
    if (local)
      return {
        pending: local.pending,
        blocked:
          stored.value === undefined ||
          (stored.value !== local.baseline && stored.value !== rawPlanning(address, local.pending)),
        durable: stored.value === rawPlanning(address, local.pending),
      };
    if (stored.value === '') return {blocked: false, durable: true};
    if (stored.value !== undefined) return {pending: parse(stored.value, address), blocked: false, durable: true};
  } catch {
    /* Preserve unknown bytes. */
  }
  return {blocked: true, durable: false};
}
async function locked<T>(address: RequestDraftAddress, body: () => T): Promise<T> {
  check(!!navigator.locks);
  return navigator.locks.request(planningKey(address), body);
}
export async function retainPlanning(address: RequestDraftAddress, pending: PendingPlanning): Promise<boolean> {
  const raw = rawPlanning(address, pending);
  parse(raw, address);
  try {
    return await locked(address, () => {
      const current = readPlanning(address);
      if (current.blocked || current.pending) return false;
      window.localStorage.setItem(planningKey(address), raw);
      const written = readPlanning(address);
      return !!written.pending && same(written.pending, pending) && written.durable;
    });
  } catch {
    return false;
  }
}
export async function bindPlanning(
  address: RequestDraftAddress,
  previous: PendingPlanning,
  next: PendingPlanning
): Promise<boolean> {
  const key = planningKey(address),
    raw = rawPlanning(address, next);
  parse(raw, address);
  try {
    return await locked(address, () => {
      const current = readPlanning(address);
      if (current.blocked || !current.pending || !same(current.pending, previous)) {
        observed.set(key, {baseline: observed.get(key)?.baseline ?? rawPlanning(address, previous), pending: next});
        return false;
      }
      const baseline = inspectPersisted(key).value;
      try {
        window.localStorage.setItem(key, raw);
        observed.delete(key);
        return true;
      } catch {
        observed.set(key, {baseline, pending: next});
        return false;
      }
    });
  } catch {
    observed.set(key, {baseline: observed.get(key)?.baseline ?? rawPlanning(address, previous), pending: next});
    return false;
  }
}
export async function settlePlanning(address: RequestDraftAddress, pending: PendingPlanning): Promise<boolean> {
  try {
    return await locked(address, () => {
      const key = planningKey(address),
        current = readPlanning(address);
      if (current.blocked || !current.pending || !same(current.pending, pending)) return false;
      const archive = planningKey({...address, subject: pending.revision.id}).replace(
        'soda-intent-planning:',
        'soda-intent-planning-receipt:'
      );
      const raw = rawPlanning(address, pending),
        stored = inspectPersisted(archive);
      if (stored.value === undefined || (stored.value !== '' && stored.value !== raw)) return false;
      window.localStorage.setItem(archive, raw);
      window.localStorage.removeItem(key);
      observed.delete(key);
      return inspectPersisted(key).value === '';
    });
  } catch {
    return false;
  }
}
export function planningReceipt(
  value: unknown,
  pending: Pick<PendingPlanning, 'revision' | 'operation' | 'assignment' | 'run'>
): IntentPlanStatus {
  const row = object(value);
  check(
    row.intent === pending.revision.intent && row.revision === pending.revision.id && row.launch_confirmed === undefined
  );
  check(['none', 'pending', 'completed', 'attention', 'failed', 'cancelled'].includes(String(row.state)));
  if (row.state === 'none') exact(row, ['intent', 'revision', 'state']);
  else {
    check(row.operation === pending.operation && nativeID(row.assignment_id) && nativeID(row.run_id));
    check(!pending.assignment || (row.assignment_id === pending.assignment && row.run_id === pending.run));
    check(row.candidates === undefined || (Number.isSafeInteger(row.candidates) && Number(row.candidates) >= 0));
    check(row.summary === undefined || typeof row.summary === 'string');
    check(row.review === undefined);
  }
  return intentPlanStatus(row);
}
export function planningLaunch(value: unknown, pending: PendingPlanning): PendingPlanning {
  const row = object(value);
  check(row.revision_id === pending.revision.id && nativeID(row.assignment_id) && nativeID(row.run_id));
  check(!pending.assignment || (row.assignment_id === pending.assignment && row.run_id === pending.run));
  check(row.launch_confirmed === undefined || typeof row.launch_confirmed === 'boolean');
  return {
    ...pending,
    assignment: row.assignment_id,
    run: row.run_id,
    launch: row.launch_confirmed === false ? 'unconfirmed' : 'confirmed',
  };
}
