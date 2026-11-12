import {check, flag, list, member, num, object, record, text, texts} from './models.js';
import {nativePath} from '../shared/format.js';
import {allowanceHolds, type AllowanceHold} from '../feed/allowance.js';

// Lenient readers for the Factory work-progress reads V01/V03/V04 consume:
// one issue's acceptance/readiness/check/merge facts, one repository's
// queue/setup/authority summary, and the outcomes of pause/resume/stop/
// retry/takeover commands. Each reads only the fields its view uses, with
// defaults for missing fields; unknown values pass through as text.

// nonEmpty reads an optional string, treating '' as absent.
const nonEmpty = (value: unknown): string | undefined =>
  typeof value === 'string' && value !== '' ? value : undefined;
const present = (value: unknown): boolean => value !== undefined && value !== null;

export interface IssueAcceptanceRef {
  id: string;
  issue_index: string;
  approver: string;
  native_revision: number;
  content_version: number;
}
export interface IssueAcceptanceStatus {
  valid: boolean;
  withdrawn: boolean;
  revision: number;
  reasons: string[];
  acceptance?: IssueAcceptanceRef;
}
export interface IssueBlocker {
  code: string;
  resolution: string;
  detail?: string;
  endpoint_repository?: string;
  endpoint_issue?: string;
}
// Closed vocabularies of the backend's recorded work facts. A value outside
// the vocabulary (a newer backend) reads as 'unknown' so an older UI still
// renders. Sources: pkg/factory/readiness.go, checks.go, merge.go,
// publication.go, assignment.go, types.go (Outcome) and
// pkg/factory/service/issue_view.go (closure).
const READINESS = ['not_authorized', 'blocked', 'queued', 'active', 'needs_intervention', 'cancelled', 'completed'] as const;
const CHECK_VERDICTS = ['pass', 'pending', 'failed', 'refused'] as const;
const MERGE_STAGES = ['open', 'merged', 'failed', 'withdrawn', 'fenced'] as const;
const PUBLICATION_STAGES = ['open', 'published', 'failed', 'withdrawn', 'fenced'] as const;
const ATTEMPT_STAGES = ['assigned', 'finished', 'retained'] as const;
const ATTEMPT_OUTCOMES = ['succeeded', 'failed', 'cancelled', 'needs-human'] as const;
const CLOSURE_STATES = ['none', 'pending', 'confirmed'] as const;
export type ReadinessState = (typeof READINESS)[number] | 'unknown';
export type CheckVerdict = (typeof CHECK_VERDICTS)[number] | 'unknown';
export type MergeStage = (typeof MERGE_STAGES)[number] | 'unknown';
export type PublicationStage = (typeof PUBLICATION_STAGES)[number] | 'unknown';
export type AttemptStage = (typeof ATTEMPT_STAGES)[number] | 'unknown';
export type AttemptOutcome = (typeof ATTEMPT_OUTCOMES)[number] | 'unknown';
export type ClosureState = (typeof CLOSURE_STATES)[number] | 'unknown';

export interface IssueReadiness {
  readiness: ReadinessState;
  reason: string;
  revision: number;
  assessed_unix: number;
  acceptance?: string;
  blockers: IssueBlocker[];
}
export interface IssueCheckResult {
  context: string;
  passed: boolean;
  state?: string;
}

