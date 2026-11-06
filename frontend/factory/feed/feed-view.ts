import {html, svg} from 'lit';
import {live} from 'lit/directives/live.js';
import type {TemplateResult} from 'lit';
import type {FeedPostDetail, PostPipelineState, FeedEntry, FeedJudgment} from './feed-response.js';
import {postAttemptsExhausted, feedEntryKey} from './feed-response.js';
import {feedRolePortraits, roleLabel} from './roles.js';
import {describePostLine, feedStateWords} from './feed-thread.js';
import type {FeedState, FeedThreadItem} from './feed-thread.js';
import {renderFeedCommunication} from './feed-communication.js';
import {questionPrompt} from './feed-actions.js';
import type {FeedCardLinks} from './feed-communication.js';
import type {FactoryRepositoryLinks} from '../app/navigation.js';
import {renderRecordAction} from './feed-record-actions.js';
import {renderJevChoice} from '../shared/jev-answer.js';
import type {FeedArtifactContext} from './feed-artifact.js';

// One render path for every Feed entry. Each entry becomes one card of one
// of four types — a work record, a worker message, a Factory event or a
// human post — inside the same shell: byline, state pill and headline,
// the card's own content, the Next line, its folded steps and the reply
// drawer. Models carry recorded facts only; nothing here does I/O.

export interface FeedRepositoryRef {
  readonly id: string;
  readonly path: string | undefined;
  readonly label: string;
  /** The repository's SodaOS places, when its owner/repo name is known. */
  readonly places?: FactoryRepositoryLinks;
  /** Shown inside this repository's own feed. */
  readonly current?: boolean;
}

export type FeedCardType = 'work' | 'message' | 'event' | 'post';

function shortFeedID(id: string): string {
  return `${id.slice(0, 12)}${id.length > 12 ? '…' : ''}`;
}

function formatStamp(at: number | undefined): string {
  if (at === undefined || !Number.isSafeInteger(at) || at <= 0) return 'time unrecorded';
  return new Date(at * 1000).toLocaleString(undefined, {
    month: 'short',
    day: 'numeric',
    hour: '2-digit',
    minute: '2-digit',
  });
}

function clockText(at: number | undefined): string {
  if (at === undefined || !Number.isSafeInteger(at) || at <= 0) return '';
  return new Date(at * 1000).toLocaleTimeString(undefined, {hour: '2-digit', minute: '2-digit'});
}

export function cardType(entry: FeedEntry): FeedCardType {
  const communication = entry.payload?.communication;
  if (communication) return communication.componentId ? 'event' : 'message';
  return entry.kind === 'post' ? 'post' : 'work';
}

export type PostOutcomeTone = 'progress' | 'settled' | 'failed' | 'blocked';

export interface PostOutcomeModel {
  readonly messageKind: 'post' | 'reply';
  readonly issueMakerLink?: string | undefined;
  readonly postId: string;
  readonly text: string;
  readonly tone: PostOutcomeTone;
  readonly issueLink: string | undefined;
  readonly issue: number;
  readonly factoryStatusLink?: string | undefined;
  // Still advancing on the server: the feed refresh picks up its next state.
  readonly pending: boolean;
  readonly phase?: PostPhase;
  readonly canRetry: boolean;
  readonly needsClarification: boolean;
  readonly revision: number;
  readonly detail: string | undefined;
  readonly productChange: PostPipelineState['productChange'];
  readonly productRepository: string;
  readonly busy: boolean;
  readonly routing: PostPipelineState['routing'];
  readonly authoring?: PostPipelineState['authoring'];
  readonly requestLink?: string | undefined;
  readonly setupLink?: string | undefined;
}

// JEV's reply is one explicit state machine: the stored pipeline states plus
// any stage the server cannot run (stalled) select exactly one phase, and
// each phase owns its words, its tone and whether JEV is actually working.
export type PostPhase =
  | 'issue_maker'
  | 'stalled_jev'
  | 'stalled_context'
  | 'reading'
  | 'assess_failed'
  | 'exhausted'
  | 'discussion'
  | 'clarify'
  | 'feedback_finding'
  | 'feedback_delivering'
  | 'feedback_added'
  | 'stalled_product_lead'
  | 'drafting'
  | 'draft_queued'
  | 'draft_waiting'
  | 'draft_settling'
  | 'draft_failed'
  | 'draft_unknown'
  | 'draft_invalid'
  | 'stalled_forge'
  | 'creating'
  | 'create_failed'
  | 'create_blocked'
  | 'created';

