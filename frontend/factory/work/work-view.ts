import {html} from 'lit';
import type {TemplateResult} from 'lit';
import {activityTimestamp} from '../shared/format.js';
import type {FactoryWorkLinks} from '../app/navigation.js';
import {id} from '../api/models.js';
import type {
  FactoryIssueDetail,
  PauseResult,
  PendingRuns,
  ProjectStopResult,
  ResumeResult,
  RetryDecision,
  StopReceipt,
  TakeoverRecord,
  WorkCapabilities,
} from '../api/work-response.js';

// Pure work-detail and intervention presentation for V03/V04. Model
// builders turn one issue's recorded facts into display state; render
// helpers project those models to Lit templates. No transport, identity
// lookup or mutable state lives here.

export type DeliveryState =
  | 'delivered'
  | 'merge_confirmed_closure_pending'
  | 'checks_failed'
  | 'checks_pending'
  | 'evidence_refused'
  | 'cancelled'
  | 'needs_attention'
  | 'waiting'
  | 'working'
  | 'queued'
  | 'not_authorized'
  | 'recorded_complete_without_merge'
  | 'unrecorded'
  | 'merge_fenced'
  | 'merge_failed'
  | 'checks_superseded'
  | 'unrecognized';

export interface WorkDetailModel {
  readonly delivery: DeliveryState;
  readonly deliveryText: string;
  readonly acceptanceText: string;
  readonly acceptanceInvalid: boolean;
  readonly readinessText: string;
  readonly attemptText: string;
  readonly publicationText: string;
  readonly reviewText: string;
  readonly checksText: string;
  readonly mergeText: string;
  readonly closureText: string;
  readonly requestedText: string;
  readonly observedText: string;
  readonly failedChecks: readonly string[];
  readonly blockers: readonly string[];
  readonly issueLink: string | undefined;
  readonly pullLink: string | undefined;
}

function unreachable(value: never): never {
  throw new Error(`Unhandled delivery input: ${String(value)}`);
}

function mergeDelivery(detail: FactoryIssueDetail): DeliveryState | undefined {
  const stage = detail.merge?.stage;
  switch (stage) {
    case undefined:
    case 'open':
    case 'withdrawn':
      return undefined;
    case 'merged':
      return detail.closure.state === 'confirmed' ? 'delivered' : 'merge_confirmed_closure_pending';
    case 'fenced':
      return 'merge_fenced';
    case 'failed':
      return 'merge_failed';
    case 'unknown':
      return 'unrecognized';
    default:
      return unreachable(stage);
  }
}

function checksDelivery(detail: FactoryIssueDetail): DeliveryState | undefined {
  const verdict = detail.checks?.verdict;
  const stale = detail.checks?.stale === true;
  switch (verdict) {
    case undefined:
    case 'pass':
      return undefined;
    case 'failed':
      return stale ? 'checks_superseded' : 'checks_failed';
    case 'pending':
      return stale ? 'checks_superseded' : 'checks_pending';
    case 'refused':
      return 'evidence_refused';
    case 'unknown':
      return 'unrecognized';
    default:
      return unreachable(verdict);
  }
}

function readinessDelivery(detail: FactoryIssueDetail): DeliveryState {
  const readiness = detail.readiness?.readiness;
  switch (readiness) {
    case undefined:
      return 'unrecorded';
    case 'cancelled':
      return 'cancelled';
    case 'needs_intervention':
      return 'needs_attention';
    case 'blocked':
      return 'waiting';
    case 'active':
      return 'working';
    case 'queued':
      return detail.attempt?.stage === 'assigned' ? 'working' : 'queued';
    case 'not_authorized':
      return 'not_authorized';
    case 'completed':
      return 'recorded_complete_without_merge';
    case 'unknown':
      return 'unrecognized';
    default:
      return unreachable(readiness);
  }
}

function deliveryState(detail: FactoryIssueDetail): DeliveryState {
  return mergeDelivery(detail) ?? checksDelivery(detail) ?? readinessDelivery(detail);
}

