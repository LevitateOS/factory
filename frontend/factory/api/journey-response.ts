import {flag, list, num, object, optionalNum, record, text, texts} from './models.js';

// Lenient readers for the work-journey read: one issue's recorded phases,
// tasks with lanes, commit/PR evidence, structured findings with
// corrections, Jev judgments with effective waits, D13 completion gates with
// proposals and adoption, delivered-task evidence behind each adoption
// binding, and the verified application revision behind each adopted
// revision. Absent sections read as empty, never as guessed work; unknown
// values pass through as text. Evidence identities stay text here: native
// navigation links are built only in the view layer.

// nonEmpty reads an optional string, treating '' as absent.
const nonEmpty = (value: unknown): string | undefined =>
  typeof value === 'string' && value !== '' ? value : undefined;

export interface JourneyPhase {
  readonly phase: string;
  readonly state: string;
}

export interface JourneyTask {
  readonly key: string;
  readonly lane: string;
  readonly state: string;
  readonly issue?: string;
}

export interface JourneyCommit {
  readonly commit: string;
  readonly base: string;
  readonly pr_number: string;
  readonly pr_id: string;
}

export interface JourneyFinding {
  readonly key: string;
  readonly severity: string;
  readonly defect: string;
  readonly fix_kind: string;
}

export interface JourneyCorrection {
  readonly candidate?: string;
  readonly effect?: string;
  readonly completion?: string;
  readonly reason?: string;
}

export interface JourneyJudgment {
  readonly cycle_id?: string;
  readonly cycle_state?: string;
  readonly kind: string;
  readonly selected: string;
  readonly wait: boolean;
  readonly model?: string;
  readonly confidence?: number;
  readonly probability?: number;
  readonly wait_reason?: string;
}

export interface JourneyGate {
  readonly completion_id: string;
  readonly cycle_id: string;
  readonly state: string;
  readonly repository: string;
  readonly generation: number;
  readonly retryable: boolean;
  readonly decision?: string;
  readonly proposal?: string;
  readonly wait_reason?: string;
  readonly proposal_digest?: string;
  readonly inventory_digest?: string;
  readonly receipt_digests: readonly string[];
}

export interface JourneyAdoption {
  readonly intent: string;
  readonly revision: string;
  readonly state: string;
  readonly task: string;
  readonly issue_id: string;
  readonly acceptance: string;
}

export interface JourneyDeliveredTask {
  readonly task: string;
  readonly issue: string;
  readonly acceptance: string;
  readonly completion: string;
  readonly publication: string;
  readonly generation: number;
}

export interface JourneyVerifiedApp {
  readonly intent: string;
  readonly revision: string;
  readonly source_commit: string;
  readonly manifest: string;
}

export interface WorkJourney {
  readonly repository: string;
  readonly issue: string;
  readonly observed_unix: number;
  readonly phases: readonly JourneyPhase[];
  readonly tasks: readonly JourneyTask[];
  readonly commits: readonly JourneyCommit[];
  readonly findings: readonly JourneyFinding[];
  readonly corrections: readonly JourneyCorrection[];
  readonly judgments: readonly JourneyJudgment[];
  readonly gates: readonly JourneyGate[];
  readonly adoptions: readonly JourneyAdoption[];
  readonly delivered_tasks: readonly JourneyDeliveredTask[];
  readonly verifications: readonly JourneyVerifiedApp[];
}

function journeyPhase(value: unknown): JourneyPhase {
  const raw = record(value);
  return {phase: text(raw.phase), state: text(raw.state)};
}

function journeyTask(value: unknown): JourneyTask {
  const raw = record(value);
  const issue = nonEmpty(raw.issue);
  return {
    key: text(raw.key),
    lane: text(raw.lane),
    state: text(raw.state),
    ...(issue === undefined ? {} : {issue}),
  };
}

function journeyCommit(value: unknown): JourneyCommit {
  const raw = record(value);
  return {commit: text(raw.commit), base: text(raw.base), pr_number: text(raw.pr_number), pr_id: text(raw.pr_id)};
}

function journeyFinding(value: unknown): JourneyFinding {
  const raw = record(value);
  return {key: text(raw.key), severity: text(raw.severity), defect: text(raw.defect), fix_kind: text(raw.fix_kind)};
}

