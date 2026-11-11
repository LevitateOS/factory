import {LitElement, html} from 'lit';
import {check} from '../api/models.js';
import type {FactoryTransport} from '../api/transport.js';
import type {IssueRef} from '../api/collaboration.js';
import {issueCreateAllowed, issueCreateOutcome} from '../api/issue-create-response.js';
import {factoryCommandId} from '../api/run-response.js';
import {getJSON} from '../api/read.js';
import {JournalCommand} from '../shared/journal-command.js';
import {
  issueCreateRecordKey,
  emptyIssueCreation,
  readIssueCreation,
  reloadIssueCreation,
  saveIssueCreation,
  samePendingIssue,
  validIssueWriting,
} from './create-record.js';
import type {IssueCreateAddress, IssueCreation, IssueCreationSaved, PendingIssue} from './create-record.js';

export interface IssueCreateContext extends IssueCreateAddress {
  readonly transport: FactoryTransport;
  commandScope(signal: AbortSignal): Promise<string>;
  openIssue(issue: IssueRef, signal: AbortSignal): void | Promise<void>;
}
export type IssueCreatePorts = Omit<IssueCreateContext, 'openIssue'>;

/** Shared human creation UI. Original writing is durable before dispatch and
 * automatic recovery only checks the saved command, never submits another. */