const deliveryCopy: Record<DeliveryState, string> = {
  delivered: 'Done: merged and the issue is closed.',
  merge_confirmed_closure_pending: 'Merged. Closing the issue.',
  checks_failed: 'Checks failed.',
  checks_pending: 'Waiting for checks.',
  evidence_refused: 'Check results do not match the latest change.',
  cancelled: 'Cancelled.',
  needs_attention: 'Needs attention.',
  waiting: 'Waiting on other work.',
  working: 'Working.',
  queued: 'Queued.',
  not_authorized: 'Not allowed.',
  recorded_complete_without_merge: 'Marked done without a merge.',
  unrecorded: 'Not started yet.',
  merge_fenced: 'Merge needs attention: its outcome is unknown and a person must resolve it.',
  merge_failed: 'Merge failed.',
  checks_superseded: 'Checks are for an older version.',
  unrecognized: 'Status unknown.',
};

const plural = (count: number, word: string) => `${count} ${word}${count === 1 ? '' : 's'}`;

function acceptanceCopy(detail: FactoryIssueDetail): {text: string; invalid: boolean} {
  if (detail.status.withdrawn) return {text: 'The request was withdrawn.', invalid: true};
  if (!detail.status.valid)
    return {
      text: `The request is not ready${detail.status.reasons.length ? `: ${detail.status.reasons.slice(0, 3).join('; ')}` : '.'}`,
      invalid: true,
    };
  return {text: '', invalid: false};
}

function readinessCopy(detail: FactoryIssueDetail): string {
  return detail.readiness?.reason ? `${detail.readiness.reason}.` : '';
}

function checksCopy(detail: FactoryIssueDetail): string {
  if (!detail.checks) return '';
  return `Checks ${detail.checks.verdict}${detail.checks.resolution ? `: ${detail.checks.resolution}` : '.'}${detail.checks.stale ? ' A newer version has replaced this one.' : ''}`;
}

function mergeCopy(detail: FactoryIssueDetail): string {
  if (!detail.merge) return '';
  return `Merge ${detail.merge.stage}${detail.merge.resolution ? `: ${detail.merge.resolution}` : '.'}${detail.merge.stale ? ' A newer version has replaced this one.' : ''}`;
}

function attemptCopy(detail: FactoryIssueDetail): string {
  if (!detail.attempt) return '';
	if (detail.publication?.origin === 'external' && detail.attempt.stage === 'retained' && detail.attempt.run === '') return 'Using an existing pull request.';
  const outcome = detail.attempt.outcome ? `, ${detail.attempt.outcome}` : '';
  return `Try ${detail.attempt.attempts}: ${detail.attempt.role} ${detail.attempt.stage}${outcome}.`;
}

function publicationCopy(detail: FactoryIssueDetail): string {
  const publication = detail.publication;
  if (!publication) return '';
  const pr = publication.pr_number ? `Pull request #${publication.pr_number}` : 'Pull request not opened yet';
  return `${pr} into ${publication.target_branch}: ${plural(publication.corrections_total, 'correction')}, ${plural(publication.reviews_total, 'review')}.`;
}

function reviewCopy(detail: FactoryIssueDetail): string {
  const latest = detail.publication?.reviews.at(-1);
  if (!latest) return '';
  if (latest.event === 'COMMENT' && latest.agent_verdict) {
    const verdict = latest.agent_verdict === 'APPROVED' ? 'looks good' :
      latest.agent_verdict === 'REQUEST_CHANGES' ? 'asked for changes' : 'gave no verdict';
    return `Latest agent review ${verdict}.`;
  }
  const event = latest.event === 'APPROVED' ? 'approved' : latest.event === 'CHANGES_REQUESTED' ? 'asked for changes' : 'commented';
  return `Latest review ${event}.`;
}

function closureCopy(detail: FactoryIssueDetail): string {
  if (detail.closure.state === 'confirmed') {
    const stamp = activityTimestamp(detail.closure.closed_unix ?? 0);
    return `Issue closed${stamp ? ` ${stamp.text}` : ''}.`;
  }
  return detail.closure.state === 'pending' ? 'Closing the issue.' : '';
}

function requestedCopy(detail: FactoryIssueDetail): string {
  return detail.requested_retry ? `Another try was requested: ${detail.requested_retry.reason}. It has not started yet.` : '';
}

function observedCopy(detail: FactoryIssueDetail): string {
  const stamp = activityTimestamp(detail.observed_unix);
  return stamp ? `Updated ${stamp.text}.` : '';
}

/** The host admits destinations; response URL hints never override its navigation. */
export function workIssueLink(links: FactoryWorkLinks | undefined, number: string): string | undefined {
  return id(number) ? links?.issue(number) : undefined;
}
function workPullLink(links: FactoryWorkLinks | undefined, number: string): string | undefined {
  return id(number) ? links?.pull(number) : undefined;
}

