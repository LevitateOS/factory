import {LitElement, html, type PropertyValues} from 'lit';
import {guard} from 'lit/directives/guard.js';
import {check, id} from '../api/models.js';
import type {ChangedFile} from '../api/collaboration.js';
import type {FactoryTransport} from '../api/transport.js';
import {getJSON, FactoryReadError} from '../api/read.js';
import {issueMarkdown, markdownLink} from '../shared/markdown.js';
import '../review/diff.js';
import {pullItem, pullFiles, pullChecks, pullReviews, pullThreads, type PullItem, type PullThreadView} from './response.js';

export interface PullDetailContext {
  readonly transport: FactoryTransport;
  readonly host: string;
  readonly repository: string;
  readonly pull: string;
  readonly actor: string;
  readonly scope: string;
  readonly initialTab?: 'reviews' | 'threads' | 'files' | 'checks';
  resolveLink(href: string): string | undefined;
  openLink(url: string, signal: AbortSignal): void | Promise<void>;
}
type Tab = 'reviews' | 'threads' | 'files' | 'checks';
const empty = () => ({
  busy: false, message: '', pull: undefined as PullItem | undefined,
  files: [] as readonly ChangedFile[], threads: [] as readonly PullThreadView[],
  reviews: [] as ReturnType<typeof pullReviews>['items'],
  checks: [] as ReturnType<typeof pullChecks>['items'], gate: '',
  next: '', diff: undefined as {base: string; head: string} | undefined,
});

/** Current PR read, then one bounded page of the selected conversation/files/checks. */
export class SodaFactoryPull extends LitElement {
  static properties = {view: {state: true}, tab: {state: true}};
  declare private view: ReturnType<typeof empty>;
  declare private tab: Tab;
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
    this.tab = 'reviews';
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
    this.tab = context.initialTab ?? 'reviews';
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
    this.tab = 'reviews'; this.view = empty();
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
  private async load() {
    const context = this.current;
    if (!context || !this.live(context)) return;
    this.read.abort(); this.read = new AbortController();
    const serial = ++this.serial, signal = AbortSignal.any([this.lifetime.signal, this.read.signal]);
    const cursor = this.cursor, tab = this.tab;
    const path = `factory/collaboration/repositories/${context.repository}/pulls/${context.pull}`;
    const prior = this.view.pull, priorDiff = this.view.diff;
    this.view = {...empty(), busy: true};
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
      } else if (tab === 'checks') {
        const head = pull.head.commit;
        if (!head) { this.view = {...empty(), pull, message: 'The current head commit is unavailable.'}; return; }
        query.set('head', head);
        const page = pullChecks(await getJSON(context.transport, path + '/checks?' + query, signal), context.repository, context.pull, head, cursor);
        if (!this.live(context, serial)) return;
        this.view = {...empty(), pull, checks: page.items, gate: page.gate, next: page.next};
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
          <button type="button" aria-current=${this.tab === 'reviews' ? 'page' : 'false'} @click=${() => this.select('reviews')}>Reviews</button>
          <button type="button" aria-current=${this.tab === 'threads' ? 'page' : 'false'} @click=${() => this.select('threads')}>Inline discussions</button>
          <button type="button" aria-current=${this.tab === 'files' ? 'page' : 'false'} @click=${() => this.select('files')}>Files</button>
          <button type="button" aria-current=${this.tab === 'checks' ? 'page' : 'false'} @click=${() => this.select('checks')}>Checks</button>
        </nav>
        ${this.tab === 'files' ? html`<section aria-label="Changed files">
          ${this.view.diff && this.view.diff.head !== pull.head.commit ? html`<p role="status">This diff uses a different head from the pull request summary. Refresh before reviewing.</p>` : ''}
          ${this.view.files.length ? this.view.files.map(file => html`<soda-factory-diff .file=${file} .threads=${[]} .allowDraft=${false}></soda-factory-diff>`) : html`<p>No files on this page.</p>`}
        </section>` : this.tab === 'checks' ? html`<section aria-label="Checks">
          <p>Status gate: ${this.view.gate || 'unavailable'}</p>
          <ul>${this.view.checks.map(row => html`<li><strong>${row.name}</strong> · ${row.state} · ${row.conclusion}${row.required ? ' · Required' : ''}<p>${row.description}</p></li>`)}</ul>
          ${!this.view.checks.length ? html`<p>No checks on this page.</p>` : ''}
        </section>` : this.tab === 'reviews' ? html`<section aria-label="Reviews">
          <ul>${this.view.reviews.map(review => html`<li>${review.author} · ${review.state}${review.stale ? ' · Stale' : ''}<p>${review.body}</p></li>`)}</ul>
          ${!this.view.reviews.length ? html`<p>No reviews on this page.</p>` : ''}
        </section>` : html`<section aria-label="Inline discussions"><ol>${this.view.threads.map(thread => html`<li>
            <strong>${thread.currentAnchor?.new_path || thread.currentAnchor?.old_path || thread.anchor?.new_path || thread.anchor?.old_path || 'Location unavailable'}</strong>
            ${thread.anchor?.lines ? html` · ${thread.anchor.side} line ${thread.anchor.lines.start}${thread.anchor.lines.end !== thread.anchor.lines.start ? `–${thread.anchor.lines.end}` : ''}` : ''}
            · ${thread.resolution}${thread.outdated ? ' · Outdated' : ''}
            <ol>${thread.comments.map(comment => html`<li>${comment.author}${comment.createdAt ? html` · <time datetime=${comment.createdAt}>${comment.createdAt.slice(0, 10)}</time>` : ''}<p>${comment.body}</p></li>`)}</ol>
            ${thread.commentsNext ? html`<p>More comments are available in this discussion.</p>` : ''}
          </li>`)}</ol>
          ${!this.view.threads.length ? html`<p>No inline discussions on this page.</p>` : ''}
        </section>`}
        <nav aria-label="Pull request pages"><button type="button" @click=${this.back} ?disabled=${this.view.busy || !this.previous.length}>Previous page</button><button type="button" @click=${this.next} ?disabled=${this.view.busy || !this.view.next}>Next page</button></nav>
      ` : ''}
    </section>`;
  }
}
customElements.define('soda-factory-pull', SodaFactoryPull);