export interface IssueChecks {
  verdict: CheckVerdict;
  reason: string;
  resolution: string;
  head_oid: string;
  base_oid: string;
  pr_number: string;
  pr_id: string;
  policy_revision: number;
  native_revision: number;
  revision: number;
  assessed_unix: number;
  stale: boolean;
  results: IssueCheckResult[];
}
export interface IssueMerge {
  stage: MergeStage;
  resolution: string;
  head_oid: string;
  base_oid: string;
  pr_number: string;
  pr_id: string;
  revision: number;
  stale: boolean;
  reason?: string;
  merged_commit?: string;
  finished_unix?: number;
}
export interface WorkAttempt {
  id: string;
  role: string;
  stage: AttemptStage;
  run: string;
  attempts: number;
  created_unix: number;
  outcome?: AttemptOutcome;
  reason?: string;
  finished_unix?: number;
}
export interface WorkCorrection {
  updated_unix: number;
  candidate?: string;
  effect?: string;
  completion?: string;
  reason?: string;
}
export interface WorkReview {
  run: string;
  head_oid: string;
  event: string;
  agent_verdict?: string;
  effect?: string;
  completion?: string;
  reason?: string;
}
export interface WorkPublication {
	origin?: 'external';
  candidate: string;
  base_sha: string;
  target_branch: string;
  id: string;
  run: string;
  publish_effect: string;
  pr_create_effect: string;
  stage: PublicationStage;
  corrections_total: number;
  reviews_total: number;
  created_unix: number;
  pr_number?: string;
  pr_id?: string;
  reason?: string;
  finished_unix?: number;
  corrections: WorkCorrection[];
  reviews: WorkReview[];
}
export interface WorkClosure {
  state: ClosureState;
  occurrence?: string;
  closed_unix?: number;
}
export interface WorkCapability {
  available: boolean;
  reason?: string;
}
export interface RetainedCandidateFunding {
  requirements?: string;
  approval?: string;
  previous_root?: string;
  carry_unused?: boolean;
  native?: {profile: string; authentication?: string; credential_sha256?: string};
  publication: string;
  candidate: string;
  base: string;
  acceptance: string;
  connection: string;
  grant_id: string;
  generation: number;
  policy: number;
  limits: {active_minutes: number; correction_cycles: number; review_rounds: number; max_children: number};
}
export function retainedCandidateFunding(value: unknown): RetainedCandidateFunding | undefined {
  if (!value || typeof value !== 'object' || Array.isArray(value)) return undefined;
  const r = value as Record<string, unknown>,
    l = r.limits as Record<string, unknown> | undefined;
  const n = r.native as Record<string, unknown> | undefined;
  const source =
    n === undefined
      ? typeof r.grant_id === 'string' &&
        r.grant_id.length > 0 &&
        r.grant_id.length <= 128 &&
        typeof r.generation === 'number' &&
        Number.isSafeInteger(r.generation) &&
        r.generation > 0
      : n !== null &&
        typeof n === 'object' &&
        !Array.isArray(n) &&
        Object.keys(n).every((k) => ['profile', 'authentication', 'credential_sha256'].includes(k)) &&
        typeof n.profile === 'string' &&
        /^[a-f0-9]{32}$/.test(n.profile) &&
        typeof r.connection === 'string' &&
        /^[a-f0-9]{32}$/.test(r.connection) &&
        r.grant_id === '' &&
        r.generation === 0 &&
        (n.authentication === 'muse_login'
          ? n.credential_sha256 === undefined || n.credential_sha256 === ''
          : (n.authentication === undefined || n.authentication === '') &&
            typeof n.credential_sha256 === 'string' &&
            /^[a-f0-9]{64}$/.test(n.credential_sha256));
  const integer = (x: unknown, min: number, max = Number.MAX_SAFE_INTEGER) =>
    typeof x === 'number' && Number.isSafeInteger(x) && x >= min && x <= max;
  if (
    Object.keys(r).length !==
      (n === undefined ? 9 : 10) + (r.previous_root === undefined ? 0 : 1) + (r.carry_unused === undefined ? 0 : 1) + (r.requirements === undefined && r.approval === undefined ? 0 : 2) ||
    ((r.requirements !== undefined || r.approval !== undefined) && !['requirements', 'approval'].every((k) => typeof r[k] === 'string' && /^d[a-f0-9]{24}$/.test(r[k] as string))) ||
    (r.previous_root !== undefined &&
      (typeof r.previous_root !== 'string' || !/^[a-f0-9]{32}$/.test(r.previous_root))) ||
    (r.carry_unused !== undefined && (r.carry_unused !== true || r.previous_root === undefined)) ||
    !source ||
    typeof r.publication !== 'string' ||
    !/^[a-f0-9]{32}$/.test(r.publication) ||
    !['candidate', 'base'].every((k) => typeof r[k] === 'string' && /^(?:[a-f0-9]{40}|[a-f0-9]{64})$/.test(r[k] as string)) ||
    r.candidate === r.base ||
    typeof r.acceptance !== 'string' ||
    !/^d[a-f0-9]{24}$/.test(r.acceptance) ||
    !['connection'].every(
      (k) => typeof r[k] === 'string' && (r[k] as string).length > 0 && new TextEncoder().encode(r[k] as string).length <= 128
    ) ||
    !integer(r.policy, 1) ||
    !l ||
    typeof l !== 'object' ||
    Array.isArray(l) ||
    Object.keys(l).length !== 4 ||
    !integer(l.active_minutes, 1) ||
    !integer(l.correction_cycles, 0) ||
    !integer(l.review_rounds, 1, 16) ||
    !integer(l.max_children, 1, 4)
  )
    return undefined;
  return value as RetainedCandidateFunding;
}
export interface MergePreparation {
	requirements_id?: string;
	approval_id?: string;
  retry_no_send?: true;
  publication: string;
  merge_id: string;
  revision: number;
  policy: number;
  head: string;
  base: string;
}
export function mergePreparation(value: unknown): MergePreparation | undefined {
  if (!value || typeof value !== 'object' || Array.isArray(value)) return undefined;
  const r = value as Record<string, unknown>;
  const setup = r.requirements_id !== undefined || r.approval_id !== undefined;
  if (
    Object.keys(r).length !== 6 + (r.retry_no_send === undefined ? 0 : 1) + (setup ? 2 : 0) ||
    (setup && !['requirements_id', 'approval_id'].every(k => typeof r[k] === 'string' && /^d[a-f0-9]{24}$/.test(r[k] as string))) ||
    (r.retry_no_send !== undefined && r.retry_no_send !== true) ||
    !['publication', 'merge_id'].every((k) => typeof r[k] === 'string' && /^[a-f0-9]{32}$/.test(r[k] as string)) ||
    !['head', 'base'].every((k) => typeof r[k] === 'string' && /^(?:[a-f0-9]{40}|[a-f0-9]{64})$/.test(r[k] as string)) ||
    typeof r.revision !== 'number' ||
    !Number.isSafeInteger(r.revision) ||
    r.revision < 0 ||
    typeof r.policy !== 'number' ||
    !Number.isSafeInteger(r.policy) ||
    r.policy < 1
  )
    return undefined;
  return value as MergePreparation;
}
export interface WorkCapabilities {
	adopt_external?: WorkCapability;
  retry_review?: WorkCapability & {review_retry: ReviewRetry};
  prepare_merge?: WorkCapability & {merge_preparation: MergePreparation};
  fund_retained?: WorkCapability & {funding: RetainedCandidateFunding};
  /** Older hosts keep this action unavailable. */
  publish_retained?: WorkCapability;
  watch: WorkCapability;
  stop: WorkCapability;
  retry: WorkCapability;
  takeover: WorkCapability;
  pause: WorkCapability;
  resume: WorkCapability;
}

