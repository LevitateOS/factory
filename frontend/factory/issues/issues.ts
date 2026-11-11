import {LitElement, html} from 'lit';
import type {PropertyValues} from 'lit';
import {check, id} from '../api/models.js';
import type {IssueRef} from '../api/collaboration.js';
import {issueListPage} from '../api/issue-response.js';
import type {IssueFilter, IssueListItem} from '../api/issue-response.js';
import type {FactoryTransport} from '../api/transport.js';
import {getJSON, FactoryReadError} from '../api/read.js';

export interface IssueListContext {
  readonly transport: FactoryTransport;
  readonly host: string;
  readonly repository: string;
  readonly actor: string;
  /** Stable host-admitted profile/provider/account reference, never a secret. */
  readonly scope: string;
  openIssue(issue: IssueRef, signal: AbortSignal): void | Promise<void>;
}
const empty = () => ({busy: false, opening: '', message: '', items: [] as readonly IssueListItem[], next: ''});

/** Read-only production list. Hosts own URL/history and current account
 * admission; selecting an issue grants no mutation or execution authority. */
export class SodaFactoryIssues extends LitElement {
  static properties = {view: {state: true}, filter: {state: true}};
  declare private view: ReturnType<typeof empty>;
  declare private filter: IssueFilter;
  private current: IssueListContext | undefined;
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
  protected createRenderRoot() {
    return this;
  }
  get context(): IssueListContext | undefined {
    return this.current;
  }
  set context(context: IssueListContext | undefined) {
    if (context === this.current && (!context || this.key(context) === this.binding)) return;
    this.retire();
    if (!context) return;
    check(id(context.repository) && id(context.actor) && /^[a-f0-9]{32}$/.test(context.host));
    check(typeof context.scope === 'string' && !!context.scope.trim() && context.scope.length <= 1024);
    check(!/[\u0000-\u001f\u007f]/.test(context.scope) && !!context.transport.generation);
    check(typeof context.openIssue === 'function');
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
  private key(context: IssueListContext) {
    return JSON.stringify([
      context.host,
      context.repository,
      context.actor,
      context.scope,
      context.transport.generation,
    ]);
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
  private live(context: IssueListContext, serial = this.serial) {
    if (context !== this.current || serial !== this.serial || this.lifetime.signal.aborted) return false;
    if (this.key(context) !== this.binding) {
      this.retire();
      return false;
    }
    return true;
  }
  refresh() {
    return this.load();
  }
  private async load() {
    const context = this.current;
    if (!context || !this.live(context)) return;
    this.read.abort();
    this.read = new AbortController();
    const serial = ++this.serial,
      signal = AbortSignal.any([this.lifetime.signal, this.read.signal]);
    const state = this.filter,
      cursor = this.cursor;
    this.view = {...empty(), busy: true};
    const query = new URLSearchParams({state, limit: '20'});
    if (cursor) query.set('cursor', cursor);
    try {
      const data = await getJSON(
        context.transport,
        `factory/collaboration/repositories/${context.repository}/issues?${query}`,
        signal
      );
      if (!this.live(context, serial)) return;
      const page = issueListPage(data, context.host, context.repository, state, cursor);
      this.view = {...empty(), items: page.items ?? [], next: page.next};
    } catch (error) {
      if (!this.live(context, serial)) return;
      const hidden = error instanceof FactoryReadError && [401, 403, 404].includes(error.status);
      let message = hidden ? 'Issues are not available to this account.' : 'Issues could not be loaded. Try again.';
      if (error instanceof FactoryReadError && error.status === 429 && error.detail.code === 'provider_rate_limited') {
        const detail = error.detail.message;
        message = detail && detail.length <= 256 && !/[\u0000-\u001f\u007f-\u009f]/.test(detail)
          ? detail
          : 'The provider is rate limited. Wait before refreshing issues.';
      }
      this.view = {...empty(), message};
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
  private async open(item: IssueListItem) {
    const context = this.current,
      serial = this.serial;
    if (!context || !this.live(context, serial) || this.view.opening || !this.view.items.includes(item)) return;
    this.view = {...this.view, opening: item.ref.number, message: ''};
    try {
      await context.openIssue({...item.ref}, AbortSignal.any([this.lifetime.signal, this.read.signal]));
      if (this.live(context, serial)) this.view = {...this.view, opening: ''};
    } catch {
      if (this.live(context, serial))
        this.view = {...this.view, opening: '', message: 'This issue could not be opened. Try again.'};
    }
  }
  render() {
    if (!this.current) return html``;
    return html`<section class="soda-issue-list" aria-label="Issues" aria-busy=${this.view.busy}>
      <header>
        <h2>Issues</h2>
        <label
          >Issue state
          <select
            aria-label="Issue state"
            .value=${this.filter}
            @change=${this.changeFilter}
            ?disabled=${!!this.view.opening}
          >
            <option value="open">Open</option>
            <option value="closed">Closed</option>
            <option value="all">All</option>
          </select></label
        ><button type="button" @click=${this.refresh} ?disabled=${this.view.busy || !!this.view.opening}>
          Refresh issues
        </button>
      </header>
      <p role="status">
        ${this.view.busy ? 'Loading issues…' : this.view.message || (!this.view.items.length ? (this.view.next ? 'No issues on this page. More results are available on the next page.' : 'No issues in this view.') : '')}
      </p>
      <ul>
        ${this.view.items.map(
          (item) => html`<li>
            <span class="soda-issue-state"
              >${item.state === 'unknown' ? 'State unavailable' : item.state === 'open' ? 'Open' : 'Closed'}</span
            >
            <button
              class="soda-issue-title"
              type="button"
              @click=${() => this.open(item)}
              ?disabled=${!!this.view.opening}
            >
              ${item.title || 'Untitled issue'}
            </button>
            <p class="soda-issue-meta">
              #${item.ref.number} · ${item.author?.login || 'Unknown author'}
              ${item.created_at ? html` · Opened <time datetime=${item.created_at}>${item.created_at.slice(0, 10)}</time>` : ''}
              ${item.updated_at ? html` · Updated <time datetime=${item.updated_at}>${item.updated_at.slice(0, 10)}</time>` : ''}
            </p>
            <p class="soda-issue-meta">
              ${item.assignees === null ? 'Assignees unavailable' : item.assignees.length ? 'Assigned to ' + item.assignees.map((a) => a.login).join(', ') : 'Unassigned'}
            </p>
            <div class="soda-issue-labels">
              ${item.labels === null ? 'Labels unavailable' : item.labels.slice(0, 5).map((label) => html`<span>${label.name}</span>`)}${item.labels && item.labels.length > 5 ? html`<span>+${item.labels.length - 5} more</span>` : ''}
            </div>
          </li>`
        )}
      </ul>
      <nav aria-label="Issue pages">
        <button
          type="button"
          @click=${this.back}
          ?disabled=${this.view.busy || !!this.view.opening || !this.previous.length}
        >
          Previous page
        </button>
        <button type="button" @click=${this.next} ?disabled=${this.view.busy || !!this.view.opening || !this.view.next}>
          Next page
        </button>
      </nav>
    </section>`;
  }
}
customElements.define('soda-factory-issues', SodaFactoryIssues);
