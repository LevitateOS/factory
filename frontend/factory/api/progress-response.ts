import {flag, list, num, optionalNum, record, text, texts} from './models.js';

// Lenient readers for the request-overview, issue-plan, repository-access
// and scope-revision reads. Missing or mistyped fields fall back to
// defaults; extra fields and unknown states pass through as text.

const nonEmpty = (value: unknown): string | undefined =>
  typeof value === 'string' && value !== '' ? value : undefined;

export interface RequestDirection {
  readonly objective: string;
  readonly ideas_by_decision: Record<string, number>;
  readonly ideas_by_source: Record<string, number>;
  readonly effective_outcomes: readonly string[];
  readonly ideas_total: number;
  readonly tasks_total: number;
  readonly unresolved_assumptions: number;
  readonly approach?: string;
}
export interface RequestContext {
  readonly repository: string;
  readonly department?: string;
}
export interface RequestMapping {
  readonly task_key: string;
  readonly issue_id: string;
  readonly issue_index: string;
  readonly acceptance_id: string;
  readonly created_unix: number;
  readonly effective: boolean;
}
export interface RequestCounts {
  readonly delivered: number;
  readonly working: number;
  readonly waiting: number;
  readonly attention: number;
  readonly total: number;
  readonly outcomes_total: number;
  readonly outcomes_evidenced: number;
}
export interface RequestAttention {
  readonly code: string;
  readonly issue?: string;
  readonly idea?: string;
}
export interface RequestOverview {
  readonly direction: RequestDirection;
  readonly context: RequestContext;
  readonly generation: string;
  readonly setup: {state: string; project?: string};
  readonly work_items: RequestCounts;
  readonly mappings: readonly RequestMapping[];
  readonly attention: readonly RequestAttention[];
  readonly intent: string;
  readonly revision: string;
  readonly state: string;
  readonly observed_unix: number;
}
function countMap(value: unknown): Record<string, number> {
  const out: Record<string, number> = {};
  for (const [key, count] of Object.entries(record(value))) if (typeof count === 'number') out[key] = count;
  return out;
}
function requestDirection(value: unknown): RequestDirection {
  const raw = record(value);
  const approach = nonEmpty(raw.approach);
  return {
    objective: text(raw.objective),
    ideas_by_decision: countMap(raw.ideas_by_decision),
    ideas_by_source: countMap(raw.ideas_by_source),
    effective_outcomes: texts(raw.effective_outcomes),
    ideas_total: num(raw.ideas_total),
    tasks_total: num(raw.tasks_total),
    unresolved_assumptions: num(raw.unresolved_assumptions),
    ...(approach === undefined ? {} : {approach}),
  };
}
function requestContext(value: unknown): RequestContext {
  const raw = record(value);
  const department = nonEmpty(raw.department);
  return {repository: text(raw.repository), ...(department === undefined ? {} : {department})};
}
function requestMapping(value: unknown): RequestMapping {
  const raw = record(value);
  return {
    task_key: text(raw.task_key),
    issue_id: text(raw.issue_id),
    issue_index: text(raw.issue_index),
    acceptance_id: text(raw.acceptance_id),
    created_unix: num(raw.created_unix),
    effective: flag(raw.effective),
  };
}
// The backend omits zero by_state buckets (sparse maps), so absent buckets
// read as zero.
function requestCounts(value: unknown): RequestCounts {
  const raw = record(value);
  const by = record(raw.by_state);
  return {
    delivered: num(by.delivered),
    working: num(by.working),
    waiting: num(by.waiting),
    attention: num(by.attention),
    total: num(raw.total),
    outcomes_total: num(raw.outcomes_total),
    outcomes_evidenced: num(raw.outcomes_evidenced),
  };
}
function requestAttention(value: unknown): RequestAttention {
  const raw = record(value);
  const issue = nonEmpty(raw.issue);
  const idea = nonEmpty(raw.idea);
  return {
    code: text(raw.code),
    ...(issue === undefined ? {} : {issue}),
    ...(idea === undefined ? {} : {idea}),
  };
}
function requestSetup(value: unknown): {state: string; project?: string} {
  const raw = record(value);
  const project = nonEmpty(raw.project);
  return {state: text(raw.state), ...(project === undefined ? {} : {project})};
}
// requestOverview reads one owner-private request overview. Generation
// states pass through verbatim; counts are evidenced work-item facts.
export function requestOverview(value: unknown, intent: string): RequestOverview {
  const data = record(value);
  return {
    direction: requestDirection(data.direction),
    context: requestContext(data.context),
    generation: typeof data.generation === 'string' ? data.generation : text(record(data.generation).state),
    setup: requestSetup(data.setup),
    work_items: requestCounts(data.work_items),
    mappings: list(data.mappings).map(requestMapping),
    attention: list(data.attention).map(requestAttention),
    intent,
    revision: text(data.revision),
    state: text(data.state),
    observed_unix: num(data.observed_unix),
  };
}

