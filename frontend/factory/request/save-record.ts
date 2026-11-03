import {check, id, object} from '../api/models.js';
import {decisionID, intentRevision} from '../api/request-response.js';
import type {IntentRevision} from '../api/request-response.js';
import {requestDraftKey, validRequestDraft} from './draft.js';
import type {RequestDraft, RequestDraftAddress} from './draft.js';
import {inspectPersisted} from '../shared/drafts.js';

/** The immutable revision ID is the existing server save identity. */
export interface PendingSave {
  host: string;
  revision: IntentRevision;
  before: RequestDraft;
  after: RequestDraft;
}
export interface SaveRead {
  pending?: PendingSave;
  blocked: boolean;
}
export function saveKey(address: RequestDraftAddress): string {
  return requestDraftKey(address).replace('soda-intent-draft:', 'soda-intent-save:');
}
const receiptKey = (address: RequestDraftAddress, revision: string) =>
  saveKey({...address, subject: revision}).replace('soda-intent-save:', 'soda-intent-save-receipt:');
const exact = (row: Record<string, unknown>, required: string[], optional: string[] = []) =>
  check(
    required.every((key) => Object.hasOwn(row, key)) &&
      Object.keys(row).every((key) => required.includes(key) || optional.includes(key))
  );
const text = (value: unknown, limit: number) => check(typeof value === 'string' && value.length <= limit);
function ids(value: unknown, limit: number) {
  check(
    Array.isArray(value) && value.length <= limit && value.every(decisionID) && new Set(value).size === value.length
  );
}
/** Validate all authored fields before comparing; the display decoder is intentionally lenient. */
export function revisionInput(value: unknown): IntentRevision {
  const row = object(value);
  exact(
    row,
    ['id', 'intent', 'repository', 'objective', 'ideas', 'tasks', 'state', 'max_issues'],
    ['predecessor', 'department', 'approach', 'effective_outcomes']
  );
  check(decisionID(row.id) && decisionID(row.intent) && (row.repository === '0' || id(row.repository)));
  check(row.predecessor === undefined || decisionID(row.predecessor));
  check(row.department === undefined || row.department === '' || decisionID(row.department));
  text(row.objective, 16000);
  check(['draft', 'accepted', 'declined', 'rejected', 'abandoned'].includes(String(row.state)));
  check(Number.isInteger(row.max_issues) && Number(row.max_issues) >= 1 && Number(row.max_issues) <= 64);
  check(
    row.approach === undefined ||
      row.approach === '' ||
      row.approach === 'complete_first' ||
      row.approach === 'incremental'
  );
  check(Array.isArray(row.ideas) && row.ideas.length <= 50 && Array.isArray(row.tasks) && row.tasks.length <= 64);
  for (const value of row.ideas) {
    const idea = object(value);
    exact(
      idea,
      ['id', 'description', 'consequence', 'assumption', 'decision'],
      ['correction', 'source', 'provenance', 'actor', 'evidence', 'material']
    );
    check(decisionID(idea.id) && typeof idea.assumption === 'boolean');
    text(idea.description, 4000);
    text(idea.consequence, 4000);
    check(['pending', 'accept', 'decline', 'edit'].includes(String(idea.decision)));
    for (const key of ['correction', 'source', 'provenance', 'evidence'])
      if (idea[key] !== undefined) text(idea[key], 4000);
    check(idea.actor === undefined || idea.actor === '0' || id(idea.actor));
    check(idea.material === undefined || typeof idea.material === 'boolean');
  }
  for (const value of row.tasks) {
    const task = object(value);
    exact(task, ['key', 'objective', 'outcomes', 'idea_ids'], ['depends_on']);
    check(decisionID(task.key));
    text(task.objective, 4000);
    check(Array.isArray(task.outcomes) && task.outcomes.length <= 64);
    for (const outcome of task.outcomes) text(outcome, 4000);
    ids(task.idea_ids, 64);
    if (task.depends_on !== undefined) ids(task.depends_on, 64);
  }
  if (row.effective_outcomes !== undefined) ids(row.effective_outcomes, 50);
  const revision = intentRevision(row);
  // Match Go's omitempty representation while preserving every authored value and array order.
  for (const idea of revision.ideas) {
    if (idea.actor === '0') delete idea.actor;
    if (idea.material === false) delete idea.material;
  }
  for (const task of revision.tasks) if (task.depends_on?.length === 0) delete task.depends_on;
  if (revision.effective_outcomes?.length === 0) delete revision.effective_outcomes;
  return revision;
}
export function savedReceipt(value: unknown, actor: string, pending: PendingSave): IntentRevision {
  const row = object(value);
  check(
    row.owner === actor &&
      Number.isSafeInteger(row.created_unix) &&
      Number(row.created_unix) > 0 &&
      row.planning_calls === 0
  );
  const {owner: _owner, created_unix: _time, planning_calls: _calls, ...authored} = row;
  const saved = revisionInput(authored);
  check(JSON.stringify(saved) === JSON.stringify(revisionInput(pending.revision)));
  return saved;
}
function parse(raw: string, address: RequestDraftAddress): PendingSave {
  check(raw.length <= 32 * 1024 * 1024);
  const row = object(JSON.parse(raw));
  exact(row, ['scope', 'actor', 'subject', 'pending']);
  check(row.scope === address.scope && row.actor === address.actor && row.subject === address.subject);
  const pending = object(row.pending);
  exact(pending, ['host', 'revision', 'before', 'after']);
  check(typeof pending.host === 'string' && /^[a-f0-9]{32}$/.test(pending.host));
  revisionInput(pending.revision);
  const revision = pending.revision as unknown as IntentRevision;
  check(validRequestDraft(address, pending.before));
  check(validRequestDraft({...address, subject: revision.intent}, pending.after));
  const before = pending.before as unknown as RequestDraft,
    after = pending.after as unknown as RequestDraft;
  check(
    address.subject === 'new'
      ? before.base === '' && !revision.predecessor
      : address.subject === revision.intent && before.base === revision.predecessor
  );
  check(after.base === revision.id && after.repository === revision.repository);
  check(
    after.objective === before.objective &&
      after.changeText === before.changeText &&
      after.maxIssues === revision.max_issues
  );
  check(
    JSON.stringify(after.ideas) ===
      JSON.stringify(
        revision.ideas.map(({id, decision, correction}) => ({
          id,
          decision,
          ...(correction === undefined ? {} : {correction}),
        }))
      )
  );
  return {host: pending.host, revision, before, after};
}
export function readSave(address: RequestDraftAddress): SaveRead {
  const stored = inspectPersisted(saveKey(address));
  if (stored.value === '') return {blocked: false};
  try {
    if (stored.value !== undefined) return {pending: parse(stored.value, address), blocked: false};
  } catch {
    /* Preserve unknown bytes. */
  }
  return {blocked: true};
}
export const sameSave = (a: PendingSave, b: PendingSave): boolean => JSON.stringify(a) === JSON.stringify(b);
/** Web Locks serialize admission across documents; never pretend localStorage is CAS. */
async function locked<T>(address: RequestDraftAddress, body: () => T): Promise<T> {
  check(!!navigator.locks);
  return navigator.locks.request(saveKey(address), body);
}
export async function retainSave(address: RequestDraftAddress, pending: PendingSave): Promise<boolean> {
  const raw = JSON.stringify({...address, pending});
  parse(raw, address);
  try {
    return await locked(address, () => {
      const current = readSave(address);
      if (current.blocked || current.pending) return false;
      window.localStorage.setItem(saveKey(address), raw);
      const saved = readSave(address);
      return !!saved.pending && sameSave(saved.pending, pending);
    });
  } catch {
    return false;
  }
}
/** Archive exact recovery metadata before removing the pending slot. Neither write replaces unknown bytes. */
export async function settleSave(address: RequestDraftAddress, pending: PendingSave): Promise<boolean> {
  try {
    return await locked(address, () => {
      const current = readSave(address);
      if (current.blocked || !current.pending || !sameSave(current.pending, pending)) return false;
      const key = receiptKey(address, pending.revision.id),
        stored = inspectPersisted(key),
        raw = JSON.stringify({...address, pending});
      if (stored.value === undefined || (stored.value !== '' && stored.value !== raw)) return false;
      window.localStorage.setItem(key, raw);
      window.localStorage.removeItem(saveKey(address));
      return inspectPersisted(saveKey(address)).value === '';
    });
  } catch {
    return false;
  }
}
/** Caller must freshly prove receipt absence and a head that fences this predecessor. */
export async function releaseObsoleteSave(address: RequestDraftAddress, pending: PendingSave): Promise<boolean> {
  try {
    return await locked(address, () => {
      const current = readSave(address);
      if (current.blocked || !current.pending || !sameSave(current.pending, pending)) return false;
      window.localStorage.removeItem(saveKey(address));
      return inspectPersisted(saveKey(address)).value === '';
    });
  } catch {
    return false;
  }
}
export function savedDraft(address: RequestDraftAddress, revision: IntentRevision): RequestDraft | undefined {
  try {
    const raw = inspectPersisted(receiptKey(address, revision.id)).value;
    if (!raw) return;
    const row = object(JSON.parse(raw));
    check(row.subject === 'new' || row.subject === revision.intent);
    const pending = parse(raw, {...address, subject: String(row.subject)});
    check(JSON.stringify(revisionInput(revision)) === JSON.stringify(revisionInput(pending.revision)));
    return pending.after;
  } catch {
    return;
  }
}