export function postPhase(state: PostPipelineState): PostPhase {
  if (state.issueMaker) return 'issue_maker';
  if (postAttemptsExhausted(state)) return 'exhausted';
  if (state.judgment === 'pending')
    return state.stalled === 'jev' ? 'stalled_jev' : state.stalled === 'context' ? 'stalled_context' : 'reading';
  if (state.judgment === 'failed') return 'assess_failed';
  switch (state.route) {
    case 'discussion':
      return 'discussion';
    case 'clarify':
      return 'clarify';
    case 'existing_work':
      if (state.effect === 'applied' || state.effect === 'verified') return 'feedback_added';
      return (state.existingIssue || state.issue || 0) > 0 ? 'feedback_delivering' : 'feedback_finding';
  }
  if (state.draft === 'pending') {
    if (state.stalled === 'product_lead') return 'stalled_product_lead';
    switch (state.authoring?.state) {
      case 'waiting':
        return 'draft_waiting';
      case 'running':
        return 'drafting';
      case 'settling':
        return 'draft_settling';
      case 'failed':
        return 'draft_failed';
      case 'unknown':
        return 'draft_unknown';
      default:
        return 'draft_queued';
    }
  }
  if (state.draft === 'invalid') return 'draft_invalid';
  if (state.effect === 'pending') return state.stalled === 'forge' ? 'stalled_forge' : 'creating';
  if (state.effect === 'failed') return 'create_failed';
  if (state.effect === 'blocked') return 'create_blocked';
  return 'created';
}

const postPhases: Readonly<
  Record<PostPhase, {text: (issue: number) => string; tone: PostOutcomeTone; working: boolean}>
> = {
  issue_maker: {
    text: () => 'This is new work. I handed it to Issue Maker; follow its post for progress and each created issue.',
    tone: 'settled',
    working: false,
  },
  stalled_jev: {
    text: () => 'I’m not connected on this server yet, so I can’t read this. An administrator needs to connect JEV.',
    tone: 'blocked',
    working: false,
  },
  stalled_context: {
    text: () =>
      'I need to refresh this request’s issues using your current repository access. Retry the request to continue.',
    tone: 'blocked',
    working: false,
  },
  reading: {text: () => 'I’m reading your request and working out what it needs…', tone: 'progress', working: true},
  assess_failed: {
    text: () => 'I couldn’t make sense of this one. Edit your request or retry.',
    tone: 'failed',
    working: false,
  },
  exhausted: {
    text: () => 'I stopped after several tries. Edit your request or retry and I’ll pick it up again.',
    tone: 'failed',
    working: false,
  },
  discussion: {
    text: () => 'This reads as a discussion, so I’ve left it here for people to answer.',
    tone: 'settled',
    working: false,
  },
  clarify: {text: () => 'I need a little more from you before I can route this.', tone: 'progress', working: false},
  feedback_finding: {
    text: () => 'This is feedback on existing work. I’m finding the issue it belongs to…',
    tone: 'progress',
    working: true,
  },
  feedback_delivering: {
    text: (issue) => `This belongs to issue #${issue}. I’m adding your feedback there now…`,
    tone: 'progress',
    working: true,
  },
  feedback_added: {text: (issue) => `I added this as feedback on issue #${issue}.`, tone: 'settled', working: false},
  stalled_product_lead: {
    text: () => 'This is new work, but no Product Lead is set up on this server to write the issue.',
    tone: 'blocked',
    working: false,
  },
  drafting: {
    text: () => 'The Product Lead run has started to prepare this new work as an issue. Waiting for its result.',
    tone: 'progress',
    working: true,
  },
  draft_queued: {
    text: () => 'JEV identified new work. The Product Lead is queued; no run has started yet.',
    tone: 'progress',
    working: false,
  },
  draft_waiting: {
    text: () => 'The Product Lead is waiting. It is not working on the issue right now.',
    tone: 'blocked',
    working: false,
  },
  draft_settling: {
    text: () => 'The Product Lead run ended. Its result is being checked before creating the issue.',
    tone: 'progress',
    working: false,
  },
  draft_failed: {
    text: () => 'The Product Lead could not prepare the issue. The server will retry within this request’s limit.',
    tone: 'failed',
    working: false,
  },
  draft_unknown: {
    text: () => 'The Product Lead’s current status is unavailable. It will be checked again.',
    tone: 'blocked',
    working: false,
  },
  draft_invalid: {
    text: () => 'I couldn’t turn this into a valid issue, so nothing was created.',
    tone: 'failed',
    working: false,
  },
  stalled_forge: {
    text: () => 'The issue is written, but this server can’t create issues in the repository yet.',
    tone: 'blocked',
    working: false,
  },
  creating: {text: () => 'The issue is written. I’m creating it now…', tone: 'progress', working: true},
  create_failed: {
    text: () => 'Creating the issue failed. Retrying won’t make a duplicate.',
    tone: 'failed',
    working: false,
  },
  create_blocked: {
    text: () =>
      'I stopped: the repository changed or access was lost before I could create the issue. Your request is kept.',
    tone: 'failed',
    working: false,
  },
  created: {text: (issue) => `I created issue #${issue} for this.`, tone: 'settled', working: false},
};

