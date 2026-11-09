import {html} from 'lit';
import type {TemplateResult} from 'lit';
import {check, object} from '../api/models.js';
import {decisionID} from '../api/request-response.js';
import type {IntentRevision} from '../api/request-response.js';
import {activityTimestamp} from '../shared/format.js';
import {allowanceHoldText} from '../feed/allowance.js';
import type {FactoryIssueDetail, FactoryRepositorySummary, WorkCapabilities} from '../api/work-response.js';
import {describeIssueDetail, workIssueLink} from './work-view.js';
import type {FactoryWorkLinks} from '../app/navigation.js';
import type {DeliveryState} from './work-view.js';
import type {
  FactoryAccess,
  IssuePlan,
  PriorityComparison,
  RequestAttention,
  RequestOverview,
  RevisionEntry,
  RevisionGeneration,
  RevisionHistory,
  RevisionIdeaChange,
  RevisionTaskChange,
} from '../api/progress-response.js';

// Pure accepted-request overview, planning/why-next, direction-revision
// and optional-effect models for V01/V02/V05/V07. Each model composes
// only its own recorded reads; missing, waiting and unavailable states
// stay distinct and no selection, plan or effect is invented.

import type {IntentGeneration} from '../api/intent-generation-response.js';
export {intentGeneration} from '../api/intent-generation-response.js';
export type {IntentGeneration, IntentTaskBinding} from '../api/intent-generation-response.js';

export type SourceState = 'ready' | 'missing' | 'unavailable';
export interface WorkOverviewInput {
  readonly revision?: IntentRevision;
  readonly generation?: IntentGeneration;
  readonly summary?: FactoryRepositorySummary;
  readonly issues: readonly FactoryIssueDetail[];
  readonly unreadIssues: readonly string[];
  readonly links: FactoryWorkLinks | undefined;
  readonly intentState: SourceState;
  readonly generationState: SourceState;
  readonly summaryState: SourceState;
}
export interface OverviewCounts {
  readonly delivered: number;
  readonly working: number;
  readonly waiting: number;
  readonly attention: number;
  readonly other: number;
}
export interface AttentionItem {
  readonly issue: string;
  readonly text: string;
  readonly link: string | undefined;
}
export interface DependencyRow {
  readonly task: string;
  readonly objective: string;
  readonly dependsOn: readonly string[];
  readonly issue: string | undefined;
  readonly issueLink: string | undefined;
}
export interface WorkOverviewModel {
  readonly direction: string;
  readonly counts: OverviewCounts;
  readonly countsText: string;
  readonly attention: readonly AttentionItem[];
  readonly dependencies: readonly DependencyRow[];
  readonly setupText: string;
  readonly allowanceText: string;
  readonly outcomeNote: string;
  readonly availabilityNote: string;
  readonly dispatch?: FactoryRepositorySummary['dispatch'];
}