export interface PriorityCandidate {
  readonly id: string;
  readonly repository: string;
  readonly issue: string;
  readonly readiness: string;
  readonly first_seen_unix: number;
  readonly assessed_unix: number;
  readonly age_seconds: number;
  readonly impact_known: boolean;
  readonly risk_known: boolean;
  readonly effort_known: boolean;
  readonly reason?: string;
  readonly acceptance?: string;
  readonly blocker_codes: readonly string[];
}
export interface PriorityCall {
  readonly selected?: string;
  readonly model?: string;
  readonly failure?: string;
  readonly confidence?: number;
  readonly input_tokens?: number;
  readonly output_tokens?: number;
}
export interface PriorityComparison {
  readonly candidates: readonly PriorityCandidate[];
  readonly calls: readonly PriorityCall[];
  readonly snapshot: string;
  readonly outcome: string;
  readonly created_unix: number;
  readonly candidates_total: number;
  readonly other_count: number;
  readonly probability_count: number;
  readonly selected_visible: boolean;
  readonly probabilities_full: boolean;
  readonly probabilities?: Record<string, number>;
  readonly selected?: string;
}
export interface IssuePlan {
  readonly comparisons: readonly PriorityComparison[];
  readonly repository: string;
  readonly issue: string;
  readonly observed_unix: number;
  readonly next_before_unix?: number;
  readonly next_before_snapshot?: string;
}
function judgmentNumbers(raw: Record<string, unknown>) {
  return {
    ...(typeof raw.confidence === 'number' ? {confidence: raw.confidence} : {}),
    ...(typeof raw.input_tokens === 'number' ? {input_tokens: raw.input_tokens} : {}),
    ...(typeof raw.output_tokens === 'number' ? {output_tokens: raw.output_tokens} : {}),
  };
}
function probabilities(raw: Record<string, unknown>): {probabilities?: Record<string, number>} {
  return raw.probabilities === undefined ? {} : {probabilities: countMap(raw.probabilities)};
}
function priorityCandidate(value: unknown): PriorityCandidate {
  const raw = record(value);
  const reason = nonEmpty(raw.reason);
  const acceptance = nonEmpty(raw.acceptance);
  return {
    id: text(raw.id),
    repository: text(raw.repository),
    issue: text(raw.issue),
    readiness: text(raw.readiness),
    first_seen_unix: num(raw.first_seen_unix),
    assessed_unix: num(raw.assessed_unix),
    age_seconds: num(raw.age_seconds),
    impact_known: flag(raw.impact_known),
    risk_known: flag(raw.risk_known),
    effort_known: flag(raw.effort_known),
    ...(reason === undefined ? {} : {reason}),
    ...(acceptance === undefined ? {} : {acceptance}),
    blocker_codes: texts(raw.blocker_codes),
  };
}
function priorityCall(value: unknown): PriorityCall {
  const raw = record(value);
  const selected = nonEmpty(raw.selected);
  const model = nonEmpty(raw.model);
  const failure = nonEmpty(raw.failure);
  return {
    ...(selected === undefined ? {} : {selected}),
    ...(model === undefined ? {} : {model}),
    ...(failure === undefined ? {} : {failure}),
    ...judgmentNumbers(raw),
  };
}
function priorityComparison(value: unknown): PriorityComparison {
  const raw = record(value);
  const selected = nonEmpty(raw.selected);
  return {
    candidates: list(raw.candidates).map(priorityCandidate),
    calls: list(raw.calls).map(priorityCall),
    snapshot: text(raw.snapshot),
    outcome: text(raw.outcome),
    created_unix: num(raw.created_unix),
    candidates_total: num(raw.candidates_total),
    other_count: num(raw.other_count),
    probability_count: num(raw.probability_count),
    selected_visible: flag(raw.selected_visible),
    probabilities_full: flag(raw.probabilities_full),
    ...probabilities(raw),
    ...(selected === undefined ? {} : {selected}),
  };
}
// issuePlan reads one issue's recent priority comparisons. Paging
// cursors travel as a pair; a half cursor is dropped.
export function issuePlan(value: unknown, repository: string, issue: string): IssuePlan {
  const data = record(value);
  const before = optionalNum(data.next_before_unix);
  const snapshot = nonEmpty(data.next_before_snapshot);
  return {
    comparisons: list(data.comparisons).map(priorityComparison),
    repository,
    issue,
    observed_unix: num(data.observed_unix),
    ...(before === undefined || snapshot === undefined
      ? {}
      : {next_before_unix: before, next_before_snapshot: snapshot}),
  };
}