// Proposed product changes keep their own review states.
function productChangeOutcome(change: NonNullable<PostPipelineState['productChange']>): [string, PostOutcomeTone] {
  switch (change.state) {
    case 'review_required':
      return [
        change.canReview
          ? 'Review this proposed change before updating the accepted direction.'
          : 'Waiting for the request owner to review this proposed change.',
        'progress',
      ];
    case 'draft':
      return ['The change is saved in the build request for review. Accept its direction before building.', 'progress'];
    case 'accepted':
      return [
        'The changed direction is accepted. Follow its build and verification before applying a version.',
        'settled',
      ];
    case 'superseded':
      return [
        'This proposal refers to an earlier direction. Refresh the request before proposing another change.',
        'failed',
      ];
    default:
      return ['The proposed change detail is currently unavailable. It reloads on its own.', 'progress'];
  }
}

function validRouting(value: PostPipelineState['routing']): PostPipelineState['routing'] {
  if (!value || typeof value.selected !== 'string' || !Array.isArray(value.options)) return undefined;
  const options = value.options.filter(
    (option) => option && typeof option.key === 'string' && Number.isFinite(option.probability)
  );
  return options.length ? {...value, options} : undefined;
}

// describePostOutcome projects one stored post request to its single
// visible outcome line plus the controls the author or owner may use.
export function describePostOutcome(
  state: PostPipelineState,
  repository: FeedRepositoryRef,
  busy = false,
  messageKind: 'post' | 'reply' = 'post'
): PostOutcomeModel {
  const issue = (state.route === 'existing_work' ? state.existingIssue || state.issue : state.issue) ?? 0;
  const phase = state.productChange ? undefined : postPhase(state);
  const [text, tone] = state.productChange
    ? productChangeOutcome(state.productChange)
    : [postPhases[phase!].text(issue), postPhases[phase!].tone];
  return {
    messageKind,
    postId: state.postId,
    issueMakerLink: state.issueMaker?.post ? repository.places?.feed(state.issueMaker.post) : undefined,
    text,
    tone,
    issueLink: issue > 0 && repository.path !== undefined ? repository.places?.issue(issue) : undefined,
    issue,
    factoryStatusLink: issue > 0 && repository.path !== undefined ? repository.places?.factoryStatus(issue) : undefined,
    // Dots only while someone is actually working on it.
    pending: phase ? postPhases[phase].working : state.productChange?.state === 'unavailable',
    ...(phase ? {phase} : {}),
    canRetry: state.canRetry === true,
    needsClarification: state.route === 'clarify',
    revision: state.revision,
    detail: state.issueMaker ? undefined : state.authoring?.detail || state.detail || undefined,
    productChange: state.productChange,
    productRepository: repository.id,
    busy,
    routing: validRouting(state.routing),
    authoring: state.authoring,
    requestLink: state.authoring?.intent ? repository.places?.request(state.authoring.intent) : undefined,
    setupLink:
      state.authoring?.state === 'waiting' && state.authoring.reason === 'preparation_not_ready'
        ? repository.places?.settings?.('factory')
        : undefined,
  };
}

const productActions: Readonly<Record<string, string>> = {
  review_required: 'Review proposed change',
  draft: 'Continue change review',
  accepted: 'Open accepted change',
};