const deliveryBuckets: Record<DeliveryState, keyof OverviewCounts> = {
  delivered: 'delivered',
  working: 'working',
  queued: 'working',
  waiting: 'waiting',
  checks_pending: 'waiting',
  merge_confirmed_closure_pending: 'waiting',
  recorded_complete_without_merge: 'waiting',
  needs_attention: 'attention',
  checks_failed: 'attention',
  evidence_refused: 'attention',
  not_authorized: 'attention',
  cancelled: 'other',
  unrecorded: 'other',
};
function bucketDelivery(delivery: DeliveryState): keyof OverviewCounts {
  return deliveryBuckets[delivery];
}
function countIssues(input: WorkOverviewInput): {counts: OverviewCounts; attention: AttentionItem[]} {
  const counts = {delivered: 0, working: 0, waiting: 0, attention: 0, other: 0};
  const attention: AttentionItem[] = [];
  for (const issue of input.issues) {
    const model = describeIssueDetail(issue, input.links);
    counts[bucketDelivery(model.delivery)] += 1;
    if (bucketDelivery(model.delivery) === 'attention')
      attention.push({issue: issue.issue, text: `Issue #${issue.issue}: ${model.deliveryText}`, link: model.issueLink});
  }
  for (const issue of input.unreadIssues)
    attention.push({
      issue,
      text: `Issue #${issue} could not be read; it is not counted above.`,
      link: workIssueLink(input.links, issue),
    });
  return {counts, attention};
}
function countsText(counts: OverviewCounts): string {
  const parts: string[] = [];
  if (counts.delivered > 0) parts.push(`${counts.delivered} delivered`);
  if (counts.working > 0) parts.push(`${counts.working} working`);
  if (counts.waiting > 0) parts.push(`${counts.waiting} waiting`);
  if (counts.attention > 0) parts.push(`${counts.attention} needs your decision`);
  if (counts.other > 0) parts.push(`${counts.other} other`);
  return parts.length > 0 ? parts.join(' · ') : 'No counted work.';
}
function directionText(input: WorkOverviewInput): string {
  if (input.intentState === 'unavailable') return 'Accepted direction could not be read.';
  if (input.intentState === 'missing' || !input.revision) return 'No accepted request recorded.';
  return input.revision.objective;
}
function issueForTask(generation: IntentGeneration | undefined, task: string): string | undefined {
  return generation?.bindings.find((binding) => binding.task === task)?.issue;
}
function dependencyRows(input: WorkOverviewInput): DependencyRow[] {
  if (!input.revision) return [];
  return input.revision.tasks.map((task) => {
    const issue = issueForTask(input.generation, task.key);
    const dependsOn = (task.depends_on ?? []).map((key) => {
      const dep = issueForTask(input.generation, key);
      return dep ? `issue #${dep}` : `task ${key.slice(0, 9)}`;
    });
    return {
      task: task.key,
      objective: task.objective,
      dependsOn,
      issue,
      issueLink: issue === undefined ? undefined : workIssueLink(input.links, issue),
    };
  });
}
function setupText(input: WorkOverviewInput): string {
  if (input.summaryState === 'unavailable') return 'Project setup could not be read.';
  if (input.summaryState === 'missing' || !input.summary) return 'No Factory setup recorded for this repository.';
  if (!input.summary.preparation) return 'Project setup missing: no preparation recorded.';
  return input.summary.preparation.ready ? 'Project setup ready.' : 'Project setup missing: preparation incomplete.';
}
function outcomeNote(input: WorkOverviewInput, counts: OverviewCounts): string {
  if (input.issues.length === 0) return 'No work items recorded, so no outcome evidence exists yet.';
  if (counts.delivered === input.issues.length && input.unreadIssues.length === 0)
    return 'Every counted work item is delivered. Product-outcome evidence is not yet recorded separately.';
  return 'Overall completion needs evidence for the accepted product outcomes, not closed work items alone.';
}
function availabilityNote(input: WorkOverviewInput): string {
  const missing: string[] = [];
  if (input.intentState === 'unavailable') missing.push('accepted direction');
  if (input.generationState === 'unavailable') missing.push('native work mappings');
  if (input.summaryState === 'unavailable') missing.push('queue and setup');
  if (input.unreadIssues.length > 0)
    missing.push(`${input.unreadIssues.length} work item${input.unreadIssues.length === 1 ? '' : 's'}`);
  return missing.length > 0 ? `Unavailable right now: ${missing.join(', ')}.` : '';
}
// describeWorkOverview projects the accepted direction, factual
// work-item counts, attention, dependency rows and setup state. Each
// section reflects only its own read; unavailable reads are named.
export function describeWorkOverview(input: WorkOverviewInput): WorkOverviewModel {
  const {counts, attention} = countIssues(input);
  return {
    direction: directionText(input),
    counts,
    countsText: countsText(counts),
    attention,
    dependencies: dependencyRows(input),
    setupText: setupText(input),
    allowanceText: allowanceHoldText(input.summary?.allowance ?? []),
    outcomeNote: outcomeNote(input, counts),
    availabilityNote: availabilityNote(input),
    ...(input.summary?.dispatch ? {dispatch: input.summary.dispatch} : {}),
  };
}

