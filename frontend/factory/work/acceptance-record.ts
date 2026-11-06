import {check, id, object} from '../api/models.js';
import {decisionID} from '../api/request-response.js';
import type {AcceptanceSource} from '../api/acceptance-response.js';
import {PendingRecord} from '../shared/retained-record.js';
import type {RetainedPending} from '../shared/retained-record.js';

export interface AcceptanceAddress {
  readonly scope: string;
  readonly repository: string;
  readonly issue: string;
}
export interface AcceptanceDecision {
  readonly decision_id: string;
  readonly predecessor?: string;
  readonly title_digest: string;
  readonly title_occurrence: string;
  readonly content_digest: string;
  readonly content_version: number;
  readonly native_revision: number;
  readonly sources: readonly AcceptanceSource[];
  readonly resolutions: readonly AcceptanceSource[];
  readonly prerequisites: readonly {
    readonly occurrence: string;
    readonly depends_on: string;
    readonly endpoint_repository: string;
    readonly endpoint_issue: string;
    readonly outcome: 'code' | 'result';
    readonly prerequisite_acceptance?: string;
  }[];
}
const digest = (value: unknown) => typeof value === 'string' && /^[a-f0-9]{64}$/.test(value);
const version = (value: unknown) => typeof value === 'number' && Number.isSafeInteger(value) && value >= 0;
function fields(row: Record<string, unknown>, required: string[], optional: string[] = []) {
  check(
    required.every((key) => Object.hasOwn(row, key)) &&
      Object.keys(row).every((key) => required.includes(key) || optional.includes(key))
  );
}
/** Validate the exact decision body we persist, without inferring missing selections. */
function validDecision(value: unknown): asserts value is AcceptanceDecision {
  const row = object(value);
  fields(
    row,
    [
      'decision_id',
      'title_digest',
      'title_occurrence',
      'content_digest',
      'content_version',
      'native_revision',
      'sources',
      'resolutions',
      'prerequisites',
    ],
    ['predecessor']
  );
  check(
    decisionID(row.decision_id) &&
      (row.predecessor === undefined || (decisionID(row.predecessor) && row.predecessor !== row.decision_id)) &&
      digest(row.title_digest) &&
      digest(row.content_digest) &&
      (row.title_occurrence === 'original' || id(row.title_occurrence)) &&
      version(row.content_version) &&
      version(row.native_revision) &&
      row.native_revision !== 0
  );
  check(
    Array.isArray(row.sources) && Array.isArray(row.resolutions) && row.sources.length + row.resolutions.length <= 50
  );
  const comments = new Set<string>();
  for (const item of [...row.sources, ...row.resolutions]) {
    const source = object(item);
    fields(source, ['id', 'digest', 'content_version']);
    check(id(source.id) && !comments.has(source.id) && digest(source.digest) && version(source.content_version));
    comments.add(source.id);
  }
  check(Array.isArray(row.prerequisites) && row.prerequisites.length <= 50);
  const occurrences = new Set<string>();
  for (const item of row.prerequisites) {
    const prerequisite = object(item);
    fields(
      prerequisite,
      ['occurrence', 'depends_on', 'endpoint_repository', 'endpoint_issue', 'outcome'],
      ['prerequisite_acceptance']
    );
    check(
      id(prerequisite.occurrence) &&
        !occurrences.has(prerequisite.occurrence) &&
        id(prerequisite.depends_on) &&
        id(prerequisite.endpoint_repository) &&
        id(prerequisite.endpoint_issue) &&
        (prerequisite.outcome === 'code'
          ? decisionID(prerequisite.prerequisite_acceptance)
          : prerequisite.outcome === 'result' && prerequisite.prerequisite_acceptance === undefined)
    );
    occurrences.add(prerequisite.occurrence);
  }
}
function key(address: AcceptanceAddress): string {
  check(
    typeof address.scope === 'string' &&
      address.scope.trim().length > 0 &&
      address.scope.length <= 1024 &&
      !/[\u0000-\u001f\u007f]/.test(address.scope) &&
      id(address.repository) &&
      id(address.issue)
  );
  return `soda-work-acceptance:v1:${encodeURIComponent(address.scope)}:${address.repository}:${address.issue}`;
}
export function sameAcceptance(a: AcceptanceDecision, b: AcceptanceDecision): boolean {
  return JSON.stringify(a) === JSON.stringify(b);
}
function record(address: AcceptanceAddress): PendingRecord<AcceptanceDecision> {
  return new PendingRecord<AcceptanceDecision>(
    key(address),
    65536,
    (raw) => {
      const row = object(JSON.parse(raw));
      fields(row, ['scope', 'repository', 'issue', 'decision']);
      check(row.scope === address.scope && row.repository === address.repository && row.issue === address.issue);
      validDecision(row.decision);
      return row.decision;
    },
    (pending) => JSON.stringify({...address, decision: pending}),
    sameAcceptance
  );
}
export function readAcceptance(address: AcceptanceAddress): RetainedPending<AcceptanceDecision> {
  return record(address).read();
}
export function retainAcceptance(
  address: AcceptanceAddress,
  pending: AcceptanceDecision
): 'saved' | 'unavailable' | 'conflict' {
  validDecision(pending);
  return record(address).retain(pending);
}
export function settleAcceptance(address: AcceptanceAddress, pending: AcceptanceDecision): boolean {
  return record(address).settle(pending);
}
