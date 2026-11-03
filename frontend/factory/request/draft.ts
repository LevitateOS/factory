import {check, id} from '../api/models.js';
import {decisionID} from '../api/request-response.js';
import type {IntentTask, ProductIdea} from '../api/request-response.js';
import {inspectPersisted, writeStored} from '../shared/drafts.js';

export interface RequestDraftAddress {
  scope: string;
  actor: string;
  subject: string;
}
/** Exact pre-prune edges and target idea lineage; never inferred during restore. */
export interface ShelvedDepRepair {
  deps: string[];
  ideas: string[];
  depIdeas?: Record<string, string[]>;
}
export interface RequestDraft {
  base: string;
  repository: string;
  objective: string;
  department: string;
  maxIssues: number;
  changeText: string;
  ideas: {id: string; decision: ProductIdea['decision']; correction?: string}[];
  approach?: 'complete_first' | 'incremental';
  shelf: IntentTask[];
  shelfDeps: Record<string, ShelvedDepRepair>;
}
export type DraftProblem = 'unavailable' | 'invalid' | 'foreign' | 'conflict';
export interface RequestDraftRead {
  raw: string | undefined;
  draft: RequestDraft | undefined;
  durable: boolean;
  problem: DraftProblem | undefined;
  /** The actual stored copy, if validated for this account and subject. */
  persisted: RequestDraft | undefined;
}
// Keep unsent bytes and the original persisted baseline across component remounts.
// A later successful read must not mistake another document's writing for that baseline.
const pending = new Map<string, {raw: string; baseline: string | undefined; protected: boolean}>();
export function requestDraftKey(address: RequestDraftAddress): string {
  check(
    typeof address.scope === 'string' &&
      address.scope.trim().length > 0 &&
      address.scope.length <= 1024 &&
      !/[\u0000-\u001f\u007f]/.test(address.scope) &&
      id(address.actor) &&
      (address.subject === 'new' || decisionID(address.subject))
  );
  return `soda-intent-draft:v1:${encodeURIComponent(address.scope)}:${address.actor}:${address.subject}`;
}
const object = (value: unknown): value is Record<string, unknown> =>
  !!value && typeof value === 'object' && !Array.isArray(value);
const exact = (row: Record<string, unknown>, required: string[], optional: string[] = []) =>
  required.every((key) => Object.hasOwn(row, key)) &&
  Object.keys(row).every((key) => required.includes(key) || optional.includes(key));