// JEV's reply under a request: her portrait, what she is doing right now
// (with a working indicator while the feed's own refresh catches up), and
// the actions the author may take.
export function renderOutcome(outcome: PostOutcomeModel, assetBase = ''): TemplateResult {
  const change = outcome.productChange;
  const productAction = change?.canReview ? productActions[change.state] : undefined;
  const labels: Partial<Record<PostPhase, string>> = {
    drafting: 'Run started',
    draft_queued: 'Queued',
    draft_waiting: 'Waiting',
    draft_settling: 'Checking result',
    draft_failed: 'Retrying',
    draft_unknown: 'Status unavailable',
    draft_invalid: 'Needs attention',
    stalled_product_lead: 'Not connected',
  };
  const status = outcome.phase ? labels[outcome.phase] : undefined;
  const authoring = status !== undefined;
  const actor = authoring ? 'Factory' : 'JEV';
  const portrait = feedRolePortraits[actor];
  return html`<div class="soda-feed-outcome" data-tone=${outcome.tone} data-phase=${outcome.phase ?? ''}>
    <span class="soda-feed-outcome-avatar" aria-hidden="true"
      >${portrait ? html`<img src=${assetBase + portrait} alt="" width="24" height="24" loading="lazy" />` : 'J'}</span
    >
    <div class="soda-feed-outcome-body">
      <p class="soda-feed-outcome-who">
        <strong>${actor}</strong>${status ? html`<span class="soda-feed-authoring-label">${status}</span>` : ''}
        ${outcome.pending ? html`<span class="soda-feed-working" aria-label="working"><i></i><i></i><i></i></span>` : ''}
      </p>
      <p role=${outcome.tone === 'failed' ? 'alert' : 'status'}>
        ${outcome.text} ${outcome.issueLink ? html`<a href=${outcome.issueLink}>Open issue #${outcome.issue}</a>` : ''}
        ${outcome.issueMakerLink ? html`<a href=${outcome.issueMakerLink}>Follow Issue Maker</a>` : ''}
        ${outcome.factoryStatusLink ? html`<a href=${outcome.factoryStatusLink}>Factory status</a>` : ''}
      </p>
      ${
        outcome.routing
          ? renderJevChoice(
              outcome.routing.options.map((option) => ({
                key: option.key,
                label: option.label || option.key,
                probability: option.probability,
              })),
              outcome.routing.selected,
              Number.isFinite(outcome.routing.confidence) ? outcome.routing.confidence : undefined
            )
          : ''
      }
      ${outcome.detail && !outcome.phase?.startsWith('stalled') ? html`<p>${outcome.detail}</p>` : ''}
      ${
        authoring && !outcome.phase?.startsWith('stalled') && outcome.authoring?.checkedUnix
          ? html`<p class="soda-feed-authoring-meta">
              ${outcome.authoring.sinceUnix ? html`In this state since <time datetime=${new Date(outcome.authoring.sinceUnix * 1000).toISOString()}>${formatStamp(outcome.authoring.sinceUnix)}</time>. ` : ''}
              Last checked
              <time datetime=${new Date(outcome.authoring.checkedUnix * 1000).toISOString()}
                >${new Date(outcome.authoring.checkedUnix * 1000).toLocaleTimeString(undefined, {hour: '2-digit', minute: '2-digit', second: '2-digit'})}</time
              >.
            </p>`
          : ''
      }
      ${
        outcome.requestLink || outcome.setupLink
          ? html`<p class="soda-feed-authoring-links">
              ${outcome.requestLink ? html`<a href=${outcome.requestLink}>View request</a>` : ''}
              ${outcome.setupLink ? html`<a href=${outcome.setupLink}>Review Project setup</a>` : ''}
            </p>`
          : ''
      }
      ${
        productAction
          ? html`<button
              type="button"
              class="ui button"
              data-post-product-change=${outcome.postId}
              data-product-revision=${outcome.revision}
              data-product-repository=${outcome.productRepository}
              ?disabled=${outcome.busy}
            >
              ${productAction}
            </button>`
          : ''
      }
      ${
        outcome.canRetry
          ? html`<button
              type="button"
              data-post-revise=${outcome.postId}
              data-post-repository=${outcome.productRepository}
              data-message-kind=${outcome.messageKind}
            >
              ${outcome.needsClarification ? 'Answer clarification' : `Edit ${outcome.messageKind} and retry`}
            </button>`
          : ''
      }
    </div>
  </div>`;
}

export interface ComposerModel {
  readonly key: string;
  readonly draft: string;
  readonly submitAvailable: boolean;
  readonly label?: string;
  readonly submitLabel?: string;
  readonly compact?: boolean;
  readonly discussionLink?: string | undefined;
  readonly discussionFactoryStatus?: string | undefined;
  /** One line until focused (the repository post box). */
  readonly collapsed?: boolean;
}

// Ctrl/Cmd+Enter posts from any composer.
function submitShortcut(event: KeyboardEvent) {
  if (event.key !== 'Enter' || !(event.ctrlKey || event.metaKey) || !(event.target instanceof HTMLElement)) return;
  const button = event.target
    .closest('section[aria-label=Discussion]')
    ?.querySelector<HTMLButtonElement>('button[data-post-submit]');
  if (!button || button.disabled) return;
  event.preventDefault();
  button.click();
}

// The repository request box: one card holding the text and its Post action.
function renderRequestComposer(model: ComposerModel): TemplateResult {
  const open = !model.collapsed;
  return html`<section aria-label="Discussion" class="soda-feed-composer" data-open=${open ? 'true' : 'false'}>
    <label
      ><span class="soda-visually-hidden">${model.label ?? 'New post'}</span>
      <textarea
        name="reply"
        data-feed-draft=${model.key}
        rows=${open ? '3' : '1'}
        maxlength="4000"
        placeholder="Ask for a change, report a problem, or ask a question…"
        .value=${live(model.draft)}
        @keydown=${submitShortcut}
      ></textarea>
    </label>
    <div class="soda-feed-composer-foot">
      <span class="soda-feed-compose-hint">JEV turns it into new work, feedback on an issue, or a discussion.</span>
      <button
        type="button"
        class="ui primary button"
        data-post-submit=${model.key}
        .disabled=${live(!model.submitAvailable)}
        title="Post (Ctrl+Enter)"
      >
        ${model.submitLabel ?? 'Post'}
      </button>
    </div>
  </section>`;
}

export function renderComposer(model: ComposerModel): TemplateResult {
  if (model.label === 'New post') return renderRequestComposer(model);
  return html`<section aria-label="Discussion">
    <label
      >${model.compact ? html`<span class="soda-visually-hidden">${model.label ?? 'Reply draft'}</span>` : (model.label ?? 'Reply draft')}
      <textarea
        name="reply"
        data-feed-draft=${model.key}
        rows=${model.compact || model.collapsed ? '1' : '3'}
        maxlength="4000"
        placeholder="Write a reply…"
        .value=${live(model.draft)}
        @keydown=${submitShortcut}
      ></textarea>
    </label>
    ${
      model.collapsed
        ? ''
        : html`<p class="soda-feed-compose-actions">
            <button
              type="button"
              class="ui primary button"
              data-post-submit=${model.key}
              .disabled=${live(!model.submitAvailable)}
              title="Reply (Ctrl+Enter)"
            >
              ${model.submitLabel ?? 'Reply'}
            </button>
            ${model.discussionLink ? html`<a href=${model.discussionLink}>Open issue discussion</a>` : ''}
            ${model.discussionFactoryStatus ? html`<a href=${model.discussionFactoryStatus}>Factory status</a>` : ''}
          </p>`
    }
  </section>`;
}

export interface RepliesModel {
  readonly key: string;
  readonly open: boolean;
  readonly countText: string;
  readonly faces: readonly string[];
}

export function describeReplies(
  key: string,
  open: boolean,
  thread: {items: readonly {authorName: string}[]; next: number} | undefined
): RepliesModel {
  const count = thread?.items.length;
  return {
    key,
    open,
    countText:
      count === undefined
        ? 'Replies'
        : count === 1
          ? '1 reply'
          : `${count}${(thread?.next ?? 0) > 0 ? '+' : ''} replies`,
    faces: (thread?.items ?? []).slice(0, 3).map((item) => (item.authorName || '?').slice(0, 1).toUpperCase()),
  };
}

export interface FeedPostModel {
  readonly parent?: {author: string; href: string};
  readonly repositoryPath?: string | undefined;
  readonly entry: FeedEntry;
  readonly key: string;
  readonly type: FeedCardType;
  readonly state: FeedState;
  readonly name: string;
  readonly role: string;
  readonly avatarFile: string | undefined;
  readonly project: string;
  readonly timeText: string;
  readonly pill: string;
  readonly headline: string;
  readonly detail: string;
  readonly next: string;
  readonly workItem: string | undefined;
  readonly links: readonly {label: string; href: string}[];
  readonly post: FeedPostDetail | undefined;
  readonly outcome: PostOutcomeModel | undefined;
  /** An ordinary question: asked once, answered in the box under it. */
  readonly question: boolean;
  readonly composer: ComposerModel;
  readonly replies: RepliesModel;
  readonly thread: Pick<FeedThreadItem, 'changes' | 'level'> | undefined;
}

function sourceLinks(entry: FeedEntry, repository: FeedRepositoryRef): {label: string; href: string}[] {
  if (repository.path === undefined) return [];
  const links = repository.current ? [] : [{label: `Open ${repository.label}`, href: repository.path}];
  const parent = entry.post?.parentId;
  if (parent && repository.places) links.push({label: 'Parent post', href: repository.places.feed(parent)});
  const issue = entry.issue ? repository.places?.issue(entry.issue) : undefined;
  if (issue) {
    links.push({label: `Open issue #${entry.issue}`, href: issue});
    if (repository.places && entry.issue)
      links.push({label: 'Factory status', href: repository.places.factoryStatus(entry.issue)});
  }
  const pull = entry.pr ? repository.places?.pull(entry.pr) : undefined;
  if (pull) links.push({label: `Open pull request #${entry.pr}`, href: pull});
  return links;
}

