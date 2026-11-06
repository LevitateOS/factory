import {LitElement, html} from 'lit';
import type {PropertyValues} from 'lit';
import {guard} from 'lit/directives/guard.js';
import {check, id} from '../api/models.js';
import {issueDetail, issueCommentPage} from '../api/issue-response.js';
import type {IssueDetail} from '../api/issue-response.js';
import type {IssueComment} from '../api/collaboration.js';
import type {IssueListContext} from './issues.js';
import {getJSON, FactoryReadError} from '../api/read.js';
import {issueMarkdown, markdownLink} from '../shared/markdown.js';

export interface IssueDetailContext extends Omit<IssueListContext, 'openIssue'> {
  readonly issue: string;
  /** Resolve provider-specific relative/fragment links; unsupported links stay text. */
  resolveLink(href: string): string | undefined;
  openLink(url: string, signal: AbortSignal): void | Promise<void>;
}
const empty = () => ({
  busy: false,
  opening: false,
  message: '',
  issue: undefined as IssueDetail | undefined,
  comments: [] as readonly IssueComment[],
  next: '',
});

/** Shared read-only issue description and ordinary discussion, independent of
 * the Factory work-detail model and of provider/native navigation ownership. */
export class SodaFactoryIssue extends LitElement {
  static properties = {view: {state: true}};
  declare private view: ReturnType<typeof empty>;
  private current: IssueDetailContext | undefined;
  private binding = '';
  private cursor = '';
  private previous: string[] = [];
  private serial = 0;
  private lifetime = new AbortController();
  private read = new AbortController();
  /** Display availability only; it grants no Factory or provider authority. */
  private notify(available: boolean) {
    const context = this.current;
    if (context)
      this.dispatchEvent(
        new CustomEvent('factory-issue-read', {
          detail: {
            host: context.host,
            repository: context.repository,
            actor: context.actor,
            scope: context.scope,
            issue: context.issue,
            available,
            ...(available && this.view.issue ? {external: this.view.issue.ref.external_id} : {}),
          },
        })
      );
  }
  constructor() {
    super();
    this.view = empty();
    this.addEventListener('auxclick', (event) => void this.open(event));
  }
  protected createRenderRoot() {
    return this;
  }
  get context() {
    return this.current;
  }
  set context(context: IssueDetailContext | undefined) {
    if (context === this.current && (!context || this.key(context) === this.binding)) return;
    this.retire();
    if (!context) return;
    check(id(context.repository) && id(context.issue) && id(context.actor) && /^[a-f0-9]{32}$/.test(context.host));
    check(
      typeof context.scope === 'string' &&
        !!context.scope.trim() &&
        context.scope.length <= 1024 &&
        !/[\u0000-\u001f\u007f]/.test(context.scope)
    );
    check(
      !!context.transport.generation &&
        typeof context.openLink === 'function' &&
        typeof context.resolveLink === 'function'
    );
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
  private key(context: IssueDetailContext) {
    return JSON.stringify([
      context.host,
      context.repository,
      context.issue,
      context.actor,
      context.scope,
      context.transport.generation,
    ]);
  }
  private retire() {
    this.notify(false);
    this.read.abort();
    this.lifetime.abort();
    this.read = new AbortController();
    this.lifetime = new AbortController();
    this.serial++;
    this.current = undefined;
    this.binding = '';
    this.cursor = '';
    this.previous = [];
    this.view = empty();
  }
  private live(context: IssueDetailContext, serial = this.serial) {
    if (this.current !== context || this.serial !== serial || this.lifetime.signal.aborted) return false;
    if (this.key(context) !== this.binding) {
      this.retire();
      return false;
    }
    return true;
  }
  refresh() {
    this.cursor = '';
    this.previous = [];
    return this.load();
  }
  private async load() {
    const context = this.current;
    if (!context || !this.live(context)) return;
    const parent = this.view.issue?.ref;
    this.read.abort();
    this.read = new AbortController();
    const serial = ++this.serial,
      signal = AbortSignal.any([this.lifetime.signal, this.read.signal]);
    const cursor = this.cursor,
      path = `factory/collaboration/repositories/${context.repository}/issues/${context.issue}`;
    this.view = {...empty(), busy: true};
    this.notify(false);
    try {
      const issue = issueDetail(
        await getJSON(context.transport, path, signal),
        context.host,
        context.repository,
        context.issue
      );
      if (!this.live(context, serial)) return;
      check(!cursor || !parent || issue.ref.external_id === parent.external_id);
      const query = new URLSearchParams({limit: '20'});
      if (cursor) query.set('cursor', cursor);
      const comments = issueCommentPage(
        await getJSON(context.transport, path + '/comments?' + query, signal),
        context.host,
        issue.ref,
        cursor
      );
      if (!this.live(context, serial)) return;
      this.view = {...empty(), issue, comments: comments.items ?? [], next: comments.next};
      this.notify(true);
    } catch (error) {
      if (!this.live(context, serial)) return;
      const hidden = error instanceof FactoryReadError && [401, 403, 404].includes(error.status);
      this.view = {
        ...empty(),
        message: hidden
          ? 'This issue is not available to this account.'
          : 'The issue and conversation could not be loaded. Try again.',
      };
    }
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
  private async open(event: MouseEvent) {
    if (event.button > 1) return;
    const anchor = event.composedPath().find((node) => node instanceof HTMLAnchorElement);
    if (!(anchor instanceof HTMLAnchorElement) || !this.contains(anchor)) return;
    event.preventDefault();
    const context = this.current,
      serial = this.serial;
    if (!context || !this.live(context, serial) || this.view.opening || !this.view.issue) return;
    const url = markdownLink(anchor.href);
    if (!url) return;
    this.view = {...this.view, opening: true, message: ''};
    try {
      await context.openLink(url, AbortSignal.any([this.lifetime.signal, this.read.signal]));
      if (this.live(context, serial)) this.view = {...this.view, opening: false};
    } catch {
      if (this.live(context, serial))
        this.view = {...this.view, opening: false, message: 'This link could not be opened. Try again.'};
    }
  }
  render() {
    const context = this.current,
      issue = this.view.issue;
    if (!context) return html``;
    return html`<section
      class="soda-issue-detail"
      aria-label="Issue detail"
      aria-busy=${this.view.busy}
      @click=${this.open}
    >
      <header>
        <h2>${issue ? issue.title || 'Untitled issue' : 'Issue'}</h2>
        <button type="button" @click=${this.refresh} ?disabled=${this.view.busy || this.view.opening}>
          Refresh issue
        </button>
      </header>
      <p role="status">${this.view.busy ? 'Loading issue and conversation…' : this.view.message}</p>
      ${
        issue
          ? html`
              <p class="soda-issue-meta">
                #${issue.ref.number} ·
                ${issue.state === 'unknown' ? 'State unavailable' : issue.state === 'open' ? 'Open' : 'Closed'} ·
                ${issue.author?.login || 'Unknown author'}
                ${issue.created_at ? html` · Opened <time datetime=${issue.created_at}>${issue.created_at.slice(0, 10)}</time>` : ''}
                ${issue.updated_at ? html` · Updated <time datetime=${issue.updated_at}>${issue.updated_at.slice(0, 10)}</time>` : ''}
                ${issue.closed_at ? html` · Closed <time datetime=${issue.closed_at}>${issue.closed_at.slice(0, 10)}</time>` : ''}
              </p>
              <p class="soda-issue-meta">
                ${issue.assignees === null ? 'Assignees unavailable' : issue.assignees.length ? 'Assigned to ' + issue.assignees.map((actor) => actor.login).join(', ') : 'Unassigned'}
              </p>
              <div class="soda-issue-labels">
                ${issue.labels === null ? 'Labels unavailable' : issue.labels.map((label) => html`<span>${label.name}</span>`)}
              </div>
              <article aria-label="Issue description" class="soda-markdown">
                ${issue.body ? guard([issue.ref.external_id, issue.body, context.resolveLink], () => issueMarkdown(issue.body, (href) => context.resolveLink(href))) : html`<p>No description provided.</p>`}
              </article>
              <h3>Conversation</h3>
              ${!this.view.comments.length ? html`<p>No comments on this page.</p>` : ''}
              <ol class="soda-issue-comments">
                ${this.view.comments.map(
                  ({comment}) => html`<li>
                    <article id=${'issuecomment-' + comment.external_id} aria-label=${'Comment ' + comment.external_id}>
                      <header>
                        ${comment.author?.login || 'Unknown author'}${comment.created_at ? html` · <time datetime=${comment.created_at}>${comment.created_at.slice(0, 10)}</time>` : ''}${comment.updated_at && comment.updated_at !== comment.created_at ? html` · Edited <time datetime=${comment.updated_at}>${comment.updated_at.slice(0, 10)}</time>` : ''}
                      </header>
                      <div class="soda-markdown">
                        ${comment.body ? guard([comment.external_id, comment.body, context.resolveLink], () => issueMarkdown(comment.body, (href) => context.resolveLink(href), 'comment-' + comment.external_id)) : html`<p>Empty comment.</p>`}
                      </div>
                    </article>
                  </li>`
                )}
              </ol>
              <nav aria-label="Conversation pages">
                <button
                  type="button"
                  @click=${this.back}
                  ?disabled=${this.view.busy || this.view.opening || !this.previous.length}
                >
                  Previous comments</button
                ><button
                  type="button"
                  @click=${this.next}
                  ?disabled=${this.view.busy || this.view.opening || !this.view.next}
                >
                  Next comments
                </button>
              </nav>
            `
          : ''
      }
    </section>`;
  }
}
customElements.define('soda-factory-issue', SodaFactoryIssue);
