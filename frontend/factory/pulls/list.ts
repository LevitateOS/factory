import {LitElement, html, type PropertyValues} from 'lit';
import {check, id} from '../api/models.js';
import type {PullRef} from '../api/collaboration.js';
import type {FactoryTransport} from '../api/transport.js';
import {getJSON, FactoryReadError} from '../api/read.js';
import {pullList, type PullItem} from './response.js';

export interface PullListContext {
  readonly transport: FactoryTransport;
  readonly host: string;
  readonly repository: string;
  readonly actor: string;
  readonly scope: string;
  openPull(pull: PullRef, signal: AbortSignal): void | Promise<void>;
}

const empty = () => ({busy: false, opening: '', message: '', items: [] as readonly PullItem[], next: ''});

/** The same provider read and navigation in Fountain and the desktop frame. */
export class SodaFactoryPulls extends LitElement {
  static properties = {view: {state: true}, filter: {state: true}};
  declare private view: ReturnType<typeof empty>;
  declare private filter: 'open' | 'closed' | 'all';
  private current: PullListContext | undefined;
  private binding = '';
  private serial = 0;
  private cursor = '';
  private previous: string[] = [];
  private lifetime = new AbortController();
  private read = new AbortController();

  constructor() {
    super();
    this.view = empty();
    this.filter = 'open';
  }
  protected createRenderRoot() { return this; }
  get context() { return this.current; }
  set context(context: PullListContext | undefined) {
    if (context === this.current && (!context || this.key(context) === this.binding)) return;
    this.retire();
    if (!context) return;
    check(id(context.repository) && id(context.actor) && /^[a-f0-9]{32}$/.test(context.host));
    check(typeof context.scope === 'string' && !!context.scope.trim() && context.scope.length <= 1024);
    check(!/[\u0000-\u001f\u007f]/.test(context.scope) && !!context.transport.generation);
    check(typeof context.openPull === 'function');
    this.current = context;
    this.binding = this.key(context);
    this.requestUpdate('context');
  }
  protected willUpdate(changed: PropertyValues) {
    if (changed.has('context') && this.current) void this.load();
  }
  disconnectedCallback() {
    this.context = undefined;
    super.disconnectedCallback();
  }
  private key(context: PullListContext) {
    return JSON.stringify([context.host, context.repository, context.actor, context.scope, context.transport.generation]);
  }
  private retire() {
    this.lifetime.abort();
    this.read.abort();
    this.lifetime = new AbortController();
    this.read = new AbortController();
    this.serial++;
    this.current = undefined;
    this.binding = '';
    this.cursor = '';
    this.previous = [];
    this.filter = 'open';
    this.view = empty();
  }
  private live(context: PullListContext, serial = this.serial) {
    if (context !== this.current || serial !== this.serial || this.lifetime.signal.aborted) return false;
    if (this.key(context) !== this.binding) { this.retire(); return false; }
    return true;
  }
  refresh() { return this.load(); }
  private async load() {
    const context = this.current;
    if (!context || !this.live(context)) return;
    this.read.abort();
    this.read = new AbortController();
    const serial = ++this.serial;
    const signal = AbortSignal.any([this.lifetime.signal, this.read.signal]);
    const cursor = this.cursor, state = this.filter;
    this.view = {...empty(), busy: true};
    const query = new URLSearchParams({state, limit: '20'});
    if (cursor) query.set('cursor', cursor);
    try {
      const data = await getJSON(context.transport, `factory/collaboration/repositories/${context.repository}/pulls?${query}`, signal);
      if (!this.live(context, serial)) return;
      const page = pullList(data, context.host, context.repository, state, cursor);
      this.view = {...empty(), items: page.items, next: page.next};
    } catch (error) {
      if (!this.live(context, serial)) return;
      const hidden = error instanceof FactoryReadError && [401, 403, 404].includes(error.status);
      this.view = {...empty(), message: hidden ? 'Pull requests are not available to this account.' : 'Pull requests could not be loaded. Try again.'};
    }
  }
  private changeFilter(event: Event) {
    const value = (event.currentTarget as HTMLSelectElement).value;
    if (value !== 'open' && value !== 'closed' && value !== 'all') return;
    this.filter = value;
    this.cursor = '';
    this.previous = [];
    void this.load();
  }
  private next() {
    if (this.view.busy || this.view.opening || !this.view.next) return;
    this.previous.push(this.cursor);
    this.cursor = this.view.next;
    void this.load();
  }
  private back() {
    if (this.view.busy || this.view.opening || !this.previous.length) return;
    this.cursor = this.previous.pop()!;
    void this.load();
  }
  private async open(item: PullItem) {
    const context = this.current, serial = this.serial;
    if (!context || !this.live(context, serial) || this.view.opening || !this.view.items.includes(item)) return;
    this.view = {...this.view, opening: item.ref.number, message: ''};
    try {
      await context.openPull({...item.ref}, AbortSignal.any([this.lifetime.signal, this.read.signal]));
      if (this.live(context, serial)) this.view = {...this.view, opening: ''};
    } catch {
      if (this.live(context, serial)) this.view = {...this.view, opening: '', message: 'This pull request could not be opened. Try again.'};
    }
  }
  render() {
    if (!this.current) return html``;
    return html`<section class="soda-pull-list" aria-label="Pull requests" aria-busy=${this.view.busy}>
      <header><h2>Pull requests</h2>
        <label>State <select aria-label="Pull request state" .value=${this.filter} @change=${this.changeFilter} ?disabled=${!!this.view.opening}>
          <option value="open">Open</option><option value="closed">Closed</option><option value="all">All</option>
        </select></label>
        <button type="button" @click=${this.refresh} ?disabled=${this.view.busy || !!this.view.opening}>Refresh pull requests</button>
      </header>
      <p role="status">${this.view.busy ? 'Loading pull requests…' : this.view.message || (!this.view.items.length ? (this.view.next ? 'No pull requests on this page. More results are available.' : 'No pull requests in this view.') : '')}</p>
      <ul>${this.view.items.map(item => html`<li>
        <span class="soda-pull-state">${item.state === 'unknown' ? 'State unavailable' : item.state}${item.draft ? ' draft' : ''}</span>
        <button type="button" class="soda-pull-title" @click=${() => this.open(item)} ?disabled=${!!this.view.opening}>${item.title || 'Untitled pull request'}</button>
        <p class="soda-pull-meta">#${item.ref.number} · ${item.author?.login || 'Unknown author'} · ${item.head.name} → ${item.base.name}
          ${item.updated_at ? html` · Updated <time datetime=${item.updated_at}>${item.updated_at.slice(0, 10)}</time>` : ''}
        </p>
      </li>`)}</ul>
      <nav aria-label="Pull request pages">
        <button type="button" @click=${this.back} ?disabled=${this.view.busy || !!this.view.opening || !this.previous.length}>Previous page</button>
        <button type="button" @click=${this.next} ?disabled=${this.view.busy || !!this.view.opening || !this.view.next}>Next page</button>
      </nav>
    </section>`;
  }
}
customElements.define('soda-factory-pulls', SodaFactoryPulls);