export interface ReviewRetry {
  publication: string;
  run: string;
  operation: string;
  head: string;
  base: string;
  policy: number;
}
export function reviewRetry(value: unknown): ReviewRetry | undefined {
  if (!value || typeof value !== 'object' || Array.isArray(value)) return undefined;
  const r = value as Record<string, unknown>;
  if (Object.keys(r).length !== 6 ||
    !['publication', 'run'].every(k => typeof r[k] === 'string' && /^[a-f0-9]{32}$/.test(r[k] as string)) ||
    typeof r.operation !== 'string' || !/^[a-zA-Z0-9._:-]{1,128}$/.test(r.operation) ||
    !['head', 'base'].every(k => typeof r[k] === 'string' && /^(?:[a-f0-9]{40}|[a-f0-9]{64})$/.test(r[k] as string)) ||
    r.head === r.base || typeof r.policy !== 'number' || !Number.isSafeInteger(r.policy) || r.policy < 1) return undefined;
  return value as ReviewRetry;
}
export interface RequestedRetry {
  command_id: string;
  reason: string;
  prior_run: string;
  queued: boolean;
}
export interface WorkLinks {
  issue_url: string;
  pull_url?: string;
}
export interface FactoryIssueDetail {
  repository: string;
  issue: string;
  status: IssueAcceptanceStatus;
  closure: WorkClosure;
  capabilities: WorkCapabilities;
  observed_unix: number;
  readiness?: IssueReadiness;
  checks?: IssueChecks;
  merge?: IssueMerge;
  attempt?: WorkAttempt;
  publication?: WorkPublication;
  requested_retry?: RequestedRetry;
  links?: WorkLinks;
}

