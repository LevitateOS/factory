import {
  check,
  flag,
  id,
  list,
  num,
  object,
  optionalNum,
  optionalText,
  record,
  text,
  texts,
} from './models.js';

export interface FactoryAuthority {
  effective: boolean;
  dispatch_open: boolean;
  missing: string[];
}
export interface FactoryControl {
  dispatch_open: boolean;
  paused: boolean;
  unsettled_runs: number;
  withdrawal_cause?: string;
}
export interface FactoryRun {
  id: string;
  role: string;
  issue?: string;
  attempt?: string;
  outcome?: 'succeeded' | 'failed' | 'cancelled' | 'needs-human';
  reconciled: boolean;
}
// factoryAuthorityText renders the admitted authority verdict as a bounded
// status suffix. Reason codes are stable backend identifiers, never secrets.
export function factoryAuthorityText(authority: FactoryAuthority | undefined) {
  if (!authority) return '';
  if (authority.effective) return ' · Factory ready';
  return ' · Factory needs: ' + authority.missing.slice(0, 3).join(', ');
}
// factoryControlText renders the admitted intervention state as a bounded
// status suffix: sticky pause, closed dispatch and unsettled runs.
export function factoryControlText(control: FactoryControl | undefined) {
  if (!control) return '';
  const parts: string[] = [];
  if (control.paused) parts.push('paused');
  if (!control.dispatch_open)
    parts.push('dispatch closed' + (control.withdrawal_cause ? ': ' + control.withdrawal_cause : ''));
  if (control.unsettled_runs > 0) parts.push(control.unsettled_runs + ' unsettled');
  if (parts.length === 0) return '';
  return ' · Factory ' + parts.join(', ');
}
// factoryCommandId generates a client command identity for one idempotent
// lifecycle control. The backend replays a reused identity instead of
// executing twice.
export function factoryCommandId() {
  return crypto.randomUUID().replace(/-/g, '');
}
export function spaceFactoryAuthority(row: Record<string, unknown>): FactoryAuthority | undefined {
  if (row.factory_authority === undefined) return undefined;
  const detail = record(row.factory_authority);
  return {effective: flag(detail.effective), dispatch_open: flag(detail.dispatch_open), missing: texts(detail.missing)};
}
export function spaceFactoryControl(row: Record<string, unknown>): FactoryControl | undefined {
  if (row.factory_control === undefined) return undefined;
  const detail = record(row.factory_control);
  const cause = text(detail.withdrawal_cause);
  return {
    dispatch_open: flag(detail.dispatch_open),
    paused: flag(detail.paused),
    unsettled_runs: num(detail.unsettled_runs),
    ...(cause ? {withdrawal_cause: cause} : {}),
  };
}
// factoryOutcomeText words a recorded run outcome for people. A recorded
// "succeeded" only says the run finished, not that its work landed, so it
// reads "ended"; the run's own output carries the result.
const factoryOutcomeWords: Readonly<Record<NonNullable<FactoryRun['outcome']>, string>> = {
  succeeded: 'ended',
  failed: 'stopped with an error',
  cancelled: 'cancelled',
  'needs-human': 'needs your decision',
};
function factoryOutcomeText(run: FactoryRun) {
  return run.outcome ? (factoryOutcomeWords[run.outcome] ?? run.outcome) : 'active';
}
// factoryRunText renders one recorded run as a bounded status line: its role,
// issue or attempt binding and recorded outcome. Liveness stays on the run
// status route; this text never claims the process is running.
export function factoryRunText(run: FactoryRun) {
  const target = run.issue ? `issue #${run.issue}` : run.attempt ? run.attempt : `run ${run.id.slice(0, 8)}`;
  return `${run.role} · ${target} · ${factoryOutcomeText(run)}`;
}
const factoryRoleNames: Readonly<Record<string, string>> = {
  'soda-coder': 'Coding agent',
  'soda-reviewer': 'Review agent',
};
// factoryRunLabel is the short row label: a plain role name, the issue when
// there is one, and the outcome. Attempt and run identifiers stay in
// factoryRunText, which the row carries as its title.
export function factoryRunLabel(run: FactoryRun) {
  const role = factoryRoleNames[run.role] ?? run.role;
  return [role, ...(run.issue ? [`issue #${run.issue}`] : []), factoryOutcomeText(run)].join(' · ');
}
function spaceFactoryRun(value: unknown): FactoryRun {
  const run = record(value);
  const issue = optionalText(run.issue),
    attempt = optionalText(run.attempt),
    outcome = optionalText(run.outcome);
  return {
    id: text(run.id),
    role: text(run.role),
    reconciled: flag(run.reconciled),
    ...(outcome ? {outcome: outcome as NonNullable<FactoryRun['outcome']>} : {}),
    ...(issue ? {issue} : {}),
    ...(attempt ? {attempt} : {}),
  };
}
export function spaceFactoryRuns(row: Record<string, unknown>) {
  return list(row.factory_runs).map(spaceFactoryRun);
}
export interface FactoryRunView {
  repository: string;
  issue?: string;
  attempt?: string;
}
export interface FactoryRunState {
  phase: string;
  live: boolean;
  terminal: boolean;
  output_truncated: boolean;
  container?: string;
  unit?: string;
  invocation?: string;
  exit_code?: number;
  reason?: string;
  retirement?: string;
  output?: string;
}
export interface FactoryRunDetail {
  id: string;
  project_id: string;
  role: string;
  harness: string;
  model: string;
  reconciled: boolean;
  outcome?: FactoryRun['outcome'];
  summary?: string;
  view?: FactoryRunView;
  state?: FactoryRunState;
  context_selection?: FactoryContextSelection;
  verification_retry?: {available: boolean};
  delivery_repair?: {available: boolean; progress?: DeliveryRepairProgress};
}
export interface DeliveryRepairProgress {
  cause_id: string;
  cycle_id: string;
  failed_run: string;
  source_commit: string;
  state: 'collecting' | 'ready' | 'investigating' | 'assessing' | 'applying' | 'adopted' | 'attention' | 'stale';
  reason?: string;
  run_id?: string;
  failures: {check_ref: string; detail: string}[];
  failure_count: number;
  judgment?: {option: 'accept' | 'needs_attention'; accept_probability: number; confidence: number};
}
function deliveryRepairProgress(value: unknown, run: string): DeliveryRepairProgress {
  const data = record(value);
  const failures = list(data.failures).map((item) => {
    const failure = record(item);
    return {check_ref: text(failure.check_ref), detail: text(failure.detail)};
  });
  const reason = optionalText(data.reason),
    runId = optionalText(data.run_id);
  let judgment: DeliveryRepairProgress['judgment'];
  if (data.judgment !== undefined && data.judgment !== null) {
    const row = record(data.judgment);
    judgment = {
      option: text(row.option) as 'accept' | 'needs_attention',
      accept_probability: num(row.accept_probability),
      confidence: num(row.confidence),
    };
  }
  return {
    cause_id: text(data.cause_id),
    cycle_id: text(data.cycle_id),
    failed_run: run,
    source_commit: text(data.source_commit),
    state: text(data.state) as DeliveryRepairProgress['state'],
    failures,
    failure_count: num(data.failure_count, failures.length),
    ...(reason === undefined ? {} : {reason}),
    ...(runId === undefined ? {} : {run_id: runId}),
    ...(judgment ? {judgment} : {}),
  };
}
export interface FactoryContextSelection {
  fingerprint: string;
  scope_digest: string;
  required_digest?: string;
  availability: string;
  reason: string;
  action: 'hold' | 'serve' | 'stop';
  can_use_current: boolean;
}
function factoryContextSelection(value: unknown): FactoryContextSelection {
  const row = record(value);
  const required = optionalText(row.required_digest);
  return {
    fingerprint: text(row.fingerprint),
    scope_digest: text(row.scope_digest),
    ...(required === undefined ? {} : {required_digest: required}),
    availability: text(row.availability),
    reason: text(row.reason),
    action: text(row.action) as FactoryContextSelection['action'],
    can_use_current: flag(row.can_use_current),
  };
}
function factoryRunDetailView(value: unknown, repositoryId: string): FactoryRunView {
  const view = record(value);
  check(id(view.repository) && view.repository === repositoryId);
  const issue = optionalText(view.issue),
    attempt = optionalText(view.attempt);
  return {repository: view.repository, ...(issue ? {issue} : {}), ...(attempt ? {attempt} : {})};
}
function factoryRunDetailState(value: unknown): FactoryRunState {
  const state = record(value);
  const optional = {
    container: optionalText(state.container),
    unit: optionalText(state.unit),
    invocation: optionalText(state.invocation),
    exit_code: optionalNum(state.exit_code),
    reason: optionalText(state.reason),
    retirement: optionalText(state.retirement),
    output: optionalText(state.output),
  };
  return {
    phase: text(state.phase),
    live: flag(state.live),
    terminal: flag(state.terminal),
    output_truncated: flag(state.output_truncated),
    ...Object.fromEntries(Object.entries(optional).filter(([, entry]) => entry !== undefined)),
  };
}
const optionalSection = (value: unknown) => value !== undefined && value !== null;
// The caller selects the run; a returned view must belong to the exact canonical
// repository. Never replace a provider/native scope with the requested local ID.
export function factoryRunStatusResponse(value: unknown, runId: string, repositoryId: string): FactoryRunDetail {
  const data = object(value),
    run = record(data.run);
  const outcome = optionalText(run.outcome),
    summary = optionalText(run.summary);
  const repair = record(data.delivery_repair);
  return {
    id: runId,
    project_id: text(run.project_id),
    role: text(run.role),
    reconciled: flag(run.reconciled),
    harness: text(run.harness),
    model: text(run.model),
    ...(outcome ? {outcome: outcome as NonNullable<FactoryRun['outcome']>} : {}),
    ...(summary === undefined ? {} : {summary}),
    ...(optionalSection(data.view) ? {view: factoryRunDetailView(data.view, repositoryId)} : {}),
    ...(optionalSection(data.state) ? {state: factoryRunDetailState(data.state)} : {}),
    ...(optionalSection(data.context_selection)
      ? {context_selection: factoryContextSelection(data.context_selection)}
      : {}),
    ...(optionalSection(data.verification_retry)
      ? {verification_retry: {available: flag(record(data.verification_retry).available)}}
      : {}),
    ...(optionalSection(data.delivery_repair)
      ? {
          delivery_repair: {
            available: flag(repair.available),
            ...(optionalSection(repair.progress) ? {progress: deliveryRepairProgress(repair.progress, runId)} : {}),
          },
        }
      : {}),
  };
}
export interface FactoryStatus {
  run_id: string;
  phase: string;
  container: string;
  unit: string;
  invocation: string;
  reason: string;
  live: boolean;
  terminal: boolean;
  exit_code: number | null;
}
// factoryStatusFrame reads one status frame of the run the viewer is
// attached to. Empty container/unit/invocation means no process binding was
// ever recorded; the viewer never guesses one.
export function factoryStatusFrame(value: unknown, runId: string): FactoryStatus {
  const frame = record(value);
  return {
    run_id: runId,
    phase: text(frame.phase),
    live: flag(frame.live),
    terminal: flag(frame.terminal),
    container: text(frame.container),
    unit: text(frame.unit),
    invocation: text(frame.invocation),
    reason: text(frame.reason),
    exit_code: optionalNum(frame.exit_code) ?? null,
  };
}
/** Opaque Factory run identity; unrelated native terminal ownership stays host-side. */
export const factoryRunID = (value: unknown): value is string =>
  typeof value === 'string' && /^[0-9a-f]{32}$/.test(value);