function detailLinks(
  detail: FactoryIssueDetail,
  links: FactoryWorkLinks | undefined
): {issueLink: string | undefined; pullLink: string | undefined} {
  const prNumber = detail.checks?.pr_number ?? detail.merge?.pr_number ?? detail.publication?.pr_number;
  return {
    issueLink: workIssueLink(links, detail.issue),
    pullLink: prNumber === undefined ? undefined : workPullLink(links, prNumber),
  };
}

function failedCheckContexts(detail: FactoryIssueDetail): readonly string[] {
  return (detail.checks?.results ?? []).filter((result) => !result.passed).map((result) => result.context);
}
// describeIssueDetail projects one issue's recorded facts to display
// state. Delivery needs both merge and closure evidence; every other
// state names the recorded verdict behind it.
export function describeIssueDetail(detail: FactoryIssueDetail, links: FactoryWorkLinks | undefined): WorkDetailModel {
  const acceptance = acceptanceCopy(detail);
  const delivery = deliveryState(detail);
  const destinations = detailLinks(detail, links);
  return {
    delivery,
    deliveryText: deliveryCopy[delivery],
    acceptanceText: acceptance.text,
    acceptanceInvalid: acceptance.invalid,
    readinessText: readinessCopy(detail),
    attemptText: attemptCopy(detail),
    publicationText: publicationCopy(detail),
    reviewText: reviewCopy(detail),
    checksText: checksCopy(detail),
    mergeText: mergeCopy(detail),
    closureText: closureCopy(detail),
    requestedText: requestedCopy(detail),
    observedText: observedCopy(detail),
    failedChecks: failedCheckContexts(detail),
    blockers: (detail.readiness?.blockers ?? []).map((blocker) => blocker.resolution),
    issueLink: destinations.issueLink,
    pullLink: destinations.pullLink,
  };
}

export function renderWorkDetail(model: WorkDetailModel): TemplateResult {
  return html`
    <section aria-label="Work detail">
      <p role="status">${model.deliveryText}</p>
      ${model.acceptanceText ? html`<p>${model.acceptanceText}</p>` : ''}
      ${model.readinessText ? html`<p>${model.readinessText}</p>` : ''}
      ${model.blockers.length ? html`<ul>
        ${model.blockers.map((blocker) => html`<li>${blocker}</li>`)}
      </ul>` : ''}
      ${model.attemptText ? html`<p>${model.attemptText}</p>` : ''}
      ${model.publicationText ? html`<p>${model.publicationText}</p>` : ''}
      ${model.reviewText ? html`<p>${model.reviewText}</p>` : ''}
      ${model.checksText ? html`<p>${model.checksText}</p>` : ''}
      ${model.failedChecks.length > 0 ? html`<p>Failed checks: ${model.failedChecks.join(', ')}.</p>` : ''}
      ${model.mergeText ? html`<p>${model.mergeText}</p>` : ''}
      ${model.closureText ? html`<p>${model.closureText}</p>` : ''}
      ${model.requestedText ? html`<p>${model.requestedText}</p>` : ''}
      ${model.observedText ? html`<p>${model.observedText}</p>` : ''}
      <p>
        ${model.issueLink ? html`<a href=${model.issueLink}>Issue</a>` : ''}
        ${model.pullLink ? html`<a href=${model.pullLink}>Pull request</a>` : ''}
      </p>
    </section>
  `;
}

const capabilityReasonCopy: Record<string, string> = {
  repository_write_required: 'Needs repository write authority.',
  repository_not_visible: 'Repository is not visible to you.',
  native_authority_unavailable: 'Native authority is unavailable.',
  session_changed: 'Session changed; reconnect before acting.',
  no_run: 'No run recorded for this work.',
  run_unsettled: 'The run is not settled.',
  run_settled: 'The run is already settled.',
  retry_queued: 'A retry is already queued.',
  membership_required: 'Project membership is required.',
  dispatch_open: 'Dispatch is already open.',
  dispatch_closed: 'Dispatch is already closed.',
};

