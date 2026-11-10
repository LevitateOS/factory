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
  | 'unrecorded';

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
  readonly blockers: readonly {code: string; resolution: string; endpoint?: string}[];
  readonly candidate: string;
  readonly issueLink: string | undefined;
  readonly pullLink: string | undefined;
}

function blockerEndpoint(blocker: {endpoint_repository?: string; endpoint_issue?: string}): string | undefined {
  if (blocker.endpoint_repository === undefined || blocker.endpoint_issue === undefined) return undefined;
  return `repository ${blocker.endpoint_repository} issue #${blocker.endpoint_issue}`;
}

function describeBlocker(blocker: {
  code: string;
  resolution: string;
  endpoint_repository?: string;
  endpoint_issue?: string;
}): {code: string; resolution: string; endpoint?: string} {
  const endpoint = blockerEndpoint(blocker);
  return {code: blocker.code, resolution: blocker.resolution, ...(endpoint === undefined ? {} : {endpoint})};
}

function mergeDelivery(detail: FactoryIssueDetail): DeliveryState | undefined {
  if (detail.merge?.stage !== 'merged') return undefined;
  return detail.closure.state === 'confirmed' ? 'delivered' : 'merge_confirmed_closure_pending';
}

function checksDelivery(detail: FactoryIssueDetail): DeliveryState | undefined {
  if (detail.checks?.verdict === 'failed') return 'checks_failed';
  if (detail.checks?.verdict === 'pending') return 'checks_pending';
  if (detail.checks?.verdict === 'refused') return 'evidence_refused';
  return undefined;
}

function readinessDelivery(detail: FactoryIssueDetail): DeliveryState {
  switch (detail.readiness?.readiness) {
    case 'cancelled':
      return 'cancelled';
    case 'needs_intervention':
      return 'needs_attention';
    case 'blocked':
      return 'waiting';
    case 'active':
      return 'working';
    case 'queued':
      return 'queued';
    case 'not_authorized':
      return 'not_authorized';
    case 'completed':
      return 'recorded_complete_without_merge';
    default:
      return 'unrecorded';
  }
}

function deliveryState(detail: FactoryIssueDetail): DeliveryState {
  return mergeDelivery(detail) ?? checksDelivery(detail) ?? readinessDelivery(detail);
}

const deliveryCopy: Record<DeliveryState, string> = {
  delivered: 'Delivered: merge confirmed and original-issue closure confirmed.',
  merge_confirmed_closure_pending: 'Merge confirmed, issue closure pending.',
  checks_failed: 'Checks failed on the exact reviewed candidate.',
  checks_pending: 'Awaiting required checks on the exact reviewed candidate.',
  evidence_refused: 'Check evidence refused: the candidate moved or evidence is incomplete.',
  cancelled: 'Cancelled.',
  needs_attention: 'Needs attention.',
  waiting: 'Waiting on prerequisites.',
  working: 'Working.',
  queued: 'Queued.',
  not_authorized: 'Not authorized.',
  recorded_complete_without_merge: 'Recorded complete without merge evidence.',
  unrecorded: 'No recorded work yet.',
};

function acceptanceCopy(detail: FactoryIssueDetail): {text: string; invalid: boolean} {
  if (detail.status.withdrawn) return {text: 'Acceptance withdrawn.', invalid: true};
  if (!detail.status.valid)
    return {
      text: `Acceptance invalid${detail.status.reasons.length ? `: ${detail.status.reasons.slice(0, 3).join('; ')}` : '.'}`,
      invalid: true,
    };
  return {text: 'Acceptance valid.', invalid: false};
}

function readinessCopy(detail: FactoryIssueDetail): string {
  if (!detail.readiness) return 'No readiness record.';
  return `Readiness ${detail.readiness.readiness}: ${detail.readiness.reason}.`;
}

function checksCopy(detail: FactoryIssueDetail): string {
  if (!detail.checks) return 'No check assessment recorded.';
  const stale = detail.checks.stale ? ' Stale: the head was superseded by a newer candidate.' : '';
  return `Checks ${detail.checks.verdict} on ${detail.checks.head_oid.slice(0, 12)} over base ${detail.checks.base_oid.slice(0, 12)}: ${detail.checks.resolution}${stale}`;
}