export interface AccessViewer {
  readonly code_write: boolean;
  readonly owner_or_admin: boolean;
  readonly environment_owner: boolean;
  readonly operator: boolean;
  readonly member: boolean;
  readonly session_current: boolean;
}
export interface AccessRequiredAction {
  readonly code: string;
  readonly resolve_by: string;
  readonly setting: string;
}
export interface AccessEffective {
  readonly effective: boolean;
  readonly dispatch_open: boolean;
  readonly missing: readonly string[];
}
export interface FactoryAccess {
  readonly repository: string;
  readonly observed_unix: number;
  readonly viewer: AccessViewer;
  readonly effective: AccessEffective;
  readonly required_actions: readonly AccessRequiredAction[];
}
// factoryAccess reads one repository's viewer standing and effective
// verdict with required actions.
export function factoryAccess(value: unknown, repository: string): FactoryAccess {
  const data = record(value);
  const viewer = record(data.viewer);
  const effective = record(data.effective);
  return {
    repository,
    observed_unix: num(data.observed_unix),
    viewer: {
      code_write: flag(viewer.code_write),
      owner_or_admin: flag(viewer.owner_or_admin),
      environment_owner: flag(viewer.environment_owner),
      operator: flag(viewer.operator),
      member: flag(viewer.member),
      session_current: flag(viewer.session_current),
    },
    effective: {
      effective: flag(effective.effective),
      dispatch_open: flag(effective.dispatch_open),
      missing: texts(effective.missing),
    },
    required_actions: list(data.required_actions).map((entry) => {
      const raw = record(entry);
      return {code: text(raw.code), resolve_by: text(raw.resolve_by), setting: text(raw.setting)};
    }),
  };
}

