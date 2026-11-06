import type {FeedCommunication, FeedEntry, FeedJudgment} from './feed-response.js';

// Parent links connect one logical thread. Main-feed placement is independent:
// starts, handoffs, outcomes and attention surface; routine replies stay inside.

import {roleKey, roleLabel} from './roles.js';
export {roleLabel} from './roles.js';

export type FeedState = 'needs_you' | 'problem' | 'waiting' | 'in_progress' | 'done' | 'decided' | 'info';
export type FeedLevel = 'post' | 'update' | 'reply';

export const feedStateWords: Readonly<Record<FeedState, string>> = {
  needs_you: 'Needs you',
  problem: 'Problem',
  waiting: 'Waiting',
  in_progress: 'In progress',
  done: 'Done',
  decided: 'Decided',
  info: 'For your information',
};

// What SodaOS did with a delivery judgment. Ready merges on its own, review
// again starts another review, everything else waits for the founder.
function deliveryState(judgment: FeedJudgment): FeedState {
  const effective = judgment.effective ?? '';
  if (effective === 'ready_to_merge') return 'done';
  if (effective === 'review_again') return 'in_progress';
  if (effective === '') return judgment.outcome ? 'decided' : 'info';
  return 'needs_you';
}

function communicationState(communication: FeedCommunication, entry: FeedEntry): FeedState {
  if (communication.disposition !== undefined) return 'decided';
  if (communication.kind !== 'ask' && (communication.severity === 'critical' || communication.severity === 'warning'))
    return 'problem';
  switch (communication.kind) {
    case 'ask':
      return communication.questionState === 'open' || communication.questionState === undefined
        ? 'needs_you'
        : 'decided';
    case 'blocked':
      return 'waiting';
    case 'stuck':
    case 'finish_incomplete':
      return 'problem';
    case 'flag':
      return communication.severity === 'info' ? 'info' : 'problem';
    case 'partial':
      return 'in_progress';
    case 'record_decision':
    case 'answer':
      return 'decided';
    case 'system':
      return statusState(entry);
    default:
      return 'info';
  }
}

// statusState reads the recorded status words of one record or event. An
// unknown status is information, never success.
function statusState(entry: FeedEntry): FeedState {
  const status = (entry.status ?? '').toLowerCase();
  if (/needs-human|clarify/.test(status)) return 'needs_you';
  if (/paused|pending|queued|waiting|settling/.test(status)) return 'waiting';
  if (
    /fail|error|refus|reject|invalid|blocked|stuck|unconfirmed|cancel|stopped|request-changes|exhaust|denied|conflict/.test(
      status
    )
  )
    return 'problem';
  if (status === 'run:succeeded') return 'info';
  if (/judgment|decid|declined|route:/.test(status)) return 'decided';
  if (/finished:succeeded|completed|merged|verified|applied|accepted|delivered|created|valid/.test(status))
    return 'done';
  if (/running|assigned|assess|progress|working|submitted|draft|:/.test(status)) return 'in_progress';
  return 'info';
}

// feedState gives one entry exactly one of the seven states. It reads the
// recorded facts only; an unknown status is information, never success.
export function feedState(entry: FeedEntry): FeedState {
  const payload = entry.payload;
  if (payload?.communication) return communicationState(payload.communication, entry);
  if (payload?.judgment?.kind === 'delivery') return deliveryState(payload.judgment);
  if (entry.activity === 'judgment' || payload?.judgment) return 'decided';
  if (entry.activity === 'merged' || entry.activity === 'completed') return 'done';
  if (entry.kind === 'post') return 'info';
  return statusState(entry);
}

// Posts keep their own position in the timeline. Parentage supplies context;
// it does not decide whether a post is hidden inside another post.
export interface FeedThreadItem {
  readonly entry: FeedEntry;
  readonly state: FeedState;
  readonly level: FeedLevel;
  readonly changes: readonly FeedEntry[];
}

export function whoOf(entry: FeedEntry): string {
  if (entry.author) return entry.author;
  return roleLabel(entry.actor && !/^\d+$/.test(entry.actor) ? entry.actor : undefined) ?? 'SodaOS';
}

function clip(text: string, limit = 72): string {
  const line = text.replace(/\s+/g, ' ').trim();
  return line.length > limit ? `${line.slice(0, limit - 1).trimEnd()}…` : line;
}

export function threadFeed(entries: readonly FeedEntry[], focused = false): FeedThreadItem[] {
  const byID = new Map(entries.map((entry) => [entry.id, entry]));
  return entries
    .filter((entry) => {
      if (focused) return true;
      const parentID = entry.parentId || entry.post?.parentId || entry.payload?.communication?.parentId;
      if (!parentID) return true;
      const state = feedState(entry);
      const communication = entry.payload?.communication;
      if (
        ['needs_you', 'problem', 'done', 'decided'].includes(state) ||
        ['blocked', 'stuck', 'finish_incomplete', 'artifact', 'learned'].includes(communication?.kind ?? '')
      )
        return true;
      const parent = byID.get(parentID);
      const parentActor = parent?.actor || entry.parentActor || parent?.author || entry.parentAuthor;
      const parentIssue = parent?.issue ?? entry.parentIssue;
      // The same role starting another issue still gets a separate feed post.
      if (entry.issue && entry.issue !== parentIssue) return true;
      if (entry.actor && parentActor && roleKey(entry.actor) !== roleKey(parentActor)) return true;
      // A missing parent snapshot must not hide work. The server includes the
      // parent's actor and issue even when the parent is outside this page.
      return !parent && !entry.parentAuthor;
    })
    .map((entry) => ({entry, state: feedState(entry), level: 'post', changes: []}));
}

