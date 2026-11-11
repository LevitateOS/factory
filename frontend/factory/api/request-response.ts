import {check, record, list, member, text, num, flag, optionalNum, texts, SodaRequestError} from './models.js';

export interface Department {
  id: string;
  name: string;
  description: string;
}
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
export interface ProposalBranch {
  outcome: string;
  result: string;
}
export interface ProposalStep {
  actor: string;
  action: string;
  result: string;
  branches?: ProposalBranch[];
}
export interface ProposalAlternative {
  key: string;
  name: string;
  audience?: string;
  summary?: string;
  steps: ProposalStep[];
  visibility?: string;
  consequences?: string;
  evidence?: string;
  outcomes: string[];
}
export interface Exclusion {
  idea: string;
  description: string;
  consequence: string;
  provenance?: 'human' | 'autonomous';
}
export interface ProposalContent {
  producer: string;
  supported: boolean;
  intent?: string;
  revision?: string;
  objective?: string;
  facts?: string[];
  alternatives?: ProposalAlternative[];
  excluded?: string[];
  exclusions?: Exclusion[];
  unknowns: string[];
}
const REVISION_STATES = ['draft', 'accepted', 'declined', 'rejected', 'abandoned'] as const;
export type IntentRevisionState = (typeof REVISION_STATES)[number] | 'unknown';
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
export interface AssumptionChallenge {
  idea: string;
  assumption: string;
  consequence: string;
  recommendation: string;
}
export interface Milestone {
  name: string;
  result: string;
}
export interface ConsolidatedProposal {
  producer: string;
  intent: string;
  revision: string;
  objective: string;
  recommended: string;
  alternative: ProposalAlternative;
  outcomes: string[];
  constraints: string[];
  excluded?: string[];
  milestones: Milestone[];
  advice: string;
}
export interface ConsistencyMismatch {
  class: 'effect_added' | 'outcome_gap' | 'summary_drift' | 'unverified';
  detail: string;
}
export interface ConsistencyReport {
  producer: string;
  intent: string;
  revision: string;
  mismatches: ConsistencyMismatch[];
  confidence: number;
  advice: string;
}
export interface Exploration {
  content: ProposalContent;
  challenges: AssumptionChallenge[];
  settled: string[];
  recommendation?: ConsolidatedProposal;
  consistency?: ConsistencyReport;
}
export interface SavedRequest {
  intent: string;
  revision: string;
  state: IntentRevisionState;
  objective: string;
  repository: string;
  department?: string;
  created_unix: number;
  effective_count: number;
  task_count: number;
  effective_outcomes?: string[];
}
export const decisionID = (value: unknown): value is string =>
  typeof value === 'string' && /^d[0-9a-f]{24}$/.test(value);
// Lenient readers: each field falls back to a default when missing or of
// another type; extra fields and unknown enum values pass through.
const nonEmpty = (value: unknown): string | undefined =>
  typeof value === 'string' && value !== '' ? value : undefined;
