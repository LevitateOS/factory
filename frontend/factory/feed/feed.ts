import {LitElement, html, type TemplateResult} from 'lit';
import {repeat} from 'lit/directives/repeat.js';
import type {FactoryTransport} from '../api/transport.js';
import type {FactoryFeedHost, FactoryRepositoryCatalog, FactoryRepositoryLinks} from '../app/navigation.js';
import {inspectStored, readStored, writeStored} from '../shared/drafts.js';
import {allowanceHolds, allowanceHoldText, type AllowanceHold} from './allowance.js';
import {check, id, object, readSodaJSON} from '../api/models.js';
import {activityTimestamp} from '../shared/format.js';
import {decisionID} from '../api/request-response.js';
import type {FeedCardLinks} from './feed-communication.js';
import {
  postSourceForEntry,
  feedEntryKey,
  compareRepositoryIDs,
  feedEntryKinds,
  feedQueryString,
  mergeFeedEntries,
  newIdempotencyKey,
  parseFeedPostDetail,
  parseFeedPageView,
  parseFeedSubscriptionView,
  feedRevisionSource,
  feedRevisionReceipt,
  feedRevisionBodyDigest,
  feedReviewSource,
} from './feed-response.js';
import type {FeedPostDetail, FeedPostBody, FeedEntry, FeedEntryKind, FeedSubscriptionView} from './feed-response.js';
import {
  describePostOutcome,
  describeReplies,
  describeFeedPost,
  renderComposer,
  renderFeedPost,
  renderOutcome,
} from './feed-view.js';
import {renderFeedCommunication} from './feed-communication.js';
import type {FeedArtifactContext} from './feed-artifact.js';
import {renderConsultation, renderContribution} from './consultation.js';
import {roleLabel, threadFeed} from './feed-thread.js';
import {feedRolePortraits} from './feed-portraits.js';
import {readRevision, retainRevision, settleRevision, samePendingRevision, sameRevisionWriting, revisionBody} from './revision-record.js';
import type {RevisionAddress, PendingRevision} from './revision-record.js';
import {readReview, retainReview, settleReview, sameReview} from './review-record.js';
import type {ReviewAddress, PendingReview} from './review-record.js';

export interface FactoryFeedContext {
  transport: FactoryTransport;
  host: FactoryFeedHost;
  repository: string;
  repositoryName?: string;
  account?: string;
  global?: boolean;
}

type RepositoryLabels = FactoryRepositoryCatalog;

interface Thread {
  items: FeedPostDetail[];
  next: number;
  busy: boolean;
  message: string;
  // Older pages were appended: a quiet refresh leaves the thread alone so
  // the reader keeps their place; Refresh replies starts over.
  pagedOlder: boolean;
}

interface Revising {
  id: string;
  messageKind: 'post' | 'reply';
  revision: number;
  repository: string;
  body: string;
  baseBody: string;
  blocked?: boolean;
  original?: string;
  question?: string;
  // The request text the server holds after a refused stale submit.
  current?: string;
  address?: RevisionAddress | undefined;
  pending?: PendingRevision | undefined;
  recoveryBlocked?: boolean;
}

const feedPageSize = 20;
const postID = /^[a-f0-9]{32}$/;

function failureMessage(status: number | undefined, fallback: string): string {
  if (status === 403)
    return 'One or more subscribed projects are not visible to you. The feed shows only permitted activity.';
  return fallback;
}

// Relative reply age for post rows ("now", "12m", "2h", "3d"); older
// replies fall back to a short date.
export function relativeReplyTime(at: number, now = Date.now()): string {
  const seconds = Math.floor((now - at * 1000) / 1000);
  if (seconds < 60) return 'now';
  const minutes = Math.floor(seconds / 60);
  if (minutes < 60) return `${minutes}m`;
  const hours = Math.floor(minutes / 60);
  if (hours < 24) return `${hours}h`;
  const days = Math.floor(hours / 24);
  if (days < 7) return `${days}d`;
  return new Date(at * 1000).toLocaleDateString(undefined, {month: 'short', day: 'numeric'});
}

/** Today's clock time, otherwise a short date and time. */
function shortTime(unix: number): string {
  const date = new Date(unix * 1000);
  const today = new Date().toDateString() === date.toDateString();
  return date.toLocaleString(
    undefined,
    today ? {hour: 'numeric', minute: '2-digit'} : {month: 'short', day: 'numeric', hour: 'numeric', minute: '2-digit'}
  );
}

export class SodaFactoryFeed extends LitElement {
  static properties = {view: {state: true}};
  declare private view: {
    message: string;
    busy: boolean;
    readFailed: boolean;
    subscription: FeedSubscriptionView | undefined;
    labels: RepositoryLabels;
    entries: FeedEntry[];
    next: string;
    truncated: boolean;
    pagedBack: boolean;
    confirmedUnix: number;
    postBusy: string[];
    allowance: AllowanceHold[];
  };
  private current: FactoryFeedContext | undefined;
  private lifetime = new AbortController();
  private generation = '';
  private accessFailure: 401 | 403 | undefined;
  private rechecking = false;
  // One serial: a context switch, filter change or disconnect bumps it, and
  // every response that lands after a bump is dropped.
  private serial = 0;
  // The serial whose latest-page read is in flight, if any.
  private reading: number | undefined;
  private refreshTimer: ReturnType<typeof setInterval> | undefined;
  private drafts = new Map<string, string>();
  private revising: Revising | undefined;
  private revisionEditor = 0;
  private threads = new Map<string, Thread>();
  private threadReads = new Map<string, Promise<void>>();
  private openDrawers = new Set<string>();
  // The viewer's canonical Factory actor id (the feed subscription's subscriber): Open
  // request shows only on the viewer's own requests.
  private viewerID: string | undefined;
  // Verified delivery id → the origin of a Ready app the viewer may open.
  private appOrigins = new Map<string, string>();
  // The repository post box stays one line until focused.
  private postOpen = false;
  private replyingTo = new Set<string>();
  private composeRequested = false;
  // A post to show alone (the Feed place's ?post=, e.g. a Knowledge item's source).
  private focusPost: string | undefined;
  private librarySourcePending: string | undefined;
  // A newer source open or leaving the post supersedes a read in flight.
  private librarySerial = 0;
  private focusedEntry: FeedEntry | undefined;
  private filters = {view: 'all', search: '', role: '', current: false};

  protected createRenderRoot() {
    return this;
  }

  constructor() {
    super();
    // The repository post box opens from one line when focused.
    this.addEventListener('focusin', (event) => {
      if (this.postOpen || !(event.target instanceof Element) || !event.target.closest('.soda-feed-post-box')) return;
      this.postOpen = true;
      this.requestUpdate();
    });
    this.view = this.emptyView();
  }

  private emptyView(): typeof this.view {
    return {
      message: '',
      busy: false,
      readFailed: false,
      subscription: undefined,
      labels: {refs: new Map(), listedAll: false},
      entries: [],
      next: '',
      truncated: false,
      pagedBack: false,
      confirmedUnix: 0,
      allowance: [],
      postBusy: [],
    };
  }

  set context(context: FactoryFeedContext | undefined) {
    if (context?.repository) check(id(context.repository));
    if (context) check(!!context.transport.generation);
    if (
      context?.transport === this.current?.transport &&
      context?.repository === this.current?.repository &&
      context?.account === this.current?.account &&
      context?.global === this.current?.global &&
      context?.host.draftScope === this.current?.host.draftScope &&
      (context?.transport.generation ?? '') === this.generation
    )
      return;
    clearInterval(this.refreshTimer);
    this.refreshTimer = undefined;
    this.serial++;
    this.reading = undefined;
    this.lifetime.abort();
    this.lifetime = new AbortController();
    this.current = context;
    this.generation = context?.transport.generation ?? '';
    this.accessFailure = undefined;
    this.rechecking = false;
    this.drafts.clear();
    this.threads.clear();
    this.threadReads.clear();
    this.openDrawers.clear();
    this.revising = undefined;
    this.librarySerial++;
    this.librarySourcePending = undefined;
    this.viewerID = undefined;
    this.appOrigins = new Map();
    this.postOpen = false;
    this.replyingTo.clear();
    this.focusedEntry = undefined;
    this.filters = {view: 'all', search: '', role: '', current: false};
    this.view = this.emptyView();
    if (context) {
      void this.reload();
      if (this.focusPost) void this.openSourcePost(this.focusPost);
      this.startPolling();
    }
  }

  disconnectedCallback() {
    this.context = undefined;
    super.disconnectedCallback();
  }

  private live(serial: number): boolean {
    if (serial !== this.serial || !this.current) return false;
    if (this.current.transport.generation !== this.generation) {
      this.context = undefined;
      return false;
    }
    return true;
  }

  private bindingLive(context: FactoryFeedContext, signal = this.lifetime.signal): boolean {
    return signal === this.lifetime.signal && !signal.aborted && this.current === context && this.live(this.serial);
  }

  private startPolling() {
    if (this.refreshTimer || this.accessFailure) return;
    this.refreshTimer = setInterval(() => {
      if (this.isConnected && document.visibilityState === 'visible' && !this.view.busy) void this.readLatest(false);
    }, 10000);
  }

  private accessMessage() {
    return this.accessFailure === 401
      ? 'Your session expired. Refresh after signing in to read and post again. Your writing is kept.'
      : 'Access to this feed changed. Refresh to check your current access. Your writing is kept.';
  }