const text = (value: unknown, limit: number): value is string => typeof value === 'string' && value.length <= limit;
function ids(value: unknown, limit: number, nonempty = false): value is string[] {
  return (
    Array.isArray(value) &&
    value.length <= limit &&
    (!nonempty || value.length > 0) &&
    value.every(decisionID) &&
    new Set(value).size === value.length
  );
}
function task(value: unknown): value is IntentTask {
  if (!object(value) || !exact(value, ['key', 'objective', 'outcomes', 'idea_ids'], ['depends_on'])) return false;
  return (
    decisionID(value.key) &&
    text(value.objective, 4000) &&
    Array.isArray(value.outcomes) &&
    value.outcomes.length <= 64 &&
    value.outcomes.every((entry) => text(entry, 4000) && entry.length > 0) &&
    ids(value.idea_ids, 64, true) &&
    (value.depends_on === undefined || ids(value.depends_on, 64))
  );
}
function repairs(value: unknown): value is Record<string, ShelvedDepRepair> {
  return (
    object(value) &&
    Object.keys(value).length <= 64 &&
    Object.entries(value).every(
      ([key, row]) =>
        decisionID(key) &&
        object(row) &&
        exact(row, ['deps', 'ideas'], ['depIdeas']) &&
        ids(row.deps, 64) &&
        ids(row.ideas, 50, true) &&
        (row.depIdeas === undefined ||
          (object(row.depIdeas) &&
            Object.keys(row.depIdeas).length <= 64 &&
            Object.entries(row.depIdeas).every(([dep, links]) => decisionID(dep) && ids(links, 50, true))))
    )
  );
}
function valid(row: Record<string, unknown>): boolean {
  if (
    !exact(
      row,
      [
        'scope',
        'actor',
        'subject',
        'base',
        'repository',
        'objective',
        'department',
        'maxIssues',
        'changeText',
        'ideas',
        'shelf',
        'shelfDeps',
      ],
      ['approach']
    )
  )
    return false;
  if (
    !(row.base === '' || decisionID(row.base)) ||
    !(row.repository === '0' || id(row.repository)) ||
    !text(row.objective, 16000) ||
    !text(row.department, 25) ||
    (row.department !== '' && !decisionID(row.department)) ||
    !Number.isInteger(row.maxIssues) ||
    Number(row.maxIssues) < 1 ||
    Number(row.maxIssues) > 64 ||
    !text(row.changeText, 4000) ||
    !Array.isArray(row.ideas) ||
    row.ideas.length > 50 ||
    !row.ideas.every(
      (idea) =>
        object(idea) &&
        exact(idea, ['id', 'decision'], ['correction']) &&
        decisionID(idea.id) &&
        ['pending', 'accept', 'decline', 'edit'].includes(String(idea.decision)) &&
        (idea.correction === undefined || text(idea.correction, 4000))
    ) ||
    (row.approach !== undefined && row.approach !== 'complete_first' && row.approach !== 'incremental') ||
    !Array.isArray(row.shelf) ||
    row.shelf.length > 64 ||
    !row.shelf.every(task) ||
    !repairs(row.shelfDeps)
  )
    return false;
  const ideaIDs = new Set(row.ideas.map((idea) => idea.id));
  return (
    ideaIDs.size === row.ideas.length &&
    new Set(row.shelf.map((entry) => entry.key)).size === row.shelf.length &&
    row.shelf.every((entry) => entry.idea_ids.every((link) => ideaIDs.has(link))) &&
    (row.subject !== 'new' ||
      (row.base === '' && row.repository === '0' && row.ideas.length === 0 && row.shelf.length === 0))
  );
}
function decode(raw: string | undefined, address: RequestDraftAddress): {draft?: RequestDraft; problem?: DraftProblem} {
  if (raw === '') return {};
  if (raw === undefined) return {problem: 'unavailable'};
  if (raw.length <= 32 * 1024 * 1024) {
    try {
      const row: unknown = JSON.parse(raw);
      if (object(row) && valid(row)) {
        if (row.scope !== address.scope || row.actor !== address.actor || row.subject !== address.subject)
          return {problem: 'foreign'};
        const {scope: _scope, actor: _actor, subject: _subject, ...draft} = row;
        return {draft: draft as unknown as RequestDraft};
      }
    } catch {
      /* Keep the original bytes. */
    }
  }
  return {problem: 'invalid'};
}
export function validRequestDraft(address: RequestDraftAddress, value: unknown): value is RequestDraft {
  if (!object(value) || ['scope', 'actor', 'subject'].some((key) => Object.hasOwn(value, key))) return false;
  return !!decode(JSON.stringify({...address, ...value}), address).draft;
}
export function readRequestDraft(address: RequestDraftAddress): RequestDraftRead {
  const key = requestDraftKey(address),
    saved = inspectPersisted(key),
    local = pending.get(key);
  const raw = local?.raw ?? saved.value,
    result = decode(raw, address),
    persisted = decode(saved.value, address);
  const conflict =
    local &&
    (local.protected ||
      (saved.value !== undefined &&
        saved.value !== local.baseline &&
        saved.value !== local.raw &&
        !(local.baseline === undefined && saved.value === '')));
  return {
    raw,
    draft: result.draft,
    persisted: persisted.draft,
    durable: !local && saved.durable,
    problem:
      result.problem ??
      persisted.problem ??
      (conflict ? 'conflict' : saved.value === undefined ? 'unavailable' : undefined),
  };
}
export function retainRequestDraft(
  address: RequestDraftAddress,
  draft: RequestDraft,
  expected: string | undefined,
  protect = false
): {status: 'saved' | 'unavailable' | 'conflict' | 'invalid'; raw: string | undefined} {
  const key = requestDraftKey(address),
    raw = JSON.stringify({...address, ...draft});
  if (!decode(raw, address).draft) return {status: 'invalid', raw: expected};
  const current = readRequestDraft(address),
    local = pending.get(key),
    stored = inspectPersisted(key);
  // An unpersisted copy belongs to its observing view too. Never replace a
  // newer document-local copy on behalf of a stale component.
  if (local && current.raw !== expected) return {status: 'conflict', raw: expected};
  const baseline = local ? local.baseline : expected;
  const protectedCopy = protect || !!local?.protected;
  const persistedProblem = decode(stored.value, address).problem;
  const conflict =
    current.raw !== expected ||
    protectedCopy ||
    persistedProblem === 'invalid' ||
    persistedProblem === 'foreign' ||
    (stored.value !== undefined &&
      stored.value !== baseline &&
      stored.value !== local?.raw &&
      stored.value !== raw &&
      !(baseline === undefined && stored.value === ''));
  if (conflict || stored.value === undefined) {
    pending.set(key, {raw, baseline, protected: protectedCopy});
    return {status: conflict ? 'conflict' : 'unavailable', raw};
  }
  if (writeStored(key, raw)) {
    pending.delete(key);
    return {status: 'saved', raw};
  }
  pending.set(key, {raw, baseline, protected: false});
  return {status: 'unavailable', raw};
}
