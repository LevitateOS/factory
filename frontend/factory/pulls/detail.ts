import {LitElement, html, type PropertyValues} from 'lit';
import {guard} from 'lit/directives/guard.js';
import {check, id} from '../api/models.js';
import type {ChangedFile} from '../api/collaboration.js';
import type {FactoryTransport} from '../api/transport.js';
import {getJSON, FactoryReadError} from '../api/read.js';
import {issueMarkdown, markdownLink} from '../shared/markdown.js';
import '../review/diff.js';
import './review.js';
import './merge.js';
import './inline.js';
import './thread-resolution.js';
import '../issues/comment.js';
import type {DiffThread} from '../review/diff.js';
import {pullItem, pullFiles, pullChecks, pullReviews, pullThreads, pullThreadComments, pullComments, type PullItem, type PullThreadView} from './response.js';

export interface PullDetailContext {
  readonly transport: FactoryTransport;
  readonly host: string;
  readonly repository: string;
  readonly pull: string;
  readonly actor: string;
  readonly scope: string;
  readonly initialTab?: Tab;
  readonly commandScope?: (signal: AbortSignal) => Promise<string>;
  resolveLink(href: string): string | undefined;
  openLink(url: string, signal: AbortSignal): void | Promise<void>;
}
type Tab = 'conversation' | 'reviews' | 'threads' | 'files' | 'checks';
const empty = () => ({
  busy: false, message: '', pull: undefined as PullItem | undefined,
  files: [] as readonly ChangedFile[], threads: [] as readonly PullThreadView[],
  comments: [] as ReturnType<typeof pullComments>['items'],
  reviews: [] as ReturnType<typeof pullReviews>['items'],
  checks: [] as ReturnType<typeof pullChecks>['items'], gate: '',
  next: '', threadNext: '', threadMessage: '', threadBusy: false,
  diff: undefined as {base: string; head: string} | undefined,
});

/** Current PR read, then one bounded page of the selected conversation/files/checks. */
export class SodaFactoryPull extends LitElement {
  static properties = {view: {state: true}, tab: {state: true}};
  declare private view: ReturnType<typeof empty>;
  declare private tab: Tab;
  private checkKind: 'status' | 'job' = 'status';
  private current: PullDetailContext | undefined;
  private binding = '';
  private serial = 0;
  private cursor = '';
  private previous: string[] = [];
  private lifetime = new AbortController();
  private read = new AbortController();