  private loseAccess(status: 401 | 403) {
    this.accessFailure = status;
    this.rechecking = false;
    this.serial++;
    this.reading = undefined;
    clearInterval(this.refreshTimer);
    this.refreshTimer = undefined;
    this.lifetime.abort();
    this.lifetime = new AbortController();
    this.threads.clear();
    this.threadReads.clear();
    this.openDrawers.clear();
    this.revising = undefined;
    this.librarySerial++;
    this.librarySourcePending = undefined;
    this.focusedEntry = undefined;
    this.viewerID = undefined;
    this.appOrigins.clear();
    this.replyingTo.clear();
    this.view = {...this.emptyView(), readFailed: true, message: this.accessMessage()};
  }

  private async request(context: FactoryFeedContext, path: string, init?: RequestInit): Promise<Response> {
    const binding = this.lifetime.signal;
    if (!this.bindingLive(context, binding)) throw Error('Factory runtime binding changed.');
    if (this.accessFailure && init?.method && init.method !== 'GET') throw Error(this.accessMessage());
    const signal = AbortSignal.any([binding, init?.signal ?? AbortSignal.timeout(15000)]);
    signal.throwIfAborted();
    const response = await context.transport.request(path, {...init, signal});
    if (!this.bindingLive(context, binding) || signal.aborted) {
      void response.body?.cancel().catch(() => {});
      signal.throwIfAborted();
      throw Error('Factory runtime binding changed.');
    }
    if (response.status === 401) this.loseAccess(401);
    return response;
  }

  private async readJSON(context: FactoryFeedContext, path: string, init?: RequestInit): Promise<unknown> {
    const binding = this.lifetime.signal,
      signal = AbortSignal.any([binding, init?.signal ?? AbortSignal.timeout(15000)]);
    const response = await this.request(context, path, {method: 'GET', ...init, signal});
    if (!response.ok) {
      if (
        this.bindingLive(context, binding) &&
        response.status === 403 &&
        (init?.method ?? 'GET') === 'GET' &&
        path.startsWith('factory/feed')
      )
        this.loseAccess(403);
      const error = Error(failureMessage(response.status, 'The combined feed is unavailable.'));
      (error as {status?: number}).status = response.status;
      void response.body?.cancel().catch(() => {});
      throw error;
    }
    const large = path.startsWith('factory/feed?') || path.startsWith('factory/feed/');
    const value = await readSodaJSON(response, large ? 4 * 1024 * 1024 : 65536);
    signal.throwIfAborted();
    if (!this.bindingLive(context, binding)) throw Error('Factory runtime binding changed.');
    return value;
  }

  private artifactContext(repository: string): FeedArtifactContext | undefined {
    const context = this.current;
    if (
      !context ||
      !this.live(this.serial) ||
      !id(repository) ||
      (!context.global && context.repository !== repository)
    )
      return;
    return {transport: context.transport, repository, generation: this.generation, signal: this.lifetime.signal};
  }

  private account(): string {
    return this.current?.host.draftScope ?? '';
  }

  private storageKey(key: string) {
    return `${this.account()}:${key}`;
  }

  private scopeRepositories(): string[] {
    const subscription = this.view.subscription;
    // Until a subscription is saved (revision 0), /feed shows every candidate
    // repository it already loads; nothing is written.
    if (this.current?.global)
      return subscription?.revision === 0
        ? [...this.view.labels.refs.keys()].sort(compareRepositoryIDs).slice(0, 1024)
        : (subscription?.repositories ?? []);
    const current = this.current?.repository;
    return id(current) ? [current] : [];
  }

  private async reload() {
    const context = this.current;
    if (!context || !this.live(this.serial) || this.view.busy) return;
    if (!context.global && !id(context.repository)) {
      this.view = {...this.view, message: 'The combined feed needs its current project binding.'};
      return;
    }
    const serial = this.serial,
      recovering = !!this.accessFailure;
    this.rechecking = true;
    this.view = {...this.view, busy: true, readFailed: false, message: this.view.readFailed ? '' : this.view.message};
    try {
      const subscription = context.global
        ? parseFeedSubscriptionView(await this.readJSON(context, 'factory/feed/subscription'))
        : {
            subscriber: context.account ?? context.transport.generation,
            revision: 0,
            repositories: [context.repository],
            kinds: [],
          };
      if (!this.live(serial)) return;
      if (context.global) this.viewerID = subscription.subscriber;
      else void this.readViewerID(context);
      this.view = {...this.view, subscription};
      const labels = await this.resolveLabels(context, subscription);
      if (!this.live(serial)) return;
      this.view = {...this.view, labels};
      void this.readAppOrigins(context);
      await this.readLatest(true);
      if (recovering && this.live(serial) && !this.accessFailure && this.focusPost && !this.focusedEntry)
        void this.openSourcePost(this.focusPost);
    } catch (error) {
      if (this.live(serial))
        this.view = {
          ...this.view,
          busy: false,
          readFailed: true,
          message: error instanceof Error ? error.message : 'The combined feed is unavailable.',
        };
    } finally {
      if (this.live(serial)) this.rechecking = false;
    }
  }

  private resolveLabels(context: FactoryFeedContext, subscription: FeedSubscriptionView): Promise<RepositoryLabels> {
    return context.host.repositories(subscription.repositories, this.lifetime.signal);
  }

  private feedPath(cursor: string): string {
    return (
      feedQueryString({
        repositories: this.scopeRepositories(),
        kinds: [],
        limit: feedPageSize,
        cursor,
        ...this.filters,
      }) + (this.current?.global ? '' : '&scope=project')
    );
  }

  // readLatest re-reads the newest page. Posts arrive with their stored text
  // and post outcome, so this one read refreshes every card; open threads
  // re-read their newest replies alongside.
  private async readLatest(announce: boolean) {
    const context = this.current;
    if (!context || !this.live(this.serial) || this.reading === this.serial) return;
    if (this.accessFailure && !this.rechecking) return;
    const serial = this.serial;
    if (this.scopeRepositories().length === 0) {
      this.view = {
        ...this.view,
        busy: false,
        message: 'Choose at least one repository to follow before reading the Feed.',
      };
      return;
    }
    this.reading = serial;
    this.view = {...this.view, busy: true, readFailed: false, message: this.view.readFailed ? '' : this.view.message};
    const anchor = Array.from(this.querySelectorAll<HTMLElement>('.soda-feed-post')).find(
      (el) => el.getBoundingClientRect().bottom > 0
    );
    const anchorTop = anchor?.getBoundingClientRect().top;
    try {
      const page = parseFeedPageView(await this.readJSON(context, this.feedPath('')));
      if (!this.live(serial)) return;
      this.accessFailure = undefined;
      this.startPolling();
      const focused = this.focusedEntry;
      if (focused) {
        const fresh = page.entries.find((entry) => feedEntryKey(entry) === feedEntryKey(focused));
        const refreshed = fresh ?? (await this.readSourcePost(context, focused.id));
        if (!this.live(serial)) return;
        if (this.focusedEntry?.id === focused.id) this.focusedEntry = refreshed;
      }
      const refs = new Map(this.view.labels.refs);
      for (const entry of page.entries) {
        if (entry.project) {
          const links = context.host.links(entry.repository, entry.project);
          if (links) refs.set(entry.repository, {path: links.repository, label: entry.project});
        }
      }
      const known = new Set(this.view.entries.map(feedEntryKey));
      const fresh = page.entries.filter((entry) => !known.has(feedEntryKey(entry))).length;
      const older = this.view.pagedBack
        ? this.view.entries.filter((entry) => entry.seq < (page.entries.at(-1)?.seq ?? 0))
        : [];
      let message = this.view.message;
      if (announce && known.size > 0)
        message = fresh ? `${fresh} new post${fresh === 1 ? '' : 's'}.` : 'Feed refreshed; no new posts.';
      this.view = {
        ...this.view,
        labels: {...this.view.labels, refs},
        busy: false,
        entries: mergeFeedEntries(older, page.entries),
        next: this.view.pagedBack ? this.view.next : page.next,
        truncated: this.view.pagedBack ? this.view.truncated : page.truncated,
        confirmedUnix: Math.floor(Date.now() / 1000),
        message,
      };
      if (anchor && anchorTop !== undefined) {
        await this.updateComplete;
        if (this.live(serial) && anchor.isConnected) {
          const delta = anchor.getBoundingClientRect().top - anchorTop;
          const scroller = this.closest<HTMLElement>('.soda-project-content');
          if (scroller && scroller.scrollHeight > scroller.clientHeight) scroller.scrollBy({top: delta});
          else window.scrollBy({top: delta});
        }
      }
      void this.readAllowance(serial);
      for (const key of this.openDrawers) {
        const entry = this.entryForKey(key);
        if (entry && !this.threads.get(key)?.pagedOlder) void this.loadReplies(entry);
      }
    } catch (error) {
      if (this.live(serial))
        // A failed refresh keeps the last loaded feed and says so.
        this.view = {
          ...this.view,
          busy: false,
          readFailed: true,
          message: error instanceof Error ? error.message : 'The combined feed is unavailable.',
        };
    } finally {
      if (this.reading === serial) this.reading = undefined;
    }
  }

  private async readOlder() {
    const context = this.current;
    if (!context || !this.live(this.serial) || this.view.busy || this.view.next === '') return;
    const serial = this.serial;
    this.view = {...this.view, busy: true};
    try {
      const page = parseFeedPageView(await this.readJSON(context, this.feedPath(this.view.next)));
      if (!this.live(serial)) return;
      this.view = {
        ...this.view,
        busy: false,
        entries: mergeFeedEntries(this.view.entries, page.entries),
        next: page.next,
        truncated: page.truncated,
        pagedBack: true,
        confirmedUnix: Math.floor(Date.now() / 1000),
      };
    } catch (error) {
      if (this.live(serial))
        this.view = {
          ...this.view,
          busy: false,
          message: error instanceof Error ? error.message : 'Older feed entries are unavailable.',
        };
    }
  }

