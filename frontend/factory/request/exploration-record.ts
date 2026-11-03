import {check, object} from '../api/models.js';
import {exploration} from '../api/request-response.js';
import type {Exploration, IntentRevision} from '../api/request-response.js';
import {requestDraftKey, validRequestDraft} from './draft.js';
import type {RequestDraft, RequestDraftAddress} from './draft.js';
import {revisionInput} from './save-record.js';
import {inspectPersisted} from '../shared/drafts.js';

/** The original input revision identifies the existing atomic exploration transition. */
export interface PendingExploration {
  host: string;
  revision: IntentRevision;
  writing: RequestDraft;
}
export function explorationKey(address: RequestDraftAddress): string {
  check(address.subject !== 'new');
  return requestDraftKey(address).replace('soda-intent-draft:', 'soda-intent-exploration:');
}
const exact = (row: Record<string, unknown>, keys: string[]) =>
  check(Object.keys(row).length === keys.length && keys.every((key) => Object.hasOwn(row, key)));
function parse(raw: string, address: RequestDraftAddress): PendingExploration {
  check(raw.length <= 32 * 1024 * 1024);
  const row = object(JSON.parse(raw));
  exact(row, ['scope', 'actor', 'subject', 'pending']);
  check(row.scope === address.scope && row.actor === address.actor && row.subject === address.subject);
  const pending = object(row.pending);
  exact(pending, ['host', 'revision', 'writing']);
  check(typeof pending.host === 'string' && /^[a-f0-9]{32}$/.test(pending.host));
  const revision = revisionInput(pending.revision);
  check(revision.intent === address.subject && revision.state === 'draft');
  check(
    validRequestDraft(address, pending.writing) &&
      pending.writing.base === revision.id &&
      pending.writing.repository === revision.repository
  );
  return pending as unknown as PendingExploration;
}
export function readExplorationCommand(address: RequestDraftAddress): {pending?: PendingExploration; blocked: boolean} {
  try {
    const stored = inspectPersisted(explorationKey(address));
    if (stored.value === '') return {blocked: false};
    if (stored.value !== undefined) return {pending: parse(stored.value, address), blocked: false};
  } catch {
    /* Preserve unknown bytes. */
  }
  return {blocked: true};
}
const same = (a: PendingExploration, b: PendingExploration) => JSON.stringify(a) === JSON.stringify(b);
async function locked<T>(address: RequestDraftAddress, body: () => T): Promise<T> {
  check(!!navigator.locks);
  return navigator.locks.request(explorationKey(address), body);
}
export async function retainExploration(address: RequestDraftAddress, pending: PendingExploration): Promise<boolean> {
  const raw = JSON.stringify({...address, pending});
  parse(raw, address);
  try {
    return await locked(address, () => {
      const current = readExplorationCommand(address);
      if (current.blocked || current.pending) return false;
      localStorage.setItem(explorationKey(address), raw);
      const stored = readExplorationCommand(address);
      return !!stored.pending && same(stored.pending, pending);
    });
  } catch {
    return false;
  }
}
/** Archive the exact writing and observed result before clearing its matching command. */
export async function settleExploration(
  address: RequestDraftAddress,
  pending: PendingExploration,
  result?: ExplorationResult
): Promise<boolean> {
  try {
    return await locked(address, () => {
      const current = readExplorationCommand(address);
      if (current.blocked || !current.pending || !same(current.pending, pending)) return false;
      const key = explorationKey({...address, subject: pending.revision.id}).replace(
        'soda-intent-exploration:',
        'soda-intent-exploration-receipt:'
      );
      const baseline = JSON.stringify({...address, pending}),
        raw = JSON.stringify({...address, pending, ...(result ? {result} : {})}),
        stored = inspectPersisted(key);
      if (
        stored.value === undefined ||
        (stored.value !== '' && stored.value !== raw && !(result && stored.value === baseline))
      )
        return false;
      localStorage.setItem(key, raw);
      localStorage.removeItem(explorationKey(address));
      return inspectPersisted(explorationKey(address)).value === '';
    });
  } catch {
    return false;
  }
}
export interface ExplorationResult {
  revision: IntentRevision;
  advice: Exploration;
}
/** Strict transition identity before using the existing presentation decoder. */
export function explorationResult(value: unknown, pending: PendingExploration, actor: string): ExplorationResult {
  const row = object(value);
  check(
    row.owner === actor &&
      Number.isSafeInteger(row.created_unix) &&
      Number(row.created_unix) > 0 &&
      row.planning_calls === 0
  );
  const {owner: _owner, created_unix: _created, planning_calls: _calls, exploration: raw, ...authored} = row;
  const revision = revisionInput(authored),
    basis = pending.revision;
  check(
    revision.intent === basis.intent &&
      revision.predecessor === basis.id &&
      revision.id !== basis.id &&
      revision.state === 'draft' &&
      revision.repository === basis.repository &&
      revision.objective === basis.objective &&
      revision.max_issues === basis.max_issues &&
      (revision.department || '') === (basis.department || '')
  );
  const advice = object(raw),
    content = object(advice.content);
  check(
    content.intent === basis.intent &&
      content.revision === basis.id &&
      content.objective === basis.objective &&
      typeof content.producer === 'string' &&
      content.producer.length > 0 &&
      typeof content.supported === 'boolean'
  );
  check(Array.isArray(content.unknowns) && content.unknowns.every((v) => typeof v === 'string'));
  check(
    Array.isArray(advice.challenges) &&
      advice.challenges.length <= 50 &&
      Array.isArray(advice.settled) &&
      advice.settled.length <= 50
  );
  const ideas = new Set(revision.ideas.map((idea) => idea.id));
  for (const value of advice.challenges) {
    const challenge = object(value);
    check(ideas.has(String(challenge.idea)));
    for (const key of ['assumption', 'consequence', 'recommendation']) check(typeof challenge[key] === 'string');
  }
  check(advice.settled.every((value) => typeof value === 'string' && ideas.has(value)));
  for (const key of ['recommendation', 'consistency'])
    if (advice[key] !== undefined && advice[key] !== null) {
      const recorded = object(advice[key]);
      check(recorded.intent === basis.intent && recorded.revision === basis.id);
    }
  return {revision, advice: exploration(advice)};
}
export function explorationReceipt(
  value: unknown,
  pending: PendingExploration,
  actor: string
): ExplorationResult | undefined {
  const row = object(value);
  check(row.intent === pending.revision.intent && row.basis_revision === pending.revision.id);
  if (row.state === 'none') {
    exact(row, ['intent', 'basis_revision', 'state']);
    return undefined;
  }
  exact(row, ['intent', 'basis_revision', 'state', 'result']);
  check(row.state === 'recorded');
  return explorationResult(row.result, pending, actor);
}