// issueBlocker reads one prerequisite blocker shared by the readiness and
// plan reads.
export function issueBlocker(value: unknown): IssueBlocker {
  const raw = record(value);
  return {
    code: text(raw.code),
    resolution: text(raw.resolution),
    ...(typeof raw.detail === 'string' ? {detail: raw.detail} : {}),
    ...(typeof raw.endpoint_repository === 'string' ? {endpoint_repository: raw.endpoint_repository} : {}),
    ...(typeof raw.endpoint_issue === 'string' ? {endpoint_issue: raw.endpoint_issue} : {}),
  };
}
export function issueReadiness(value: unknown): IssueReadiness {
  const raw = record(value);
  return {
    readiness: member(READINESS, raw.readiness),
    reason: text(raw.reason),
    revision: num(raw.revision),
    assessed_unix: num(raw.assessed_unix),
    ...(typeof raw.acceptance === 'string' ? {acceptance: raw.acceptance} : {}),
    blockers: list(raw.blockers).map(issueBlocker),
  };
}
function checkResult(value: unknown): IssueCheckResult {
  const raw = record(value);
  return {
    context: text(raw.context),
    passed: flag(raw.passed),
    ...(typeof raw.state === 'string' ? {state: raw.state} : {}),
  };
}
function issueChecks(value: unknown): IssueChecks {
  const raw = record(value);
  return {
    verdict: member(CHECK_VERDICTS, raw.verdict),
    reason: text(raw.reason),
    resolution: text(raw.resolution),
    head_oid: text(raw.head_oid),
    base_oid: text(raw.base_oid),
    pr_number: text(raw.pr_number),
    pr_id: text(raw.pr_id),
    policy_revision: num(raw.policy_revision),
    native_revision: num(raw.native_revision),
    revision: num(raw.revision),
    assessed_unix: num(raw.assessed_unix),
    stale: flag(raw.stale),
    results: list(raw.results).map(checkResult),
  };
}
function issueMerge(value: unknown): IssueMerge {
  const raw = record(value);
  const reason = nonEmpty(raw.reason);
  return {
    stage: member(MERGE_STAGES, raw.stage),
    resolution: text(raw.resolution),
    head_oid: text(raw.head_oid),
    base_oid: text(raw.base_oid),
    pr_number: text(raw.pr_number),
    pr_id: text(raw.pr_id),
    revision: num(raw.revision),
    stale: flag(raw.stale),
    ...(reason === undefined ? {} : {reason}),
    ...(typeof raw.merged_commit === 'string' ? {merged_commit: raw.merged_commit} : {}),
    ...(typeof raw.finished_unix === 'number' ? {finished_unix: raw.finished_unix} : {}),
  };
}
function acceptanceRef(value: unknown): IssueAcceptanceRef {
  const raw = record(value);
  return {
    id: text(raw.id),
    issue_index: text(raw.issue_index),
    approver: text(raw.approver),
    native_revision: num(raw.native_revision),
    content_version: num(raw.content_version),
  };
}
function acceptanceStatus(value: unknown): IssueAcceptanceStatus {
  const raw = record(value);
  return {
    valid: flag(raw.valid),
    withdrawn: flag(raw.withdrawn),
    revision: num(raw.revision),
    reasons: texts(raw.reasons),
    ...(present(raw.acceptance) ? {acceptance: acceptanceRef(raw.acceptance)} : {}),
  };
}
// opFields reads the optional effect/completion/reason trio shared by
// attempts, corrections and reviews; '' reads as absent.
function opFields(raw: Record<string, unknown>) {
  const effect = nonEmpty(raw.effect);
  const completion = nonEmpty(raw.completion);
  const reason = nonEmpty(raw.reason);
  return {
    ...(effect === undefined ? {} : {effect}),
    ...(completion === undefined ? {} : {completion}),
    ...(reason === undefined ? {} : {reason}),
  };
}
function workAttempt(value: unknown): WorkAttempt {
  const raw = record(value);
  const outcome = nonEmpty(raw.outcome);
  const reason = nonEmpty(raw.reason);
  return {
    id: text(raw.id),
    role: text(raw.role),
    stage: member(ATTEMPT_STAGES, raw.stage),
    run: text(raw.run),
    attempts: num(raw.attempts),
    created_unix: num(raw.created_unix),
    ...(outcome === undefined ? {} : {outcome: member(ATTEMPT_OUTCOMES, outcome)}),
    ...(reason === undefined ? {} : {reason}),
    ...(typeof raw.finished_unix === 'number' ? {finished_unix: raw.finished_unix} : {}),
  };
}
function workCorrection(value: unknown): WorkCorrection {
  const raw = record(value);
  const candidate = nonEmpty(raw.candidate);
  return {
    updated_unix: num(raw.updated_unix),
    ...(candidate === undefined ? {} : {candidate}),
    ...opFields(raw),
  };
}
function workReview(value: unknown): WorkReview {
  const raw = record(value);
  const verdict = nonEmpty(raw.agent_verdict);
  return {
    run: text(raw.run),
    head_oid: text(raw.head_oid),
    event: text(raw.event),
    ...(verdict === undefined ? {} : {agent_verdict: verdict}),
    ...opFields(raw),
  };
}
function workPublication(value: unknown): WorkPublication {
  const raw = record(value);
  const reason = nonEmpty(raw.reason);
  return {
	...(raw.origin === 'external' ? {origin: 'external' as const} : {}),
    candidate: text(raw.candidate),
    base_sha: text(raw.base_sha),
    id: text(raw.id),
    run: text(raw.run),
    publish_effect: text(raw.publish_effect),
    pr_create_effect: text(raw.pr_create_effect),
    target_branch: text(raw.target_branch),
    stage: member(PUBLICATION_STAGES, raw.stage),
    corrections_total: num(raw.corrections_total),
    reviews_total: num(raw.reviews_total),
    created_unix: num(raw.created_unix),
    ...(typeof raw.pr_number === 'string' ? {pr_number: raw.pr_number} : {}),
    ...(typeof raw.pr_id === 'string' ? {pr_id: raw.pr_id} : {}),
    ...(reason === undefined ? {} : {reason}),
    ...(typeof raw.finished_unix === 'number' ? {finished_unix: raw.finished_unix} : {}),
    corrections: list(raw.corrections).map(workCorrection),
    reviews: list(raw.reviews).map(workReview),
  };
}
function workClosure(value: unknown): WorkClosure {
  const raw = record(value);
  return {
    state: raw.state === undefined ? 'none' : member(CLOSURE_STATES, raw.state),
    ...(typeof raw.occurrence === 'string' ? {occurrence: raw.occurrence} : {}),
    ...(typeof raw.closed_unix === 'number' ? {closed_unix: raw.closed_unix} : {}),
  };
}
function workCapability(value: unknown): WorkCapability {
  const raw = record(value);
  const reason = nonEmpty(raw.reason);
  return {available: flag(raw.available), ...(reason === undefined ? {} : {reason})};
}
function workCapabilities(value: unknown): WorkCapabilities {
  const raw = record(value);
  const retry = reviewRetry(record(raw.retry_review).review_retry);
  const funding = retainedCandidateFunding(record(raw.fund_retained).funding);
  const preparation = mergePreparation(record(raw.prepare_merge).merge_preparation);
  return {
	...(present(raw.adopt_external) ? {adopt_external: workCapability(raw.adopt_external)} : {}),
    ...(retry ? {retry_review: {...workCapability(raw.retry_review), review_retry: retry}} : {}),
    ...(preparation ? {prepare_merge: {...workCapability(raw.prepare_merge), merge_preparation: preparation}} : {}),
    ...(funding ? {fund_retained: {...workCapability(raw.fund_retained), funding}} : {}),
    ...(present(raw.publish_retained) ? {publish_retained: workCapability(raw.publish_retained)} : {}),
    watch: workCapability(raw.watch),
    stop: workCapability(raw.stop),
    retry: workCapability(raw.retry),
    takeover: workCapability(raw.takeover),
    pause: workCapability(raw.pause),
    resume: workCapability(raw.resume),
  };
}
function requestedRetry(value: unknown): RequestedRetry | undefined {
  const retry = record(value).retry;
  if (!present(retry)) return undefined;
  const raw = record(retry);
  return {
    command_id: text(raw.command_id),
    reason: text(raw.reason),
    prior_run: text(raw.prior_run),
    queued: flag(raw.queued),
  };
}
// workLinks keeps only same-origin native paths: they become hrefs.
function workLinks(value: unknown): WorkLinks | undefined {
  const raw = record(value);
  if (!nativePath(raw.issue_url)) return undefined;
  return {issue_url: raw.issue_url, ...(nativePath(raw.pull_url) ? {pull_url: raw.pull_url} : {})};
}
// factoryIssueDetail reads one native issue's recorded acceptance,
// readiness, attempt, publication, review, check, merge and closure facts
// plus the viewer's current capabilities.
export function factoryIssueDetail(value: unknown, repository: string, issue: string): FactoryIssueDetail {
  const data = object(value);
  const requested = requestedRetry(data.requested);
  const links = workLinks(data.links);
  return {
    repository,
    issue,
    status: acceptanceStatus(data.status),
    closure: workClosure(data.closure),
    capabilities: workCapabilities(data.capabilities),
    observed_unix: num(data.observed_unix),
    ...(present(data.readiness) ? {readiness: issueReadiness(data.readiness)} : {}),
    ...(present(data.checks) ? {checks: issueChecks(data.checks)} : {}),
    ...(present(data.merge) ? {merge: issueMerge(data.merge)} : {}),
    ...(present(data.attempt) ? {attempt: workAttempt(data.attempt)} : {}),
    ...(present(data.publication) ? {publication: workPublication(data.publication)} : {}),
    ...(requested === undefined ? {} : {requested_retry: requested}),
    ...(links === undefined ? {} : {links}),
  };
}

