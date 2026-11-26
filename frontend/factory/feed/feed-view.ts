import {html, svg} from 'lit';
import {live} from 'lit/directives/live.js';
import type {TemplateResult} from 'lit';
import type {FeedPostDetail, PostPipelineState, FeedEntry, FeedJudgment} from './feed-response.js';
import {postAttemptsExhausted, feedEntryKey} from './feed-response.js';
import {feedRolePortraits, postRole, postRoleIds, roleLabel} from './roles.js';
import {namedRoleLabel, roleChip} from './role-chip.js';
import {describePostLine, feedStateWords} from './feed-thread.js';
import type {FeedState, FeedThreadItem} from './feed-thread.js';
import {questionContext, renderFeedCommunication} from './feed-communication.js';
import {questionPrompt} from './feed-actions.js';
import type {FeedCardLinks} from './feed-communication.js';
import type {FactoryRepositoryLinks} from '../app/navigation.js';
import {renderRecordAction} from './feed-record-actions.js';
import {renderJevChoice, renderJevReceipt} from '../shared/jev-answer.js';
import type {FeedArtifactContext} from './feed-artifact.js';
import {renderWithdrawAction, renderWithdrawalRecord, renderWithdrawnMark, withdrawnCommunication} from './feed-withdraw.js';
import {distinctLinks, renderMoreMenu} from '../shared/more-menu.js';
import type {MoreLink} from '../shared/more-menu.js';

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
  readonly postId: string;
  readonly text: string;
  readonly tone: PostOutcomeTone;
  readonly issue: number;
  /** The one thing to do next, shown inline. */
  readonly primary?: MoreLink | undefined;
  /** Other places this request reached; they go in the card's ⋯ menu. */
  readonly links: readonly MoreLink[];
  // Still advancing on the server: the feed refresh picks up its next state.
  readonly pending: boolean;
  readonly phase?: PostPhase;
  readonly canRetry: boolean;
  readonly canReroute: boolean;
  readonly needsClarification: boolean;
  readonly revision: number;
  readonly detail: string | undefined;
  readonly productChange: PostPipelineState['productChange'];
  readonly productRepository: string;
  readonly busy: boolean;
  readonly routing: PostPipelineState['routing'];
}

// JEV's reply is one explicit state machine: the stored pipeline states plus
// any stage the server cannot run (stalled) select exactly one phase, and
// each phase owns its words, its tone and whether JEV is actually working.
export type PostPhase =
  | 'issue_maker'
  | 'stalled_jev_not_connected'
  | 'stalled_jev_key_refused'
  | 'stalled_jev_busy'
  | 'stalled_jev_offline'
  | 'stalled_jev_request_refused'
  | 'stalled_context'
  | 'stalled_setup'
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

// Why a post waits for JEV; the server names the same five reasons.
const jevStalls = new Set(['jev_not_connected', 'jev_key_refused', 'jev_busy', 'jev_offline', 'jev_request_refused']);