  // readAllowance names a coder-time allowance hold on the followed
  // repositories above the posts. The status read is owner/admin only; any
  // failure leaves no notice.
  private async readAllowance(serial: number) {
    const context = this.current;
    if (!context) return;
    const settled = await Promise.allSettled(
      this.scopeRepositories()
        .slice(0, 8)
        .map(async (repository) =>
          allowanceHolds(await this.readJSON(context, `repositories/${repository}/factory`), String(repository))
        )
    );
    if (!this.live(serial)) return;
    this.view = {
      ...this.view,
      allowance: settled.flatMap((result) => (result.status === 'fulfilled' ? result.value : [])),
    };
  }

  private changeFilters(next: Partial<typeof this.filters>) {
    if (this.accessFailure) return;
    this.focusedEntry = undefined;
    this.filters = {...this.filters, ...next};
    this.serial++;
    this.reading = undefined;
    this.view = {
      ...this.view,
      busy: false,
      readFailed: false,
      message: '',
      entries: [],
      next: '',
      truncated: false,
      pagedBack: false,
    };
    void this.readLatest(false);
  }

  private backToLatest = () => {
    if (!this.current || this.view.busy) return;
    const serial = this.serial;
    this.view = {...this.view, next: '', truncated: false, pagedBack: false};
    void this.readLatest(false).then(() => {
      if (this.live(serial)) this.querySelector('.soda-feed-list')?.scrollIntoView();
    });
  };

  private projectPostKey(): string {
    return 'project:' + (this.current?.repository ?? '');
  }

  private entryForKey(key: string): FeedEntry | undefined {
    const entry = [...this.view.entries, ...(this.focusedEntry ? [this.focusedEntry] : [])].find(
      (candidate) => feedEntryKey(candidate) === key
    );
    if (entry) return entry;
    // A reply row answered from inside a thread.
    for (const [root, thread] of this.threads) {
      const reply = thread.items.find((item) => 'post:' + item.postId === key);
      const parent = reply ? this.entryForKey(root) : undefined;
      if (reply && parent)
        return {
          kind: 'post',
          id: reply.postId,
          repository: parent.repository,
          seq: 0,
          post: reply,
          ...(reply.actor ? {actor: reply.actor} : {}),
          ...(reply.issueScope ? {issue: reply.issueScope} : {}),
          ...(reply.status ? {status: reply.status} : {}),
          ...(reply.communication ? {payload: {communication: reply.communication}} : {}),
        };
    }
    return undefined;
  }

  private draftFor(key: string): string {
    let draft = this.drafts.get(key);
    if (draft === undefined) {
      draft = readStored(`soda-feed-draft:v1:${this.storageKey(key)}`).slice(0, 4000);
      this.drafts.set(key, draft);
    }
    return draft;
  }

  private setDraft(key: string, draft: string) {
    this.drafts.set(key, draft);
    writeStored(`soda-feed-draft:v1:${this.storageKey(key)}`, draft);
  }

  private revisionKey(edit: {repository: string; id: string}): string {
    return `soda-feed-revision:v2:${this.account()}:${edit.repository}:${edit.id}`;
  }

  private persistRevising() {
    const edit = this.revising;
    if (edit) writeStored(this.revisionKey(edit), JSON.stringify({body: edit.body, original: edit.original}));
  }

  private revisionAddress(repository: string, post: string): RevisionAddress | undefined {
    const author = id(this.current?.account) ? this.current.account : this.viewerID;
    if (!this.current?.host.draftScope || !id(author)) return undefined;
    return {scope: this.current.host.draftScope, repository, post, author};
  }

  private reviewAddress(repository: string, post: string): ReviewAddress | undefined {
    const actor = id(this.current?.account) ? this.current.account : this.viewerID;
    if (!this.current?.host.draftScope || !id(actor)) return undefined;
    return {scope: this.current.host.draftScope, repository, post, actor};
  }

  private renderReviewRecovery(post: string, repository: string) {
    const address = this.reviewAddress(repository, post);
    if (!address || !postID.test(post) || this.accessFailure) return '';
    const saved = readReview(address);
    if (!saved.pending && !saved.blocked) return '';
    const busy = this.view.postBusy.includes(post);
    return html`<p>A change review is saved but unconfirmed.</p>
      <button type="button" data-post-review-recover=${post} data-post-repository=${repository} ?disabled=${busy}>Check saved change review</button>
      ${saved.pending ? html`<button type="button" data-post-review-retry=${post} data-post-repository=${repository} ?disabled=${busy}>Retry saved change review</button>` : ''}`;
  }

  private syncRevision(edit: Revising) {
    const stored = edit.address ? readRevision(edit.address) : undefined;
    edit.pending = stored?.pending;
    edit.recoveryBlocked = !stored || stored.blocked;
  }

  private renderRevisionRecovery(post: string, repository: string, messageKind: 'post' | 'reply') {
    const address = this.revisionAddress(repository, post);
    if (!address || !postID.test(post) || this.accessFailure) return '';
    const stored = readRevision(address);
    if (!stored.pending && !stored.blocked) return '';
    return html`<button type="button" data-post-revise=${post} data-post-repository=${repository}
      data-message-kind=${messageKind}>Recover saved edit</button>`;
  }

  private onDraft = (event: Event) => {
    const area = event.target;
    if (!(area instanceof HTMLTextAreaElement)) return;
    if (this.revising && (area.hasAttribute('data-revision-body') || area.hasAttribute('data-revision-original'))) {
      if (area.hasAttribute('data-revision-body')) this.revising.body = area.value;
      else this.revising.original = area.value;
      this.persistRevising();
      return;
    }
    const key = area.dataset.feedDraft;
    if (!key) return;
    this.setDraft(key, area.value.slice(0, 4000));
    // Keystrokes never re-render: reading position and focus stay put.
    const submit = this.querySelector(`[data-post-submit="${CSS.escape(key)}"]`);
    if (submit instanceof HTMLButtonElement)
      submit.disabled = !!this.accessFailure || area.value.trim() === '' || this.view.postBusy.includes(key);
  };

  private save = (event: SubmitEvent) => {
    event.preventDefault();
    const form = event.currentTarget;
    if (!(form instanceof HTMLFormElement) || !this.current || !this.live(this.serial) || this.accessFailure) return;
    const context = this.current;
    const serial = this.serial;
    const data = new FormData(form);
    const repositories = [...data.getAll('repository')].flatMap((value) => {
      return id(value) ? [value] : [];
    });
    const selection = data.get('kind');
    const kinds = feedEntryKinds.filter((kind) => selection === kind);
    const body: {repositories: string[]; kinds?: FeedEntryKind[]; expected_revision: number} = {
      repositories: [...new Set(repositories)].sort(compareRepositoryIDs),
      expected_revision: Number(data.get('expected_revision')),
    };
    if (kinds.length > 0) body.kinds = kinds;
    this.view = {...this.view, busy: true};
    void (async () => {
      try {
        const response = await this.request(context, 'factory/feed/subscription', {
          method: 'PUT',
          headers: {'Content-Type': 'application/json'},
          body: JSON.stringify(body),
          signal: AbortSignal.timeout(15000),
        });
        if (!response.ok) {
          if (response.status === 409)
            throw Error('Feed subscription changed underneath the write. Refresh before saving again.');
          throw Error(failureMessage(response.status, 'Could not save the feed subscription.'));
        }
        if (!this.live(serial)) return;
        this.view = {...this.view, busy: false, message: 'Followed projects updated.'};
        await this.reload();
      } catch (error) {
        if (this.live(serial))
          this.view = {
            ...this.view,
            busy: false,
            message: error instanceof Error ? error.message : 'Could not save the feed subscription.',
          };
      }
    })();
  };

  /** New request (the Feed place's ?compose=): open the post box and focus it. */
  set compose(compose: boolean) {
    if (compose === this.composeRequested) return;
    this.composeRequested = compose;
    if (!compose) return;
    this.postOpen = true;
    this.requestUpdate();
    const serial = this.serial;
    void this.updateComplete.then(() => {
      if (this.live(serial)) this.querySelector<HTMLTextAreaElement>('.soda-feed-post-box textarea')?.focus();
    });
  }

  /** The post shown alone: the Feed place's ?post= (Knowledge's "Open source post"). */
  set post(post: string | undefined) {
    const valid = post && postID.test(post) ? post : undefined;
    if (valid === this.focusPost) return;
    this.focusPost = valid;
    if (valid) void this.openSourcePost(valid);
    else if (this.focusedEntry) this.leaveFocusedPost();
  }

  private async openSourcePost(sourceId: string) {
    if (!this.current || this.accessFailure) return;
    const context = this.current;
    const serial = this.serial,
      token = ++this.librarySerial;
    this.librarySourcePending = sourceId;
    this.requestUpdate();
    try {
      const entry = await this.readSourcePost(context, sourceId);
      if (!this.live(serial) || token !== this.librarySerial) return;
      this.focusedEntry = entry;
      this.openDrawer(feedEntryKey(entry));
    } catch (error) {
      if (this.live(serial) && token === this.librarySerial)
        this.view = {
          ...this.view,
          message: error instanceof Error ? error.message : 'Source conversation is unavailable.',
        };
    } finally {
      if (this.live(serial) && token === this.librarySerial) {
        this.librarySourcePending = undefined;
        this.requestUpdate();
      }
    }
  }