// capabilityReasonText renders one recorded capability refusal reason.
export function capabilityReasonText(reason: string | undefined): string {
  return capabilityReasonCopy[reason ?? ''] ?? 'Action unavailable.';
}
// capabilityGate reads one intervention's backend capability: whether
// the viewer may act now and, when not, the recorded reason why. The
// owning lifecycle APIs revalidate at act time.
export function capabilityGate(
  capabilities: WorkCapabilities,
  kind: InterventionKind
): {available: boolean; reasonText: string} {
  if (kind === 'project_stop') return {available: true, reasonText: ''};
  const entry = capabilities[kind];
  if (entry.available) return {available: true, reasonText: ''};
  return {available: false, reasonText: capabilityReasonText(entry.reason)};
}

export type InterventionKind = 'pause' | 'resume' | 'stop' | 'retry' | 'takeover' | 'project_stop';
export type InterventionResult = 'idle' | 'pending' | 'confirmed' | 'uncertain' | 'failed';

// upfrontCapabilities maps one issue's backend capabilities to the
// detail element's upfront intervention booleans. Unknown
// capabilities stay permissive: the backend gate and server
// authorization decide once the issue read lands, while upfront
// false would stick disabled. project_stop follows environment
// presence because its POST requires a target.
export function upfrontCapabilities(
  capabilities: WorkCapabilities | undefined,
  hasEnvironment: boolean
): Record<InterventionKind, boolean> {
  if (!capabilities)
    return {pause: true, resume: true, stop: true, retry: true, takeover: true, project_stop: hasEnvironment};
  return {
    pause: capabilities.pause.available,
    resume: capabilities.resume.available,
    stop: capabilities.stop.available,
    retry: capabilities.retry.available,
    takeover: capabilities.takeover.available,
    project_stop: hasEnvironment,
  };
}

export type InterventionAudience = 'owner' | 'collaborator';

export interface InterventionModel {
  readonly kind: InterventionKind;
  readonly label: string;
  readonly impact: string;
  readonly audience: InterventionAudience;
  readonly enabled: boolean;
  readonly result: InterventionResult;
  readonly resultText: string;
}

export interface InterventionInput {
  readonly kind: InterventionKind;
  readonly enabled: boolean;
  readonly result: InterventionResult;
  readonly resultText: string;
}

const interventionCopy: Record<InterventionKind, {label: string; impact: string; audience: InterventionAudience}> = {
  pause: {label: 'Pause work', impact: 'Agents stop starting new work here. Work done so far is kept.', audience: 'owner'},
  resume: {label: 'Resume work', impact: 'Agents pick up work here again.', audience: 'owner'},
  stop: {label: 'Stop this run', impact: 'Other runs keep going.', audience: 'collaborator'},
  retry: {label: 'Try this run again', impact: 'Starts once more after the current run ends.', audience: 'collaborator'},
  takeover: {label: 'Take over', impact: 'Copies the work so far into your own copy of the project.', audience: 'collaborator'},
  project_stop: {label: 'Stop project for everyone', impact: 'Stops shared project services for everyone.', audience: 'owner'},
};

// describeIntervention pairs one control with its impact, audience and
// requested-versus-confirmed result. Requested actions never read as
// confirmed effects; the owning lifecycle APIs revalidate authority.
export function describeIntervention(input: InterventionInput): InterventionModel {
  return {kind: input.kind, ...interventionCopy[input.kind], enabled: input.enabled, result: input.result, resultText: input.resultText};
}

export type InterventionOutcome =
  | {kind: 'pause'; receipt: PauseResult}
  | {kind: 'resume'; receipt: ResumeResult}
  | {kind: 'stop'; receipt: StopReceipt}
  | {kind: 'retry'; receipt: RetryDecision}
  | {kind: 'takeover'; receipt: TakeoverRecord}
  | {kind: 'project_stop'; receipt: ProjectStopResult}
  | {kind: 'pending_runs'; pending: PendingRuns}
  | {kind: 'refused'; status: number; message: string};

