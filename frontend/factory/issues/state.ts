import {LitElement, html} from 'lit';
import {check} from '../api/models.js';
import type {FactoryTransport} from '../api/transport.js';
import type {IssueRef} from '../api/collaboration.js';
import {issueStateAllowed} from '../api/capability-response.js';
import {issueDetail} from '../api/issue-response.js';
import {getJSON} from '../api/read.js';
import {factoryCommandId} from '../api/run-response.js';
import {JournalCommand} from '../shared/journal-command.js';
import {issueStateRecord, issueStateRecordKey, type IssueStateAddress, type PendingIssueState} from './state-record.js';

export interface IssueStateContext extends IssueStateAddress {
  readonly transport: FactoryTransport;
  readonly parent: IssueRef;
  commandScope(signal: AbortSignal): Promise<string>;
}
export type IssueStatePorts = Omit<IssueStateContext, 'parent'>;

/** Direct close/reopen uses the existing human command journal and retained lookup. */
export class SodaFactoryIssueState extends LitElement {
  static properties = {version: {state: true}};
  declare private version: number;
  private journal = new JournalCommand<PendingIssueState, IssueStateContext>({
    key: context => JSON.stringify([context.scope, context.host, context.actor, context.repository, context.issue,
      context.parent.external_id, context.transport.generation]),
    words: {
      attention: 'The saved state change needs attention. Check status.',
      completing: 'State change recorded; completion is pending.',
      unconfirmed: 'The state change is not confirmed. Its original command remains saved.',
    },
    say: message => { this.message = message; },
    retire: () => { this.context = undefined; },
    store: context => issueStateRecord(context),
  });
  private allowed = false;
  private busy = false;
  private state: 'open' | 'closed' | 'unknown' = 'unknown';
  private message = '';