function mergeCopy(detail: FactoryIssueDetail): string {
  if (!detail.merge) return 'No merge recorded.';
  const commit = detail.merge.merged_commit ? ` at ${detail.merge.merged_commit.slice(0, 12)}` : '';
  const stale = detail.merge.stale ? ' Stale: the head was superseded by a newer candidate.' : '';
  return `Merge ${detail.merge.stage}${commit}: ${detail.merge.resolution}${stale}`;
}

function attemptCopy(detail: FactoryIssueDetail): string {
  if (!detail.attempt) return 'No attempt recorded.';
  const outcome = detail.attempt.outcome ? `, ${detail.attempt.outcome}` : '';
	if (detail.publication?.origin === 'external' && detail.attempt.stage === 'retained' && detail.attempt.run === '') return 'External PR adoption retained; no initial Builder run.';
  return `Attempt ${detail.attempt.attempts}: ${detail.attempt.role} ${detail.attempt.stage}${outcome}.`;
}

function publicationCopy(detail: FactoryIssueDetail): string {
  if (!detail.publication) return 'Nothing published yet.';
  const pr = detail.publication.pr_number ? `PR #${detail.publication.pr_number}` : 'no pull request yet';
  return (
	`${detail.publication.origin === 'external' ? 'External PR' : 'Publication'} ${detail.publication.stage}: candidate ${detail.publication.candidate.slice(0, 12)} on ` +
    `${detail.publication.target_branch}, ${pr}. ${detail.publication.corrections_total} ` +
    `correction${detail.publication.corrections_total === 1 ? '' : 's'}, ${detail.publication.reviews_total} ` +
    `review${detail.publication.reviews_total === 1 ? '' : 's'}.`
  );
}

function reviewCopy(detail: FactoryIssueDetail): string {
  const publication = detail.publication;
  if (!publication || publication.reviews_total === 0) return 'No independent review recorded.';
  const latest = publication.reviews[publication.reviews.length - 1];
  if (!latest) return `${publication.reviews_total} recorded reviews; latest entry unavailable.`;
  const effect = latest.effect ? `, effect ${latest.effect}` : '';
  if (latest.event === 'COMMENT' && latest.agent_verdict) {
    const verdict = latest.agent_verdict === 'APPROVED' ? 'clear' :
      latest.agent_verdict === 'REQUEST_CHANGES' ? 'changes requested' : 'unavailable';
    return `Latest independent agent inspection: ${verdict} on ${latest.head_oid.slice(0, 12)}, published as a comment${effect}.`;
  }
  return `Latest independent review: ${latest.event} on ${latest.head_oid.slice(0, 12)}${effect}.`;
}

function closureCopy(detail: FactoryIssueDetail): string {
  if (detail.closure.state === 'confirmed') {
    const stamp = activityTimestamp(detail.closure.closed_unix ?? 0);
    return `Original-issue closure confirmed${stamp ? ` at ${stamp.text}` : ''}.`;
  }
  if (detail.closure.state === 'pending') return 'Original-issue closure pending.';
  return 'Original issue not closed.';
}

function requestedCopy(detail: FactoryIssueDetail): string {
  if (!detail.requested_retry) return '';
  return (
    `Retry requested for run ${detail.requested_retry.prior_run.slice(0, 8)}: ${detail.requested_retry.reason}. ` +
    'This request is not a confirmed new attempt.'
  );
}

function observedCopy(detail: FactoryIssueDetail): string {
  const stamp = activityTimestamp(detail.observed_unix);
  return stamp ? `Observed ${stamp.text}.` : 'Observation time unknown.';
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
function detailCandidate(detail: FactoryIssueDetail): string {
  if (!detail.checks) return 'No reviewed candidate recorded.';
  return `PR #${detail.checks.pr_number} head ${detail.checks.head_oid.slice(0, 12)} over base ${detail.checks.base_oid.slice(0, 12)}`;
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
    blockers: (detail.readiness?.blockers ?? []).map(describeBlocker),
    candidate: detailCandidate(detail),
    issueLink: destinations.issueLink,
    pullLink: destinations.pullLink,
  };
}