export interface FactoryQueueSummary {
  queued: number;
  active: number;
}
export interface FactoryPreparationSummary {
  project: string;
  ready: boolean;
}
export interface FactoryEffectiveSummary {
  effective: boolean;
  dispatch_open: boolean;
  missing: string[];
}
export interface FactoryRepositorySummary {
  repository: string;
  queue: FactoryQueueSummary;
  effective: FactoryEffectiveSummary;
  preparation?: FactoryPreparationSummary;
  allowance?: AllowanceHold[];
  dispatch?: {stage: string; observedAt: string; reasons: string[]};
}
function effectiveSummary(value: unknown): FactoryEffectiveSummary {
  const raw = record(value);
  return {effective: flag(raw.effective), dispatch_open: flag(raw.dispatch_open), missing: texts(raw.missing)};
}
// factoryRepositorySummary reads one repository's recorded queue counts,
// setup preparation and effective authority.
export function factoryRepositorySummary(value: unknown, repository: string): FactoryRepositorySummary {
  const data = object(value);
  const queue = record(data.queue);
  const allowance = allowanceHolds(data, repository);
  const preparation = present(data.preparation) ? record(data.preparation) : undefined;
  const dispatch = present(data.dispatch_observation) ? record(data.dispatch_observation) : undefined;
  return {
    repository,
    queue: {queued: num(queue.queued), active: num(queue.active)},
    effective: effectiveSummary(data.effective),
    ...(preparation === undefined
      ? {}
      : {preparation: {project: text(preparation.project), ready: flag(preparation.ready)}}),
    ...(allowance.length > 0 ? {allowance} : {}),
    ...(dispatch === undefined ? {} : {dispatch: {
      stage: text(dispatch.stage),
      observedAt: text(dispatch.dispatch_at),
      reasons: [...(Array.isArray(dispatch.waits) ? dispatch.waits : []), ...(Array.isArray(dispatch.errors) ? dispatch.errors : [])]
        .slice(0, 64).map(value => {
          const reason = record(value);
          return `${text(reason.issue) ? `Issue #${text(reason.issue)}: ` : ''}${text(reason.reason).replaceAll('_', ' ')}`;
        }),
    }}),
  };
}