export function department(value: unknown): Department {
  const raw = record(value);
  return {id: text(raw.id), name: text(raw.name), description: text(raw.description)};
}
export function departments(value: unknown): Department[] {
  return list(value).map(department);
}
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
function savedRequest(value: unknown): SavedRequest {
  const raw = record(value);
  const department = nonEmpty(raw.department);
  return {
    intent: text(raw.intent),
    revision: text(raw.revision),
    state: member(REVISION_STATES, raw.state),
    objective: text(raw.objective),
    repository: text(raw.repository, '0'),
    ...(department ? {department} : {}),
    created_unix: num(raw.created_unix),
    effective_count: num(raw.effective_count),
    task_count: num(raw.task_count),
    ...effectiveOutcomes(raw),
  };
}
export interface IntentGenerationStatus {
  state: string;
  revision: string;
  tasks: number;
  authorization?: string;
  native?: number;
}
export function intentGeneration(value: unknown): IntentGenerationStatus {
  const raw = record(value);
  const out: IntentGenerationStatus = {
    state: text(raw.state),
    revision: text(raw.revision_id),
    tasks: Object.keys(record(raw.task_issue_ids)).length,
  };
  // authorization_revision is "<revision>:<digest>"; only the revision is kept.
  const authorization = nonEmpty(raw.authorization_revision);
  if (authorization) out.authorization = authorization.split(':')[0] ?? authorization;
  const native = optionalNum(raw.native_revision);
  if (native !== undefined) out.native = native;
  return out;
}
export function intentList(value: unknown): {intents: SavedRequest[]; next_cursor?: string} {
  const raw = record(value);
  const cursor = nonEmpty(raw.next_cursor);
  return {intents: list(raw.intents).map(savedRequest), ...(cursor ? {next_cursor: cursor} : {})};
}
function branch(value: unknown): ProposalBranch {
  const raw = record(value);
  return {outcome: text(raw.outcome), result: text(raw.result)};
}
function step(value: unknown): ProposalStep {
  const raw = record(value);
  const branches = list(raw.branches).map(branch);
  return {
    actor: text(raw.actor),
    action: text(raw.action),
    result: text(raw.result),
    ...(branches.length ? {branches} : {}),
  };
}
function alternative(value: unknown): ProposalAlternative {
  const raw = record(value);
  const out: ProposalAlternative = {
    key: text(raw.key),
    name: text(raw.name),
    steps: list(raw.steps).map(step),
    outcomes: texts(raw.outcomes),
  };
  for (const field of ['audience', 'summary', 'visibility', 'consequences', 'evidence'] as const) {
    const entry = nonEmpty(raw[field]);
    if (entry) out[field] = entry;
  }
  return out;
}
function proposalContent(value: unknown): ProposalContent {
  const raw = record(value);
  const supported = flag(raw.supported);
  const out: ProposalContent = {producer: text(raw.producer), supported, unknowns: texts(raw.unknowns)};
  const intent = nonEmpty(raw.intent);
  if (intent) out.intent = intent;
  const revision = nonEmpty(raw.revision);
  if (revision) out.revision = revision;
  const objective = nonEmpty(raw.objective);
  if (objective) out.objective = objective;
  if (raw.facts !== undefined) out.facts = texts(raw.facts);
  if (supported) out.alternatives = list(raw.alternatives).map(alternative);
  if (raw.excluded !== undefined) out.excluded = texts(raw.excluded);
  if (raw.exclusions !== undefined && raw.exclusions !== null) out.exclusions = list(raw.exclusions).map(exclusion);
  return out;
}
function exclusion(value: unknown): Exclusion {
  const raw = record(value);
  const out: Exclusion = {
    idea: text(raw.idea),
    description: text(raw.description),
    consequence: text(raw.consequence),
  };
  const provenance = nonEmpty(raw.provenance);
  if (provenance) out.provenance = provenance as NonNullable<Exclusion['provenance']>;
  return out;
}
function challenge(value: unknown): AssumptionChallenge {
  const raw = record(value);
  return {
    idea: text(raw.idea),
    assumption: text(raw.assumption),
    consequence: text(raw.consequence),
    recommendation: text(raw.recommendation),
  };
}
function milestone(value: unknown): Milestone {
  const raw = record(value);
  return {name: text(raw.name), result: text(raw.result)};
}
function recommendation(value: unknown): ConsolidatedProposal {
  const raw = record(value);
  const other = alternative(raw.alternative);
  return {
    producer: text(raw.producer),
    intent: text(raw.intent),
    revision: text(raw.revision),
    objective: text(raw.objective),
    recommended: other.key,
    alternative: other,
    outcomes: texts(raw.outcomes),
    constraints: texts(raw.constraints),
    ...(raw.excluded !== undefined ? {excluded: texts(raw.excluded)} : {}),
    milestones: list(raw.milestones).map(milestone),
    advice: text(raw.advice),
  };
}
function mismatch(value: unknown): ConsistencyMismatch {
  const raw = record(value);
  return {class: text(raw.class) as ConsistencyMismatch['class'], detail: text(raw.detail)};
}
function consistency(value: unknown): ConsistencyReport {
  const raw = record(value);
  return {
    producer: text(raw.producer),
    intent: text(raw.intent),
    revision: text(raw.revision),
    mismatches: list(raw.mismatches).map(mismatch),
    confidence: num(raw.confidence),
    advice: text(raw.advice),
  };
}
export function exploration(value: unknown): Exploration {
  const raw = record(value);
  const out: Exploration = {
    content: proposalContent(raw.content),
    challenges: list(raw.challenges).map(challenge),
    settled: texts(raw.settled),
  };
  if (raw.recommendation !== undefined && raw.recommendation !== null)
    out.recommendation = recommendation(raw.recommendation);
  if (raw.consistency !== undefined && raw.consistency !== null) out.consistency = consistency(raw.consistency);
  return out;
}
export interface SavedReview extends Exploration {
  intent: string;
  basis_revision: string;
  result_revision: string;
  saved_unix: number;
}
export interface SavedReviewRead {
  intent: string;
  head_revision: string;
  status: 'current' | 'stale' | 'missing';
  review?: SavedReview;
}
function savedReview(value: unknown): SavedReview {
  const raw = record(value);
  return {
    intent: text(raw.intent),
    basis_revision: text(raw.basis_revision),
    result_revision: text(raw.result_revision),
    saved_unix: num(raw.saved_unix),
    ...exploration(value),
  };
}
export function savedReviewRead(value: unknown): SavedReviewRead {
  const raw = record(value);
  const out: SavedReviewRead = {
    intent: text(raw.intent),
    head_revision: text(raw.head_revision),
    status: text(raw.status, 'missing') as SavedReviewRead['status'],
  };
  if (out.status !== 'missing' && raw.review !== undefined && raw.review !== null) out.review = savedReview(raw.review);
  return out;
}
export function newDecisionID(): string {
  return (
    'd' + Array.from(crypto.getRandomValues(new Uint8Array(12)), (byte) => byte.toString(16).padStart(2, '0')).join('')
  );
}
export function planActionBody(revision: string, operation: string): Record<string, unknown> {
  check(decisionID(revision) && (operation === 'propose_intent' || operation === 'decomposition_options'));
  return {revision, operation};
}
export interface IntentPlanWait {
  launched: false;
  waits: {reason: string; detail: string}[];
}
export const planWait = (value: unknown): value is IntentPlanWait => {
  if (!value || typeof value !== 'object') return false;
  const raw = value as Record<string, unknown>;
  return raw.launched === false && Array.isArray(raw.waits);
};
export function generationWaitNotice(value: unknown): string {
  const raw = record(value);
  const waits = list(raw.waits);
  const first =
    waits.length > 0 && waits[0] && typeof waits[0] === 'object' ? (waits[0] as Record<string, unknown>) : {};
  const detail =
    typeof first.detail === 'string' && first.detail ? first.detail : 'the required preparation is not ready';
  return `Factory work is waiting: ${detail} No native work was started.`;
}
const generationFailureNotices: Record<string, string> = {
  native_graph_unavailable:
    'Your product direction is saved. An administrator must enable native work creation before Factory can proceed.',
  planning_choice_unavailable:
    'Your product direction is saved. Factory needs a completed decomposition plan for this direction before native work can be created.',
  planning_attention:
    'Your product direction is saved. The recorded plan needs a product decision before native work can be created.',
  stale_planning_choice:
    'Your product direction is saved. The direction changed while Factory was planning; revise it and plan again before retrying.',
  intent_review_required:
    'Your product direction is saved. The proposed work needs reconsideration before native work can be created.',
};
export function generationFailureNotice(error: unknown): string {
  if (error instanceof SodaRequestError && error.code !== undefined) {
    const notice = generationFailureNotices[error.code];
    if (notice !== undefined) return notice;
  }
  return 'Your product direction is saved. Native work creation could not be confirmed. Factory will inspect the original request before retrying.';
}
export function planWaitNotice(value: unknown): string {
  const raw = record(value);
  const waits = list(raw.waits);
  const first =
    waits.length > 0 && waits[0] && typeof waits[0] === 'object' ? (waits[0] as Record<string, unknown>) : {};
  const detail =
    typeof first.detail === 'string' && first.detail ? first.detail : 'the required preparation is not ready';
  return `Planning is waiting: ${detail} No work was started.`;
}
export interface PlanWorkOutcome {
  notice: string;
  status?: IntentPlanStatus;
}
export const planningRunningNotice = 'Planning is running. The status below follows the run.';
export const planningFailedNotice = 'Planning failed. Your direction is saved; try again.';
export const planningCancelledNotice = 'Planning was cancelled before it could report.';
// The one wording for a plan in attention: a product question, or a planner
// that stopped without a usable answer (no question text).
export function attentionText(attention: string | undefined): string {
  return attention
    ? `Planning needs a product decision: ${attention}`
    : 'Planning stopped before it could propose work. Your request is saved.';
}
export async function runPlanWork(
  postPlan: () => Promise<unknown>,
  fetchPlan: () => Promise<unknown>,
  revision: string
): Promise<PlanWorkOutcome> {
  const posted = await postPlan();
  if (planWait(posted)) return {notice: planWaitNotice(posted)};
  const launched = intentPlanLaunch(posted);
  check(launched.revision === revision);
  const status = intentPlanStatus(await fetchPlan());
  check(status.revision === revision);
  // An unconfirmed start keeps its own notice: only the launch response
  // can lift that uncertainty. Otherwise a fast-settling run reports its
  // settled state, never a running text for it: failure and cancellation
  // restate their guidance, completion clears the notice for the review
  // flow, and attention asks for the product decision.
  if (launched.launchConfirmed === false)
    return {
      notice:
        'Planning was accepted, but its start response was not confirmed. The status below follows the existing run.',
      status,
    };
  if (status.state === 'failed') return {notice: planningFailedNotice, status};
  if (status.state === 'cancelled') return {notice: planningCancelledNotice, status};
  if (status.state === 'completed') return {notice: '', status};
  if (status.state === 'attention') return {notice: attentionText(status.attention), status};
  return {notice: planningRunningNotice, status};
}
export interface IntentPlanLaunch {
  assignment: string;
  run: string;
  revision: string;
  launchConfirmed?: boolean;
}
export function intentPlanLaunch(value: unknown): IntentPlanLaunch {
  const raw = record(value);
  return {
    assignment: text(raw.assignment_id),
    run: text(raw.run_id),
    revision: text(raw.revision_id),
    ...(typeof raw.launch_confirmed === 'boolean' ? {launchConfirmed: raw.launch_confirmed} : {}),
  };
}
const planStates = ['none', 'pending', 'completed', 'attention', 'failed', 'cancelled'] as const;
// 'unknown' is a state this client does not recognize; it is never success.
export type IntentPlanState = (typeof planStates)[number] | 'unknown';
// The server attaches a review only with its consistency report (both are
// produced together), so a review here always carries one.
export type PlanReview = Exploration & {consistency: ConsistencyReport};
export interface IntentPlanStatus {
  revision: string;
  state: IntentPlanState;
  assignment?: string;
  run?: string;
  summary?: string;
  candidates: number;
  attention?: string;
  walkthroughs?: ProposalContent;
  review?: PlanReview;
}
export function intentPlanStatus(value: unknown): IntentPlanStatus {
  const raw = record(value);
  const out: IntentPlanStatus = {
    revision: text(raw.revision),
    state: member(planStates, raw.state),
    candidates: num(raw.candidates),
  };
  const assignment = nonEmpty(raw.assignment_id);
  if (assignment) out.assignment = assignment;
  const run = nonEmpty(raw.run_id);
  if (run) out.run = run;
  const summary = nonEmpty(raw.summary);
  if (summary) out.summary = summary;
  const attention = nonEmpty(raw.attention);
  if (attention) out.attention = attention;
  if (raw.walkthroughs !== undefined && raw.walkthroughs !== null) out.walkthroughs = proposalContent(raw.walkthroughs);
  if (raw.review !== undefined && raw.review !== null) {
    const review = exploration(raw.review);
    // Advice without a consistency report is no review: it must be checked.
    if (review.consistency) out.review = {...review, consistency: review.consistency};
  }
  return out;
}