// describeFeedPost composes one entry's card from its recorded facts.
export function describeFeedPost(
  entry: FeedEntry,
  repository: FeedRepositoryRef,
  thread: FeedThreadItem,
  draft: string,
  replies: RepliesModel,
  busy: readonly string[]
): FeedPostModel {
  const type = cardType(entry);
  const recorded = entry.kind === 'record';
  const name = entry.author ?? entry.post?.authorName ?? (recorded ? 'Recorded work' : 'Discussion post');
  const role =
    roleLabel(entry.actor && !/^\d+$/.test(entry.actor) ? entry.actor : undefined) ??
    (recorded ? 'Factory' : entry.post?.parentId ? 'Reply' : 'Post');
  const state = thread.state;
  const line = describePostLine(entry, state);
  const key = feedEntryKey(entry);
  const pipeline = type === 'post' ? entry.post?.pipeline : undefined;
  const ask = entry.payload?.communication;
  // Real urgency (a critical or warning question) keeps the attention card.
  const question = ask?.kind === 'ask' && ask.severity !== 'critical' && ask.severity !== 'warning';
  return {
    entry,
    ...(repository.places && (entry.parentId || entry.post?.parentId || entry.payload?.communication?.parentId)
      ? {
          parent: {
            author: entry.parentAuthor || 'an earlier post',
            href: repository.places.feed(
              entry.parentId || entry.post?.parentId || entry.payload?.communication?.parentId
            ),
          },
        }
      : {}),
    repositoryPath: repository.path,
    key,
    type,
    state,
    name,
    role,
    avatarFile: feedRolePortraits[role],
    // Inside a repository's own feed its name is implied.
    project: repository.current ? '' : (entry.project ?? repository.label),
    timeText: entry.at ? formatStamp(entry.at) : `recorded order #${entry.seq}, newest first`,
    pill: type === 'post' ? (entry.post?.parentId ? 'Reply' : 'Post') : feedStateWords[state],
    headline: line.headline,
    detail: line.detail,
    next: state === 'in_progress' && thread.level !== 'update' ? 'Nothing needed from you' : line.next,
    workItem: line.workItem,
    links: sourceLinks(entry, repository),
    post: type === 'post' ? entry.post : undefined,
    outcome: pipeline
      ? describePostOutcome(
          pipeline,
          repository,
          busy.includes(pipeline.postId),
          entry.post?.parentId ? 'reply' : 'post'
        )
      : undefined,
    composer: {
      key,
      draft,
      compact: !question,
      ...(question ? {label: 'Your reply'} : {}),
      submitAvailable: draft.trim() !== '' && !busy.includes(key),
      discussionLink: repository.path === undefined || !entry.issue ? undefined : repository.places?.issue(entry.issue),
      discussionFactoryStatus:
        repository.path === undefined || !entry.issue ? undefined : repository.places?.factoryStatus(entry.issue),
    },
    question,
    replies,
    thread: {
      changes: thread.changes,
      level: thread.level,
    },
  };
}