export function renderWorkDetail(model: WorkDetailModel): TemplateResult {
  return html`
    <section aria-label="Work detail">
      <p role="status">${model.deliveryText}</p>
      <p>${model.acceptanceText}</p>
      <p>${model.readinessText}</p>
      <ul>
        ${model.blockers.map(
          (blocker) =>
            html`<li>${blocker.code}: ${blocker.resolution}${blocker.endpoint ? ` (${blocker.endpoint})` : ''}</li>`
        )}
      </ul>
      <p>${model.attemptText}</p>
      <p>${model.publicationText}</p>
      <p>${model.reviewText}</p>
      <p>${model.checksText}</p>
      ${model.failedChecks.length > 0 ? html`<p>Failed checks: ${model.failedChecks.join(', ')}.</p>` : ''}
      <p>${model.mergeText}</p>
      <p>${model.closureText}</p>
      ${model.requestedText ? html`<p>${model.requestedText}</p>` : ''}
      <p>${model.observedText}</p>
      <p>Reviewed candidate: ${model.candidate}</p>
      <p>
        ${model.issueLink ? html`<a href=${model.issueLink}>Native issue</a>` : 'Native issue link unavailable.'}
        ${model.pullLink ? html`<a href=${model.pullLink}>Native pull request</a>` : ''}
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
  readonly scope: string;
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
  readonly target: string;
}

const interventionCopy: Record<
  InterventionKind,
  {label: string; scope: string; impact: string; audience: InterventionAudience}
> = {
  pause: {
    label: 'Pause Factory work',
    scope: 'This repository',
    impact: 'Closes dispatch and stops outstanding runs. Retained work and terminals stay.',
    audience: 'owner',
  },
  resume: {
    label: 'Resume Factory work',
    scope: 'This repository',
    impact: 'Reopens dispatch after every recorded run settles.',
    audience: 'owner',
  },
  stop: {
    label: 'Stop run',
    scope: 'This run only',
    impact: 'Retires the run through the operator stop path. Other runs continue.',
    audience: 'collaborator',
  },
  retry: {
    label: 'Retry run',
    scope: 'This run only',
    impact: 'Queues one explicit retry after the prior run settles. No new attempt otherwise.',
    audience: 'collaborator',
  },
  takeover: {
    label: 'Take over run',
    scope: 'This run into your checkout',
    impact: 'Copies retained work into your own project checkout. Requires project membership.',
    audience: 'collaborator',
  },
  project_stop: {
    label: 'Stop Project for everyone',
    scope: 'The whole project',
    impact: 'Stops shared project services for everyone. Needs explicit confirmation.',
    audience: 'owner',
  },
};

// describeIntervention pairs one authorized control with its exact scope,
// impact, audience and pending-versus-confirmed result. Owner controls
// act on the repository or project; collaborator controls act on one
// run. Requested actions never read as confirmed effects. The owning
// lifecycle APIs revalidate authority when the control acts.
export function describeIntervention(input: InterventionInput): InterventionModel {
  const copy = interventionCopy[input.kind];
  return {
    kind: input.kind,
    label: copy.label,
    scope: `${copy.scope}: ${input.target}`,
    impact: copy.impact,
    audience: copy.audience,
    enabled: input.enabled,
    result: input.result,
    resultText: input.resultText,
  };
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
  if (!receipt.confirmed) return {result: 'pending', text: 'Stop not confirmed. Refresh the run before acting again.'};
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
    <p>${model.scope}. ${model.impact}</p>
    <p role="status">${model.resultText}</p>
  </li>`;
  // Owner and collaborator controls render in separate groups: owner
  // controls act on the repository or project, collaborator controls on
  // one run. Grouping never changes availability; the owning lifecycle
  // APIs revalidate authority when a control acts.
  const group = (audience: InterventionAudience, label: string, note: string) => {
    const rows = models.filter((model) => model.audience === audience);
    return rows.length
      ? html`<section aria-label=${label}>
          <h3>${label}</h3>
          <p>${note}</p>
          <ul>
            ${rows.map(item)}
          </ul>
        </section>`
      : '';
  };
  return html`
    <section aria-label="Factory interventions">
      ${group('owner', 'Owner controls', 'These controls act on the repository or project.')}
      ${group('collaborator', 'Collaborator controls', 'These controls act on one run only.')}
    </section>
  `;
}