  private async readSourcePost(context: FactoryFeedContext, sourceId: string): Promise<FeedEntry> {
    const value = object(await this.readJSON(context, 'factory/feed/posts/' + sourceId));
    check(id(value.repository) && (context.global === true || value.repository === context.repository));
    const source = parseFeedPostDetail(value);
    return {
      kind: value.kind === 'record' ? 'record' : 'post',
      id: source.postId,
      repository: value.repository,
      seq: 0,
      at: source.at,
      author: source.authorName,
      post: source,
      ...(typeof value.parent_id === 'string' ? {parentId: value.parent_id} : {}),
      ...(typeof value.parent_author === 'string' ? {parentAuthor: value.parent_author} : {}),
      ...(value.kind === 'record' ? {assignment: String(value.assignment || value.post_id)} : {}),
      ...(value.payload ? {payload: value.payload as NonNullable<FeedEntry['payload']>} : {}),
      ...(source.actor ? {actor: source.actor} : {}),
      ...(source.issueScope ? {issue: source.issueScope} : {}),
      ...(source.status ? {status: source.status} : {}),
      ...(source.communication ? {payload: {communication: source.communication}} : {}),
    };
  }

  private leaveFocusedPost() {
    this.librarySerial++;
    this.librarySourcePending = undefined;
    this.focusedEntry = undefined;
    this.requestUpdate();
  }

  // The subscription's subscriber is the viewer's canonical Factory actor id; the
  // repository feed reads it only to know which requests are the viewer's.
  private async readViewerID(context: FactoryFeedContext) {
    const binding = this.lifetime.signal;
    try {
      const subscriber = parseFeedSubscriptionView(
        await this.readJSON(context, 'factory/feed/subscription')
      ).subscriber;
      if (!this.bindingLive(context, binding)) return;
      this.viewerID = subscriber;
      this.requestUpdate();
    } catch {
      // Without the viewer's id no Open request link shows.
    }
  }

  private async readAppOrigins(context: FactoryFeedContext) {
    if (!context.host.applicationOrigins) return;
    const binding = this.lifetime.signal;
    try {
      const origins = await context.host.applicationOrigins(binding);
      if (!this.bindingLive(context, binding)) return;
      this.appOrigins = new Map(origins);
      this.requestUpdate();
    } catch {
      // Optional host integration: recorded Factory activity remains available.
    }
  }

  private placesFor(repository: string): FactoryRepositoryLinks | undefined {
    const ref = this.view.labels.refs.get(repository);
    return ref ? this.current?.host.links(repository, ref.label) : undefined;
  }

  private ownPlaces(): FactoryRepositoryLinks | undefined {
    const context = this.current;
    return context && !context.global && context.repositoryName
      ? context.host.links(context.repository, context.repositoryName)
      : undefined;
  }

  private cardLinks(entry: FeedEntry): FeedCardLinks {
    const places = this.placesFor(entry.repository);
    const delivery = entry.eventIdentity;
    const appOrigin = delivery ? this.appOrigins.get(delivery) : undefined;
    return {
      ...(places ? {places} : {}),
      ...(this.viewerID ? {viewer: this.viewerID} : {}),
      ...(delivery ? {delivery} : {}),
      ...(appOrigin ? {appOrigin} : {}),
    };
  }

  /** Navigation belongs to the active host and retires with its context. */
  private go(path: string) {
    if (this.live(this.serial)) this.current?.host.navigate(path);
  }

  // A draft's idempotency key survives failed submits, so a retry recovers
  // the original server-side; a confirmed submit rotates it.
  private draftIdempotencyKey(key: string): string {
    const storage = `soda-feed-draftkey:v2:${this.storageKey(key)}`;
    const stored = readStored(storage);
    if (/^[A-Za-z0-9_-]{1,64}$/.test(stored)) return stored;
    const minted = newIdempotencyKey();
    writeStored(storage, minted);
    return minted;
  }

  // submitPost saves one draft. A failed submit keeps the draft; the
  // pending payload is kept with it, so a retry after a lost answer resends
  // the original text while newer writing stays in the box.
  private async submitPost(key: string) {
    const context = this.current;
    const entry = this.entryForKey(key);
    const messageKind = entry ? 'reply' : 'post';
    const messageLabel = entry ? 'Reply' : 'Post';
    if (!context || !this.live(this.serial) || this.accessFailure || this.view.postBusy.includes(key)) return;
    const repository = entry?.repository ?? (key === this.projectPostKey() ? context.repository : '');
    const text = this.draftFor(key);
    if (!id(repository) || text.trim() === '') return;
    const pendingKey = `soda-feed-pending:v1:${this.storageKey(key)}`;
    const commandKey = `soda-feed-draftkey:v2:${this.storageKey(key)}`;
    let post: FeedPostBody = {repository, body: text, idempotency_key: ''};
    if (entry?.issue) post.issue = entry.issue;
    const source = entry ? postSourceForEntry(entry) : undefined;
    if (source) {
      post.parent_id = source.id;
    }
    const pending = readStored(pendingKey);
    if (pending) {
      try {
        const saved = object(JSON.parse(pending));
        const command = readStored(commandKey);
        check(saved.repository === repository);
        check(typeof saved.body === 'string' && saved.body.trim() !== '' && saved.body.length <= 4000);
        check(typeof saved.idempotency_key === 'string' && /^[A-Za-z0-9_-]{1,64}$/.test(saved.idempotency_key));
        check(command === '' || command === saved.idempotency_key);
        check(saved.parent_id === undefined || typeof saved.parent_id === 'string');
        check((saved.parent_id ?? '') === (post.parent_id ?? ''));
        const issue = saved.issue;
        check(issue === undefined || (typeof issue === 'number' && Number.isSafeInteger(issue) && issue >= 0));
        // Recover the immutable submitted text/key, even if the separately
        // stored nonce was lost. Never attach newer writing to this command.
        // An entry's live issue linkage may have advanced since submission.
        post = {
          repository,
          body: saved.body,
          idempotency_key: saved.idempotency_key,
          ...(saved.parent_id !== undefined ? {parent_id: saved.parent_id} : {}),
          ...(issue !== undefined ? {issue} : {}),
        };
        writeStored(commandKey, post.idempotency_key);
      } catch {
        this.view = {
          ...this.view,
          message: `A saved ${messageKind} could not be safely resumed. Its pending copy and your draft are kept.`,
        };
        return;
      }
    } else post.idempotency_key = this.draftIdempotencyKey(key);
    writeStored(pendingKey, JSON.stringify(post));
    const binding = this.lifetime.signal;
    if (entry) this.openDrawer(key);
    this.view = {...this.view, postBusy: [...this.view.postBusy, key]};
    try {
      const response = await this.request(context, 'factory/feed/posts', {
        method: 'POST',
        headers: {'Content-Type': 'application/json'},
        body: JSON.stringify(post),
        signal: AbortSignal.timeout(30000),
      });
      if (!response.ok) {
        if (response.status === 404)
          throw Error('That work is no longer visible here, or posts and replies are unavailable. The draft is kept.');
        throw Error(failureMessage(response.status, `Could not save the ${messageKind}. The draft is kept.`));
      }
      if (!this.bindingLive(context, binding)) return;
      writeStored(commandKey, '');
      writeStored(pendingKey, '');
      if (this.draftFor(key) === post.body) this.setDraft(key, '');
      this.view = {
        ...this.view,
        postBusy: this.view.postBusy.filter((row) => row !== key),
        message: this.draftFor(key)
          ? `Previous ${messageKind} saved. Your newer edits are kept, ready to send.`
          : `${messageLabel} saved.`,
      };
      await this.readLatest(false);
      if (entry && this.bindingLive(context, binding)) void this.loadReplies(entry);
    } catch (error) {
      if (this.bindingLive(context, binding))
        this.view = {
          ...this.view,
          postBusy: this.view.postBusy.filter((row) => row !== key),
          message: error instanceof Error ? error.message : `Could not save the ${messageKind}. The draft is kept.`,
        };
    }
  }

  // openRoleReply resolves a "Read the role's reply" link inside its
  // original thread: the thread opens and pages until the target post or
  // exhaustion, then the row scrolls into view and takes focus.
  private async openRoleReply(link: HTMLElement) {
    const targetId = link.dataset.roleReplyOpen ?? '';
    const key = link.dataset.roleReplyEntry ?? '';
    const entry = key ? this.entryForKey(key) : undefined;
    const serial = this.serial;
    const say = (message: string) => {
      if (!this.live(serial) || !this.openDrawers.has(key)) return;
      const region = this.querySelector(`[data-role-reply-open="${targetId}"]`)
        ?.closest('.soda-feed-consultation')
        ?.querySelector('[data-role-reply-status]');
      if (region) region.textContent = message;
      else this.view = {...this.view, message};
    };
    if (!postID.test(targetId)) return;
    if (!entry) {
      this.view = {
        ...this.view,
        message: 'The role’s reply is not visible here. Clear the filters to check the full conversation.',
      };
      return;
    }
    this.openDrawer(key);
    say('Looking for the role’s reply…');
    for (let pages = 0; pages < 25; pages++) {
      await this.loadReplies(entry, pages > 0);
      if (!this.live(serial) || !this.openDrawers.has(key)) return;
      const thread = this.threads.get(key);
      if (!thread || thread.message) return say(thread?.message || 'The role’s reply could not be read here.');
      if (thread.items.some((item) => item.postId === targetId)) {
        this.requestUpdate();
        await this.updateComplete;
        if (!this.live(serial) || !this.openDrawers.has(key)) return;
        const row = this.querySelector('#post-' + targetId);
        if (row instanceof HTMLElement) {
          row.scrollIntoView({block: 'center'});
          row.setAttribute('tabindex', '-1');
          row.focus({preventScroll: true});
        }
        return say('');
      }
      if (!thread.next) break;
    }
    say('The role’s reply is not in this thread. It may have been removed, or older replies need Show more.');
  }

  private openDrawer(key: string) {
    this.openDrawers.add(key);
    const entry = this.entryForKey(key);
    if (entry && !this.threads.has(key)) void this.loadReplies(entry);
    this.requestUpdate();
  }

