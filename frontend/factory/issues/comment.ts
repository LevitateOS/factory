import {LitElement, html} from 'lit';
import {check, id, readSodaJSON} from '../api/models.js';
import type {FactoryTransport} from '../api/transport.js';
import type {IssueRef, PullRef} from '../api/collaboration.js';
import {issueDetail} from '../api/issue-response.js';
import {issueCommentAllowed} from '../api/comment-response.js';
import {pullActionAllowed} from '../api/capability-response.js';
import {pullItem} from '../pulls/response.js';
import {commandOutcome} from '../api/command-response.js';
import {factoryCommandId} from '../api/run-response.js';
import {getJSON} from '../api/read.js';
import {
  commentRecordKey,
  emptyCommentWriting,
  readCommentWriting,
  reloadCommentWriting,
  saveCommentWriting,
  samePendingComment,
  validCommentBody,
} from './comment-record.js';
import type {CommentAddress, CommentSaved, CommentWriting} from './comment-record.js';

export interface IssueCommentContext extends CommentAddress {
  readonly transport: FactoryTransport;
  readonly parent: IssueRef | PullRef;
  /** Fresh host-admitted account custody, matching the captured stable scope. */
  commandScope(signal: AbortSignal): Promise<string>;
}
export type IssueCommentPorts = Omit<IssueCommentContext, 'parent'>;

/** One ordinary issue comment, durably captured before any submission. Restore
 * only looks up the original command; retries and cancellation require a click. */