// Small state and post icons, drawn so each reads without colour.
const stateIcons: Readonly<Record<FeedState, TemplateResult>> = {
  needs_you: svg`<circle cx="7" cy="7" r="4.2" fill="currentColor" stroke="none"/>`,
  problem: svg`<path d="M7 1.6 12.9 12.2H1.1z"/><path d="M7 5.4v3.1M7 10.3v.05"/>`,
  waiting: svg`<circle cx="7" cy="7" r="5.6"/><path d="M7 3.9V7l2.1 1.5"/>`,
  in_progress: svg`<circle cx="7" cy="7" r="5.6"/><path d="M7 1.4a5.6 5.6 0 0 1 0 11.2z" fill="currentColor" stroke="none"/>`,
  done: svg`<path d="M2.4 7.4 5.6 10.5 11.6 3.6"/>`,
  decided: svg`<path d="M7 1.4 12.6 7 7 12.6 1.4 7z" fill="currentColor" stroke="none"/>`,
  info: svg`<circle cx="7" cy="7" r="5"/>`,
};
function icon(path: TemplateResult): TemplateResult {
  return html`<svg
    class="soda-feed-ic"
    viewBox="0 0 14 14"
    aria-hidden="true"
    fill="none"
    stroke="currentColor"
    stroke-width="1.6"
    stroke-linecap="round"
    stroke-linejoin="round"
  >
    ${path}
  </svg>`;
}