function journeyCorrection(value: unknown): JourneyCorrection {
  const raw = record(value);
  const candidate = nonEmpty(raw.candidate);
  const effect = nonEmpty(raw.effect);
  const completion = nonEmpty(raw.completion);
  const reason = nonEmpty(raw.reason);
  return {
    ...(candidate === undefined ? {} : {candidate}),
    ...(effect === undefined ? {} : {effect}),
    ...(completion === undefined ? {} : {completion}),
    ...(reason === undefined ? {} : {reason}),
  };
}

function journeyJudgment(value: unknown): JourneyJudgment {
  const raw = record(value);
  const cycleID = nonEmpty(raw.cycle_id);
  const cycleState = nonEmpty(raw.cycle_state);
  const model = nonEmpty(raw.model);
  const confidence = optionalNum(raw.confidence);
  const probability = optionalNum(raw.probability);
  const waitReason = nonEmpty(raw.wait_reason);
  return {
    kind: text(raw.kind),
    selected: text(raw.selected),
    wait: flag(raw.wait),
    ...(cycleID === undefined ? {} : {cycle_id: cycleID}),
    ...(cycleState === undefined ? {} : {cycle_state: cycleState}),
    ...(model === undefined ? {} : {model}),
    ...(confidence === undefined ? {} : {confidence}),
    ...(probability === undefined ? {} : {probability}),
    ...(waitReason === undefined ? {} : {wait_reason: waitReason}),
  };
}

function journeyGate(value: unknown): JourneyGate {
  const raw = record(value);
  const decision = nonEmpty(raw.decision);
  const proposal = nonEmpty(raw.proposal);
  const waitReason = nonEmpty(raw.wait_reason);
  const proposalDigest = nonEmpty(raw.proposal_digest);
  const inventoryDigest = nonEmpty(raw.inventory_digest);
  return {
    completion_id: text(raw.completion_id),
    cycle_id: text(raw.cycle_id),
    state: text(raw.state),
    repository: text(raw.repository),
    generation: num(raw.generation),
    retryable: flag(raw.retryable),
    ...(decision === undefined ? {} : {decision}),
    ...(proposal === undefined ? {} : {proposal}),
    ...(waitReason === undefined ? {} : {wait_reason: waitReason}),
    ...(proposalDigest === undefined ? {} : {proposal_digest: proposalDigest}),
    ...(inventoryDigest === undefined ? {} : {inventory_digest: inventoryDigest}),
    receipt_digests: texts(raw.receipt_digests),
  };
}

function journeyAdoption(value: unknown): JourneyAdoption {
  const raw = record(value);
  return {
    intent: text(raw.intent),
    revision: text(raw.revision),
    state: text(raw.state),
    task: text(raw.task),
    issue_id: text(raw.issue_id),
    acceptance: text(raw.acceptance),
  };
}

function journeyDeliveredTask(value: unknown): JourneyDeliveredTask {
  const raw = record(value);
  return {
    task: text(raw.task),
    issue: text(raw.issue),
    acceptance: text(raw.acceptance),
    completion: text(raw.completion),
    publication: text(raw.publication),
    generation: num(raw.generation),
  };
}

function journeyVerifiedApp(value: unknown): JourneyVerifiedApp {
  const raw = record(value);
  return {
    intent: text(raw.intent),
    revision: text(raw.revision),
    source_commit: text(raw.source_commit),
    manifest: text(raw.manifest),
  };
}

// workJourney reads one issue's recorded work journey. Absent sections
// (including newer ones on older reads) read as empty.
export function workJourney(value: unknown, repository: string, issue: string): WorkJourney {
  const data = object(value);
  return {
    repository,
    issue,
    observed_unix: num(data.observed_unix),
    phases: list(data.phases).map(journeyPhase),
    tasks: list(data.tasks).map(journeyTask),
    commits: list(data.commits).map(journeyCommit),
    findings: list(data.findings).map(journeyFinding),
    corrections: list(data.corrections).map(journeyCorrection),
    judgments: list(data.judgments).map(journeyJudgment),
    gates: list(data.gates).map(journeyGate),
    adoptions: list(data.adoptions).map(journeyAdoption),
    delivered_tasks: list(data.delivered_tasks).map(journeyDeliveredTask),
    verifications: list(data.verifications).map(journeyVerifiedApp),
  };
}