export class SodaFactoryIssueComment extends LitElement {
  static properties = {version: {state: true}};
  declare private version: number;
  private current: IssueCommentContext | undefined;
  private binding = '';
  private generation = '';
  private lifetime = new AbortController();
  private saved: CommentSaved = {raw: '', writing: undefined, durable: true, blocked: false};
  private writing = emptyCommentWriting();
  private allowed = false;
  private busy = false;
  private message = '';
  private noEffect = '';
  constructor() {
    super();
    this.version = 0;
  }
  protected createRenderRoot() {
    return this;
  }
  get context() {
    return this.current;
  }
  private key(context: IssueCommentContext) {
    return JSON.stringify([
      context.scope,
      context.host,
      context.actor,
      context.repository,
      context.issue,
      context.parent.kind,
      context.parent.repository,
      context.parent.number,
      context.parent.external_id,
      context.transport.generation,
    ]);
  }
  set context(context: IssueCommentContext | undefined) {
    const key = context && this.key(context);
    if (this.current === context && key === this.binding) return;
    this.lifetime.abort();
    this.lifetime = new AbortController();
    this.current = undefined;
    this.binding = '';
    this.allowed = false;
    this.busy = false;
    this.message = '';
    this.noEffect = '';
    this.writing = emptyCommentWriting();
    if (context) {
      commentRecordKey(context);
      check(
        (context.parent.kind === 'issue' || context.parent.kind === 'pull') &&
          context.parent.repository === context.repository &&
          context.parent.number === context.issue &&
          id(context.issue) &&
          (context.kind === 'pull') === (context.parent.kind === 'pull')
      );
      check(
        typeof context.parent.external_id === 'string' &&
          !!context.parent.external_id.trim() &&
          typeof context.commandScope === 'function'
      );
      check(!!context.transport.generation);
      this.current = context;
      this.binding = key!;
      this.generation = context.transport.generation;
      this.restore(context);
      void this.refresh();
    }
    this.touch();
  }
  disconnectedCallback() {
    this.context = undefined;
    super.disconnectedCallback();
  }
  private touch() {
    this.version++;
  }
  private live(context: IssueCommentContext, signal: AbortSignal) {
    if (
      !(
        this.current === context &&
        this.lifetime.signal === signal &&
        !signal.aborted &&
        context.transport.generation === this.generation
      )
    )
      return false;
    if (this.key(context) !== this.binding) {
      this.context = undefined;
      return false;
    }
    return true;
  }
  private restore(context: IssueCommentContext, reload = false) {
    this.saved = reload ? reloadCommentWriting(context) : readCommentWriting(context);
    this.writing = this.saved.writing ?? emptyCommentWriting();
    if (this.saved.blocked)
      this.message =
        'Saved comment writing is unreadable or changed in another window. It was preserved. Reload saved writing to check again.';
    else if (!this.saved.durable)
      this.message = 'Comment writing is only saved in this window. Submission waits until it can be saved durably.';
  }
  private path(context: IssueCommentContext) {
    return `factory/collaboration/repositories/${context.repository}/${context.parent.kind === 'pull' ? 'pulls' : 'issues'}/${context.issue}`;
  }
  private async identity(context: IssueCommentContext, signal: AbortSignal) {
    signal.throwIfAborted();
    const scope = await context.commandScope(AbortSignal.any([signal, AbortSignal.timeout(15000)]));
    check(this.live(context, signal) && scope === context.scope);
  }
  private async admit(context: IssueCommentContext, signal: AbortSignal) {
    this.allowed = false;
    await this.identity(context, signal);
    const [value, capabilities] = await Promise.all([
      getJSON(context.transport, this.path(context), signal),
      getJSON(context.transport, this.path(context) + '/capabilities', signal),
    ]);
    check(this.live(context, signal));
    if (context.parent.kind === 'pull') {
      const pull = pullItem(value, context.host, context.repository, context.issue);
      check(pull.ref.external_id === context.parent.external_id);
      this.allowed = pullActionAllowed(capabilities, context.repository, context.actor, context.parent, pull.head.commit, 'pull.comment');
    } else {
      const issue = issueDetail(value, context.host, context.repository, context.issue);
      check(issue.ref.external_id === context.parent.external_id);
      this.allowed = issueCommentAllowed(capabilities, context.repository, context.actor, context.parent);
    }
  }
  private async refresh() {
    const context = this.current,
      signal = this.lifetime.signal;
    if (!context || this.busy || !this.live(context, signal)) return;
    this.busy = true;
    this.touch();
    try {
      await this.admit(context, signal);
      if (this.writing.pending && !this.saved.blocked) await this.deliver(context, signal, 'lookup');
      else if (!this.saved.blocked)
        this.message = this.allowed
          ? this.saved.durable
            ? ''
            : 'Comment writing is only saved in this window.'
          : 'Commenting is not available for this account.';
    } catch {
      if (this.live(context, signal)) {
        this.message =
          this.allowed && this.writing.pending
            ? 'No final outcome was confirmed. The original submission remains saved. Check status before retrying.'
            : 'Comment access could not be checked. Your writing was preserved.';
      }
    } finally {
      if (this.live(context, signal)) {
        this.busy = false;
        this.touch();
      }
    }
  }
  private write(context: IssueCommentContext, next: CommentWriting) {
    const result = saveCommentWriting(context, this.saved.raw, next);
    if (result !== 'conflict') {
      this.saved = readCommentWriting(context);
      this.writing = next;
    } else {
      this.saved = {...this.saved, blocked: true};
      this.message = 'Saved writing changed. Your text is still shown; reload saved writing before submitting.';
    }
    if (result === 'unavailable')
      this.message = 'Comment writing is only saved in this window. No submission was sent.';
    return result;
  }
  private input(event: Event) {
    const context = this.current;
    if (!context || !this.live(context, this.lifetime.signal) || this.writing.pending || this.saved.blocked) return;
    const body = (event.target as HTMLTextAreaElement).value;
    const next: CommentWriting = {draft: {revision: factoryCommandId(), body}};
    const result = this.write(context, next);
    // A conflict preserves the just-typed text in this view without overwriting
    // another document's stored draft or pending command.
    if (result === 'conflict') this.writing = next;
    else if (result === 'saved') this.message = '';
    this.touch();
  }
  private async act(action: 'submit' | 'lookup' | 'cancel') {
    const context = this.current,
      signal = this.lifetime.signal;
    if (
      !context ||
      !this.live(context, signal) ||
      this.busy ||
      this.saved.blocked ||
      this.noEffect === this.writing.pending?.command
    )
      return;
    this.busy = true;
    this.message = '';
    this.touch();
    try {
      const shown = this.writing.pending;
      if (action === 'submit') {
        await this.admit(context, signal);
        check(this.allowed && (!shown || !shown.cancel));
        check(validCommentBody(shown?.body ?? this.writing.draft.body));
      } else await this.identity(context, signal);
      check(this.live(context, signal));
      const current = readCommentWriting(context);
      const same = shown
        ? !!current.writing?.pending && samePendingComment(shown, current.writing.pending)
        : current.raw === this.saved.raw;
      if (current.blocked || !same) this.saved = {...this.saved, blocked: true};
      check(!current.blocked && same);
      if (shown) {
        this.saved = current;
        this.writing = current.writing!;
      }
      if (action === 'lookup') {
        check(shown && current.writing?.pending && samePendingComment(shown, current.writing.pending));
        await this.deliver(context, signal, action);
        return;
      }
      let pending = this.writing.pending;
      check(action !== 'submit' || !pending?.cancel);
      if (!pending) {
        check(action === 'submit');
        pending = {
          command: factoryCommandId(),
          body: this.writing.draft.body,
          external: context.parent.external_id,
          draft: this.writing.draft.revision,
          cancel: false,
        };
      }
      check(pending.external === context.parent.external_id);
      if (action === 'cancel') pending = {...pending, cancel: true};
      if (this.write(context, {...this.writing, pending}) !== 'saved') return;
      await this.deliver(context, signal, action);
    } catch {
      if (this.live(context, signal))
        this.message = this.writing.pending
          ? 'No final outcome was confirmed. The original submission remains saved. Check status before retrying.'
          : 'Comment access or saved writing changed. Nothing was submitted; reload saved writing and check access.';
    } finally {
      if (this.live(context, signal)) {
        this.busy = false;
        this.touch();
      }
    }
  }
  private async deliver(context: IssueCommentContext, signal: AbortSignal, action: 'submit' | 'lookup' | 'cancel') {
    const pending = this.writing.pending;
    check(pending && pending.external === context.parent.external_id && this.live(context, signal));
    const base = this.path(context) + '/comments',
      command = base + '/commands/' + pending.command;
    const response = await context.transport.request(
      action === 'submit' ? base : command + (action === 'cancel' ? '/cancel' : ''),
      {
        method: action === 'lookup' ? 'GET' : 'POST',
        headers: {'Content-Type': 'application/json'},
        signal: AbortSignal.any([signal, AbortSignal.timeout(35000)]),
        ...(action === 'lookup'
          ? {}
          : {body: JSON.stringify(action === 'submit' ? {command_id: pending.command, body: pending.body} : {})}),
      }
    );
    check(this.live(context, signal) && response.ok);
    const outcome = commandOutcome(await readSodaJSON(response), pending.command);
    check(this.live(context, signal));
    if (!outcome.finished) {
      this.message =
        outcome.completion === 'needs_intervention'
          ? 'The saved submission needs attention. Check status before taking another action.'
          : outcome.effect === 'committed'
            ? 'The comment was recorded; provider completion is still pending. Check status.'
            : 'The submission is not confirmed. Its original writing remains saved.';
      return;
    }
    const latest = readCommentWriting(context);
    check(!latest.blocked && latest.writing?.pending && samePendingComment(latest.writing.pending, pending));
    if (outcome.effect === 'not_committed' && latest.writing.draft.body !== pending.body) {
      // Keep both versions until the human explicitly chooses which text to
      // discard. Restore rechecks the terminal server outcome; it never submits.
      this.saved = latest;
      this.writing = latest.writing;
      this.noEffect = pending.command;
      this.message = 'No comment was posted. Your original text and a newer draft are both saved.';
      return;
    }
    const next: CommentWriting = {draft: latest.writing.draft};
    if (outcome.effect === 'committed' && next.draft.revision === pending.draft && next.draft.body === pending.body)
      next.draft = {revision: factoryCommandId(), body: ''};
    const stored = saveCommentWriting(context, latest.raw, next);
    check(stored !== 'conflict');
    this.saved = readCommentWriting(context);
    this.writing = next;
    this.message =
      outcome.effect === 'committed'
        ? pending.cancel
          ? 'The comment was already posted; cancellation was too late.'
          : 'Comment posted.'
        : pending.cancel
          ? 'Submission cancelled. Your writing was preserved.'
          : 'No comment was posted. Your writing was preserved.';
    if (stored !== 'saved') this.message += ' The local saved state still needs storage.';
    if (outcome.effect === 'committed') {
      const detail = {host: context.host, actor: context.actor, scope: context.scope,
        parent: {...context.parent}, command: pending.command};
      this.dispatchEvent(context.parent.kind === 'pull'
        ? new CustomEvent('pull-comment-saved', {detail})
        : new CustomEvent('issue-comment-saved', {detail}));
    }
  }
  private continueWriting() {
    const context = this.current,
      pending = this.writing.pending;
    if (
      !context ||
      !pending ||
      this.noEffect !== pending.command ||
      this.busy ||
      !this.live(context, this.lifetime.signal)
    )
      return;
    const latest = readCommentWriting(context);
    if (latest.blocked || !latest.writing?.pending || !samePendingComment(pending, latest.writing.pending)) {
      this.saved = {...this.saved, blocked: true};
      this.message = 'Saved writing changed. Reload it before choosing a draft.';
    } else {
      this.saved = latest;
      const result = this.write(context, {draft: latest.writing.draft});
      if (result !== 'conflict') {
        this.noEffect = '';
        if (result === 'saved') this.message = 'The newer draft is ready. Nothing was submitted.';
      }
    }
    this.touch();
  }
  render() {
    if (!this.current) return html``;
    const pending = this.writing.pending,
      blocked = this.saved.blocked;
    const body = pending?.body ?? this.writing.draft.body;
    return html`<section class="soda-issue-comment" aria-label=${this.current.parent.kind === 'pull' ? 'Write pull request comment' : 'Write issue comment'} aria-busy=${this.busy}>
      <h3>Comment</h3>
      <label
        >Comment Markdown<textarea
          aria-label="Comment Markdown"
          rows="6"
          .value=${body}
          ?disabled=${this.busy}
          ?readonly=${!!pending || blocked}
          @input=${this.input}
        ></textarea>
      </label>
      <p>${new TextEncoder().encode(body).length} / 65536 bytes</p>
      <p role="status">${this.message}</p>
      ${
        this.noEffect === pending?.command
          ? html`<button type="button" ?disabled=${this.busy || blocked} @click=${this.continueWriting}>
              Discard original text and continue with newer draft
            </button>`
          : pending
            ? html`<button type="button" ?disabled=${this.busy || blocked} @click=${() => this.act('lookup')}>
                  Check comment status
                </button>
                <button
                  type="button"
                  ?disabled=${this.busy || blocked || (!pending.cancel && !this.allowed)}
                  @click=${() => this.act(pending.cancel ? 'cancel' : 'submit')}
                >
                  ${pending.cancel ? 'Retry cancellation' : 'Retry saved comment'}
                </button>
                ${!pending.cancel ? html`<button type="button" ?disabled=${this.busy || blocked} @click=${() => this.act('cancel')}>Cancel submission</button>` : ''}`
            : html`<button
                type="button"
                ?disabled=${this.busy || blocked || !this.allowed || !validCommentBody(body)}
                @click=${() => this.act('submit')}
              >
                Post comment
              </button>`
      }
      ${
        pending && this.writing.draft.body !== pending.body
          ? html`<details>
              <summary>Newer saved draft</summary>
              <textarea aria-label="Newer saved draft" readonly rows="4" .value=${this.writing.draft.body}></textarea>
            </details>`
          : ''
      }
      <button type="button" ?disabled=${this.busy} @click=${this.refresh}>Check comment access</button>
      ${
        blocked
          ? html`<button
              type="button"
              ?disabled=${this.busy}
              @click=${() => {
                this.restore(this.current!, true);
                this.touch();
                void this.refresh();
              }}
            >
              Reload saved writing
            </button>`
          : ''
      }
    </section>`;
  }
}
customElements.define('soda-factory-issue-comment', SodaFactoryIssueComment);