// JEV's answer as bars: the share for each option, a notch at the 80% SodaOS
// needs to act on its own, and how sure JEV was.
function renderJudgment(judgment: FeedJudgment): TemplateResult {
  const names = new Map(
    (judgment.candidates ?? []).map((candidate) => [
      candidate.id,
      candidate.issue
        ? `Issue #${candidate.issue}${candidate.objective ? ` · ${candidate.objective}` : ''}`
        : (candidate.objective ?? candidate.id),
    ])
  );
  const label = (option: string) => {
    const name = names.get(option) ?? option.replaceAll('_', ' ');
    return name.charAt(0).toUpperCase() + name.slice(1);
  };
  const rows = Object.entries(judgment.probabilities ?? {})
    .sort((a, b) => b[1] - a[1])
    .slice(0, 4);
  const notch = judgment.kind === 'delivery';
  const sure = judgment.confidence;
  return html`<div class="soda-feed-jev">
    ${rows.map(([option, weight], index) => {
      const picked = option === judgment.selected;
      return html`<div class="soda-feed-jev-opt" data-top=${index === 0} data-picked=${picked}>
        <span class="soda-feed-jev-label">${picked ? icon(stateIcons.done) : ''}${label(option)}</span
        ><span class="soda-feed-jev-pc">${Math.round(weight * 100)}%</span>
        <span class="soda-feed-jev-track"
          ><b style=${`width:${Math.round(weight * 100)}%`}></b>${
            notch && index === 0 ? html`<i class="soda-feed-jev-notch" style="left:calc(80% - 1px)"></i>` : ''
          }</span
        >
      </div>`;
    })}
    ${
      notch
        ? html`<p class="soda-feed-jev-key">
            <span class="soda-feed-jev-kn"><i></i></span>SodaOS acts on its own from 80%
          </p>`
        : ''
    }
    ${
      sure !== undefined
        ? html`<p class="soda-feed-jev-sure">
            JEV is <strong>${Math.round(sure * 100)}% sure</strong> of this
            answer${notch ? (sure >= 0.8 ? ', which clears the 80% SodaOS needs' : ', under the 80% SodaOS needs') : ''}
          </p>`
        : ''
    }
  </div>`;
}

function renderPostBody(post: FeedPostDetail | undefined): TemplateResult {
  const text = !post
    ? 'Discussion text unavailable; refresh the feed to load it.'
    : post.removed
      ? 'That discussion post was removed. Its pipeline record is kept where stored.'
      : post.body || 'No post text recorded.';
  return html`<section aria-label="Discussion text">
    <p class="soda-feed-body-text">${text}${post?.edited ? ' (edited)' : ''}</p>
  </section>`;
}

// The card's own content, chosen by card type.
function renderCardContent(
  model: FeedPostModel,
  artifacts: FeedArtifactContext | undefined,
  links: FeedCardLinks,
  assetBase: string
): TemplateResult | string {
  const entry = model.entry;
  if (model.type === 'post')
    return html`${renderPostBody(model.post)}${model.outcome ? renderOutcome(model.outcome, assetBase) : ''}`;
  const communication = entry.payload?.communication;
  if (communication) return renderFeedCommunication(communication, model.key, artifacts, links);
  const judgment = entry.payload?.judgment;
  const typed = renderRecordAction(entry.payload);
  return html`${judgment ? renderJudgment(judgment) : ''}${
    typed ?? (entry.summary ? html`<p class="soda-feed-component-summary">${entry.summary}</p>` : '')
  }${entry.ref ? html`<p class="soda-feed-block-note">Candidate <code title=${entry.ref}>${shortFeedID(entry.ref)}</code></p>` : ''}`;
}

function renderByline(model: FeedPostModel, assetBase: string): TemplateResult {
  const changed = model.thread?.changes.length ? 'updated ' : '';
  return html`<div class="soda-feed-header">
    <span class="soda-feed-avatar" aria-hidden="true"
      >${
        model.avatarFile
          ? html`<img
              src=${assetBase + model.avatarFile}
              alt=""
              width="28"
              height="28"
              loading="lazy"
              decoding="async"
            />`
          : model.name.slice(0, 1).toUpperCase()
      }</span
    >
    <p>
      <strong>${model.name}</strong
      >${model.role !== model.name ? html`<span class="soda-feed-by-role"> · ${model.role}</span>` : ''}
      <span class="soda-feed-by-meta"
        >${(model.workItem ?? model.project) ? html`${model.workItem ?? model.project}<i>·</i>` : ''}${changed}${model.timeText}</span
      >
    </p>
  </div>`;
}