export interface PauseRunOutcome {
  id: string;
  confirmed: boolean;
  uncertain: boolean;
  outcome?: string;
  reason?: string;
}
export interface PauseResult {
  paused: boolean;
  repository: string;
  revision: number;
  cause: string;
  closed_by: string;
  runs: PauseRunOutcome[];
}
export interface ResumeResult {
  revision: number;
  reopened: boolean;
  effective: FactoryEffectiveSummary;
}
export interface StopReceipt {
  run_id: string;
  confirmed: boolean;
  uncertain: boolean;
  outcome?: string;
  reason?: string;
}
export interface RetryDecision {
  prior_run: string;
  command_id: string;
  reason: string;
  queued: boolean;
}
export interface TakeoverRecord {
  run: string;
  member: string;
  project: string;
  destination: string;
  copied: string;
}
// stopFields reads the confirmed/uncertain/outcome/reason facts shared by
// run-stop outcomes. Confirmed and uncertain stay distinct; '' reads as absent.
function stopFields(raw: Record<string, unknown>) {
  const outcome = nonEmpty(raw.outcome);
  const reason = nonEmpty(raw.reason);
  return {
    confirmed: flag(raw.confirmed),
    uncertain: flag(raw.uncertain),
    ...(outcome === undefined ? {} : {outcome}),
    ...(reason === undefined ? {} : {reason}),
  };
}
// runStopOutcome reads one recorded run-stop outcome from a pause or
// Project-stop receipt.
export function runStopOutcome(value: unknown): PauseRunOutcome {
  const raw = record(value);
  return {id: text(raw.id), ...stopFields(raw)};
}
// pauseResult reads one pause outcome. Uncertain runs stay fenced; they
// never read as cancelled.
export function pauseResult(value: unknown, repository: string): PauseResult {
  const data = object(value);
  const withdrawal = record(data.withdrawal);
  return {
    paused: data.paused !== false,
    repository,
    revision: num(withdrawal.revision),
    cause: text(withdrawal.cause),
    closed_by: text(withdrawal.closed_by),
    runs: list(data.runs).map(runStopOutcome),
  };
}
export function resumeResult(value: unknown): ResumeResult {
  const data = object(value);
  return {
    revision: num(data.revision),
    reopened: flag(data.reopened),
    effective: effectiveSummary(data.effective),
  };
}
// stopReceipt reads one run-stop outcome; neither confirmed nor uncertain
// is inferred from the request alone.
export function stopReceipt(value: unknown, runID: string): StopReceipt {
  return {run_id: runID, ...stopFields(object(value))};
}
export function retryDecision(value: unknown, commandID: string, priorRun: string): RetryDecision {
  const data = object(value);
  return {prior_run: priorRun, command_id: commandID, reason: text(data.reason), queued: data.queued !== false};
}
// Acknowledgement queues a diagnosis; it does not claim a repaired application.
export function deliveryRepairDecision(value: unknown, runID: string) {
  const data = object(value);
  // A result for another run cannot confirm this request.
  check(data.run_id === runID);
  return {
    run_id: runID,
    cause_id: text(data.cause_id),
    cycle_id: text(data.cycle_id),
    state: 'queued' as const,
  };
}
export function takeoverRecord(value: unknown, runID: string): TakeoverRecord {
  const data = object(value);
  return {
    run: runID,
    member: text(data.member),
    project: text(data.project),
    destination: text(data.destination),
    copied: text(data.copied),
  };
}
export interface ProjectStopResult {
  project: string;
  running: boolean;
  hold: boolean;
  hold_synced: boolean;
  withdrawal_revision: number;
  runs: PauseRunOutcome[];
}
// projectStopResult reads one explicit Project-stop outcome: the observed
// running state, maintenance-hold sync and every outstanding run's stop
// outcome.
export function projectStopResult(value: unknown, projectID: string): ProjectStopResult {
  const data = object(value);
  const environment = record(data.environment);
  const control = record(data.control);
  return {
    project: projectID,
    running: flag(environment.running),
    hold: flag(control.hold),
    hold_synced: flag(control.hold_synced),
    withdrawal_revision: num(record(control.withdrawal).revision),
    runs: list(control.runs).map(runStopOutcome),
  };
}
export {sodaErrorDetail} from './error-response.js';
export type {SodaErrorDetail} from './error-response.js';
export interface PendingRuns {
  runs: string[];
  message: string;
}
// pendingRuns reads a 409 unsettled-runs refusal: the blocking run
// identities and the retained message, never a success verdict. The error
// code is what tells this refusal from any other 409, so it stays checked.
export function pendingRuns(value: unknown): PendingRuns {
  const data = object(value);
  check(data.error === 'pending_runs');
  return {runs: texts(data.runs), message: text(data.message)};
}