export interface PostLine {
  readonly headline: string;
  readonly detail: string;
  readonly next: string;
  readonly workItem: string | undefined;
}

function percent(value: number | undefined): string {
  return value === undefined ? '' : ` (${Math.round(value * 100)}%)`;
}

const roleVerbs: Readonly<Record<string, [string, string]>> = {
  product_lead: ['planning', 'planned'],
  architect: ['investigating', 'planned'],
  builder: ['building', 'built'],
  inspector: ['reviewing', 'reviewed'],
  fixer: ['fixing', 'fixed'],
  strategist: ['reassessing', 'reassessed'],
  test_pilot: ['testing', 'tested'],
  factory: ['delivering', 'delivered'],
  system: ['delivering', 'delivered'],
};

function judgmentHeadline(judgment: FeedJudgment, entry: FeedEntry): string {
  const p = percent(judgment.probability);
  if (judgment.kind === 'delivery') {
    const pr = entry.pr ? `pull request #${entry.pr}` : 'the pull request';
    switch (judgment.effective) {
      case 'ready_to_merge':
        return `JEV said ${pr} is ready to merge${p}, so SodaOS merges it`;
      case 'review_again':
        return `JEV said ${pr} needs another review${p}`;
      case 'review_allowance_exhausted':
        return `JEV says ${pr} needs another review, but the reviews are used up`;
      case 'judgment_attention':
        return `JEV wasn't sure enough about ${pr}${p}, so it waits for you`;
      default:
        return `JEV gave no usable answer on ${pr}, so it waits for you`;
    }
  }
  const top = (judgment.candidates ?? []).find((candidate) => candidate.id === judgment.selected);
  const name = top?.issue
    ? `issue #${top.issue}`
    : top?.objective
      ? clip(top.objective, 60)
      : (judgment.selected ?? 'one option');
  return `JEV put ${name} first${p}, so it runs next`;
}

// describePostLine writes the post's one-sentence headline from the
// recorded facts: who did what, and how it turned out.
export function describePostLine(entry: FeedEntry, state: FeedState): PostLine {
  const who = whoOf(entry);
  const target = entry.pr ? `pull request #${entry.pr}` : entry.issue ? `issue #${entry.issue}` : 'the project';
  const workItem = entry.pr ? `Pull request #${entry.pr}` : entry.issue ? `Issue #${entry.issue}` : undefined;
  const communication = entry.payload?.communication;
  const judgment = entry.payload?.judgment;
  const fallback = entry.kind === 'post' ? 'Post' : `${who} recorded an update`;
  let headline = fallback;
  // A recorded summary stays in the post's own section, so it is not repeated
  // here; the detail line carries a rule or consequence only.
  let detail = '';
  if (communication) {
    // The communication's own summary follows in its section.
    headline = clip(communication.title || communication.summary || fallback, 110);
    detail = '';
  } else if (judgment) {
    headline = judgmentHeadline(judgment, entry);
    detail =
      judgment.kind === 'delivery'
        ? "SodaOS merges on its own when JEV's answer reaches 80% and JEV is at least 80% sure. Otherwise it waits for you."
        : 'SodaOS runs the top pick next. The rest wait their turn.';
  } else if (entry.activity === 'merged') {
    headline = `SodaOS merged ${entry.pr ? `pull request #${entry.pr}` : 'the pull request'}${entry.payload?.merge?.originalClosedUnix && entry.issue ? ` and closed issue #${entry.issue}` : ''}`;
  } else if (entry.activity === 'completed') {
    headline = `SodaOS completed ${entry.issue ? `issue #${entry.issue}` : 'the issue'}`;
  } else if (entry.kind === 'record') {
    const verbs = roleVerbs[roleKey(entry.actor)];
    const reviewer = roleKey(entry.actor) === 'inspector';
    const status = entry.status ?? '';
    const verdict = /verdict:([a-z_-]+)/.exec(status)?.[1] ?? entry.payload?.review?.verdict ?? '';
    if (reviewer && /request|changes|fail/.test(verdict)) headline = `${who} asked for changes on ${target}`;
    else if (reviewer && /approve|pass|clear/.test(verdict)) headline = `${who} approved ${target}`;
    else if (status === 'dispatch:assigned') headline = `${who} started on ${target}`;
    else if (status === 'run:succeeded') headline = `${who}'s run on ${target} ended`;
    else if (verbs && state === 'in_progress') headline = `${who} is ${verbs[0]} ${target}`;
    else if (verbs && state === 'done') headline = `${who} ${verbs[1]} ${target}`;
    else if (verbs && state === 'problem') headline = `${who} hit a problem ${verbs[0]} ${target}`;
    else if (verbs && state === 'waiting') headline = `${who} is waiting to start on ${target}`;
  }
  const nextFor: Readonly<Record<FeedState, string>> = {
    needs_you: 'Waits for you',
    problem: 'Needs attention before dependent work continues',
    waiting: 'Starts by itself when what it waits for is ready',
    in_progress: 'This post updates as the work moves',
    done: 'Nothing needed from you',
    decided: 'Nothing needed from you',
    info: 'Nothing needed from you',
  };
  return {headline, detail, next: nextFor[state], workItem};
}