function renderChanges(model: FeedPostModel): TemplateResult | string {
  const changes = model.thread?.changes ?? [];
  if (changes.length === 0) return '';
  return html`<details class="soda-feed-changes">
    <summary>Show changes · ${changes.length}</summary>
    <ol>
      ${changes.map(
        (entry) =>
          html`<li>
            <time>${clockText(entry.at)}</time
            ><span>${entry.payload?.communication?.title ?? entry.summary ?? entry.status ?? 'Earlier state'}</span>
          </li>`
      )}
    </ol>
  </details>`;
}

// renderFeedPost renders one card: the byline above it, then the state pill
// and headline, the card's content, the Next line and folded changes. Small
// steps follow as one-line replies, and the reply drawer stays under them.
export function renderFeedPost(
  model: FeedPostModel,
  replies: TemplateResult | string = '',
  artifacts?: FeedArtifactContext,
  assetBase = '',
  links: FeedCardLinks = {}
): TemplateResult {
  const post = model.type === 'post';
  // An ordinary question is a conversation, not an alarm: no pill, headline
  // or Next line repeating it, and its reply box is always open under it.
  const question = model.question && model.entry.payload?.communication;
  return html`<article
    class="soda-feed-post"
    id=${'feed-' + model.key.replace(':', '-')}
    data-card=${model.type}
    data-question=${question ? 'true' : 'false'}
    data-state=${model.state}
    data-level=${model.thread?.level ?? 'post'}
    data-role=${model.role.toLowerCase().replace(/[\s-]+/g, '_')}
  >
    ${model.parent ? html`<p class="soda-feed-parent">Replying to <a href=${model.parent.href}>${model.parent.author}</a></p>` : ''}
    ${renderByline(model, assetBase)}
    <div class="soda-feed-stack">
      <div class="soda-feed-card" data-state=${question ? 'question' : model.state}>
        ${
          // A post's byline already says so: no pill or headline row.
          post || question
            ? ''
            : html`<div class="soda-feed-hgroup">
                <p class="soda-feed-hl">
                  <span class="soda-feed-status soda-feed-pill" data-state=${model.state}
                    >${icon(stateIcons[model.state])}${model.pill}</span
                  >${model.headline}
                </p>
                ${model.detail ? html`<p class="soda-feed-dt">${model.detail}</p>` : ''}
              </div>`
        }
        ${
          question
            ? html`${questionPrompt(question, model.key)}${renderComposer(model.composer)}`
            : renderCardContent(model, artifacts, links, assetBase)
        }
        ${
          post || question
            ? ''
            : html`<p class="soda-feed-next" data-state=${model.state}>
                ${model.state === 'needs_you' ? html`<b class="soda-feed-age">${icon(stateIcons.waiting)}${model.next}</b>` : model.next}
              </p>`
        }
        ${renderChanges(model)}
        ${
          model.links.length > 0
            ? html`<p class="soda-feed-source-links">
                ${model.links.map((link) => html`<a href=${link.href}>${link.label}</a> `)}
              </p>`
            : ''
        }
      </div>
      <section class="soda-feed-drawer" data-open=${model.replies.open} aria-label="Replies">
        <button
          type="button"
          class="soda-feed-pull-tab"
          data-feed-drawer-toggle=${model.replies.key}
          aria-expanded=${model.replies.open}
        >
          <svg
            viewBox="0 0 24 24"
            aria-hidden="true"
            fill="none"
            stroke="currentColor"
            stroke-width="1.6"
            stroke-linecap="round"
            stroke-linejoin="round"
          >
            <path d="M21 11.5a8.5 8.5 0 0 1-12.4 7.5L3 21l2-5.6A8.5 8.5 0 1 1 21 11.5Z" />
          </svg>
          <strong>${model.replies.countText}</strong>
          ${model.replies.faces.length ? html`<span class="soda-feed-faces" aria-hidden="true">${model.replies.faces.map((face) => html`<i>${face}</i>`)}</span>` : ''}
          <svg
            class="soda-feed-chev"
            viewBox="0 0 24 24"
            aria-hidden="true"
            fill="none"
            stroke="currentColor"
            stroke-width="1.8"
            stroke-linecap="round"
            stroke-linejoin="round"
          >
            <path d="m6 9 6 6 6-6" />
          </svg>
        </button>
        <div class="soda-feed-drawer-body" .inert=${!model.replies.open}>
          <div class="soda-feed-drawer-inner">${replies} ${question ? '' : renderComposer(model.composer)}</div>
        </div>
      </section>
    </div>
  </article>`;
}