  constructor() {
    super();
    this.view = empty();
    this.tab = 'conversation';
    this.addEventListener('auxclick', event => void this.open(event));
  }
  protected createRenderRoot() { return this; }
  get context() { return this.current; }
  set context(context: PullDetailContext | undefined) {
    if (context === this.current && (!context || this.key(context) === this.binding)) return;
    this.retire();
    if (!context) return;
    check(id(context.repository) && id(context.pull) && id(context.actor) && /^[a-f0-9]{32}$/.test(context.host));
    check(typeof context.scope === 'string' && !!context.scope.trim() && context.scope.length <= 1024);
    check(!/[\u0000-\u001f\u007f]/.test(context.scope) && !!context.transport.generation);
    check(typeof context.resolveLink === 'function' && typeof context.openLink === 'function');
    this.current = context;
    this.binding = this.key(context);
    this.tab = context.initialTab ?? 'conversation';
    this.requestUpdate('context');
  }
  protected willUpdate(changed: PropertyValues) {
    if (changed.has('context') && this.current) void this.load();
  }
  disconnectedCallback() {
    this.context = undefined;
    super.disconnectedCallback();
  }
  private key(context: PullDetailContext) {
    return JSON.stringify([context.host, context.repository, context.pull, context.actor, context.scope, context.transport.generation]);
  }
  private retire() {
    this.lifetime.abort(); this.read.abort();
    this.lifetime = new AbortController(); this.read = new AbortController();
    this.serial++; this.current = undefined; this.binding = ''; this.cursor = ''; this.previous = [];
    this.tab = 'conversation'; this.checkKind = 'status'; this.view = empty();
  }
  private live(context: PullDetailContext, serial = this.serial) {
    if (this.current !== context || this.serial !== serial || this.lifetime.signal.aborted) return false;
    if (this.key(context) !== this.binding) { this.retire(); return false; }
    return true;
  }
  refresh() { this.cursor = ''; this.previous = []; return this.load(); }
  private select(tab: Tab) {
    if (tab === this.tab || this.view.busy) return;
    this.tab = tab; this.cursor = ''; this.previous = [];
    void this.load();
  }
  private selectChecks(kind: 'status' | 'job') {
    if (this.checkKind === kind || this.view.busy) return;
    this.checkKind = kind; this.cursor = ''; this.previous = [];
    void this.load();
  }
  private async load() {
    const context = this.current;
    if (!context || !this.live(context)) return;
    this.read.abort(); this.read = new AbortController();
    const serial = ++this.serial, signal = AbortSignal.any([this.lifetime.signal, this.read.signal]);
    const cursor = this.cursor, tab = this.tab;
    const path = `factory/collaboration/repositories/${context.repository}/pulls/${context.pull}`;
    const prior = this.view.pull, priorDiff = this.view.diff;
    this.view = {...this.view, busy: true, message: ''};
    try {
      const pull = pullItem(await getJSON(context.transport, path, signal), context.host, context.repository, context.pull);
      if (!this.live(context, serial)) return;
      check(!prior || prior.ref.external_id === pull.ref.external_id);
      const query = new URLSearchParams({limit: tab === 'files' ? '10' : '20'});
      if (cursor) query.set('cursor', cursor);
      if (tab === 'files') {
        const base = cursor ? priorDiff?.base : undefined, head = cursor ? priorDiff?.head : undefined;
        if (cursor) check(base && head && prior?.head.commit === pull.head.commit);
        if (base && head) { query.set('base', base); query.set('head', head); }
        const page = pullFiles(await getJSON(context.transport, path + '/files?' + query, signal), context.repository, context.pull, base || '', head || '', cursor);
        if (!this.live(context, serial)) return;
        this.view = {...empty(), pull, files: page.items, next: page.next, diff: page.diff};
        void this.loadMoreThreads('');
      } else if (tab === 'checks') {
        const head = pull.head.commit;
        if (!head) { this.view = {...empty(), pull, message: 'The current head commit is unavailable.'}; return; }
        query.set('head', head);
        const kind = this.checkKind;
        const suffix = kind === 'job' ? '/checks/jobs?' : '/checks?';
        const page = pullChecks(await getJSON(context.transport, path + suffix + query, signal), context.repository, context.pull, head, kind, cursor);
        if (!this.live(context, serial)) return;
        this.view = {...empty(), pull, checks: page.items, gate: page.gate, next: page.next};
      } else if (tab === 'conversation') {
        const raw = await getJSON(context.transport, path + '/comments?' + query, signal);
        if (!this.live(context, serial)) return;
        const comments = pullComments(raw, context.repository, context.pull, cursor);
        this.view = {...empty(), pull, comments: comments.items, next: comments.next};
      } else if (tab === 'threads') {
        const threadsRaw = await getJSON(context.transport, path + '/threads?' + query, signal);
        if (!this.live(context, serial)) return;
        const threads = pullThreads(threadsRaw, context.repository, context.pull, cursor);
        this.view = {...empty(), pull, threads: threads.items, next: threads.next};
      } else {
        const reviewsRaw = await getJSON(context.transport, path + '/reviews?' + query, signal);
        if (!this.live(context, serial)) return;
        const reviews = pullReviews(reviewsRaw, context.repository, context.pull, cursor);
        this.view = {...empty(), pull, reviews: reviews.items, next: reviews.next};
      }
    } catch (error) {
      if (!this.live(context, serial)) return;
      const hidden = error instanceof FactoryReadError && [401, 403, 404].includes(error.status);
      this.view = {...empty(), message: hidden ? 'This pull request is not available to this account.' : error instanceof FactoryReadError && error.status === 409 ? 'This pull request changed. Refresh to read the current revision.' : 'Pull request details could not be loaded. Try again.'};
    }
  }
  private async loadMoreThreads(cursor: string) {
    const context = this.current, serial = this.serial;
    if (!context || !this.live(context, serial) || this.tab !== 'files' || this.view.threadBusy || (cursor && cursor !== this.view.threadNext)) return;
    this.view = {...this.view, threadBusy: true, threadMessage: ''};
    const query = new URLSearchParams({limit: '20'});
    if (cursor) query.set('cursor', cursor);
    try {
      const raw = await getJSON(context.transport, `factory/collaboration/repositories/${context.repository}/pulls/${context.pull}/threads?${query}`, AbortSignal.any([this.lifetime.signal, this.read.signal]));
      if (!this.live(context, serial) || this.tab !== 'files') return;
      const page = pullThreads(raw, context.repository, context.pull, cursor);
      this.view = {...this.view, threads: [...this.view.threads, ...page.items], threadNext: page.next, threadBusy: false};
    } catch {
      if (this.live(context, serial) && this.tab === 'files') this.view = {...this.view, threadBusy: false, threadMessage: 'Inline discussions could not be loaded. Try again.'};
    }
  }
  private async loadThreadComments(thread: PullThreadView) {
    const context = this.current, serial = this.serial, cursor = thread.commentsNext;
    if (!context || !this.live(context, serial) || !cursor || this.view.busy || this.view.threadBusy) return;
    this.view = {...this.view, threadBusy: true, threadMessage: ''};
    const query = new URLSearchParams({limit: '20', cursor});
    try {
      const raw = await getJSON(context.transport, `factory/collaboration/repositories/${context.repository}/pulls/${context.pull}/threads/${encodeURIComponent(thread.externalID)}/comments?${query}`, AbortSignal.any([this.lifetime.signal, this.read.signal]));
      if (!this.live(context, serial)) return;
      const page = pullThreadComments(raw, context.repository, context.pull, cursor);
      this.view = {...this.view, threadBusy: false, threads: this.view.threads.map(item => item === thread ? {...item, comments: [...item.comments, ...page.items], commentsNext: page.next} : item)};
    } catch {
      if (this.live(context, serial)) this.view = {...this.view, threadBusy: false, threadMessage: 'More comments could not be loaded. Try again.'};
    }
  }
  private next() {
    if (this.view.busy || !this.view.next) return;
    this.previous.push(this.cursor); this.cursor = this.view.next; void this.load();
  }
  private back() {
    if (this.view.busy || !this.previous.length) return;
    this.cursor = this.previous.pop()!; void this.load();
  }
  private async open(event: MouseEvent) {
    if (event.button > 1) return;
    const anchor = event.composedPath().find(node => node instanceof HTMLAnchorElement);
    if (!(anchor instanceof HTMLAnchorElement) || !this.contains(anchor)) return;
    event.preventDefault();
    const context = this.current, serial = this.serial;
    if (!context || !this.live(context, serial) || !this.view.pull) return;
    const url = markdownLink(anchor.href);
    if (!url) return;
    try { await context.openLink(url, AbortSignal.any([this.lifetime.signal, this.read.signal])); }
    catch { if (this.live(context, serial)) this.view = {...this.view, message: 'This link could not be opened. Try again.'}; }
  }
  render() {
    const context = this.current, pull = this.view.pull;
    if (!context) return html``;
    return html`<section class="soda-pull-detail" aria-label="Pull request detail" aria-busy=${this.view.busy} @click=${this.open}>
      <header><h2>${pull?.title || 'Pull request'}</h2><button type="button" @click=${this.refresh} ?disabled=${this.view.busy}>Refresh pull request</button></header>
      <p role="status">${this.view.busy ? 'Loading pull request…' : this.view.message}</p>
      ${pull ? html`
        <p class="soda-pull-meta">#${pull.ref.number} · ${pull.state}${pull.draft ? ' · Draft' : ''} · ${pull.author?.login || 'Unknown author'} · ${pull.head.name} → ${pull.base.name}</p>
        <article aria-label="Pull request description" class="soda-markdown">${pull.body ? guard([pull.ref.external_id, pull.body, context.resolveLink], () => issueMarkdown(pull.body, href => context.resolveLink(href))) : html`<p>No description provided.</p>`}</article>
        <nav aria-label="Pull request sections">
          <button type="button" aria-current=${this.tab === 'conversation' ? 'page' : 'false'} @click=${() => this.select('conversation')}>Conversation</button>
          <button type="button" aria-current=${this.tab === 'reviews' ? 'page' : 'false'} @click=${() => this.select('reviews')}>Reviews</button>
          <button type="button" aria-current=${this.tab === 'threads' ? 'page' : 'false'} @click=${() => this.select('threads')}>Inline discussions</button>
          <button type="button" aria-current=${this.tab === 'files' ? 'page' : 'false'} @click=${() => this.select('files')}>Files</button>
          <button type="button" aria-current=${this.tab === 'checks' ? 'page' : 'false'} @click=${() => this.select('checks')}>Checks</button>
        </nav>
        ${this.tab === 'conversation' && context.commandScope ? html`<soda-factory-issue-comment
          .context=${{transport: context.transport, host: context.host, actor: context.actor,
            repository: context.repository, issue: context.pull, kind: 'pull' as const, scope: context.scope,
            parent: pull.ref, commandScope: context.commandScope}}
          @pull-comment-saved=${() => void this.refresh()}
        ></soda-factory-issue-comment>` : ''}
        ${this.tab === 'files' ? html`<section aria-label="Changed files">
          ${this.view.diff && this.view.diff.head !== pull.head.commit ? html`<p role="status">This diff uses a different head from the pull request summary. Refresh before reviewing.</p>` : ''}
          ${context.commandScope && pull.head.commit ? html`<soda-factory-pull-inline
            .context=${{transport: context.transport, host: context.host, actor: context.actor,
              repository: context.repository, pull: context.pull, scope: context.scope,
              parent: pull.ref, head: pull.head.commit, commandScope: context.commandScope}}
            .files=${this.view.files} .threads=${this.view.threads}
            @pull-inline-saved=${() => void this.refresh()}
          ></soda-factory-pull-inline>` : this.view.files.length ? this.view.files.map(file => {
            const threads: DiffThread[] = this.view.threads.filter(thread => {
              const anchor = thread.currentAnchor ?? thread.anchor;
              return anchor ? anchor.old_path === file.old_path && anchor.new_path === file.new_path
                : thread.recordedLocation?.path === file.old_path || thread.recordedLocation?.path === file.new_path;
            }).map(thread => ({
              external_id: thread.externalID, outdated: thread.outdated, resolution: thread.resolution,
              anchor: thread.currentAnchor ?? thread.anchor,
              recorded_location: thread.recordedLocation,
              comments: {items: thread.comments.map(comment => ({comment: {body: comment.body}})), next: thread.commentsNext},
            }));
            return html`<soda-factory-diff .file=${file} .threads=${threads} .allowDraft=${false}></soda-factory-diff>`;
          }) : ''}
          ${!this.view.files.length ? html`<p>No files on this page.</p>` : ''}
          <p role="status">${this.view.threadBusy ? 'Loading inline discussions…' : this.view.threadMessage}</p>
          ${this.view.threadNext ? html`<button type="button" ?disabled=${this.view.threadBusy} @click=${() => void this.loadMoreThreads(this.view.threadNext)}>Load more inline discussions</button>` : ''}
        </section>` : this.tab === 'checks' ? html`<section aria-label="Checks">
          <p>Status gate: ${this.view.gate || 'unavailable'}</p>
          <nav aria-label="Check types"><button type="button" aria-current=${this.checkKind === 'status' ? 'page' : 'false'} @click=${() => this.selectChecks('status')}>Statuses</button><button type="button" aria-current=${this.checkKind === 'job' ? 'page' : 'false'} @click=${() => this.selectChecks('job')}>Jobs</button></nav>
          <ul>${this.view.checks.map(row => html`<li><strong>${row.name}</strong> · ${row.state} · ${row.conclusion}${row.required ? ' · Required' : ''}<p>${row.workflow ? row.workflow + ' · ' : ''}${row.description}</p></li>`)}</ul>
          ${!this.view.checks.length ? html`<p>No checks on this page.</p>` : ''}
        </section>` : this.tab === 'conversation' ? html`<section aria-label="Conversation">
          <ol>${this.view.comments.map(comment => html`<li><strong>${comment.author}</strong>${comment.createdAt ? html` · <time datetime=${comment.createdAt}>${comment.createdAt.slice(0, 10)}</time>` : ''}<div class="soda-markdown">${guard([comment.externalID, comment.body, context.resolveLink], () => issueMarkdown(comment.body, href => context.resolveLink(href)))}</div></li>`)}</ol>
          ${!this.view.comments.length ? html`<p>No comments on this page.</p>` : ''}
        </section>` : this.tab === 'reviews' ? html`<section aria-label="Reviews">
          <ul>${this.view.reviews.map(review => html`<li>${review.author} · ${review.state}${review.stale ? ' · Stale' : ''}<p>${review.body}</p></li>`)}</ul>
          ${!this.view.reviews.length ? html`<p>No reviews on this page.</p>` : ''}
        </section>` : html`<section aria-label="Inline discussions"><ol>${this.view.threads.map(thread => html`<li>
            <strong>${thread.currentAnchor?.new_path || thread.currentAnchor?.old_path || thread.anchor?.new_path || thread.anchor?.old_path || 'Location unavailable'}</strong>
            ${thread.anchor?.lines ? html` · ${thread.anchor.side} line ${thread.anchor.lines.start}${thread.anchor.lines.end !== thread.anchor.lines.start ? `–${thread.anchor.lines.end}` : ''}` : ''}
            · ${thread.resolution}${thread.outdated ? ' · Outdated' : ''}
            <ol>${thread.comments.map(comment => html`<li>${comment.author}${comment.createdAt ? html` · <time datetime=${comment.createdAt}>${comment.createdAt.slice(0, 10)}</time>` : ''}<p>${comment.body}</p></li>`)}</ol>
            ${thread.commentsNext ? html`<button type="button" ?disabled=${this.view.threadBusy} @click=${() => void this.loadThreadComments(thread)}>Load more comments</button>` : ''}
            ${context.commandScope ? html`<soda-factory-thread-resolution
              .context=${{transport: context.transport, host: context.host, actor: context.actor,
                repository: context.repository, pull: context.pull, scope: context.scope,
                parent: pull.ref, threadID: thread.externalID, commandScope: context.commandScope}}
              @pull-thread-saved=${() => void this.refresh()}
            ></soda-factory-thread-resolution>` : ''}
          </li>`)}</ol>
          <p role="status">${this.view.threadMessage}</p>
          ${!this.view.threads.length ? html`<p>No inline discussions on this page.</p>` : ''}
        </section>`}
        <nav aria-label="Pull request pages"><button type="button" @click=${this.back} ?disabled=${this.view.busy || !this.previous.length}>Previous page</button><button type="button" @click=${this.next} ?disabled=${this.view.busy || !this.view.next}>Next page</button></nav>
        ${context.commandScope && pull.head.commit ? html`<soda-factory-pull-review
          .context=${{transport: context.transport, host: context.host, actor: context.actor,
            repository: context.repository, pull: context.pull, scope: context.scope,
            parent: pull.ref, head: pull.head.commit, commandScope: context.commandScope}}
          @pull-review-saved=${() => void this.refresh()}
        ></soda-factory-pull-review>` : ''}
        ${context.commandScope && pull.head.commit ? html`<soda-factory-pull-merge
          .context=${{transport: context.transport, host: context.host, actor: context.actor,
            repository: context.repository, pull: context.pull, scope: context.scope,
            parent: pull.ref, head: pull.head.commit, commandScope: context.commandScope}}
          @pull-merge-saved=${() => void this.refresh()}
        ></soda-factory-pull-merge>` : ''}
      ` : ''}
    </section>`;
  }
}
customElements.define('soda-factory-pull', SodaFactoryPull);
