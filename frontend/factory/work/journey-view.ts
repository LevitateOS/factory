import {html} from 'lit';
import type {TemplateResult} from 'lit';
import type {FactoryWorkLinks} from '../app/navigation.js';
import {id} from '../api/models.js';
import type {
  JourneyAdoption,
  JourneyCommit,
  JourneyCorrection,
  JourneyDeliveredTask,
  JourneyFinding,
  JourneyGate,
  JourneyJudgment,
  JourneyPhase,
  JourneyTask,
  JourneyVerifiedApp,
  WorkJourney,
} from '../api/journey-response.js';

// Work-journey presentation. Model builders turn one issue's recorded
// facts into display state; render helpers project those models to Lit
// templates. No transport, identity lookup or mutable state lives here.
// Evidence identities (commits, digests, acceptances, completions) render
// as text only; native navigation is built solely through the
// host-supplied issue/pull callbacks, so evidence and navigation links
// never mix. The read-only work-journey element owns the binding.

function journeyIssueLink(links: FactoryWorkLinks | undefined, number: string): string | undefined {
  return id(number) ? links?.issue(number) : undefined;
}

function journeyPullLink(links: FactoryWorkLinks | undefined, number: string): string | undefined {
  return id(number) ? links?.pull(number) : undefined;
}

export function describeJourneyPhase(phase: JourneyPhase): string {
  return `Phase ${phase.phase}: ${phase.state}.`;
}

export interface JourneyTaskModel {
  readonly text: string;
  readonly link: string | undefined;
}

export function describeJourneyTask(task: JourneyTask, links: FactoryWorkLinks | undefined): JourneyTaskModel {
  const lane = task.lane === 'unassigned' ? 'no lane yet' : `lane ${task.lane}`;
  const issue = task.issue === undefined ? 'no native issue mapped yet' : `issue #${task.issue}`;
  return {
    text: `Task ${task.key} (${lane}): ${task.state}, ${issue}.`,
    link: task.issue === undefined ? undefined : journeyIssueLink(links, task.issue),
  };
}

export interface JourneyCommitModel {
  readonly text: string;
  readonly pullLink: string | undefined;
}

export function describeJourneyCommit(commit: JourneyCommit, links: FactoryWorkLinks | undefined): JourneyCommitModel {
  return {
    text: `Candidate ${commit.commit.slice(0, 12)} over base ${commit.base.slice(0, 12)}: PR #${commit.pr_number}.`,
    pullLink: journeyPullLink(links, commit.pr_number),
  };
}

export function describeJourneyFinding(finding: JourneyFinding): string {
  return `${finding.severity} finding ${finding.key.slice(0, 9)} needs ${finding.fix_kind}: ${finding.defect}`;
}

export function describeJourneyCorrection(correction: JourneyCorrection): string {
  if (correction.candidate === undefined) return 'Correction pending: no candidate published yet.';
  const effect = correction.effect === undefined ? 'effect unrecorded' : `effect ${correction.effect}`;
  const completion = correction.completion === undefined ? '' : `, ${correction.completion}`;
  return `Correction ${correction.candidate.slice(0, 12)}: ${effect}${completion}.`;
}

const judgmentKindCopy: Record<string, string> = {
  priority: 'Next-work priority',
  delivery: 'Delivery acceptance',
  'reassessment-decision': 'D13 completion decision',
  'reassessment-proposal': 'D13 reassessment proposal',
};

export function describeJourneyJudgment(judgment: JourneyJudgment): string {
  const kind = judgmentKindCopy[judgment.kind] ?? 'Judgment';
  const confidence = judgment.confidence === undefined ? '' : ` Confidence ${judgment.confidence}.`;
  const cycle =
    judgment.cycle_id === undefined ? '' : ` Cycle ${judgment.cycle_id.slice(0, 9)}: ${judgment.cycle_state}.`;
  const choice = `${kind} selected ${judgment.selected}.${confidence}${cycle}`;
  if (judgment.cycle_state === 'superseded')
    return `${choice} This recorded decision was superseded by an explicit retry; it is not a current wait.`;
  if (judgment.cycle_state === 'released') return `${choice} This completion cycle was released.`;
  if (!judgment.wait) return `${choice} No wait recorded.`;
  return `${choice} Waiting: ${judgment.wait_reason}. This wait is not a confirmed new attempt.`;
}