export interface RevisionTask {
  readonly key: string;
  readonly objective: string;
  readonly outcomes: readonly string[];
  readonly idea_ids: readonly string[];
  readonly depends_on: readonly string[];
}
export interface RevisionTaskChange {
  readonly key: string;
  readonly changed_fields: readonly string[];
  readonly old_objective?: string;
  readonly new_objective?: string;
  readonly old_outcomes?: readonly string[];
  readonly new_outcomes?: readonly string[];
}
export interface RevisionTaskDiff {
  readonly added: readonly RevisionTask[];
  readonly removed: readonly RevisionTask[];
  readonly changed: readonly RevisionTaskChange[];
  readonly reused: readonly string[];
  readonly unchanged_count: number;
}
export interface RevisionIdeaBody {
  readonly id: string;
  readonly description: string;
  readonly decision: string;
}
export interface RevisionIdeaChange {
  readonly id: string;
  readonly from_decision: string;
  readonly to_decision: string;
  readonly correction_changed: boolean;
  readonly content_changed: boolean;
}
export interface RevisionIdeaDiff {
  readonly added: readonly RevisionIdeaBody[];
  readonly removed: readonly RevisionIdeaBody[];
  readonly changed: readonly RevisionIdeaChange[];
}
export interface RevisionScalarChange {
  readonly from: string;
  readonly to: string;
}
export interface RevisionComparison {
  readonly tasks: RevisionTaskDiff;
  readonly ideas: RevisionIdeaDiff;
  readonly effective_added: readonly string[];
  readonly effective_removed: readonly string[];
  readonly repository_change?: RevisionScalarChange;
  readonly state_change?: RevisionScalarChange;
  readonly initial: boolean;
  readonly edges_changed: boolean;
}
export interface RevisionWork {
  readonly task_key: string;
  readonly issue_index: string;
  readonly state: string;
  readonly authority: string;
  readonly content_version: number;
}
export interface RevisionGeneration {
  readonly state: string;
  readonly submitted_keys: readonly string[];
  readonly submitted_differs: boolean;
}
export interface RevisionEntry {
  readonly revision: string;
  readonly state: string;
  readonly repository: string;
  readonly created_unix: number;
  readonly generation: RevisionGeneration;
  readonly comparison: RevisionComparison;
  readonly work: readonly RevisionWork[];
  readonly generated_from?: string;
}
export interface RevisionHistory {
  readonly intent: string;
  readonly observed_unix: number;
  readonly revisions: readonly RevisionEntry[];
  readonly truncated: boolean;
}
function revisionTask(value: unknown): RevisionTask {
  const raw = record(value);
  return {
    key: text(raw.key),
    objective: text(raw.objective),
    outcomes: texts(raw.outcomes),
    idea_ids: texts(raw.idea_ids),
    depends_on: texts(raw.depends_on),
  };
}
// old/new texts travel only with their changed field.
function revisionTaskChange(value: unknown): RevisionTaskChange {
  const raw = record(value);
  const fields = texts(raw.changed_fields);
  return {
    key: text(raw.key),
    changed_fields: fields,
    ...(fields.includes('objective')
      ? {old_objective: text(raw.old_objective), new_objective: text(raw.new_objective)}
      : {}),
    ...(fields.includes('outcomes')
      ? {old_outcomes: texts(raw.old_outcomes), new_outcomes: texts(raw.new_outcomes)}
      : {}),
  };
}
function revisionTaskDiff(value: unknown): RevisionTaskDiff {
  const raw = record(value);
  return {
    added: list(raw.added).map(revisionTask),
    removed: list(raw.removed).map(revisionTask),
    changed: list(raw.changed).map(revisionTaskChange),
    reused: texts(raw.reused),
    unchanged_count: num(raw.unchanged_count),
  };
}
function revisionIdeaBody(value: unknown): RevisionIdeaBody {
  const raw = record(value);
  return {id: text(raw.id), description: text(raw.description), decision: text(raw.decision)};
}
function revisionIdeaChange(value: unknown): RevisionIdeaChange {
  const raw = record(value);
  return {
    id: text(raw.id),
    from_decision: text(raw.from_decision),
    to_decision: text(raw.to_decision),
    correction_changed: flag(raw.correction_changed),
    content_changed: flag(raw.content_changed),
  };
}
function revisionIdeaDiff(value: unknown): RevisionIdeaDiff {
  const raw = record(value);
  return {
    added: list(raw.added).map(revisionIdeaBody),
    removed: list(raw.removed).map(revisionIdeaBody),
    changed: list(raw.changed).map(revisionIdeaChange),
  };
}
function revisionScalarChange(value: unknown): RevisionScalarChange | undefined {
  if (value === undefined || value === null) return undefined;
  const raw = record(value);
  return {from: text(raw.from), to: text(raw.to)};
}
function revisionComparison(value: unknown): RevisionComparison {
  const raw = record(value);
  const effective = record(raw.effective_outcomes);
  const repository = revisionScalarChange(raw.repository_change);
  const state = revisionScalarChange(raw.state_change);
  return {
    tasks: revisionTaskDiff(raw.tasks),
    ideas: revisionIdeaDiff(raw.ideas),
    effective_added: texts(effective.added),
    effective_removed: texts(effective.removed),
    ...(repository === undefined ? {} : {repository_change: repository}),
    ...(state === undefined ? {} : {state_change: state}),
    initial: flag(raw.initial),
    edges_changed: flag(raw.edges_changed),
  };
}
function revisionWork(value: unknown): RevisionWork {
  const raw = record(value);
  return {
    task_key: text(raw.task_key),
    issue_index: text(raw.issue_index),
    state: text(raw.state),
    authority: text(raw.authority),
    content_version: num(raw.content_version),
  };
}
function revisionEntry(value: unknown): RevisionEntry {
  const raw = record(value);
  const generation = record(raw.generation);
  const from = nonEmpty(raw.generated_from);
  return {
    revision: text(raw.revision),
    state: text(raw.state),
    repository: text(raw.repository),
    created_unix: num(raw.created_unix),
    generation: {
      state: text(generation.state),
      submitted_keys: texts(generation.submitted_keys),
      submitted_differs: flag(generation.submitted_differs),
    },
    comparison: revisionComparison(raw.comparison),
    work: list(raw.work).map(revisionWork),
    ...(from === undefined ? {} : {generated_from: from}),
  };
}
// revisionHistory reads one request's scope-revision page: newest-first
// revisions with structural before/after and affected work.
export function revisionHistory(value: unknown, intent: string): RevisionHistory {
  const data = record(value);
  return {
    intent,
    observed_unix: num(data.observed_unix),
    revisions: list(data.revisions).map(revisionEntry),
    truncated: flag(data.truncated),
  };
}
