import {LitElement, html} from 'lit';
import {check, readSodaJSON} from '../api/models.js';
import type {FactoryTransport} from '../api/transport.js';
import type {IssueRef} from '../api/collaboration.js';
import {issueStateAllowed} from '../api/capability-response.js';
import {issueDetail} from '../api/issue-response.js';
import {commandOutcome} from '../api/command-response.js';
import {getJSON} from '../api/read.js';
import {factoryCommandId} from '../api/run-response.js';
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
  private current: IssueStateContext | undefined;
  private binding = '';
  private generation = '';
  private lifetime = new AbortController();
  private pending: PendingIssueState | undefined;
  private durable = true;
  private blocked = false;
  private allowed = false;
  private busy = false;
  private state: 'open' | 'closed' | 'unknown' = 'unknown';
  private message = '';

  constructor() { super(); this.version = 0; }
  protected createRenderRoot() { return this; }
  get context() { return this.current; }
  private key(context: IssueStateContext) {
    return JSON.stringify([context.scope, context.host, context.actor, context.repository, context.issue,
      context.parent.external_id, context.transport.generation]);
  }
  set context(context: IssueStateContext | undefined) {
    const key = context && this.key(context);
    if (context && key === this.binding && this.current) return;
    this.lifetime.abort(); this.lifetime = new AbortController();
    this.current = undefined; this.binding = ''; this.pending = undefined; this.durable = true; this.blocked = false;
    this.allowed = false; this.busy = false; this.state = 'unknown'; this.message = '';
    if (context) {
      issueStateRecordKey(context);
      check(context.parent.kind === 'issue' && context.parent.repository === context.repository && context.parent.number === context.issue);
      check(!!context.parent.external_id.trim() && typeof context.commandScope === 'function' && !!context.transport.generation);
      this.current = context; this.binding = key!; this.generation = context.transport.generation;
      this.restore(context); void this.refresh();
    }
    this.touch();
  }
  disconnectedCallback() { this.context = undefined; super.disconnectedCallback(); }
  private touch() { this.version++; }
  private live(context: IssueStateContext, signal: AbortSignal) {
    if (this.current !== context || this.lifetime.signal !== signal || signal.aborted || context.transport.generation !== this.generation) return false;
    if (this.key(context) !== this.binding) { this.context = undefined; return false; }
    return true;
  }
  private restore(context: IssueStateContext) {
    const saved = issueStateRecord(context).read();
    this.pending = saved.pending; this.blocked = saved.blocked; this.durable = saved.durable;
    if (saved.pending && saved.pending.external !== context.parent.external_id) {
      this.blocked = true;
      this.message = 'Saved state command belongs to a different issue identity. It was preserved.';
    } else if (saved.blocked) this.message = 'Saved state command could not be read. It was preserved.';
    else if (!saved.durable) this.message = 'State command storage is unavailable. No change can be sent.';
  }
  private path(context: IssueStateContext) {
    return `factory/collaboration/repositories/${context.repository}/issues/${context.issue}`;
  }
  private async identity(context: IssueStateContext, signal: AbortSignal) {
    const scope = await context.commandScope(AbortSignal.any([signal, AbortSignal.timeout(15000)]));
    check(this.live(context, signal) && scope === context.scope);
  }
  private async admit(context: IssueStateContext, signal: AbortSignal) {
    this.allowed = false; this.state = 'unknown';
    await this.identity(context, signal);
    const [raw, caps] = await Promise.all([
      getJSON(context.transport, this.path(context), signal),
      getJSON(context.transport, this.path(context) + '/capabilities', signal),
    ]);
    check(this.live(context, signal));
    const issue = issueDetail(raw, context.host, context.repository, context.issue);
    check(issue.ref.external_id === context.parent.external_id);
    this.state = issue.state;
    this.allowed = issue.state !== 'unknown' && issueStateAllowed(caps, context.repository, context.actor, context.parent, issue.state === 'open');
  }
  private async refresh() {
    const context = this.current, signal = this.lifetime.signal;
    if (!context || this.busy || !this.live(context, signal)) return;
    this.busy = true; this.touch();
    try {
      if (this.pending && !this.blocked) { await this.identity(context, signal); await this.deliver(context, signal, 'lookup'); }
      await this.admit(context, signal);
      if (!this.pending && !this.blocked && !this.message) this.message = this.allowed ? '' : 'Changing issue state is unavailable for this account.';
    } catch {
      if (this.live(context, signal)) this.message = this.pending
        ? 'The original state command is saved. Check status after access is restored.'
        : 'Issue state access could not be checked.';
    } finally { if (this.live(context, signal)) { this.busy = false; this.touch(); } }
  }
  private async act(action: 'submit' | 'retry' | 'lookup' | 'cancel', close?: boolean) {
    const context = this.current, signal = this.lifetime.signal;
    if (!context || !this.live(context, signal) || this.busy || this.blocked) return;
    this.busy = true; this.message = ''; this.touch();
    let settled = false;
    try {
      if (action === 'submit' || action === 'retry') { await this.admit(context, signal); check(this.allowed); }
      else await this.identity(context, signal);
      const saved = issueStateRecord(context).read();
      check(!saved.blocked && saved.durable);
      let pending = saved.pending;
      if (action === 'submit') {
        check(!pending && typeof close === 'boolean' && this.state === (close ? 'open' : 'closed'));
        pending = {command: factoryCommandId(), external: context.parent.external_id, close};
        check(issueStateRecord(context).retain(pending) === 'saved');
        this.pending = pending;
      } else {
        check(pending && this.pending && pending.command === this.pending.command && pending.external === context.parent.external_id);
        if (action === 'retry') check(pending.close === (this.state === 'open'));
      }
      settled = await this.deliver(context, signal, action === 'retry' ? 'submit' : action);
    } catch {
      if (this.live(context, signal)) this.message = this.pending
        ? 'No final state outcome was confirmed. Check the saved command before retrying.'
        : 'Issue state admission or saved state changed. Nothing was submitted.';
    } finally {
      if (this.live(context, signal)) {
        this.busy = false; this.touch();
        if (settled) void this.refresh();
      }
    }
  }
  private async deliver(context: IssueStateContext, signal: AbortSignal, action: 'submit' | 'lookup' | 'cancel'): Promise<boolean> {
    const pending = this.pending;
    check(pending && this.live(context, signal));
    const base = this.path(context) + '/state', command = base + '/commands/' + pending.command;
    const response = await context.transport.request(action === 'submit' ? base : command + (action === 'cancel' ? '/cancel' : ''), {
      method: action === 'lookup' ? 'GET' : 'POST', headers: {'Content-Type': 'application/json'},
      signal: AbortSignal.any([signal, AbortSignal.timeout(35000)]),
      ...(action === 'lookup' ? {} : {body: JSON.stringify(action === 'submit'
        ? {command_id: pending.command, close: pending.close} : {})}),
    });
    check(this.live(context, signal) && response.ok);
    const outcome = commandOutcome(await readSodaJSON(response), pending.command);
    check(this.live(context, signal));
    if (!outcome.finished) {
      this.message = outcome.completion === 'needs_intervention' ? 'The saved state change needs attention. Check status.'
        : outcome.effect === 'committed' ? 'State change recorded; completion is pending.'
          : 'The state change is not confirmed. Its original command remains saved.';
      return false;
    }
    check(issueStateRecord(context).settle(pending));
    this.restore(context);
    this.message = outcome.effect === 'committed' ? 'Issue state recorded. Refresh to read the provider state.'
      : action === 'cancel' ? 'State change cancelled before it was committed.' : 'No state change was committed.';
    if (outcome.effect === 'committed') this.dispatchEvent(new CustomEvent('issue-state-saved', {detail: {repository: context.repository, issue: context.issue, command: pending.command}}));
    return true;
  }
  render() {
    if (!this.current) return html``;
    const close = this.state === 'open';
    return html`<section class="soda-issue-state-control" aria-label="Issue state" aria-busy=${this.busy}>
      <p role="status">${this.message}</p>
      ${this.pending ? html`<p>Saved ${this.pending.close ? 'close' : 'reopen'} command. Check its outcome before changing state again.</p>
        <button type="button" @click=${() => this.act('lookup')} ?disabled=${this.busy || this.blocked}>Check saved state</button>
        <button type="button" @click=${() => this.act('retry')} ?disabled=${this.busy || this.blocked || !this.allowed || this.pending.close !== (this.state === 'open')}>Retry original state change</button>
        <button type="button" @click=${() => this.act('cancel')} ?disabled=${this.busy || this.blocked}>Cancel saved state change</button>`
        : html`<button type="button" @click=${() => this.act('submit', close)} ?disabled=${this.busy || this.blocked || !this.durable || !this.allowed}>${this.state === 'unknown' ? 'Issue state unavailable' : close ? 'Close issue' : 'Reopen issue'}</button>`}
    </section>`;
  }
}
customElements.define('soda-factory-issue-state', SodaFactoryIssueState);