export interface JourneyGateModel {
  readonly gateText: string;
  readonly proposalText: string;
  readonly waitText: string;
}

function gateDecisionText(gate: JourneyGate): string {
  if (gate.decision === undefined) return 'no gate decision recorded';
  return `gate decision ${gate.decision}`;
}

function gateProposalText(gate: JourneyGate): string {
  if (gate.proposal === undefined) return 'No proposal assessed yet.';
  const digest = gate.proposal_digest === undefined ? '' : ` (proposal ${gate.proposal_digest.slice(0, 12)})`;
  return `Proposal ${gate.proposal}${digest}.`;
}

function gateWaitText(gate: JourneyGate): string {
  if (gate.wait_reason === undefined) return '';
  const retry = gate.retryable ? ' An explicit retry can proceed.' : '';
  return `Waiting: ${gate.wait_reason}.${retry}`;
}

// describeJourneyGate projects one D13 completion gate to display
// state: the fenced completion cycle, its gate decision and proposal,
// and the bounded wait/retry reason. Retryable reads only on attention.
export function describeJourneyGate(gate: JourneyGate): JourneyGateModel {
  return {
    gateText:
      `Completion ${gate.completion_id.slice(0, 9)} cycle ${gate.cycle_id.slice(0, 9)} ` +
      `(generation ${gate.generation}): ${gate.state}, ${gateDecisionText(gate)}.`,
    proposalText: gateProposalText(gate),
    waitText: gateWaitText(gate),
  };
}

export interface JourneyAdoptionModel {
  readonly text: string;
  readonly link: string | undefined;
}

// describeJourneyAdoption projects one recorded adoption: the adopted
// intent revision with its exact task binding. Evidence identities
// (intent, revision, task, acceptance) render as text; the native issue
// behind the binding gets the only link, built separately.
export function describeJourneyAdoption(
  adoption: JourneyAdoption,
  links: FactoryWorkLinks | undefined
): JourneyAdoptionModel {
  return {
    text:
      `Proposal adoption ${adoption.state}: intent ${adoption.intent.slice(0, 9)} ` +
      `at revision ${adoption.revision.slice(0, 9)}, bound task ${adoption.task} ` +
      `on native issue #${adoption.issue_id} (acceptance ${adoption.acceptance.slice(0, 9)}).`,
    link: journeyIssueLink(links, adoption.issue_id),
  };
}

export interface JourneyDeliveredTaskModel {
  readonly text: string;
}

// describeJourneyDeliveredTask projects one delivered-task evidence row:
// the bound task with the canonical completion behind its native issue
// and acceptance. Completion, publication and generation are evidence
// identities and render as text: no native URL scheme names them, so no
// link is invented for them. The native issue link lives on the adoption
// and task rows, never duplicated here.
export function describeJourneyDeliveredTask(delivered: JourneyDeliveredTask): JourneyDeliveredTaskModel {
  return {
    text:
      `Task ${delivered.task} delivered on native issue #${delivered.issue} ` +
      `(acceptance ${delivered.acceptance.slice(0, 9)}): completion ${delivered.completion.slice(0, 9)} ` +
      `via publication ${delivered.publication.slice(0, 9)} (generation ${delivered.generation}).`,
  };
}

export interface JourneyVerifiedAppModel {
  readonly text: string;
}

// describeJourneyVerifiedApp projects one verified application revision:
// the exact adopted revision whose trusted check passed, with the
// recorded source commit and manifest digest. Both render as evidence
// text; only a recorded F10 verification renders here, never a link to
// an unverified claim.
export function describeJourneyVerifiedApp(verified: JourneyVerifiedApp): JourneyVerifiedAppModel {
  return {
    text:
      `Verified application revision ${verified.revision.slice(0, 9)} ` +
      `of intent ${verified.intent.slice(0, 9)}: trusted check passed ` +
      `at source ${verified.source_commit.slice(0, 12)} (manifest ${verified.manifest.slice(0, 12)}).`,
  };
}

export interface WorkJourneyModel {
  readonly headline: string;
  readonly phases: readonly string[];
  readonly tasks: readonly JourneyTaskModel[];
  readonly commits: readonly JourneyCommitModel[];
  readonly findings: readonly string[];
  readonly corrections: readonly string[];
  readonly judgments: readonly string[];
  readonly gates: readonly JourneyGateModel[];
  readonly adoptions: readonly JourneyAdoptionModel[];
  readonly delivered: readonly JourneyDeliveredTaskModel[];
  readonly verifications: readonly JourneyVerifiedAppModel[];
  readonly observedText: string;
}