  // A saved handoff is recovered by reading its source-bound draft receipt.
  // Only a fresh open or an explicit saved retry may create the draft.
  private async reviewProductChange(post: string, revision: number, repository: string, mode: 'open' | 'recover' | 'retry' = 'open') {
    const context = this.current, binding = this.lifetime.signal;
    if (!context || this.accessFailure || !postID.test(post) || !id(repository) ||
      (!context.global && context.repository !== repository) || !Number.isSafeInteger(revision) || revision < 0 ||
      this.view.postBusy.includes(post)) return;
    this.view = {...this.view, postBusy: [...this.view.postBusy, post]};
    try {
      if (!id(context.account) && !this.viewerID) await this.readViewerID(context);
      if (!this.bindingLive(context, binding)) return;
      const address = this.reviewAddress(repository, post);
      check(address);
      const stored = readReview(address);
      if (stored.blocked) {
        this.view = {...this.view, message: 'The saved change review cannot be read safely. Its bytes are kept; check storage and try again.'};
        return;
      }
      let pending = stored.pending;
      if (mode !== 'open' && !pending) {
        this.view = {...this.view, message: 'No saved change review was found. No review was submitted.'};
        return;
      }
      const path = 'factory/feed/posts/' + post;
      let source = feedReviewSource(await this.readJSON(context, path), post, repository);
      if (!this.bindingLive(context, binding)) return;
      const captured: PendingReview = {revision: pending?.revision ?? revision,
        intent: source.change.intent, acceptedRevision: source.change.acceptedRevision};
      check(source.revision === captured.revision && (!pending || sameReview(pending, captured)));
      if (source.change.state === 'unavailable') {
        this.view = {...this.view, message: 'The proposed change detail is currently unavailable. Check progress to reload it.'};
        return;
      }
      if (!source.change.canReview) {
        this.view = {...this.view, message: 'Current access to the request owner’s review is required. Any saved change review is kept.'};
        return;
      }
      if (source.change.state === 'review_required') {
        if (pending && mode !== 'retry') {
          this.view = {...this.view, message: 'No recorded review draft was confirmed. The saved handoff is kept; explicitly retry it to submit the original review.'};
          return;
        }
        pending = pending ?? captured;
        const retained = retainReview(address, pending);
        if (retained !== 'saved') {
          this.view = {...this.view, message: retained === 'unavailable'
            ? 'The review is kept in this window but could not be saved for recovery. Enable storage, then retry the saved change review. Nothing was submitted.'
            : 'Another or unreadable change review is saved. Its record is kept; check it before submitting.'};
          return;
        }
        const current = readReview(address);
        check(current.durable && current.pending && !current.blocked && sameReview(current.pending, pending));
        source = feedReviewSource(await this.readJSON(context, path + '/review', {
          method: 'POST', headers: {'Content-Type': 'application/json'}, body: JSON.stringify({revision: pending.revision}),
        }), post, repository);
        if (!this.bindingLive(context, binding)) return;
        check(source.revision === pending.revision && source.change.intent === pending.intent && source.change.acceptedRevision === pending.acceptedRevision);
      }
      const change = source.change;
      check(change.canReview && (change.state === 'draft' || change.state === 'accepted') && decisionID(change.draftRevision));
      if (pending) check(sameReview(pending, {revision: source.revision, intent: change.intent, acceptedRevision: change.acceptedRevision}));
      const places = this.placesFor(repository);
      check(places);
      if (pending) check(settleReview(address, pending));
      this.view = {...this.view, message: change.state === 'accepted' ? 'Opening the accepted change.' : 'Change saved for review. Review and accept the direction in the same build request.'};
      this.go(places.request(change.intent));
      if (this.bindingLive(context, binding)) void this.readLatest(false);
    } catch {
      if (this.bindingLive(context, binding)) this.view = {...this.view, message:
        'This change could not be opened. Refresh the conversation to check its current direction and your access; your text is kept.'};
    } finally {
      if (this.bindingLive(context, binding)) this.view = {...this.view, postBusy: this.view.postBusy.filter(row => row !== post)};
    }
  }

  // openRevision loads one request for revising and restores any unsent
  // text kept for it.
  private async openRevision(post: string, messageKind: 'post' | 'reply', repository: string) {
    const context = this.current,
      serial = this.serial;
    if (!context || !id(repository) || (!context.global && context.repository !== repository)) return;
    const editor = ++this.revisionEditor;
    try {
      const data = feedRevisionSource(await this.readJSON(context, `factory/feed/posts/${post}`), post, repository);
      if (!id(context.account) && !this.viewerID) await this.readViewerID(context);
      if (!this.live(serial) || editor !== this.revisionEditor) return;
      const edit: Revising = {
        id: data.post.postId,
        messageKind: data.post.parentId ? 'reply' : messageKind,
        revision: data.revision,
        repository: data.repository,
        body: data.post.body,
        baseBody: data.post.body,
        blocked: !data.canRetry,
        address: this.revisionAddress(repository, post),
      };
      this.syncRevision(edit);
      if (!data.canRetry && !edit.pending && !edit.recoveryBlocked) return;
      if (data.route === 'clarify') {
        edit.original = edit.body;
        edit.body = '';
        edit.question =
          typeof data.detail === 'string' && data.detail
            ? data.detail
            : 'What should change, and where should it apply?';
      }
      try {
        const saved = object(JSON.parse(readStored(this.revisionKey(edit)) || 'null'));
        if (typeof saved.body === 'string') edit.body = saved.body;
        if (typeof saved.original === 'string') edit.original = saved.original;
      } catch {
        /* Nothing kept for this request. */
      }
      // The submitted snapshot is recoverable even when a draft write failed.
      if (edit.pending && inspectStored(this.revisionKey(edit)).value === '') {
        edit.body = edit.pending.writing.body;
        if (edit.pending.writing.original === undefined) delete edit.original;
        else edit.original = edit.pending.writing.original;
      }
      this.revising = edit;
      this.requestUpdate();
      await this.updateComplete;
      if (this.live(serial) && editor === this.revisionEditor)
        this.querySelector<HTMLElement>('[data-revision-body]')?.focus();
    } catch {
      if (this.live(serial) && editor === this.revisionEditor)
        this.view = {...this.view, message: 'Could not load this request. Refresh and try again.'};
    }
  }

  // Receipt recovery is an explicit read. It never retries an effect on mount.
  private async recoverRevision() {
    const edit = this.revising, context = this.current, binding = this.lifetime.signal,
      editor = this.revisionEditor;
    if (!edit || !context || this.accessFailure || this.view.busy) return;
    this.syncRevision(edit);
    if (!edit.pending || !edit.address) {
      this.view = {...this.view, message: edit.recoveryBlocked
        ? 'The saved edit cannot be read safely. Its bytes and your writing are kept. Check storage and try again.'
        : 'No saved edit was found. Your writing is kept.'};
      return;
    }
    const pending = edit.pending, address = edit.address;
    this.view = {...this.view, busy: true};
    try {
      const digest = await feedRevisionBodyDigest(pending.body);
      if (!this.bindingLive(context, binding)) return;
      const response = await this.request(context, 'factory/feed/posts/' + address.post + '/retry/' + pending.command + '?repository=' + address.repository);
      const value = await readSodaJSON(response);
      if (!this.bindingLive(context, binding)) return;
      check(response.ok && object(value).author === address.author);
      const revision = feedRevisionReceipt(value, address.post, address.repository, pending.command, pending.revision, digest, pending.body === pending.baseBody);
      this.finishRevision(edit, pending, revision, editor);
      void this.readLatest(false);
    } catch {
      if (this.bindingLive(context, binding)) this.view = {...this.view, message:
        'The saved edit could not be confirmed. Its exact request and your writing are kept; check again or explicitly retry the saved edit.'};
    } finally {
      if (this.bindingLive(context, binding)) this.view = {...this.view, busy: false};
    }
  }

  private finishRevision(edit: Revising, pending: PendingRevision, revision: number, editor: number) {
    if (!edit.address || !settleRevision(edit.address, pending)) {
      if (this.revising) this.syncRevision(this.revising);
      this.view = {...this.view, message: 'The saved edit changed. Its record and current writing are kept; reopen it to review.'};
      return;
    }
    const live = this.revising;
    if (live?.id === edit.id) this.syncRevision(live);
    if (editor !== this.revisionEditor) {
      this.view = {...this.view, message: 'An earlier edit finished. Current writing is kept; refresh its post to check the result.'};
      return;
    }
    const newer = live?.id === edit.id && !sameRevisionWriting(live, pending.writing);
    // Do not erase writing replaced in another component/document while awaiting
    // the receipt. A newer head read also cannot be rebased backwards by recovery.
    const writing = inspectStored(this.revisionKey(edit)).value;
    const originalWriting = JSON.stringify(pending.writing);
    const replaced = writing === undefined || writing !== '' && writing !== originalWriting;
    if (newer && live) {
      this.revising = {...live, revision: Math.max(live.revision, revision),
        baseBody: live.revision > revision ? live.baseBody : pending.body};
      delete this.revising.current;
      // Persist only if the stored draft still belongs to this live editor.
      if (!replaced || writing === JSON.stringify({body: live.body, ...(live.original === undefined ? {} : {original: live.original})})) this.persistRevising();
    } else {
      if (!replaced) writeStored(this.revisionKey(edit), '');
      if (live?.id === edit.id) this.revising = undefined;
    }
    this.view = {...this.view, message: newer || replaced
      ? 'Request updated. Processing will continue here. Your newer edits are kept, ready to send.'
      : 'Request updated. Processing will continue here.'};
  }

