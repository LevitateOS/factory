import {record, list, member, text, num, flag, texts} from './models.js';

export interface ProductIdea {
  id: string;
  description: string;
  consequence: string;
  assumption: boolean;
  decision: 'pending' | 'accept' | 'decline' | 'edit';
  correction?: string;
  source?: 'requested' | 'supported' | 'autonomous' | 'assumption';
  provenance?: 'human' | 'autonomous';
  actor?: string;
  evidence?: string;
  material?: boolean;
}
export interface IntentTask {
  key: string;
  objective: string;
  outcomes: string[];
  idea_ids: string[];
  depends_on?: string[];
}
const REVISION_STATES = ['draft', 'accepted', 'declined', 'rejected', 'abandoned'] as const;
type IntentRevisionState = (typeof REVISION_STATES)[number] | 'unknown';
export interface IntentRevision {
  id: string;
  intent: string;
  predecessor?: string;
  repository: string;
  department?: string;
  objective: string;
  ideas: ProductIdea[];
  tasks: IntentTask[];
  state: IntentRevisionState;
  max_issues: number;
  approach?: 'complete_first' | 'incremental';
  effective_outcomes?: string[];
}
export const decisionID = (value: unknown): value is string =>
  typeof value === 'string' && /^d[0-9a-f]{24}$/.test(value);
// Lenient readers: each field falls back to a default when missing or of
// another type; extra fields and unknown enum values pass through.
const nonEmpty = (value: unknown): string | undefined =>
  typeof value === 'string' && value !== '' ? value : undefined;
function idea(value: unknown): ProductIdea {
  const raw = record(value);
  const out: ProductIdea = {
    id: text(raw.id),
    description: text(raw.description),
    consequence: text(raw.consequence),
    assumption: flag(raw.assumption),
    decision: text(raw.decision, 'pending') as ProductIdea['decision'],
  };
  const correction = nonEmpty(raw.correction);
  if (correction) out.correction = correction;
  const source = nonEmpty(raw.source);
  if (source) out.source = source as NonNullable<ProductIdea['source']>;
  const provenance = nonEmpty(raw.provenance);
  if (provenance) out.provenance = provenance as NonNullable<ProductIdea['provenance']>;
  const actor = nonEmpty(raw.actor);
  if (actor) out.actor = actor;
  const evidence = nonEmpty(raw.evidence);
  if (evidence) out.evidence = evidence;
  if (typeof raw.material === 'boolean') out.material = raw.material;
  return out;
}
function effectiveOutcomes(raw: Record<string, unknown>): Partial<IntentRevision> {
  if (raw.effective_outcomes === undefined) return {};
  return {effective_outcomes: texts(raw.effective_outcomes)};
}
function task(value: unknown): IntentTask {
  const raw = record(value);
  return {
    key: text(raw.key),
    objective: text(raw.objective),
    outcomes: texts(raw.outcomes),
    idea_ids: texts(raw.idea_ids),
    ...(raw.depends_on ? {depends_on: texts(raw.depends_on)} : {}),
  };
}
function references(raw: Record<string, unknown>) {
  const predecessor = nonEmpty(raw.predecessor);
  const department = nonEmpty(raw.department);
  return {...(predecessor ? {predecessor} : {}), ...(department ? {department} : {})};
}
export function intentRevision(value: unknown): IntentRevision {
  const raw = record(value);
  const approach = nonEmpty(raw.approach);
  return {
    id: text(raw.id),
    intent: text(raw.intent),
    repository: text(raw.repository, '0'),
    objective: text(raw.objective),
    state: member(REVISION_STATES, raw.state),
    max_issues: num(raw.max_issues),
    ...(approach ? {approach: approach as NonNullable<IntentRevision['approach']>} : {}),
    ideas: list(raw.ideas).map(idea),
    tasks: list(raw.tasks).map(task),
    ...references(raw),
    ...effectiveOutcomes(raw),
  };
}
export function newDecisionID(): string {
  return (
    'd' + Array.from(crypto.getRandomValues(new Uint8Array(12)), (byte) => byte.toString(16).padStart(2, '0')).join('')
  );
}
/** One idea with the owner's decision. Human provenance binds the deciding
 * owner; without an actor, or back to pending, the idea carries none. */
export function decideIdea(
  idea: ProductIdea,
  decision: ProductIdea['decision'],
  actor: string,
  correction?: string
): ProductIdea {
  const next: ProductIdea = {...idea, decision};
  if (correction !== undefined) next.correction = correction;
  else if (decision !== 'edit') delete next.correction;
  if (decision === 'pending' || !actor) {
    delete next.provenance;
    delete next.actor;
  } else {
    next.provenance = 'human';
    next.actor = actor;
  }
  return next;
}
