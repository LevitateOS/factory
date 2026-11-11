import {attentionText, type IntentPlanStatus, type IntentRevision} from '../api/request-response.js';

// planPhase is the one answer to "where is this request in its review and
// plan journey". Every label, caption, button and transition in the request
// screen reads it (through PHASES) instead of re-deriving it from the
// revision and plan fields. A plan read for another revision is treated as
// absent here, once.
export type PlanPhase =
  | {kind: 'entry'}
  | {kind: 'accepted'}
  | {kind: 'unbound'}
  | {kind: 'unloaded'}
  | {kind: 'none'}
  | {kind: 'planning'}
  | {kind: 'question'; question: string}
  | {kind: 'stopped'}
  | {kind: 'failed'}
  | {kind: 'cancelled'}
  | {kind: 'no-walkthrough'}
  | {kind: 'unchecked'}
  | {kind: 'findings'}
  | {kind: 'clean'}
  | {kind: 'unknown'};
export type PhaseKind = PlanPhase['kind'];

// The plan for the shown revision; a plan read for another one is absent.
export function planFor(revision: IntentRevision | undefined, plan: IntentPlanStatus | undefined) {
  return revision && plan?.revision === revision.id ? plan : undefined;
}

export function planPhase(revision: IntentRevision | undefined, status: IntentPlanStatus | undefined): PlanPhase {
  if (!revision) return {kind: 'entry'};
  if (revision.state === 'accepted') return {kind: 'accepted'};
  if (revision.repository === '0') return {kind: 'unbound'};
  const plan = planFor(revision, status);
  if (!plan) return {kind: 'unloaded'};
  switch (plan.state) {
    case 'none':
      return {kind: 'none'};
    case 'pending':
      return {kind: 'planning'};
    case 'attention':
      return plan.attention ? {kind: 'question', question: plan.attention} : {kind: 'stopped'};
    case 'failed':
      return {kind: 'failed'};
    case 'cancelled':
      return {kind: 'cancelled'};
    case 'completed':
      if (!plan.walkthroughs) return {kind: 'no-walkthrough'};
      if (!plan.review) return {kind: 'unchecked'};
      return plan.review.consistency.mismatches.length ? {kind: 'findings'} : {kind: 'clean'};
    default:
      return {kind: 'unknown'};
  }
}

const planFailed = 'Planning failed. Your direction is saved; try again.';
const planCancelled = 'Planning was cancelled before it could report.';
const noWalkthrough = 'This saved plan has no request-specific walkthrough. Author one in a new plan before accepting.';
const findingsText =
  'Correct or clarify the request above for the walkthrough findings (your objective and choices stay), then plan again and check before authorizing work.';
const savedNext = 'Your request and choices are saved. Continue to planning when project preparation is ready.';