  // Saved retry is a separate explicit action: it sends the retained body and
  // CAS unchanged, never the newer writing currently shown in the editor.
  private async revisePost(saved = false) {
    const edit = this.revising, context = this.current, binding = this.lifetime.signal,
      editor = this.revisionEditor;
    if (!edit || !context || this.accessFailure || this.view.busy) return;
    this.syncRevision(edit);
    if (!edit.address || edit.recoveryBlocked || (!saved && (edit.pending || edit.blocked || !edit.body.trim()))) return;
    let pending = edit.pending;
    const firstAttempt = !pending;
    if (saved && !pending) return;
    if (!pending) {
      const body = revisionBody(edit);
      if ([...body].length > 4000) {
        this.view = {...this.view, message: 'Keep the original request and answer within 4,000 characters. You can shorten the original request below.'};
        return;
      }
      pending = {command: newIdempotencyKey(), revision: edit.revision, body, baseBody: edit.baseBody,
        writing: {body: edit.body, ...(edit.original === undefined ? {} : {original: edit.original})}};
    }
    const command = pending, address = edit.address;
    const retained = retainRevision(address, command);
    this.syncRevision(edit);
    if (retained !== 'saved') {
      this.view = {...this.view, message: retained === 'unavailable'
        ? 'The exact edit is kept in this window, but could not be saved for recovery. Enable storage, then retry the saved edit. Nothing was sent.'
        : 'Another or unreadable edit is already saved. Its record and your writing are kept. Check the saved edit first.'};
      return;
    }
    this.view = {...this.view, busy: true};
    try {
      const digest = await feedRevisionBodyDigest(command.body);
      if (!this.bindingLive(context, binding)) return;
      const current = readRevision(address);
      check(!current.blocked && current.durable && current.pending && samePendingRevision(current.pending, command));
      const response = await this.request(context, 'factory/feed/posts/' + edit.id + '/retry', {
        method: 'POST', headers: {'Content-Type': 'application/json'},
        body: JSON.stringify({command_id: command.command, repository: address.repository, expected_revision: command.revision, body: command.body}),
        signal: AbortSignal.timeout(15000),
      });
      if (!this.bindingLive(context, binding)) return;
      if (response.status === 409) {
        // A newly minted command's explicit refusal is known not to have been
        // committed. An uncertain saved command must keep its original identity.
        if (firstAttempt) settleRevision(address, command);
        const source = feedRevisionSource(await this.readJSON(context, 'factory/feed/posts/' + edit.id), edit.id, edit.repository);
        if (!this.bindingLive(context, binding)) return;
        if (editor === this.revisionEditor && this.revising?.id === edit.id) {
          this.revising = {...this.revising, revision: source.revision, baseBody: source.post.body, current: source.post.body, blocked: !source.canRetry};
          this.syncRevision(this.revising);
        }
        this.view = {...this.view, message: 'This request could not be updated against its reviewed state. Current text is shown; your draft is kept.'};
        return;
      }
      const value = await readSodaJSON(response);
      if (!this.bindingLive(context, binding)) return;
      check(response.ok && object(value).author === address.author);
      const revision = feedRevisionReceipt(value, edit.id, edit.repository, command.command, command.revision, digest, command.body === command.baseBody);
      this.finishRevision(edit, command, revision, editor);
      void this.readLatest(false);
    } catch {
      if (this.bindingLive(context, binding)) this.view = {...this.view, message:
        'The edit could not be confirmed. Its exact request and your writing are kept; check the saved edit before retrying.'};
    } finally {
      if (this.bindingLive(context, binding)) this.view = {...this.view, busy: false};
    }
  }

  private onFeedClick = (event: Event) => {
    const serial = this.serial;
    if (
      event instanceof MouseEvent &&
      !event.defaultPrevented &&
      event.button === 0 &&
      !event.metaKey &&
      !event.ctrlKey &&
      !event.shiftKey &&
      !event.altKey
    ) {
      const anchor = event.target instanceof Element ? event.target.closest<HTMLAnchorElement>('a[href]') : null;
      const href = anchor?.getAttribute('href');
      if (
        anchor &&
        href?.startsWith('/') &&
        !href.startsWith('//') &&
        !anchor.hasAttribute('download') &&
        (!anchor.target || anchor.target === '_self')
      ) {
        event.preventDefault();
        this.go(href);
        return;
      }
    }
    const target = event.target;
    if (!(target instanceof HTMLElement)) return;
    const closest = (selector: string) => target.closest<HTMLElement>(selector);
    const parentId = closest('[data-reply-compose]')?.dataset.replyCompose;
    if (parentId) {
      this.replyingTo.add(parentId);
      this.requestUpdate();
      void this.updateComplete.then(() => {
        if (this.live(serial))
          this.querySelector<HTMLElement>(`[data-feed-draft="post:${CSS.escape(parentId)}"]`)?.focus();
      });
      return;
    }
    const cancelReply = closest('[data-reply-cancel]')?.dataset.replyCancel;
    if (cancelReply) {
      this.replyingTo.delete(cancelReply);
      this.requestUpdate();
      return;
    }
    const productChange = closest('[data-post-product-change]');
    const savedReview = closest('[data-post-review-recover], [data-post-review-retry]');
    if (savedReview) {
      void this.reviewProductChange(savedReview.dataset.postReviewRecover ?? savedReview.dataset.postReviewRetry ?? '', 0,
        savedReview.dataset.postRepository ?? '', savedReview.hasAttribute('data-post-review-retry') ? 'retry' : 'recover');
      return;
    }
    if (productChange) {
      void this.reviewProductChange(
        productChange.dataset.postProductChange ?? '',
        Number(productChange.dataset.productRevision),
        productChange.dataset.productRepository ?? ''
      );
      return;
    }
    const drawer = closest('[data-feed-drawer-toggle]')?.dataset.feedDrawerToggle;
    if (drawer) {
      if (this.openDrawers.delete(drawer)) this.requestUpdate();
      else this.openDrawer(drawer);
      return;
    }
    const roleReply = closest('[data-role-reply-open]');
    if (roleReply) {
      event.preventDefault();
      void this.openRoleReply(roleReply);
      return;
    }
    const reviseButton = closest('[data-post-revise]');
    const revise = reviseButton?.dataset.postRevise;
    if (revise) {
      void this.openRevision(
        revise,
        reviseButton?.dataset.messageKind === 'reply' ? 'reply' : 'post',
        reviseButton?.dataset.postRepository ?? ''
      );
      return;
    }
    if (closest('[data-revision-cancel]')) {
      this.revisionEditor++;
      if (this.revising) {
        this.syncRevision(this.revising);
        if (!this.revising.pending && !this.revising.recoveryBlocked) writeStored(this.revisionKey(this.revising), '');
      }
      this.revising = undefined;
      this.requestUpdate();
      return;
    }
    if (closest('[data-revision-recover]')) {
      void this.recoverRevision();
      return;
    }
    if (closest('[data-revision-retry]')) {
      void this.revisePost(true);
      return;
    }
    if (closest('[data-revision-submit]')) {
      void this.revisePost();
      return;
    }
    const answer = closest('[data-feed-answer]');
    if (answer instanceof HTMLButtonElement && answer.dataset.feedAnswer) {
      const key = answer.dataset.feedAnswer,
        text = answer.dataset.feedAnswerText ?? '';
      const area = this.querySelector<HTMLTextAreaElement>(`[data-feed-draft="${CSS.escape(key)}"]`);
      if (area) {
        // Selecting a choice edits the draft; only Submit invokes JEV.
        area.value = (area.value.trim() ? `${area.value}\n${text}` : text).slice(0, 4000);
        this.openDrawer(key);
        area.dispatchEvent(new Event('input', {bubbles: true}));
        void this.updateComplete.then(() => {
          if (this.live(serial))
            this.querySelector<HTMLTextAreaElement>(`[data-feed-draft="${CSS.escape(key)}"]`)?.focus();
        });
      }
      return;
    }
    const submit = closest('[data-post-submit]')?.dataset.postSubmit;
    if (submit) {
      void this.submitPost(submit);
      return;
    }
  };

  // loadReplies reads one thread's newest page, or with `older` the page
  // below what is shown. A second caller joins the read in flight.
  private loadReplies(entry: FeedEntry, older = false): Promise<void> {
    const key = feedEntryKey(entry);
    const running = this.threadReads.get(key);
    if (running) return running;
    const run: Promise<void> = this.readThreadPage(entry, older).finally(() => {
      if (this.threadReads.get(key) === run) this.threadReads.delete(key);
    });
    this.threadReads.set(key, run);
    return run;
  }

  private async readThreadPage(entry: FeedEntry, older: boolean): Promise<void> {
    const context = this.current,
      source = postSourceForEntry(entry),
      serial = this.serial;
    if (!context || !source) return;
    const key = feedEntryKey(entry);
    const previous = this.threads.get(key);
    const before = older ? (previous?.next ?? 0) : 0;
    if (older && before === 0) return;
    this.threads.set(key, {
      items: previous?.items ?? [],
      next: previous?.next ?? 0,
      pagedOlder: false,
      ...previous,
      busy: !previous || older,
      message: '',
    });
    this.requestUpdate();
    try {
      const query = new URLSearchParams({
        repository: String(entry.repository),
      });
      if (before > 0) query.set('before', String(before));
      const result = object(await this.readJSON(context, `factory/feed/posts/${source.id}/replies?` + query));
      check(Array.isArray(result.items));
      // Pages arrive newest-first; threads read oldest-first.
      const page = result.items
        .map(parseFeedPostDetail)
        .filter((item) => !item.removed)
        .reverse();
      if (!this.live(serial)) return;
      const kept = older ? (this.threads.get(key)?.items ?? []) : [];
      const ids = new Set(page.map((item) => item.postId));
      this.threads.set(key, {
        items: [...page, ...kept.filter((item) => !ids.has(item.postId))],
        next: typeof result.next_before === 'number' ? result.next_before : 0,
        busy: false,
        message: '',
        pagedOlder: older,
      });
    } catch (error) {
      if (!this.live(serial)) return;
      const denied = error instanceof Error && (error as {status?: number}).status === 403;
      const failed = this.threads.get(key);
      this.threads.set(key, {
        items: failed?.items ?? [],
        next: failed?.next ?? 0,
        busy: false,
        pagedOlder: failed?.pagedOlder ?? false,
        message: denied && error instanceof Error ? error.message : 'Replies could not be loaded. Try Refresh replies.',
      });
    }
    this.requestUpdate();
  }