export function renderWorkOverview(model: WorkOverviewModel): TemplateResult {
  return html`
    <section aria-label="Work overview">
      <p>${model.direction}</p>
      <p role="status">${model.countsText}</p>
      ${model.allowanceText ? html`<p role="alert">${model.allowanceText}</p>` : ''}
      ${model.availabilityNote ? html`<p>${model.availabilityNote}</p>` : ''}
      ${
        model.attention.length > 0
          ? html`<ul>
              ${model.attention.map(
                (item) =>
                  html`<li>
                    ${item.text} ${item.link ? html`<a href=${item.link}>Open issue #${item.issue}</a>` : ''}
                  </li>`
              )}
            </ul>`
          : ''
      }
      <p>${model.setupText}</p>
      ${model.dispatch ? html`<details>
        <summary>Dispatch status</summary>
        <p>Latest observed stage: ${model.dispatch.stage.replaceAll('_', ' ')}.</p>
        ${model.dispatch.observedAt ? html`<p>Last dispatch pass: <time datetime=${model.dispatch.observedAt}>${model.dispatch.observedAt}</time>.</p>` : ''}
        <ul>${model.dispatch.reasons.map(reason => html`<li>${reason}</li>`)}</ul>
        <p>This is the last recorded observation. Refresh overview to check again.</p>
      </details>` : ''}
      <p>${model.outcomeNote}</p>
      <ul>
        ${model.dependencies.map(
          (row) => html`<li>
            ${row.objective}
            ${row.dependsOn.length > 0 ? html`<span>Depends on: ${row.dependsOn.join(', ')}.</span>` : ''}
            ${row.issueLink ? html`<a href=${row.issueLink}>Issue #${row.issue}</a>` : 'No native issue mapped yet.'}
          </li>`
        )}
      </ul>
    </section>
  `;
}

export interface EligibleWork {
  readonly issue: string;
  readonly assessed_unix: number;
  readonly ageText: string;
  readonly link: string | undefined;
}
export interface WhyNextModel {
  readonly eligible: readonly EligibleWork[];
  readonly selection: string;
}
function ageText(assessedUnix: number, nowUnix: number): string {
  if (assessedUnix <= 0 || nowUnix < assessedUnix) return 'assessment time unknown';
  const minutes = Math.floor((nowUnix - assessedUnix) / 60);
  if (minutes < 1) return 'assessed just now';
  if (minutes < 60) return `assessed ${minutes} minute${minutes === 1 ? '' : 's'} ago`;
  const hours = Math.floor(minutes / 60);
  if (hours < 48) return `assessed ${hours} hour${hours === 1 ? '' : 's'} ago`;
  return `assessed ${Math.floor(hours / 24)} days ago`;
}
// describeWhyNext explains next-work selection from recorded facts
// only: a lone eligible issue needs no consultation, while several
// eligible issues await a recorded priority comparison.
export function describeWhyNext(
  issues: readonly FactoryIssueDetail[],
  links: FactoryWorkLinks | undefined,
  nowUnix: number
): WhyNextModel {
  const eligible = issues
    .filter((issue) => issue.readiness?.readiness === 'queued')
    .map((issue) => ({
      issue: issue.issue,
      assessed_unix: issue.readiness?.assessed_unix ?? 0,
      ageText: ageText(issue.readiness?.assessed_unix ?? 0, nowUnix),
      link: workIssueLink(links, issue.issue),
    }))
    .sort((a, b) => (a.assessed_unix === 0 ? 1 : b.assessed_unix === 0 ? -1 : a.assessed_unix - b.assessed_unix));
  if (eligible.length === 0) return {eligible, selection: 'No work is currently eligible to start.'};
  if (eligible.length === 1)
    return {eligible, selection: `Issue #${eligible[0]?.issue} is next: it is the only eligible work.`};
  return {
    eligible,
    selection:
      `${eligible.length} issues are eligible. No recorded priority comparison selected one, so none is marked next. ` +
      'Oldest assessment first.',
  };
}

export interface PrerequisiteModel {
  readonly issue: string;
  readonly link: string | undefined;
  readonly waitingText: string;
  readonly planNote: string;
}
// describePrerequisites names each issue's waiting reasons from its
// recorded readiness. Planning records do not exist in this read, so
// the plan note says exactly that instead of inventing freshness.
export function describePrerequisites(
  issues: readonly FactoryIssueDetail[],
  links: FactoryWorkLinks | undefined
): PrerequisiteModel[] {
  return issues.map((issue) => {
    const blockers = issue.readiness?.blockers ?? [];
    return {
      issue: issue.issue,
      link: workIssueLink(links, issue.issue),
      waitingText:
        blockers.length === 0
          ? `Issue #${issue.issue}: ${issue.readiness ? `readiness ${issue.readiness.readiness}` : 'no readiness record'}.`
          : `Issue #${issue.issue} waits on: ${blockers.map((blocker) => blocker.resolution).join(' ')}`,
      planNote: 'No recorded plan for this work.',
    };
  });
}

export function renderWorkPlan(whyNext: WhyNextModel, prerequisites: readonly PrerequisiteModel[]): TemplateResult {
  return html`
    <section aria-label="Planning and next work">
      <p role="status">${whyNext.selection}</p>
      <ul>
        ${whyNext.eligible.map(
          (item) => html`<li>
            ${item.link ? html`<a href=${item.link}>Issue #${item.issue}</a>` : `Issue #${item.issue}`}:
            ${item.ageText}.
          </li>`
        )}
      </ul>
      <ul>
        ${prerequisites.map(
          (row) => html`<li>
            ${row.link ? html`<a href=${row.link}>Issue #${row.issue}</a>` : `Issue #${row.issue}`}: ${row.waitingText}
            ${row.planNote}
          </li>`
        )}
      </ul>
    </section>
  `;
}

export interface DirectionRevisionModel {
  readonly headline: string;
  readonly attribution: string;
  readonly decisions: readonly {description: string; decision: string; correction?: string}[];
  readonly taskCount: number;
  readonly deltaText: string;
}
// describeDirectionRevision presents an accepted revision's
// attributable facts. Revision comparison has no read here, so it says
// exactly that.
export function describeDirectionRevision(
  revision: IntentRevision | undefined,
  state: SourceState
): DirectionRevisionModel {
  if (state === 'unavailable') {
    return {
      headline: 'Accepted direction could not be read.',
      attribution: '',
      decisions: [],
      taskCount: 0,
      deltaText: 'Revision comparison unavailable.',
    };
  }
  if (state === 'missing' || !revision) {
    return {
      headline: 'No accepted direction recorded.',
      attribution: '',
      decisions: [],
      taskCount: 0,
      deltaText: 'Revision comparison unavailable.',
    };
  }
  return {
    headline: revision.objective,
    attribution: `Revision ${revision.id}${revision.predecessor ? `, follows ${revision.predecessor}` : ''}. State: ${revision.state}.`,
    decisions: revision.ideas.map((idea) => ({
      description: idea.description,
      decision: idea.decision,
      ...(idea.correction === undefined ? {} : {correction: idea.correction}),
    })),
    taskCount: revision.tasks.length,
    deltaText: 'Revision comparison unavailable.',
  };
}

export function renderDirectionRevision(model: DirectionRevisionModel): TemplateResult {
  return html`
    <section aria-label="Direction revision">
      <p>${model.headline}</p>
      ${model.attribution ? html`<p>${model.attribution}</p>` : ''}
      <ul>
        ${model.decisions.map(
          (idea) => html`<li>
            ${idea.description} Decision: ${idea.decision}.${idea.correction ? ` Correction: ${idea.correction}.` : ''}
          </li>`
        )}
      </ul>
      <p>${model.taskCount} planned work items.</p>
      <p>${model.deltaText}</p>
    </section>
  `;
}

export interface RequestMappingRow {
  readonly task: string;
  readonly issue: string;
  readonly effective: boolean;
  readonly issueLink: string | undefined;
}
export interface RequestOverviewModel {
  readonly direction: string;
  readonly stateText: string;
  readonly counts: OverviewCounts;
  readonly countsText: string;
  readonly attention: readonly AttentionItem[];
  readonly mappings: readonly RequestMappingRow[];
  readonly setupText: string;
  readonly generationText: string;
  readonly outcomeNote: string;
  readonly observedText: string;
}

function requestAttentionItem(item: RequestAttention, links: FactoryWorkLinks | undefined): AttentionItem {
  if (item.code === 'work_attention' && item.issue !== undefined)
    return {
      issue: item.issue,
      text: `Issue #${item.issue} needs attention.`,
      link: workIssueLink(links, item.issue),
    };
  if (item.code === 'unresolved_assumption')
    return {issue: '', text: 'One consequential assumption still needs your decision.', link: undefined};
  return {issue: '', text: 'Native work generation was refused.', link: undefined};
}
function requestSetupText(state: string): string {
  if (state === 'repository_unassigned') return 'No repository assigned to this request.';
  if (state === 'project_missing') return 'Project setup missing: no project prepared.';
  if (state === 'preparing') return 'Project setup preparing.';
  if (state === 'ready') return 'Project setup ready.';
  return 'Project setup status unavailable.';
}
function requestGenerationText(state: string): string {
  if (state === 'refused') return 'Native work generation was refused.';
  if (state === 'pending') return 'Native work generation pending.';
  if (state === 'committed' || state === 'completed') return 'Native work generated.';
  if (state === 'not_requested') return 'Native work not requested yet.';
  return `Generation state: ${state}.`;
}
function requestDirectionText(view: RequestOverview): string {
  if (view.state === 'accepted') return view.direction.objective;
  return `Request is ${view.state}: ${view.direction.objective}`;
}
// describeRequestOverview projects the H-R request overview: the exact
// direction, evidenced work-item counts, attention, native mappings and
// setup. Outcome evidence counts render as counts, never percentages.
export function describeRequestOverview(
  view: RequestOverview,
  links: FactoryWorkLinks | undefined
): RequestOverviewModel {
  const counted =
    view.work_items.delivered + view.work_items.working + view.work_items.waiting + view.work_items.attention;
  const counts: OverviewCounts = {
    delivered: view.work_items.delivered,
    working: view.work_items.working,
    waiting: view.work_items.waiting,
    attention: view.work_items.attention,
    other: Math.max(0, view.work_items.total - counted),
  };
  const observed = activityTimestamp(view.observed_unix);
  return {
    direction: requestDirectionText(view),
    stateText:
      view.state === 'accepted'
        ? `${view.direction.tasks_total} work items planned, ${view.direction.unresolved_assumptions} unresolved assumptions.`
        : `Revision ${view.revision.slice(0, 9)}.`,
    counts,
    countsText: countsText(counts),
    attention: view.attention.map((item) => requestAttentionItem(item, links)),
    mappings: view.mappings.map((mapping) => ({
      task: mapping.task_key.slice(0, 9),
      issue: mapping.issue_index,
      effective: mapping.effective,
      issueLink: workIssueLink(links, mapping.issue_index),
    })),
    setupText: requestSetupText(view.setup.state),
    generationText: requestGenerationText(view.generation),
    outcomeNote:
      view.work_items.outcomes_total === 0
        ? 'No accepted product outcomes recorded.'
        : `${view.work_items.outcomes_evidenced} of ${view.work_items.outcomes_total} accepted product outcomes evidenced.`,
    observedText: observed ? `Observed ${observed.text}.` : 'Observation time unknown.',
  };
}

// OverviewScope is the child-view feed the reached overview
// publishes: the read keys it used plus the bound work items with
// the capabilities from its own issue reads, when it read them.
// A retired scope carries repository '' and no issues.
export interface OverviewScopeIssue {
  readonly issue: string;
  readonly capabilities?: WorkCapabilities;
}
export interface OverviewScope {
  readonly repository: string;
  readonly intentID?: string;
  readonly issues: readonly OverviewScopeIssue[];
}
// overviewScopeFromMappings feeds scope from H-R request mappings:
// effective mappings only; capabilities stay unknown (the overview
// route reads no per-issue capabilities).
export function overviewScopeFromMappings(
  mappings: readonly RequestMappingRow[],
  repository: string,
  intentID?: string
): OverviewScope {
  return {
    repository,
    ...(intentID === undefined ? {} : {intentID}),
    issues: mappings.filter((row) => row.effective).map((row) => ({issue: row.issue})),
  };
}
// overviewScopeFromBound feeds scope from composed fallback reads:
// every issue the overview read, with the capabilities it saw.
export function overviewScopeFromBound(
  details: readonly FactoryIssueDetail[],
  repository: string,
  intentID?: string
): OverviewScope {
  return {
    repository,
    ...(intentID === undefined ? {} : {intentID}),
    issues: details.map((detail) => ({issue: detail.issue, capabilities: detail.capabilities})),
  };
}
export function retiredOverviewScope(): OverviewScope {
  return {repository: '', issues: []};
}

export function renderRequestOverview(model: RequestOverviewModel): TemplateResult {
  return html`
    <section aria-label="Request overview">
      <p>${model.direction}</p>
      <p>${model.stateText}</p>
      <p role="status">${model.countsText}</p>
      ${
        model.attention.length > 0
          ? html`<ul>
              ${model.attention.map(
                (item) =>
                  html`<li>
                    ${item.text} ${item.link ? html`<a href=${item.link}>Open issue #${item.issue}</a>` : ''}
                  </li>`
              )}
            </ul>`
          : ''
      }
      <p>${model.setupText}</p>
      <p>${model.generationText}</p>
      <p>${model.outcomeNote}</p>
      <p>${model.observedText}</p>
      <ul>
        ${model.mappings.map(
          (row) => html`<li>
            Task ${row.task} →
            ${row.issueLink ? html`<a href=${row.issueLink}>issue #${row.issue}</a>` : `issue #${row.issue}`}
            ${row.effective ? '' : '(outside the authorized subset)'}
          </li>`
        )}
      </ul>
    </section>
  `;
}

export interface ChoiceCandidateRow {
  readonly issue: string;
  readonly ageText: string;
  readonly estimatesText: string;
  readonly link: string | undefined;
}
export interface RecordedChoiceModel {
  readonly selection: string;
  readonly judgmentText: string;
  readonly candidates: readonly ChoiceCandidateRow[];
  readonly moreText: string;
}
function durationText(seconds: number): string {
  if (seconds < 60) return `${seconds} second${seconds === 1 ? '' : 's'}`;
  const minutes = Math.floor(seconds / 60);
  if (minutes < 60) return `${minutes} minute${minutes === 1 ? '' : 's'}`;
  const hours = Math.floor(minutes / 60);
  if (hours < 48) return `${hours} hour${hours === 1 ? '' : 's'}`;
  return `${Math.floor(hours / 24)} days`;
}
function estimatesText(impact: boolean, risk: boolean, effort: boolean): string {
  const known = [...(impact ? ['impact'] : []), ...(risk ? ['risk'] : []), ...(effort ? ['effort'] : [])];
  return known.length > 0 ? `${known.join(', ')} recorded` : 'no estimates recorded';
}
function choiceSelection(comparison: PriorityComparison): string {
  if (comparison.selected !== undefined && comparison.selected_visible) {
    const winner = comparison.candidates.find((candidate) => candidate.id === comparison.selected);
    const probability = comparison.probabilities?.[comparison.selected];
    const odds = probability === undefined ? '' : ` (recorded probability ${probability}).`;
    if (winner) return `Recorded choice: issue #${winner.issue}.${odds}`;
    return `Recorded choice: ${comparison.selected}.${odds}`;
  }
  if (!comparison.selected_visible) return "Another repository's work was selected.";
  return 'No choice recorded in the latest comparison.';
}
function judgmentText(comparison: PriorityComparison): string {
  const latest = comparison.calls[comparison.calls.length - 1];
  if (!latest) return 'No judgment call recorded.';
  if (latest.failure) return `Latest judgment call failed: ${latest.failure}.`;
  const confidence = latest.confidence === undefined ? '' : ` Confidence ${latest.confidence}.`;
  return `Latest judgment${latest.model ? ` by ${latest.model}` : ''}.${confidence}`;
}
// describeRecordedChoice explains the newest recorded next-work
// comparison: the recorded choice with its uncertainty, the eligible
// candidates with fairness inputs, and masked foreign winners.
export function describeRecordedChoice(plan: IssuePlan, links: FactoryWorkLinks | undefined): RecordedChoiceModel {
  const comparison = plan.comparisons[0];
  if (!comparison)
    return {
      selection: 'No recorded priority comparison names this issue.',
      judgmentText: '',
      candidates: [],
      moreText: '',
    };
  return {
    selection: choiceSelection(comparison),
    judgmentText: judgmentText(comparison),
    candidates: comparison.candidates.map((candidate) => ({
      issue: candidate.issue,
      ageText: `waiting ${durationText(candidate.age_seconds)}`,
      estimatesText: estimatesText(candidate.impact_known, candidate.risk_known, candidate.effort_known),
      link: workIssueLink(links, candidate.issue),
    })),
    moreText: plan.next_before_unix !== undefined ? 'Older comparisons exist beyond this page.' : '',
  };
}

export function renderRecordedChoice(choice: RecordedChoiceModel): TemplateResult {
  return html`
    <section aria-label="Recorded choice">
      <p>${choice.selection}</p>
      ${choice.judgmentText ? html`<p>${choice.judgmentText}</p>` : ''}
      ${choice.moreText ? html`<p>${choice.moreText}</p>` : ''}
      <ul>
        ${choice.candidates.map(
          (row) => html`<li>
            ${row.link ? html`<a href=${row.link}>Issue #${row.issue}</a>` : `Issue #${row.issue}`}: ${row.ageText},
            ${row.estimatesText}.
          </li>`
        )}
      </ul>
    </section>
  `;
}

export interface AccessStandingModel {
  readonly powersText: string;
  readonly verdictText: string;
  readonly actionsText: readonly string[];
}

function standingPowers(access: FactoryAccess): string {
  const held: string[] = [];
  if (access.viewer.local_owner) held.push('local computer owner');
  if (access.viewer.code_write) held.push('code write');
  if (access.viewer.owner_or_admin) held.push('owner or admin');
  if (access.viewer.environment_owner) held.push('environment owner');
  if (access.viewer.operator) held.push('execution operator');
  if (access.viewer.member) held.push('project member');
  const session = access.viewer.session_current
    ? 'Session current.'
    : 'Session changed; saves are refused until it is current.';
  return held.length === 0 ? `You hold no repository powers. ${session}` : `You hold: ${held.join(', ')}. ${session}`;
}

// Wire codes stay on the wire; the sidebar shows local status text.
const missingPhrases: Readonly<Record<string, string>> = {
  policy_missing: 'no Factory policy recorded',
  operator_grant_missing: 'no operator grant recorded',
  capacity_missing: 'no dispatch capacity recorded',
  environment_grant_missing: 'no environment grant recorded',
  authority_missing: 'no Factory authority recorded',
};

function missingPhrase(code: string): string {
  return missingPhrases[code] ?? 'a prerequisite is missing';
}

function standingVerdict(access: FactoryAccess): string {
  if (access.effective.effective && access.effective.dispatch_open) return 'Factory dispatch is effective and open.';
  if (access.effective.effective) return 'Authority is effective but dispatch is closed.';
  const missing =
    access.effective.missing.length === 0 ? 'no reason given' : access.effective.missing.map(missingPhrase).join(', ');
  return `Factory authority is not effective: ${missing}.`;
}

function standingAction(code: string, resolveBy: string, setting: string): string {
  const resolver = resolveBy === '' ? 'unassigned' : resolveBy;
  const section = setting === '' ? 'no setting named' : setting;
  return `${missingPhrase(code)}: resolve by ${resolver} via ${section}.`;
}

// describeAccessStanding states the viewer's exact powers and the
// authority verdict behind permission-gated controls. Unknown
// resolvers stay visible as unassigned, never guessed.
export function describeAccessStanding(access: FactoryAccess | undefined, state: SourceState): AccessStandingModel {
  if (state !== 'ready' || !access)
    return {
      powersText:
        state === 'unavailable' ? 'Your permissions could not be read.' : 'No permission record for this repository.',
      verdictText: 'Factory authority is unknown.',
      actionsText: [],
    };
  return {
    powersText: standingPowers(access),
    verdictText: standingVerdict(access),
    actionsText: access.required_actions.map((action) =>
      standingAction(action.code, action.resolve_by, action.setting)
    ),
  };
}

export function renderAccessStanding(model: AccessStandingModel): TemplateResult {
  return html`
    <section aria-label="Access standing">
      <p>${model.powersText}</p>
      <p role="status">${model.verdictText}</p>
      ${
        model.actionsText.length === 0
          ? ''
          : html`<ul>
              ${model.actionsText.map((line) => html`<li>${line}</li>`)}
            </ul>`
      }
    </section>
  `;
}

export interface RevisionEntryModel {
  readonly headerText: string;
  readonly lineageText: string;
  readonly tasksText: string;
  readonly taskLines: readonly string[];
  readonly ideasText: string;
  readonly ideaLines: readonly string[];
  readonly scopeLines: readonly string[];
  readonly workLines: readonly string[];
}
export interface RevisionHistoryModel {
  readonly stateText: string;
  readonly truncatedNote: string;
  readonly entries: readonly RevisionEntryModel[];
}

function revisionTaskChangedLine(change: RevisionTaskChange): string {
  const parts = [`${change.key} (${change.changed_fields.join(', ')})`];
  if (change.old_objective !== undefined)
    parts.push(`objective: ${change.old_objective} → ${change.new_objective ?? ''}`);
  if (change.old_outcomes !== undefined)
    parts.push(`outcomes: ${(change.old_outcomes ?? []).join('; ')} → ${(change.new_outcomes ?? []).join('; ')}`);
  return parts.join('; ');
}

function revisionTaskLines(entry: RevisionEntry): string[] {
  const lines = entry.comparison.tasks.added.map((task) => `+ ${task.key}: ${task.objective}`);
  for (const task of entry.comparison.tasks.removed) lines.push(`- ${task.key}: ${task.objective}`);
  for (const change of entry.comparison.tasks.changed) lines.push(`~ ${revisionTaskChangedLine(change)}`);
  return lines;
}

function revisionIdeaChangedLine(change: RevisionIdeaChange): string {
  const flags: string[] = [];
  if (change.correction_changed) flags.push('correction changed');
  if (change.content_changed) flags.push('content changed');
  const note = flags.length === 0 ? 'decision only' : flags.join(', ');
  return `${change.id}: ${change.from_decision} → ${change.to_decision} (${note})`;
}

function revisionIdeaLines(entry: RevisionEntry): string[] {
  const lines = entry.comparison.ideas.added.map((idea) => `+ ${idea.id} (${idea.decision}): ${idea.description}`);
  for (const idea of entry.comparison.ideas.removed) lines.push(`- ${idea.id} (${idea.decision}): ${idea.description}`);
  for (const change of entry.comparison.ideas.changed) lines.push(`~ ${revisionIdeaChangedLine(change)}`);
  return lines;
}

function revisionScopeLines(entry: RevisionEntry): string[] {
  const lines: string[] = [];
  if (entry.comparison.effective_added.length > 0)
    lines.push(`Effective outcomes added: ${entry.comparison.effective_added.join(', ')}.`);
  if (entry.comparison.effective_removed.length > 0)
    lines.push(`Effective outcomes removed: ${entry.comparison.effective_removed.join(', ')}.`);
  if (entry.comparison.repository_change)
    lines.push(`Repository ${entry.comparison.repository_change.from} → ${entry.comparison.repository_change.to}.`);
  if (entry.comparison.state_change)
    lines.push(`State ${entry.comparison.state_change.from} → ${entry.comparison.state_change.to}.`);
  if (entry.comparison.edges_changed) lines.push('Dependency edges changed.');
  return lines;
}

function revisionSubmittedNote(generation: RevisionGeneration): string {
  if (generation.state === 'not_requested') return '';
  const count = `${generation.submitted_keys.length} submitted key${generation.submitted_keys.length === 1 ? '' : 's'}`;
  const differs = generation.submitted_differs ? ' Differs from accepted direction.' : '';
  return ` Submitted graph: ${count}.${differs}`;
}

function revisionEntryLineage(entry: RevisionEntry): string {
  const from = entry.generated_from === undefined ? '' : ` Generated from ${entry.generated_from}.`;
  const first = entry.comparison.initial ? ' First revision.' : '';
  const stamp = activityTimestamp(entry.created_unix);
  const when = stamp === undefined ? 'time unknown' : stamp.text;
  return `Repository ${entry.repository}, recorded ${when}.${from}${first} Generation ${entry.generation.state}.${revisionSubmittedNote(entry.generation)}`;
}

function describeRevisionEntry(entry: RevisionEntry): RevisionEntryModel {
  const tasks = entry.comparison.tasks;
  const ideas = entry.comparison.ideas;
  return {
    headerText: `Revision ${entry.revision} (${entry.state})`,
    lineageText: revisionEntryLineage(entry),
    tasksText: `Tasks: ${tasks.added.length} added, ${tasks.removed.length} removed, ${tasks.changed.length} changed, ${tasks.reused.length} reused, ${tasks.unchanged_count} unchanged.`,
    taskLines: revisionTaskLines(entry),
    ideasText: `Ideas: ${ideas.added.length} added, ${ideas.removed.length} removed, ${ideas.changed.length} changed.`,
    ideaLines: revisionIdeaLines(entry),
    scopeLines: revisionScopeLines(entry),
    workLines: entry.work.map(
      (row) =>
        `${row.task_key} → issue ${row.issue_index}: ${row.state} (${row.authority}), content version ${row.content_version}`
    ),
  };
}

// describeRevisionHistory composes the before/after and affected
// work of each shown scope revision from the authoritative v3
// classification, keeping the submitted graph distinct from the
// accepted direction it was generated against.
export function describeRevisionHistory(
  history: RevisionHistory | undefined,
  state: SourceState
): RevisionHistoryModel {
  if (state !== 'ready' || !history)
    return {
      stateText: state === 'unavailable' ? 'Revision history could not be read.' : 'No revision history recorded yet.',
      truncatedNote: '',
      entries: [],
    };
  return {
    stateText: `${history.revisions.length} revision${history.revisions.length === 1 ? '' : 's'} shown, newest first.`,
    truncatedNote: history.truncated ? 'Older revisions exist beyond this page.' : '',
    entries: history.revisions.map(describeRevisionEntry),
  };
}

export function renderRevisionHistory(model: RevisionHistoryModel): TemplateResult {
  return html`
    <section aria-label="Revision history">
      <p role="status">${model.stateText}</p>
      ${model.truncatedNote ? html`<p>${model.truncatedNote}</p>` : ''}
      ${model.entries.map(
        (entry) => html`<article>
          <h4>${entry.headerText}</h4>
          <p>${entry.lineageText}</p>
          <p>${entry.tasksText}</p>
          ${
            entry.taskLines.length === 0
              ? ''
              : html`<ul>
                  ${entry.taskLines.map((line) => html`<li>${line}</li>`)}
                </ul>`
          }
          <p>${entry.ideasText}</p>
          ${
            entry.ideaLines.length === 0
              ? ''
              : html`<ul>
                  ${entry.ideaLines.map((line) => html`<li>${line}</li>`)}
                </ul>`
          }
          ${entry.scopeLines.map((line) => html`<p>${line}</p>`)}
          ${
            entry.workLines.length === 0
              ? html`<p>No mapped work for this revision.</p>`
              : html`<ul>
                  ${entry.workLines.map((line) => html`<li>${line}</li>`)}
                </ul>`
          }
        </article>`
      )}
    </section>
  `;
}