interface Row {
  chip: string;
  // Status line under Build preparation; '' shows none.
  status: string;
  caption: string;
  // Next-step text. gated: the missing-behavior, unanswered-question and
  // choose-repository guidance takes precedence over it.
  next: string;
  gated: boolean;
  // Why a completed plan cannot be accepted yet ('' when it can).
  block: string;
  // Primary button: disabled always, until the review is ready, or never.
  primary: 'always' | 'ready' | 'never';
  // A new planning attempt may start from this phase (the real transitions).
  attempt: boolean;
  // Which plan-start button shows: none, Plan work, Author walkthrough.
  button: '' | 'plan' | 'author';
  // The recorded cause of a stopped plan is offered.
  reason: boolean;
}
const row = (r: Partial<Row> & {chip: string; caption: string; next: string}): Row => ({
  status: '',
  gated: true,
  block: '',
  primary: 'ready',
  attempt: false,
  button: '',
  reason: false,
  ...r,
});
const CONTINUE = 'Continue to planning';
const PHASES: Record<Exclude<PhaseKind, 'question'>, Row> = {
  entry: row({chip: '', caption: CONTINUE, next: savedNext}),
  accepted: row({chip: 'Accepted', caption: CONTINUE, next: savedNext}),
  unbound: row({
    chip: '',
    caption: 'Choose repository',
    next: 'Choose where to keep this application. Your request and choices stay together.',
  }),
  unloaded: row({chip: '', caption: CONTINUE, next: savedNext, button: 'plan'}),
  none: row({
    chip: '',
    status: 'No planning attempt is recorded for this draft.',
    caption: CONTINUE,
    next: savedNext,
    attempt: true,
    button: 'plan',
  }),
  planning: row({
    chip: 'Planning…',
    status: 'Planning is recorded. Check the existing run for progress.',
    caption: 'Planning…',
    next: '',
    gated: false,
    primary: 'always',
  }),
  stopped: row({
    chip: 'Planning stopped',
    status: attentionText(undefined),
    caption: CONTINUE,
    next: 'The planner stopped without a usable answer; your request needs no change. Continue to planning to try again. It uses no planning calls.',
    gated: false,
    attempt: true,
    button: 'plan',
    reason: true,
  }),
  failed: row({
    chip: 'Planning failed',
    status: planFailed,
    caption: CONTINUE,
    next: savedNext,
    attempt: true,
    button: 'plan',
    reason: true,
  }),
  cancelled: row({
    chip: 'Planning cancelled',
    status: planCancelled,
    caption: CONTINUE,
    next: savedNext,
    attempt: true,
    button: 'plan',
    reason: true,
  }),
  'no-walkthrough': row({
    chip: 'Planning complete',
    status: 'Planning is complete, but this plan has no request-specific walkthrough.',
    caption: 'Plan walkthrough first',
    next: noWalkthrough,
    block: noWalkthrough,
    primary: 'always',
    attempt: true,
    button: 'author',
  }),
  unchecked: row({
    chip: 'Ready to check',
    status: 'Planning is complete. Check the walkthrough with JEV before accepting.',
    caption: 'Check walkthrough',
    next: 'Review the walkthrough, then check it before authorizing work.',
  }),
  findings: row({
    chip: 'Checked · needs correction',
    status: findingsText,
    caption: 'Correct walkthrough first',
    next: findingsText,
    gated: false,
    block: findingsText,
    primary: 'always',
  }),
  clean: row({
    chip: 'Checked · ready to accept',
    status: 'Planning is complete. JEV checked the walkthrough; Accept and build creates its work.',
    caption: 'Accept and build',
    next: 'App delivery is tracked separately from this plan.',
  }),
  unknown: row({
    chip: '',
    status: 'Planning reported a status this view does not recognize. Your request is saved; refresh status.',
    caption: CONTINUE,
    next: 'Refresh the planning status.',
    gated: false,
    primary: 'always',
  }),
};
const QUESTION = (question: string): Row =>
  row({
    chip: 'Needs your decision',
    status: attentionText(question),
    caption: 'Edit request',
    next: 'Review the planning question above and update your direction before continuing.',
    gated: false,
    primary: 'never',
    reason: true,
  });
export const rowOf = (phase: PlanPhase): Row =>
  phase.kind === 'question' ? QUESTION(phase.question) : PHASES[phase.kind];

// Whether a settled phase may start a new planning attempt (a successor
// draft, or planning itself for a recorded none).
export const canStartAttempt = (phase: PlanPhase) => rowOf(phase).attempt;

// planStage is the plan status family of a phase, for transition notices:
// the completed phases are one stage, as are both attention phases.
export function planStage(phase: PlanPhase) {
  switch (phase.kind) {
    case 'planning':
      return 'pending';
    case 'question':
    case 'stopped':
      return 'attention';
    case 'failed':
    case 'cancelled':
    case 'none':
    case 'unloaded':
    case 'unknown':
      return phase.kind;
    case 'no-walkthrough':
    case 'unchecked':
    case 'findings':
    case 'clean':
      return 'completed';
    default:
      return 'other';
  }
}

export type Readiness = 'no-tasks' | 'incomplete' | 'ready';
export interface PhaseCopy {
  chip: string;
  status: string;
  caption: string;
  next: string;
  block: string;
  action: 'revise' | 'edit' | 'continue';
  // Disabled by the phase alone; the view adds busy and blocked.
  disabled: boolean;
  button: Row['button'];
  reason: boolean;
}
// phaseCopy applies the unsaved-edit overlay (dirty) and the work-readiness
// gates to a phase's row.
export function phaseCopy(phase: PlanPhase, dirty: boolean, readiness: Readiness): PhaseCopy {
  const r = rowOf(phase);
  let next = r.next;
  if (r.gated)
    next =
      readiness === 'no-tasks'
        ? 'Explain the missing behavior in your desired outcome, then update the proposal.'
        : readiness === 'incomplete'
          ? 'Answer the questions and choose the ideas needed for this workflow before continuing.'
          : r.next;
  if (dirty) next = 'Save your changes with Update proposal before continuing.';
  const live = phase.kind === 'planning';
  return {
    chip: r.chip,
    status: r.status,
    caption: dirty ? 'Update proposal' : r.caption,
    next,
    block: r.block,
    action: dirty ? 'revise' : phase.kind === 'question' ? 'edit' : 'continue',
    disabled: live || (!dirty && (r.primary === 'always' || (r.primary === 'ready' && readiness !== 'ready'))),
    button: r.button,
    reason: r.reason,
  };
}