  protected render() {
    if (!this.live(this.serial)) return html``;
    const serial = this.serial;
    const confirmed = activityTimestamp(this.view.confirmedUnix);
    return html`<section
      class="soda-feed"
      aria-label="Feed"
      @click=${(event: Event) => {
        if (this.live(serial)) this.onFeedClick(event);
      }}
      @input=${(event: Event) => {
        if (this.live(serial)) this.onDraft(event);
      }}
    >
      ${this.current?.global ? '' : this.renderProjectPost()}
      <header class="soda-feed-toolbar">
        ${this.accessFailure ? '' : this.renderViews()}
        <span class="soda-feed-toolbar-actions">
          ${
            confirmed
              ? html`<span class="soda-feed-updated"
                  >Updated
                  <time datetime=${confirmed.iso} title=${confirmed.text}
                    >${shortTime(this.view.confirmedUnix)}</time
                  ></span
                >`
              : ''
          }
          <button
            type="button"
            class="ui button soda-feed-utility soda-feed-icon-button"
            aria-label="Refresh feed"
            title="Refresh feed"
            ?disabled=${this.view.busy}
            @click=${() => void this.reload()}
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
              <path d="M20 12a8 8 0 1 1-2.34-5.66M20 3v4h-4" />
            </svg>
          </button>
          ${
            this.ownPlaces()?.knowledge
              ? html`<a class="soda-feed-utility" href=${this.ownPlaces()?.knowledge?.() ?? ''}>Knowledge</a>`
              : ''
          }
        </span>
      </header>
      ${this.accessFailure ? html`<p role="status" data-feed-access>${this.accessMessage()}</p>` : this.renderFilters()}
      ${
        this.focusedEntry
          ? html`<p>
              Source conversation
              <button
                type="button"
                @click=${() => {
                  this.leaveFocusedPost();
                  // A post opened by its address goes back to the Feed's own address.
                  const places = this.focusPost ? this.ownPlaces() : undefined;
                  if (places) this.go(places.feed());
                }}
              >
                Back to feed
              </button>
            </p>`
          : ''
      }
      <p data-feed-message aria-live="polite">${this.view.message}</p>
      ${
        this.view.allowance.length > 0
          ? html`<p class="soda-feed-brief soda-feed-alert" role="alert" data-feed-allowance>
              ${allowanceHoldText(this.view.allowance)}
            </p>`
          : ''
      }
      ${this.view.readFailed && this.view.entries.length ? html`<p class="soda-feed-updated">Showing the last loaded activity. Refresh to check for updates.</p>` : ''}
      ${this.renderRevision()}
      ${
        this.view.pagedBack
          ? html`<button type="button" class="ui button" ?disabled=${this.view.busy} @click=${this.backToLatest}>
              Back to latest
            </button>`
          : ''
      }
      ${this.current?.global ? this.renderSubscription() : ''} ${this.renderList()}
    </section>`;
  }

  private renderViews() {
    const views = [
      ['all', 'All'],
      ['attention', 'Needs me'],
      ['decisions', 'Choices & approvals'],
      ['evidence', 'Evidence'],
    ];
    return html`<nav class="soda-feed-tabs" aria-label="Filter conversation">
      ${views.map(
        ([value, label]) => html`<button
          type="button"
          aria-pressed=${this.filters.view === value ? 'true' : 'false'}
          @click=${() => this.changeFilters({view: value ?? 'all'})}
        >
          ${label}
        </button>`
      )}
    </nav>`;
  }

  private renderFilters() {
    const roles = [
      'human',
      'issue_maker',
      'product_lead',
      'architect',
      'builder',
      'inspector',
      'fixer',
      'strategist',
      'test_pilot',
      'jev',
      'factory',
    ];
    return html`<div class="soda-feed-filters">
      <form
        @submit=${(event: SubmitEvent) => {
          event.preventDefault();
          if (event.currentTarget instanceof HTMLFormElement)
            this.changeFilters({search: String(new FormData(event.currentTarget).get('search') ?? '').trim()});
        }}
      >
        <span class="soda-feed-search">
          <svg
            viewBox="0 0 24 24"
            aria-hidden="true"
            fill="none"
            stroke="currentColor"
            stroke-width="1.8"
            stroke-linecap="round"
          >
            <circle cx="11" cy="11" r="6.5" />
            <path d="m16 16 4.5 4.5" />
          </svg>
          <input
            type="search"
            name="search"
            aria-label="Search conversation"
            placeholder="Search posts"
            maxlength="256"
            .value=${this.filters.search}
          />
        </span>
        <button type="submit" class="soda-visually-hidden">Search</button>
        <select
          aria-label="Filter by role"
          .value=${this.filters.role}
          @change=${(event: Event) => {
            if (event.target instanceof HTMLSelectElement) this.changeFilters({role: event.target.value});
          }}
        >
          <option value="">All authors</option>
          ${roles.map((role) => html`<option value=${role}>${role === 'human' ? 'People' : roleLabel(role)}</option>`)}
        </select>
        <label
          ><input
            type="checkbox"
            .checked=${this.filters.current}
            @change=${(event: Event) => {
              if (event.target instanceof HTMLInputElement) this.changeFilters({current: event.target.checked});
            }}
          />Current knowledge only</label
        >
      </form>
    </div>`;
  }

  private renderRevision() {
    const edit = this.revising;
    if (!edit) return '';
    return html`<section class="soda-feed-revision" aria-label=${`Edit ${edit.messageKind}`}>
      ${
        edit.current !== undefined
          ? html`<p role="alert">This request changed since you opened it. Its current text:</p>
              <blockquote>${edit.current}</blockquote>`
          : ''
      }
      ${
        edit.original !== undefined
          ? html`<details>
              <summary>Original request · edit if needed</summary>
              <label
                >Original request<textarea
                  data-revision-original
                  rows="4"
                  maxlength="4000"
                  .value=${edit.original}
                ></textarea>
              </label>
            </details>`
          : ''
      }
      <label
        >${edit.question ?? `Edit your ${edit.messageKind}`}<textarea
          data-revision-body
          rows="5"
          maxlength="4000"
          .value=${edit.body}
        ></textarea>
      </label>
      <p>Submitting reassesses this request and may start work using the project’s existing allowance.</p>
      ${edit.pending ? html`<p role="status">An earlier edit is saved but unconfirmed. Checking it sends no edit; retrying uses its original writing.</p>
        <blockquote>${edit.pending.body}</blockquote>` : ''}
      ${edit.recoveryBlocked ? html`<p role="status">The saved edit record or current account could not be read safely. Your writing and saved bytes are kept.</p>` : ''}
      ${edit.pending || edit.recoveryBlocked ? html`<button type="button" data-revision-recover ?disabled=${this.view.busy}>Check saved edit</button>` : ''}
      ${edit.pending ? html`<button type="button" data-revision-retry ?disabled=${this.view.busy || edit.recoveryBlocked}>Retry saved edit</button>` : ''}
      <button type="button" data-revision-submit ?disabled=${this.view.busy || edit.blocked || !!edit.pending || edit.recoveryBlocked}>
        ${edit.question ? 'Submit answer' : `Save ${edit.messageKind} and retry`}</button
      ><button type="button" data-revision-cancel>Cancel</button>
    </section>`;
  }

  private renderProjectPost() {
    const context = this.current;
    if (!context || !id(context.repository)) return html``;
    const key = this.projectPostKey();
    const draft = this.draftFor(key);
    return html`<div class="soda-feed-post-box">
      ${renderComposer({
        key,
        draft,
        label: 'New post',
        submitLabel: 'Post',
        submitAvailable: !this.accessFailure && draft.trim() !== '' && !this.view.postBusy.includes(key),
        collapsed: !this.postOpen && draft.trim() === '',
      })}
    </div>`;
  }

  private renderSubscription() {
    const subscription = this.view.subscription;
    if (this.accessFailure || !subscription || !this.current?.global) return html``;
    const candidates = [...this.view.labels.refs.entries()].sort((a, b) => compareRepositoryIDs(a[0], b[0]));
    // Until a subscription is saved, every candidate shows and is checked.
    const member = new Set(
      subscription.revision === 0 ? candidates.map(([repository]) => repository) : subscription.repositories
    );
    return html`${
        !this.view.labels.listedAll
          ? html`<p role="status">Some repositories could not be loaded. Refresh to try again.</p>`
          : ''
      }
      <details class="soda-feed-subscription">
        <summary>Choose repositories</summary>
        <form @submit=${this.save}>
          <fieldset>
            <legend>Followed repositories</legend>
            <input type="hidden" name="expected_revision" value=${String(subscription.revision)} />
            ${subscription.revision === 0 ? html`<p>Showing available repositories until you choose.</p>` : ''}
            ${candidates.map(
              ([repository, ref]) => html`<label
                ><input
                  type="checkbox"
                  name="repository"
                  value=${String(repository)}
                  .checked=${member.has(repository)}
                  ?disabled=${this.view.busy}
                />${ref.label}</label
              >`
            )}
          </fieldset>
          <fieldset>
            <legend>Show</legend>
            <select name="kind" aria-label="Activity type" ?disabled=${this.view.busy}>
              <option value="all" .selected=${subscription.kinds.length !== 1}>All activity</option>
              <option value="record" .selected=${subscription.kinds.length === 1 && subscription.kinds[0] === 'record'}>
                Worker activity
              </option>
              <option value="post" .selected=${subscription.kinds.length === 1 && subscription.kinds[0] === 'post'}>
                Posts and replies
              </option>
            </select>
          </fieldset>
          <button class="ui button" type="submit" ?disabled=${this.view.busy}>Save feed subscription</button>
        </form>
      </details>`;
  }