export class SodaFactoryIssueCreate extends LitElement {
  static properties = {version: {state: true}};
  declare private version: number;
  private journal = new JournalCommand<PendingIssue, IssueCreateContext>({
    key: (context) =>
      JSON.stringify([context.scope, context.host, context.actor, context.repository, context.transport.generation]),
    words: {
      attention: 'The saved submission needs attention. Check status before taking another action.',
      attentionCommitted: 'The issue was created; provider completion needs attention. Check status.',
      completing: 'The issue was created; provider completion is still pending. Check status.',
      unconfirmed: 'The submission is not confirmed. Its original title and Markdown remain saved.',
    },
    say: (message) => {
      this.message = message;
    },
    retire: () => {
      this.context = undefined;
    },
    byObject: true,
  });
  private saved: IssueCreationSaved = {raw: '', writing: undefined, blocked: false, durable: true};
  private writing = emptyIssueCreation();
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
    return this.journal.context;
  }
  set context(context: IssueCreateContext | undefined) {
    if (!this.journal.rebind(context)) return;
    this.allowed = false;
    this.busy = false;
    this.message = '';
    this.noEffect = '';
    this.writing = emptyIssueCreation();
    if (context) {
      issueCreateRecordKey(context);
      check(typeof context.openIssue === 'function');
      this.journal.attach(context);
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
  private restore(context: IssueCreateContext, reload = false) {
    this.saved = reload ? reloadIssueCreation(context) : readIssueCreation(context);
    this.writing = this.saved.writing ?? emptyIssueCreation();
    if (this.saved.blocked)
      this.message =
        'Saved issue writing is unreadable or changed in another window. It was preserved. Reload saved writing to check again.';
    else if (!this.saved.durable)
      this.message = 'Issue writing is only saved in this window. Submission waits for durable storage.';
  }
  private path(context: IssueCreateContext) {
    return `factory/collaboration/repositories/${context.repository}`;
  }
  private async admit(context: IssueCreateContext, signal: AbortSignal) {
    this.allowed = false;
    await this.journal.identity(context, signal);
    const value = await getJSON(context.transport, this.path(context) + '/capabilities', signal);
    check(this.journal.live(context, signal));
    this.allowed = issueCreateAllowed(value, context.repository, context.actor);
  }
  private async refresh() {
    const context = this.journal.context,
      signal = this.journal.signal;
    if (!context || this.busy || !this.journal.live(context, signal)) return;
    this.busy = true;
    this.touch();
    try {
      await this.journal.identity(context, signal);
      if (this.writing.pending && !this.saved.blocked) {
        // Capability failure may disable retry, but does not prevent scoped
        // receipt recovery or cancellation after repository access changes.
        try {
          await this.admit(context, signal);
        } catch {
          this.allowed = false;
        }
        check(this.journal.live(context, signal));
        await this.deliver(context, signal, 'lookup');
      } else if (!this.saved.blocked) {
        await this.admit(context, signal);
        this.message = this.writing.created
          ? 'Issue created. Open it or write another issue.'
          : !this.saved.durable
            ? 'Issue writing is only saved in this window.'
            : this.allowed
              ? ''
              : 'Issue creation is not available for this account.';
      }
    } catch {
      if (this.journal.live(context, signal))
        this.message = this.writing.pending
          ? 'No final outcome was confirmed. The original submission remains saved. Check status before retrying.'
          : 'Issue access could not be checked. Your writing was preserved.';
    } finally {
      if (this.journal.live(context, signal)) {
        this.busy = false;
        this.touch();
      }
    }
  }
  private write(context: IssueCreateContext, next: IssueCreation) {
    const result = saveIssueCreation(context, this.saved.raw, next);
    if (result !== 'conflict') {
      this.saved = readIssueCreation(context);
      this.writing = next;
    } else {
      this.saved = {...this.saved, blocked: true};
      this.message = 'Saved writing changed. Your text is still shown; reload saved writing before submitting.';
    }
    if (result === 'unavailable') this.message = 'Issue writing is only saved in this window. No submission was sent.';
    return result;
  }
  private input(event: Event, field: 'title' | 'body') {
    const context = this.journal.context;
    if (!context || !this.journal.live(context, this.journal.signal) || this.writing.pending || this.saved.blocked) return;
    const next: IssueCreation = {
      ...this.writing,
      draft: {...this.writing.draft, revision: factoryCommandId(), [field]: (event.target as HTMLInputElement).value},
    };
    const result = this.write(context, next);
    if (result === 'conflict') this.writing = next;
    else if (result === 'saved') this.message = '';
    this.touch();
  }
  private async act(action: 'submit' | 'lookup' | 'cancel') {
    const context = this.journal.context,
      signal = this.journal.signal;
    if (
      !context ||
      !this.journal.live(context, signal) ||
      this.busy ||
      this.saved.blocked ||
      this.writing.created ||
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
        check(validIssueWriting(shown?.title ?? this.writing.draft.title, shown?.body ?? this.writing.draft.body));
      } else await this.journal.identity(context, signal);
      check(this.journal.live(context, signal));
      const current = readIssueCreation(context);
      const same = shown
        ? !!current.writing?.pending && samePendingIssue(shown, current.writing.pending)
        : current.raw === this.saved.raw;
      if (current.blocked || !same) this.saved = {...this.saved, blocked: true};
      check(!current.blocked && same);
      if (shown) {
        this.saved = current;
        this.writing = current.writing!;
      }
      if (action === 'lookup') {
        check(shown);
        await this.deliver(context, signal, action);
        return;
      }
      let pending = this.writing.pending;
      check(action !== 'submit' || !pending?.cancel);
      if (!pending) {
        check(action === 'submit');
        pending = {
          command: factoryCommandId(),
          title: this.writing.draft.title,
          body: this.writing.draft.body,
          draft: this.writing.draft.revision,
          cancel: false,
        };
      }
      if (action === 'cancel') pending = {...pending, cancel: true};
      if (this.write(context, {...this.writing, pending}) !== 'saved') return;
      await this.deliver(context, signal, action);
    } catch {
      if (this.journal.live(context, signal))
        this.message = this.writing.pending
          ? 'No final outcome was confirmed. The original submission remains saved. Check status before retrying.'
          : 'Issue access or saved writing changed. Nothing was submitted; reload saved writing and check access.';
    } finally {
      if (this.journal.live(context, signal)) {
        this.busy = false;
        this.touch();
      }
    }
  }
  private async deliver(context: IssueCreateContext, signal: AbortSignal, action: 'submit' | 'lookup' | 'cancel') {
    const pending = this.writing.pending;
    check(pending);
    const base = this.path(context);
    const outcome = await this.journal.deliver(context, signal, action, pending, {
      endpoints: {submit: base + '/issues', commands: base + '/issue-creation/commands'},
      body: {command_id: pending.command, title: pending.title, body: pending.body},
      parse: (body, command) => issueCreateOutcome(body, command, context.repository),
    });
    if (outcome.kind !== 'finished') return;
    if (outcome.effect === 'committed' && !outcome.issue) {
      this.message =
        'The issue was created, but its destination is not currently readable. Check status; do not create a replacement.';
      return;
    }
    const latest = readIssueCreation(context);
    check(!latest.blocked && latest.writing?.pending && samePendingIssue(latest.writing.pending, pending));
    if (
      outcome.effect === 'not_committed' &&
      (latest.writing.draft.title !== pending.title || latest.writing.draft.body !== pending.body)
    ) {
      this.saved = latest;
      this.writing = latest.writing;
      this.noEffect = pending.command;
      this.message = 'No issue was created. Your original writing and a newer draft are both saved.';
      return;
    }
    const next: IssueCreation = {draft: latest.writing.draft};
    if (outcome.effect === 'committed') {
      check(outcome.issue);
      next.created = {command: pending.command, issue: outcome.issue};
      if (
        next.draft.revision === pending.draft &&
        next.draft.title === pending.title &&
        next.draft.body === pending.body
      )
        next.draft = emptyIssueCreation().draft;
    }
    const stored = saveIssueCreation(context, latest.raw, next);
    check(stored !== 'conflict');
    this.saved = readIssueCreation(context);
    this.writing = next;
    this.message = this.journal.finished(pending, outcome, {
      committed: 'Issue created.',
      cancelled: 'Submission cancelled. Your writing was preserved.',
      none: 'No issue was created. Your writing was preserved.',
    });
    if (stored !== 'saved') this.message += ' The local saved state still needs storage.';
    if (next.created && stored === 'saved') await this.openCreated(context, signal);
  }
  private async openCreated(context = this.journal.context, signal = this.journal.signal) {
    const created = this.writing.created;
    if (!context || !created || this.saved.blocked || !this.journal.live(context, signal)) return;
    try {
      await this.journal.identity(context, signal);
      check(this.journal.live(context, signal));
      await context.openIssue({...created.issue}, signal);
    } catch {
      if (this.journal.live(context, signal))
        this.message = 'Issue created. Its destination is saved; open it when navigation is available.';
    }
    if (this.journal.live(context, signal)) this.touch();
  }
  private continueWriting() {
    const context = this.journal.context,
      pending = this.writing.pending,
      created = this.writing.created;
    if (
      !context ||
      this.busy ||
      !this.journal.live(context, this.journal.signal) ||
      (!created && this.noEffect !== pending?.command)
    )
      return;
    const latest = readIssueCreation(context);
    if (
      latest.blocked ||
      !latest.writing ||
      (pending
        ? !latest.writing.pending || !samePendingIssue(pending, latest.writing.pending)
        : !created || latest.writing.created?.command !== created.command)
    ) {
      this.saved = {...this.saved, blocked: true};
      this.message = 'Saved writing changed. Reload it before choosing a draft.';
    } else {
      this.saved = latest;
      if (this.write(context, {draft: latest.writing.draft}) !== 'conflict') {
        this.noEffect = '';
        this.message = 'The draft is ready. Nothing was submitted.';
      }
    }
    this.touch();
  }
  render() {
    if (!this.journal.context) return html``;
    const pending = this.writing.pending,
      created = this.writing.created,
      blocked = this.saved.blocked;
    const title = pending?.title ?? this.writing.draft.title,
      body = pending?.body ?? this.writing.draft.body;
    return html`<section class="soda-issue-create" aria-label="New issue" aria-busy=${this.busy}>
      <h2>New issue</h2>
      <label
        >Title<input
          aria-label="Issue title"
          .value=${title}
          ?disabled=${this.busy}
          ?readonly=${!!pending || blocked}
          @input=${(event: Event) => this.input(event, 'title')}
      /></label>
      <p>${new TextEncoder().encode(title).length} / 255 title bytes</p>
      <label
        >Description Markdown<textarea
          aria-label="Issue Markdown"
          rows="10"
          .value=${body}
          ?disabled=${this.busy}
          ?readonly=${!!pending || blocked}
          @input=${(event: Event) => this.input(event, 'body')}
        ></textarea>
      </label>
      <p>${new TextEncoder().encode(body).length} / 65536 description bytes</p>
      <p role="status">${this.message}</p>
      ${
        created
          ? html`<button type="button" ?disabled=${this.busy || blocked} @click=${() => this.openCreated()}>
                Open created issue</button
              ><button type="button" ?disabled=${this.busy || blocked} @click=${this.continueWriting}>
                Write another issue
              </button>`
          : this.noEffect === pending?.command
            ? html`<button type="button" ?disabled=${this.busy || blocked} @click=${this.continueWriting}>
                Discard original writing and continue with newer draft
              </button>`
            : pending
              ? html`
                  <button type="button" ?disabled=${this.busy || blocked} @click=${() => this.act('lookup')}>
                    Check issue status
                  </button>
                  <button
                    type="button"
                    ?disabled=${this.busy || blocked || (!pending.cancel && !this.allowed)}
                    @click=${() => this.act(pending.cancel ? 'cancel' : 'submit')}
                  >
                    ${pending.cancel ? 'Retry cancellation' : 'Retry saved issue'}
                  </button>
                  ${!pending.cancel ? html`<button type="button" ?disabled=${this.busy || blocked} @click=${() => this.act('cancel')}>Cancel submission</button>` : ''}
                `
              : html`<button
                  type="button"
                  ?disabled=${this.busy || blocked || !this.allowed || !validIssueWriting(title, body)}
                  @click=${() => this.act('submit')}
                >
                  Create issue
                </button>`
      }
      ${
        pending && (this.writing.draft.title !== pending.title || this.writing.draft.body !== pending.body)
          ? html`<details>
              <summary>Newer saved draft</summary>
              <input aria-label="Newer issue title" readonly .value=${this.writing.draft.title} /><textarea
                aria-label="Newer issue Markdown"
                readonly
                rows="4"
                .value=${this.writing.draft.body}
              ></textarea>
            </details>`
          : ''
      }
      <button type="button" ?disabled=${this.busy} @click=${this.refresh}>Check issue access</button>
      ${
        blocked
          ? html`<button
              type="button"
              ?disabled=${this.busy}
              @click=${() => {
                this.restore(this.journal.context!, true);
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
customElements.define('soda-factory-issue-create', SodaFactoryIssueCreate);