function journeyHeadline(journey: WorkJourney): string {
  const parts: string[] = [];
  if (journey.tasks.length > 0) parts.push(`${journey.tasks.length} tasks`);
  if (journey.findings.length > 0) parts.push(`${journey.findings.length} findings`);
  if (journey.gates.length > 0) parts.push(`${journey.gates.length} completion gates`);
  if (journey.delivered_tasks.length > 0) parts.push(`${journey.delivered_tasks.length} delivered tasks`);
  if (journey.verifications.length > 0) parts.push(`${journey.verifications.length} verified applications`);
  if (parts.length > 0) return `Work journey: ${parts.join(' · ')}.`;
  return [journey.phases, journey.commits, journey.corrections, journey.judgments, journey.adoptions].some(
    (rows) => rows.length
  )
    ? 'Recorded work history.'
    : 'No journey sections recorded.';
}

// describeWorkJourney projects one recorded journey to display state.
// Every section reflects only its own facts; missing sections stay
// silent instead of inventing progress.
export function describeWorkJourney(journey: WorkJourney, links: FactoryWorkLinks | undefined): WorkJourneyModel {
  return {
    headline: journeyHeadline(journey),
    phases: journey.phases.map(describeJourneyPhase),
    tasks: journey.tasks.map((task) => describeJourneyTask(task, links)),
    commits: journey.commits.map((commit) => describeJourneyCommit(commit, links)),
    findings: journey.findings.map(describeJourneyFinding),
    corrections: journey.corrections.map(describeJourneyCorrection),
    judgments: journey.judgments.map(describeJourneyJudgment),
    gates: journey.gates.map(describeJourneyGate),
    adoptions: journey.adoptions.map((adoption) => describeJourneyAdoption(adoption, links)),
    delivered: journey.delivered_tasks.map(describeJourneyDeliveredTask),
    verifications: journey.verifications.map(describeJourneyVerifiedApp),
    observedText: describeJourneyObserved(journey.observed_unix),
  };
}

function describeJourneyObserved(observedUnix: number): string {
  if (!Number.isSafeInteger(observedUnix) || observedUnix <= 0) return 'Observation time unrecorded.';
  const date = new Date(observedUnix * 1000);
  if (!Number.isFinite(date.getTime())) return 'Observation time unrecorded.';
  return `Observed ${date.toLocaleString(undefined, {month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit'})}.`;
}

export function renderWorkJourney(model: WorkJourneyModel): TemplateResult {
  const section = (title: string, rows: readonly (string | TemplateResult)[]) =>
    rows.length
      ? html`<section aria-label=${title}>
          <h3>${title}</h3>
          <ul>
            ${rows.map((row) => html`<li>${row}</li>`)}
          </ul>
        </section>`
      : '';
  return html`
    <section aria-label="Work journey">
      <p role="status">${model.headline}</p>
      ${section('Recorded phases', model.phases)}
      ${section(
        'Tasks',
        model.tasks.map((task) => html`${task.text} ${task.link ? html`<a href=${task.link}>Open issue</a>` : ''}`)
      )}
      ${section(
        'Published candidates',
        model.commits.map(
          (commit) =>
            html`${commit.text} ${commit.pullLink ? html`<a href=${commit.pullLink}>Open pull request</a>` : ''}`
        )
      )}
      ${section('Review findings', model.findings)} ${section('Corrections', model.corrections)}
      ${section('Recorded decisions', model.judgments)}
      ${section(
        'Completion reviews',
        model.gates.map((gate) => html`${gate.gateText} ${gate.proposalText} ${gate.waitText}`)
      )}
      ${section(
        'Proposal adoption',
        model.adoptions.map(
          (adoption) => html`${adoption.text} ${adoption.link ? html`<a href=${adoption.link}>Open issue</a>` : ''}`
        )
      )}
      ${section(
        'Delivered tasks',
        model.delivered.map((row) => html`${row.text}`)
      )}
      ${section(
        'Verified application',
        model.verifications.map((row) => html`${row.text}`)
      )}
      <p>${model.observedText}</p>
    </section>
  `;
}