  private repositoryRef(repository: string) {
    const ref = this.view.labels.refs.get(repository);
    const places = this.placesFor(repository);
    return {
      id: repository,
      path: places?.repository ?? ref?.path,
      label: ref?.label ?? `repository ${repository}`,
      // A repository's own feed needs no link back to itself.
      current: !this.current?.global,
      ...(places ? {places} : {}),
    };
  }

  private renderList() {
    const entries = this.focusedEntry ? [this.focusedEntry] : this.view.entries;
    if (entries.length === 0) return this.renderEmptyFeed();
    // Small steps fold under the post they belong to; running work is one
    // post that updates in place.
    return html`<div class="soda-feed-list">
        ${repeat(
          threadFeed(entries, this.focusedEntry !== undefined),
          (item) => feedEntryKey(item.entry),
          (item) => {
            const key = feedEntryKey(item.entry);
            const post = describeFeedPost(
              item.entry,
              this.repositoryRef(item.entry.repository),
              item,
              this.draftFor(key),
              describeReplies(key, this.openDrawers.has(key), this.threads.get(key)),
              this.view.postBusy
            );
            return html`${renderFeedPost(
              post,
              this.renderReplies(item.entry),
              this.artifactContext(item.entry.repository),
              this.current?.transport.assetBase,
              this.cardLinks(item.entry)
            )}${item.entry.kind === 'post' ? html`${this.renderRevisionRecovery(item.entry.id, item.entry.repository, 'post')}${this.renderReviewRecovery(item.entry.id, item.entry.repository)}` : ''}`;
          }
        )}
      </div>
      ${
        this.view.truncated && this.view.next !== ''
          ? html`<button
              type="button"
              class="ui button"
              ?disabled=${this.view.busy}
              @click=${() => void this.readOlder()}
            >
              Load older entries
            </button>`
          : ''
      }`;
  }

  private renderEmptyFeed() {
    if (this.view.busy) return html`<section class="soda-feed-empty" role="status"><p>Loading activity…</p></section>`;
    if (this.view.readFailed)
      return html`<section class="soda-feed-empty" aria-label="Feed unavailable">
        <h3>Activity could not be loaded.</h3>
        <p>Your draft is kept. Try loading the feed again.</p>
        <button type="button" class="ui button" @click=${() => void this.reload()}>Retry loading activity</button>
      </section>`;
    const filtered =
      this.filters.view !== 'all' || this.filters.search !== '' || this.filters.role !== '' || this.filters.current;
    if (filtered)
      return html`<section class="soda-feed-empty" aria-label="No matching posts">
        <h3>No matching posts.</h3>
        <p>Try another search or clear the filters to see all activity in this feed.</p>
        <button
          type="button"
          class="ui button"
          @click=${() => this.changeFilters({view: 'all', search: '', role: '', current: false})}
        >
          Clear filters
        </button>
      </section>`;
    return html`<section class="soda-feed-empty" aria-label="No activity">
      <h3>No activity yet.</h3>
      <p>
        ${this.current?.global ? 'Posts and replies from your followed projects will appear here.' : 'Questions, updates and decisions will appear here as they are recorded. Start a conversation with a post above.'}
      </p>
    </section>`;
  }

  private renderReplies(entry: FeedEntry): TemplateResult {
    if (!postSourceForEntry(entry)) return html``;
    const key = feedEntryKey(entry),
      thread = this.threads.get(key);
    if (!thread) return html`<p role="status" class="soda-feed-thread-status">Loading replies…</p>`;
    // Consecutive JEV calls for one decision fold into one group.
    const groups: {key: string; items: FeedPostDetail[]}[] = [];
    for (const reply of thread.items) {
      const previous = groups.at(-1),
        prior = previous?.items.at(-1)?.consultation;
      if (
        reply.consultation &&
        prior &&
        reply.consultation.group_id === prior.group_id &&
        reply.consultation.assignment === prior.assignment
      )
        previous?.items.push(reply);
      else groups.push({key: reply.postId, items: [reply]});
    }
    return html`<div class="soda-feed-thread">
      <p class="soda-feed-thread-head">
        <span role="status"
          >${thread.busy ? 'Loading replies…' : thread.message || (thread.items.length ? '' : 'No replies yet.')}</span
        >
        <button
          type="button"
          class="soda-feed-icon-button"
          aria-label="Refresh replies"
          title="Refresh replies"
          ?disabled=${thread.busy}
          @click=${() => void this.loadReplies(entry)}
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
            <path d="M20 12a8 8 0 1 1-2.34-5.66M20 3v4h-4" />
          </svg>
        </button>
      </p>
      <ul class="soda-feed-replies">
        ${repeat(
          groups,
          (group) => group.key,
          (group) =>
            group.items.length > 1
              ? html`<li class="soda-feed-consultation-group">
                  <details>
                    <summary>
                      ${group.items[0]?.authorName || 'Worker'} · ${group.items.length} recorded JEV calls for this
                      decision
                    </summary>
                    <ul class="soda-feed-replies">
                      ${repeat(
                        group.items,
                        (reply) => reply.postId,
                        (reply) => this.renderReply(entry, reply)
                      )}
                    </ul>
                  </details>
                </li>`
              : this.renderReply(entry, group.items[0]!)
        )}
      </ul>
      ${thread.next ? html`<button type="button" class="soda-feed-more" ?disabled=${thread.busy} @click=${() => void this.loadReplies(entry, true)}>Show more</button>` : ''}
    </div>`;
  }

  private renderReply(entry: FeedEntry, reply: FeedPostDetail): TemplateResult {
    const portrait = feedRolePortraits[roleLabel(reply.actor) ?? ''];
    const issueHref =
      reply.actor === 'issue_maker' && reply.issueScope
        ? this.placesFor(entry.repository)?.issue(reply.issueScope)
        : undefined;
    const key = 'post:' + reply.postId;
    const thread = this.threads.get(feedEntryKey(entry));
    const parent =
      thread?.items.find((post) => post.postId === reply.parentId) ??
      (entry.id === reply.parentId
        ? {postId: entry.id, authorName: entry.author || entry.post?.authorName}
        : undefined);
    return html`<li class="soda-feed-reply" id=${'post-' + reply.postId} data-role=${reply.actor ?? 'human'}>
      <span class="soda-feed-avatar" aria-hidden="true"
        >${
          portrait
            ? html`<img src=${(this.current?.transport.assetBase ?? '') + portrait} alt="" width="30" height="30" />`
            : (reply.authorName || '?').slice(0, 1).toUpperCase()
        }</span
      >
      <div class="soda-feed-reply-main">
        <p class="soda-feed-reply-who">
          <strong>${reply.authorName || 'Reply'}</strong>
          <time datetime=${new Date(reply.at * 1000).toISOString()} title=${new Date(reply.at * 1000).toLocaleString()}
            >· ${relativeReplyTime(reply.at)}</time
          >
          ${reply.edited ? html` <span class="soda-feed-reply-edited">(edited)</span>` : ''}
        </p>
        ${parent ? html`<p class="soda-feed-reply-parent">Reply to <a href=${'#post-' + parent.postId}>${parent.authorName || 'post'}</a></p>` : ''}
        ${issueHref ? html`<p><a href=${issueHref}>Open issue #${reply.issueScope}: ${reply.communication?.title || 'Created issue'}</a></p>` : ''}
        ${reply.body && (reply.contribution || reply.consultation) ? html`<p class="soda-feed-body-text">${reply.body}</p>` : ''}
        ${
          reply.contribution
            ? renderContribution(reply.contribution, {entryKey: feedEntryKey(entry)})
            : reply.consultation
              ? renderConsultation(reply.consultation)
              : reply.communication
                ? renderFeedCommunication(
                    reply.communication,
                    'post:' + reply.postId,
                    this.artifactContext(entry.repository)
                  )
                : html`<p class="soda-feed-body-text">${reply.body}</p>`
        }
        ${
          reply.pipeline
            ? renderOutcome(
                describePostOutcome(
                  reply.pipeline,
                  this.repositoryRef(entry.repository),
                  this.view.postBusy.includes(reply.postId),
                  'reply'
                ),
                this.current?.transport.assetBase
              )
            : ''
        }
        <button type="button" data-reply-compose=${reply.postId}>Reply</button>
        ${this.renderRevisionRecovery(reply.postId, entry.repository, 'reply')}
        ${this.renderReviewRecovery(reply.postId, entry.repository)}
        <button type="button" data-feed-drawer-toggle=${key} aria-expanded=${this.openDrawers.has(key)}>Replies</button>
        ${this.openDrawers.has(key) ? this.renderReplies(this.entryForKey(key)!) : ''}
        ${
          this.replyingTo.has(reply.postId)
            ? html`${renderComposer({
                  key,
                  draft: this.draftFor(key),
                  label: `Reply to ${reply.authorName || 'post'}`,
                  compact: true,
                  submitAvailable: this.draftFor(key).trim() !== '' && !this.view.postBusy.includes(key),
                })}<button type="button" data-reply-cancel=${reply.postId}>Cancel reply</button>`
            : ''
        }
      </div>
    </li>`;
  }
}

customElements.define('soda-factory-feed', SodaFactoryFeed);

declare global {
  interface HTMLElementTagNameMap {
    'soda-factory-feed': SodaFactoryFeed;
  }
}