  constructor() { super(); this.version = 0; }
  protected createRenderRoot() { return this; }
  get context() { return this.journal.context; }
  set context(context: IssueStateContext | undefined) {
    if (!this.journal.rebind(context)) return;
    this.allowed = false; this.busy = false; this.state = 'unknown'; this.message = '';
    if (context) {
      issueStateRecordKey(context);
      check(context.parent.kind === 'issue' && context.parent.repository === context.repository && context.parent.number === context.issue);
      check(!!context.parent.external_id.trim());
      this.journal.attach(context);
      this.restore(context); void this.refresh();
    }
    this.touch();
  }
  disconnectedCallback() { this.context = undefined; super.disconnectedCallback(); }
  private touch() { this.version++; }
  private restore(context: IssueStateContext) {
    const saved = this.journal.restore(context);
    if (saved.pending && saved.pending.external !== context.parent.external_id) {
      this.journal.blocked = true;
      this.message = 'Saved state command belongs to a different issue identity. It was preserved.';
    } else if (saved.blocked) this.message = 'Saved state command could not be read. It was preserved.';
    else if (!saved.durable) this.message = 'State command storage is unavailable. No change can be sent.';
  }
  private path(context: IssueStateContext) {
    return `factory/collaboration/repositories/${context.repository}/issues/${context.issue}`;
  }
  private async admit(context: IssueStateContext, signal: AbortSignal) {
    this.allowed = false; this.state = 'unknown';
    await this.journal.identity(context, signal);
    const [raw, caps] = await Promise.all([
      getJSON(context.transport, this.path(context), signal),
      getJSON(context.transport, this.path(context) + '/capabilities', signal),
    ]);
    check(this.journal.live(context, signal));
    const issue = issueDetail(raw, context.host, context.repository, context.issue);
    check(issue.ref.external_id === context.parent.external_id);
    this.state = issue.state;
    this.allowed = issue.state !== 'unknown' && issueStateAllowed(caps, context.repository, context.actor, context.parent, issue.state === 'open');
  }
  private async refresh() {
    const context = this.journal.context, signal = this.journal.signal;
    if (!context || this.busy || !this.journal.live(context, signal)) return;
    this.busy = true; this.touch();
    try {
      if (this.journal.pending && !this.journal.blocked) { await this.journal.identity(context, signal); await this.deliver(context, signal, 'lookup'); }
      await this.admit(context, signal);
      if (!this.journal.pending && !this.journal.blocked && !this.message) this.message = this.allowed ? '' : 'Changing issue state is unavailable for this account.';
    } catch {
      if (this.journal.live(context, signal)) this.message = this.journal.pending
        ? 'The original state command is saved. Check status after access is restored.'
        : 'Issue state access could not be checked.';
    } finally { if (this.journal.live(context, signal)) { this.busy = false; this.touch(); } }
  }
  private async act(action: 'submit' | 'retry' | 'lookup' | 'cancel', close?: boolean) {
    const context = this.journal.context, signal = this.journal.signal;
    if (!context || !this.journal.live(context, signal) || this.busy || this.journal.blocked) return;
    this.busy = true; this.message = ''; this.touch();
    let settled = false;
    try {
      if (action === 'submit' || action === 'retry') { await this.admit(context, signal); check(this.allowed); }
      else await this.journal.identity(context, signal);
      let pending = this.journal.stored(context);
      if (action === 'submit') {
        check(!pending && typeof close === 'boolean' && this.state === (close ? 'open' : 'closed'));
        pending = {command: factoryCommandId(), external: context.parent.external_id, close, cancel: false};
        this.journal.retain(context, pending);
      } else {
        check(pending && this.journal.pending && pending.command === this.journal.pending.command && pending.external === context.parent.external_id);
        if (action === 'retry') check(!pending.cancel && pending.close === (this.state === 'open'));
        if (action === 'cancel') this.journal.requestCancel(context, pending);
      }
      settled = await this.deliver(context, signal, action === 'retry' ? 'submit' : action);
    } catch {
      if (this.journal.live(context, signal)) this.message = this.journal.pending
        ? 'No final state outcome was confirmed. Check the saved command before retrying.'
        : 'Issue state admission or saved state changed. Nothing was submitted.';
    } finally {
      if (this.journal.live(context, signal)) {
        this.busy = false; this.touch();
        if (settled) void this.refresh();
      }
    }
  }
  private async deliver(context: IssueStateContext, signal: AbortSignal, action: 'submit' | 'lookup' | 'cancel'): Promise<boolean> {
    const pending = this.journal.pending;
    check(pending);
    const base = this.path(context) + '/state';
    const outcome = await this.journal.deliver(context, signal, action, pending, {
      endpoints: {submit: base, commands: base + '/commands'},
      body: {command_id: pending.command, close: pending.close},
    });
    if (outcome.kind !== 'finished') return false;
    this.journal.settle(context, pending);
    this.message = this.journal.finished(pending, outcome, {
      committed: 'Issue state recorded. Refresh to read the provider state.',
      tooLate: 'The state change was already recorded; cancellation was too late. Refresh to read the provider state.',
      cancelled: 'State change cancelled before it was committed.', none: 'No state change was committed.',
    });
    if (outcome.effect === 'committed') this.dispatchEvent(new CustomEvent('issue-state-saved', {detail: {repository: context.repository, issue: context.issue, command: pending.command}}));
    return true;
  }
  render() {
    if (!this.journal.context) return html``;
    const close = this.state === 'open';
    return html`<section class="soda-issue-state-control" aria-label="Issue state" aria-busy=${this.busy}>
      <p role="status">${this.message}</p>
      ${this.journal.pending ? html`<p>Saved ${this.journal.pending.close ? 'close' : 'reopen'} command. Check its outcome before changing state again.</p>
        <button type="button" @click=${() => this.act('lookup')} ?disabled=${this.busy || this.journal.blocked}>Check saved state</button>
        <button type="button" @click=${() => this.act('retry')} ?disabled=${this.busy || this.journal.blocked || this.journal.pending.cancel || !this.allowed || this.journal.pending.close !== (this.state === 'open')}>Retry original state change</button>
        <button type="button" @click=${() => this.act('cancel')} ?disabled=${this.busy || this.journal.blocked}>Cancel saved state change</button>`
        : html`<button type="button" @click=${() => this.act('submit', close)} ?disabled=${this.busy || this.journal.blocked || !this.journal.durable || !this.allowed}>${this.state === 'unknown' ? 'Issue state unavailable' : close ? 'Close issue' : 'Reopen issue'}</button>`}
    </section>`;
  }
}
customElements.define('soda-factory-issue-state', SodaFactoryIssueState);