export function postPhase(state: PostPipelineState): PostPhase {
  if (state.issueMaker) return 'issue_maker';
  if (postAttemptsExhausted(state)) return 'exhausted';
  if (state.judgment === 'pending')
    return state.stalled && jevStalls.has(state.stalled)
      ? (`stalled_${state.stalled}` as PostPhase)
      : state.stalled === 'context' ? 'stalled_context' : state.stalled === 'setup' ? 'stalled_setup' : 'reading';
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
  stalled_jev_not_connected: {
    text: () => 'I’m not connected yet, so I can’t read this. Connect JEV in Accounts and I’ll pick it up.',
    tone: 'blocked',
    working: false,
  },
  stalled_jev_key_refused: {
    text: () => 'TypeSafe stopped accepting the JEV key, so I can’t read this. Replace the key in Accounts and I’ll pick it up.',
    tone: 'blocked',
    working: false,
  },
  stalled_jev_busy: {
    text: () => 'TypeSafe is busy right now. I’ll read this by myself shortly.',
    tone: 'progress',
    working: false,
  },
  stalled_jev_offline: {
    text: () => 'Factory can’t reach TypeSafe right now. I’ll read this by myself when it can.',
    tone: 'progress',
    working: false,
  },
  stalled_jev_request_refused: {
    text: () => 'TypeSafe wouldn’t take this request, often because it is too long. Edit the post, or choose who should take it.',
    tone: 'blocked',
    working: false,
  },
  stalled_context: {
    text: () =>
      'I need to refresh this request’s issues using your current repository access. Retry the request to continue.',
    tone: 'blocked',
    working: false,
  },
  stalled_setup: {
    text: () => 'I can’t read this yet: this project has no Muse login chosen for its roles. Choose one in Settings and I’ll read it.',
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
    case 'draft':
      return ['Reviewing a proposed change to an accepted direction is unavailable in this app. The proposal is kept.', 'failed'];
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
  const places = repository.path === undefined ? undefined : repository.places;
  const issueHref = issue > 0 ? places?.issue(issue) : undefined;
  const progress = issue > 0 ? places?.progress(issue) : state.authoring?.intent ? places?.request(state.authoring.intent) : undefined;
  const makerPost = state.issueMaker?.post ? repository.places?.feed(state.issueMaker.post) : undefined;
  const setup =
    state.authoring?.state === 'waiting' && state.authoring.reason === 'preparation_not_ready'
      ? repository.places?.settings?.('factory')
      : undefined;
  return {
    messageKind,
    postId: state.postId,
    text,
    tone,
    issue,
    primary: makerPost
      ? {label: 'Follow Issue Maker', href: makerPost}
      : setup
        ? {label: 'Open settings', href: setup}
        : undefined,
    links: [
      ...(issueHref ? [{label: `Open issue #${issue}`, href: issueHref}] : []),
      ...(progress ? [{label: 'See progress', href: progress}] : []),
    ],
    // Dots only while someone is actually working on it.
    pending: phase ? postPhases[phase].working : state.productChange?.state === 'unavailable',
    ...(phase ? {phase} : {}),
    canRetry: state.canRetry === true,
    canReroute: state.canReroute === true,
    needsClarification: state.route === 'clarify',
    revision: state.revision,
    detail: state.issueMaker ? undefined : state.authoring?.detail || state.detail || undefined,
    productChange: state.productChange,
    productRepository: repository.id,
    busy,
    routing: validRouting(state.routing),
  };
}

// JEV's reply under a request: her portrait, what she is doing right now
// (with a working indicator while the feed's own refresh catches up), and
// the actions the author may take.
export function renderOutcome(outcome: PostOutcomeModel, assetBase = ''): TemplateResult {
  const change = outcome.productChange;
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
  const actor = status !== undefined ? 'Factory' : 'JEV';
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
        ${outcome.text} ${outcome.primary ? html`<a href=${outcome.primary.href}>${outcome.primary.label}</a>` : ''}
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
      ${
        outcome.routing?.model
          ? renderJevReceipt({
              question: 'Where does this post belong: new work, a discussion, a question back to you, or an existing issue?',
              rule: 'Act on the top option when it is at least 70% likely; otherwise ask you.',
              ruled: outcome.routing.chosen
                ? `You chose “${routingLabel(outcome.routing, outcome.routing.chosen)}” instead.`
                : outcome.routing.unsure
                  ? 'No option reached 70%, so I asked you.'
                  : 'The top option cleared it.',
              model: outcome.routing.model,
              inputTokens: outcome.routing.inputTokens,
              outputTokens: outcome.routing.outputTokens,
            })
          : ''
      }
      ${
        outcome.routing?.chosen
          ? html`<p class="soda-feed-rerouted">You sent this to “${routingLabel(outcome.routing, outcome.routing.chosen)}”.</p>`
          : ''
      }
      ${outcome.routing && outcome.canReroute ? renderReroute(outcome, outcome.routing) : ''}
      ${outcome.detail && !outcome.phase?.startsWith('stalled') ? html`<p>${outcome.detail}</p>` : ''}
      ${
        outcome.canRetry
          ? html`<button
              type="button"
              data-post-revise=${outcome.postId}
              data-post-repository=${outcome.productRepository}
              data-message-kind=${outcome.messageKind}
              data-clarification=${outcome.needsClarification ? 'true' : 'false'}
            >
              ${outcome.needsClarification ? 'Answer clarification' : `Edit ${outcome.messageKind} and retry`}
            </button>`
          : ''
      }
    </div>
  </div>`;
}

type Routing = NonNullable<PostPipelineState['routing']>;

function routingLabel(routing: Routing, key: string): string {
  return routing.options.find((option) => option.key === key)?.label || key;
}

// "Wrong call? Choose where it goes": while nothing has acted on JEV's
// decision, the author picks one of the places she weighed instead. Asking a
// question back is JEV's move, so those options are not offered.
function renderReroute(outcome: PostOutcomeModel, routing: Routing): TemplateResult {
  const current = routing.chosen || (routing.unsure ? '' : routing.selected);
  const options = routing.options.filter((option) => !option.key.startsWith('clarify_'));
  return html`<details class="soda-feed-reroute">
    <summary>Wrong call? Choose where it goes</summary>
    <p>Nothing has started yet. Pick where this post belongs and it goes there instead.</p>
    <div class="soda-feed-reroute-options">
      ${options.map(
        (option) => html`<button
          type="button"
          data-post-reroute=${outcome.postId}
          data-reroute-choice=${option.key}
          data-post-repository=${outcome.productRepository}
          data-post-revision=${String(outcome.revision)}
          ?disabled=${outcome.busy || option.key === current}
        >
          ${option.label || option.key}${option.key === routing.selected ? html` <small>JEV’s pick</small>` : ''}
        </button>`
      )}
    </div>
  </details>`;
}

export interface ComposerModel {
  readonly key: string;
  readonly draft: string;
  readonly submitAvailable: boolean;
  readonly label?: string;
  readonly submitLabel?: string;
  readonly compact?: boolean;
  /** One line until focused (the repository post box). */
  readonly collapsed?: boolean;
  /** The repository post box's chosen first reader ('' for none). */
  readonly readFirstBy?: string;
  readonly assetBase?: string;
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
// "Read first by" (slice 5.6): a routing hint sent as read_first_by. JEV still
// classifies the post; the named role is only assessed first for a reply.
function renderReadFirstPicker(model: ComposerModel): TemplateResult {
  return html`<label class="soda-feed-read-first-pick"
    >Read first by
    <select data-feed-read-first=${model.key}>
      <option value="" ?selected=${!model.readFirstBy}>JEV routes it</option>
      ${postRoleIds.map((role) => html`<option value=${role} ?selected=${model.readFirstBy === role}>${roleLabel(role)}</option>`)}
    </select></label
  >`;
}

function renderRequestComposer(model: ComposerModel): TemplateResult {
  const open = !model.collapsed;
  const reader = open ? postRole(model.readFirstBy) : undefined;
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
    ${open ? renderReadFirstPicker(model) : ''}
    <div class="soda-feed-composer-foot">
      <span class="soda-feed-compose-hint"
        >${
          reader
            ? html`${roleChip(reader, model.assetBase)} is considered first for a reply while it has work running here.
                JEV still routes the post.`
            : 'JEV turns it into new work, feedback on an issue, or a discussion.'
        }</span
      >
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
  readonly workItem: string | undefined;
  readonly links: readonly MoreLink[];
  readonly post: FeedPostDetail | undefined;
  readonly outcome: PostOutcomeModel | undefined;
  /** An ordinary question: asked once, answered in the box under it. */
  readonly question: boolean;
  readonly composer: ComposerModel;
  readonly replies: RepliesModel;
  readonly thread: Pick<FeedThreadItem, 'changes' | 'level'> | undefined;
}

// The card's ⋯ menu: where this entry's work lives. The parent post is
// already linked from "Replying to" above the card.
function sourceLinks(entry: FeedEntry, repository: FeedRepositoryRef): MoreLink[] {
  if (repository.path === undefined) return [];
  const links: MoreLink[] = repository.current ? [] : [{label: `Open ${repository.label}`, href: repository.path}];
  const issue = entry.issue ? repository.places?.issue(entry.issue) : undefined;
  if (issue) links.push({label: `Open issue #${entry.issue}`, href: issue});
  if (entry.issue && repository.places) links.push({label: 'See progress', href: repository.places.progress(entry.issue)});
  const pull = entry.pr ? repository.places?.pull(entry.pr) : undefined;
  if (pull) links.push({label: `Open pull request #${entry.pr}`, href: pull});
  return links;
}

function entryRole(entry: FeedEntry): string {
  return (
    roleLabel(entry.actor && !/^\d+$/.test(entry.actor) ? entry.actor : undefined) ??
    (entry.kind === 'record' ? 'Factory' : entry.post?.parentId ? 'Reply' : 'Post')
  );
}

export interface NeedsMeRow {
  readonly href: string;
  readonly headline: string;
  readonly role: string;
  readonly avatarFile: string | undefined;
  readonly name: string;
  readonly at: number | undefined;
  /** The role a waiting question is addressed to. */
  readonly addressedTo: string | undefined;
}

// needsMeRows lists the posts whose thread state waits for the viewer, in feed order.
export function needsMeRows(items: readonly FeedThreadItem[]): NeedsMeRow[] {
  return items
    .filter((item) => item.state === 'needs_you')
    .map(({entry, state}) => {
      const role = entryRole(entry);
      return {
        href: '#feed-' + feedEntryKey(entry).replace(':', '-'),
        headline: describePostLine(entry, state).headline,
        role,
        avatarFile: feedRolePortraits[role],
        name: entry.author ?? role,
        at: entry.at,
        addressedTo: namedRoleLabel(entry.payload?.communication?.addressedTo),
      };
    });
}

// renderNeedsMe is the rail beside the list: one row per waiting post, each a
// link that jumps to the post. More may wait on pages not loaded yet.
export function renderNeedsMe(
  rows: readonly NeedsMeRow[],
  ageText: (at: number) => string,
  assetBase: string,
  older: boolean
): TemplateResult | string {
  if (!rows.length) return '';
  return html`<aside class="soda-feed-needs" aria-label="Needs me">
    <h2>Needs me <span>· ${rows.length}</span></h2>
    <ul>
      ${rows.map(
        (row) => html`<li>
          <a href=${row.href}>
            <span class="soda-feed-needs-face" aria-hidden="true"
              >${row.avatarFile ? html`<img src=${assetBase + row.avatarFile} alt="" width="28" height="28" loading="lazy" />` : row.name.slice(0, 1).toUpperCase()}</span
            >
            <span class="soda-feed-needs-text"
              ><span class="soda-feed-needs-hl">${row.headline}</span
              ><span class="soda-feed-needs-meta">${row.role}${row.addressedTo ? ` asks ${row.addressedTo}` : ''}${row.at ? ' · ' + ageText(row.at) : ''}</span></span
            >
          </a>
        </li>`
      )}
    </ul>
    ${older ? html`<p class="soda-feed-needs-older">More may be waiting further down.</p>` : ''}
  </aside>`;
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
  const role = entryRole(entry);
  const state = thread.state;
  const line = describePostLine(entry, state);
  const key = feedEntryKey(entry);
  const pipeline = type === 'post' ? entry.post?.pipeline : undefined;
  const outcome = pipeline
    ? describePostOutcome(pipeline, repository, busy.includes(pipeline.postId), entry.post?.parentId ? 'reply' : 'post')
    : undefined;
  const ask = entry.payload?.communication;
  // Real urgency (a critical or warning question) and a request for allowance
  // or access keep the attention card.
  // A withdrawn question takes no answers, so it is not a conversation card.
  const question =
    ask?.kind === 'ask' && !ask.request && !entry.withdrawn && ask.severity !== 'critical' && ask.severity !== 'warning';
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
    timeText: entry.at ? formatStamp(entry.at) : '',
    pill: type === 'post' ? (entry.post?.parentId ? 'Reply' : 'Post') : feedStateWords[state],
    headline: line.headline,
    detail: line.detail,
    workItem: line.workItem,
    links: distinctLinks([...sourceLinks(entry, repository), ...(outcome?.links ?? [])]),
    post: type === 'post' ? entry.post : undefined,
    outcome,
    composer: {
      key,
      draft,
      compact: !question,
      ...(question ? {label: 'Your reply'} : {}),
      submitAvailable: draft.trim() !== '' && !busy.includes(key),
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
            <span class="soda-feed-jev-kn"><i></i></span>Factory acts on its own from 80%
          </p>`
        : ''
    }
    ${
      sure !== undefined
        ? html`<p class="soda-feed-jev-sure">
            JEV is <strong>${Math.round(sure * 100)}% sure</strong> of this
            answer${notch ? (sure >= 0.8 ? ', which clears the 80% Factory needs' : ', under the 80% Factory needs') : ''}
          </p>`
        : ''
    }
  </div>`;
}

function renderPostBody(post: FeedPostDetail | undefined, assetBase = ''): TemplateResult {
  const text = !post
    ? 'This post is unavailable.'
    : post.removed
      ? 'This post was removed.'
      : post.body;
  return html`<section aria-label="Discussion text">
    <p class="soda-feed-body-text">${text}${post?.edited ? ' (edited)' : ''}</p>
    ${post?.readFirstBy && !post.removed ? html`<p class="soda-feed-read-first">Read first by ${roleChip(post.readFirstBy, assetBase)}</p>` : ''}
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
  const withdrawal = entry.withdrawal ?? entry.post?.withdrawal;
  if (withdrawal) return renderWithdrawalRecord(withdrawal, model.name, links.places);
  if (model.type === 'post')
    return html`${renderPostBody(model.post, assetBase)}${model.outcome ? renderOutcome(model.outcome, assetBase) : ''}`;
  const communication = entry.payload?.communication;
  if (communication)
    return renderFeedCommunication(
      entry.withdrawn ? withdrawnCommunication(communication) : communication,
      model.key,
      artifacts,
      links
    );
  const judgment = entry.payload?.judgment;
  const typed = renderRecordAction(entry.payload);
  return html`${judgment ? renderJudgment(judgment) : ''}${
    typed ?? (entry.summary ? html`<p class="soda-feed-component-summary">${entry.summary}</p>` : '')
  }`;
}

// renderPortrait is the post's face in the timeline gutter, beside the byline
// and card.
function renderPortrait(model: FeedPostModel, assetBase: string): TemplateResult {
  return html`<div class="soda-feed-gutter">
    <span class="soda-feed-avatar" aria-hidden="true"
      >${
        model.avatarFile
          ? html`<img
              src=${assetBase + model.avatarFile}
              alt=""
              width="44"
              height="44"
              loading="lazy"
              decoding="async"
            />`
          : model.name.slice(0, 1).toUpperCase()
      }</span
    >
  </div>`;
}

function renderByline(model: FeedPostModel): TemplateResult {
  const changed = model.thread?.changes.length ? 'updated ' : '';
  return html`<div class="soda-feed-header">
    <p>
      <strong>${model.name}</strong
      >${model.role !== model.name ? html`<span class="soda-feed-by-role"> · ${model.role}</span>` : ''}
      <span class="soda-feed-by-meta"
        >${[model.workItem ?? model.project, changed + model.timeText].filter((part) => part.trim()).map((part, index) => (index ? html`<i>·</i>${part}` : part))}</span
      >
    </p>
    ${renderMoreMenu('More for this post', model.links)}
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
  const entry = model.entry;
  const withdrawn = entry.withdrawn ?? entry.post?.withdrawn;
  // The person withdraws their own posts; agent posts, withdrawals and typed
  // answers (corrected by replying) have no action.
  const own =
    post && !withdrawn && !entry.post?.withdrawal && !entry.post?.answer && !entry.post?.removed && !!links.viewer && entry.actor === links.viewer;
  return html`<article
    class="soda-feed-post"
    id=${'feed-' + model.key.replace(':', '-')}
    data-card=${model.type}
    data-question=${question ? 'true' : 'false'}
    data-state=${model.state}
    data-level=${model.thread?.level ?? 'post'}
    data-role=${model.role.toLowerCase().replace(/[\s-]+/g, '_')}
    data-withdrawn=${withdrawn ? 'true' : 'false'}
  >
    ${renderPortrait(model, assetBase)}
    <div class="soda-feed-column">
      ${model.parent ? html`<p class="soda-feed-parent">Replying to <a href=${model.parent.href}>${model.parent.author}</a></p>` : ''}
      ${renderByline(model)}
      <div class="soda-feed-stack">
        <div class="soda-feed-card" data-state=${question ? 'question' : model.state}>
          ${withdrawn ? renderWithdrawnMark(withdrawn, links.places) : ''}
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
              ? html`${questionPrompt(question, model.key, questionContext(artifacts, links))}${renderComposer(model.composer)}`
              : renderCardContent(model, artifacts, links, assetBase)
          }
          ${own ? renderWithdrawAction(entry.id, questionContext(artifacts, links)) : ''}
          ${renderChanges(model)}
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
    </div>
  </article>`;
}