function settledRunCount(runs: readonly {confirmed: boolean; uncertain: boolean}[]): {
  settled: number;
  uncertain: number;
} {
  return {
    settled: runs.filter((run) => run.confirmed && !run.uncertain).length,
    uncertain: runs.filter((run) => run.uncertain).length,
  };
}
function pauseOutcomeText(receipt: PauseResult): {result: InterventionResult; text: string} {
  const {settled, uncertain} = settledRunCount(receipt.runs);
  const fence = uncertain > 0 ? ` ${uncertain} run${uncertain === 1 ? ' stays' : 's stay'} fenced, not cancelled.` : '';
  return {
    result: 'confirmed',
    text: `Pause confirmed. Dispatch closed; ${settled} run${settled === 1 ? '' : 's'} stopped.${fence}`,
  };
}
function stopOutcomeText(receipt: StopReceipt): {result: InterventionResult; text: string} {
  if (receipt.uncertain)
    return {
      result: 'uncertain',
      text: 'Stop uncertain: the run effect is not confirmed. Inspect the run before retrying.',
    };
  if (!receipt.confirmed) return {result: 'pending', text: 'Stop not confirmed.'};
  return {result: 'confirmed', text: `Stop confirmed${receipt.outcome ? `: ${receipt.outcome}` : ''}.`};
}
function projectStopOutcomeText(receipt: ProjectStopResult): {result: InterventionResult; text: string} {
  const {settled, uncertain} = settledRunCount(receipt.runs);
  const hold = receipt.hold_synced
    ? 'Maintenance hold synced.'
    : 'Maintenance hold did not sync; the stop still proceeded.';
  const fence = uncertain > 0 ? ` ${uncertain} run${uncertain === 1 ? ' stays' : 's stay'} fenced.` : '';
  const running = receipt.running
    ? 'The project still reports running; ask the operator to inspect.'
    : 'The project is stopped.';
  return {
    result: 'confirmed',
    text: `Project stop confirmed. ${settled} run${settled === 1 ? '' : 's'} stopped.${fence} ${hold} ${running}`,
  };
}
type RunOutcome = Exclude<InterventionOutcome, {kind: 'pause' | 'resume' | 'stop' | 'retry' | 'takeover'}>;
function runOutcomeText(outcome: RunOutcome): {result: InterventionResult; text: string} {
  switch (outcome.kind) {
    case 'project_stop':
      return projectStopOutcomeText(outcome.receipt);
    case 'pending_runs':
      return {
        result: 'failed',
        text: `Refused: ${outcome.pending.runs.length} recorded run${outcome.pending.runs.length === 1 ? ' is' : 's are'} not settled. Stop or reconcile them first.`,
      };
    case 'refused':
      return {result: 'failed', text: `Refused: ${outcome.message}`};
  }
}
// interventionResultText turns one parsed intervention outcome into a
// pending-versus-confirmed result. Requested actions never read as
// confirmed effects; uncertain runs stay fenced, never cancelled.
export function interventionResultText(outcome: InterventionOutcome): {result: InterventionResult; text: string} {
  switch (outcome.kind) {
    case 'pause':
      return pauseOutcomeText(outcome.receipt);
    case 'resume':
      return outcome.receipt.reopened
        ? {result: 'confirmed', text: 'Resume confirmed. Dispatch reopened.'}
        : {result: 'confirmed', text: 'Resume recorded. Dispatch did not reopen; runs may still be unsettled.'};
    case 'stop':
      return stopOutcomeText(outcome.receipt);
    case 'retry':
      return {result: 'confirmed', text: 'Retry queued. The prior run stays settled; no other attempt was created.'};
    case 'takeover':
      return {result: 'confirmed', text: `Takeover confirmed into ${outcome.receipt.destination}.`};
    default:
      return runOutcomeText(outcome);
  }
}

export interface InterventionCommands {
  readonly act: (kind: InterventionKind) => void;
}

export function renderInterventions(
  models: readonly InterventionModel[],
  commands: InterventionCommands
): TemplateResult {
  const item = (model: InterventionModel) => html`<li>
    <button type="button" class="ui button" ?disabled=${!model.enabled} @click=${() => commands.act(model.kind)}>
      ${model.label}
    </button>
    <p>${model.impact}</p>
    ${model.resultText ? html`<p role="status">${model.resultText}</p>` : ''}
  </li>`;
  // Project-wide controls and controls for one run render apart. Grouping
  // never changes availability.
  const group = (audience: InterventionAudience, label: string) => {
    const rows = models.filter((model) => model.audience === audience);
    return rows.length
      ? html`<section aria-label=${label}>
          <h3>${label}</h3>
          <ul>
            ${rows.map(item)}
          </ul>
        </section>`
      : '';
  };
  return html`
    <section aria-label="Controls">
      ${group('owner', 'Whole project')}
      ${group('collaborator', 'This run')}
    </section>
  `;
}
