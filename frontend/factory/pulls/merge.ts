import {LitElement, html} from 'lit';
import {check, object} from '../api/models.js';
import type {FactoryTransport} from '../api/transport.js';
import type {PullRef} from '../api/collaboration.js';
import {pullActionAllowed} from '../api/capability-response.js';
import {getJSON} from '../api/read.js';
import {factoryCommandId} from '../api/run-response.js';
import {JournalCommand} from '../shared/journal-command.js';
import {pullItem} from './response.js';
import {mergeRecord, mergeRecordKey, noMergeMessage, validMergeMessage, type MergeAddress, type MergeMethod, type PendingMerge} from './merge-record.js';

const mergeMethods: readonly MergeMethod[] = ['merge', 'squash', 'rebase', 'rebase-merge', 'fast-forward-only'];
const mergeMethodLabel: Record<MergeMethod, string> = {
  merge: 'Merge commit', squash: 'Squash', rebase: 'Rebase',
  'rebase-merge': 'Rebase and merge', 'fast-forward-only': 'Fast-forward only',
};
function availableMergeMethods(value: unknown): MergeMethod[] {
  const facts = object(value).merge_methods;
  if (facts === undefined) return [];
  const methods = object(facts);
  return mergeMethods.filter(method => {
    const fact = object(methods[method]);
    check(['allowed', 'denied', 'unsupported', 'unavailable', 'unknown'].includes(String(fact.state)));
    return fact.state === 'allowed';
  });
}

export interface PullMergeContext extends MergeAddress {
  readonly transport: FactoryTransport;
  readonly parent: PullRef;
  readonly head: string;
  commandScope(signal: AbortSignal): Promise<string>;
}

/** Explicit human merge intent; retained before submission and looked up on remount. */
export class SodaFactoryPullMerge extends LitElement {
  static properties = {version: {state: true}};
  declare private version: number;
  private journal = new JournalCommand<PendingMerge, PullMergeContext>({
    key: context => JSON.stringify([context.scope, context.host, context.actor, context.repository, context.pull,
      context.parent.external_id, context.head, context.transport.generation]),
    words: {
      attention: 'The saved merge needs attention. Check status.',
      completing: 'Merge effect recorded; completion is pending.',
      unconfirmed: 'The merge is not confirmed. Its original command remains saved.',
    },
    say: message => { this.status = message; },
    retire: () => { this.context = undefined; },
    store: context => mergeRecord(context),
  });
  private allowed = false;
  private methods: MergeMethod[] = [];
  private busy = false;
  private method: MergeMethod = 'merge';
  private message = '';
  private status = '';

  constructor() { super(); this.version = 0; }
  protected createRenderRoot() { return this; }
  get context() { return this.journal.context; }
  set context(context: PullMergeContext | undefined) {
    if (!this.journal.rebind(context)) return;
    this.allowed = false; this.methods = []; this.busy = false; this.method = 'merge'; this.message = ''; this.status = '';
    if (context) {
      mergeRecordKey(context);
      check(context.parent.kind === 'pull' && context.parent.repository === context.repository && context.parent.number === context.pull);
      check(!!context.parent.external_id.trim() && /^[a-f0-9]{40}(?:[a-f0-9]{24})?$/i.test(context.head));
      this.journal.attach(context);
      this.restore(context); void this.refresh();
    }
    this.touch();
  }
  disconnectedCallback() { this.context = undefined; super.disconnectedCallback(); }
  private touch() { this.version++; }
  private restore(context: PullMergeContext) {
    const saved = this.journal.restore(context);
    if (saved.blocked) this.status = 'Saved merge command could not be read. It was preserved.';
    else if (!saved.durable) this.status = 'Merge command storage is unavailable. No new merge can be sent.';
  }
  private path(context: PullMergeContext) {
    return `factory/collaboration/repositories/${context.repository}/pulls/${context.pull}`;
  }
  private async admit(context: PullMergeContext, signal: AbortSignal) {
    this.allowed = false; this.methods = [];
    await this.journal.identity(context, signal);
    const [raw, caps] = await Promise.all([
      getJSON(context.transport, this.path(context), signal),
      getJSON(context.transport, this.path(context) + '/capabilities', signal),
    ]);
    check(this.journal.live(context, signal));
    const pull = pullItem(raw, context.host, context.repository, context.pull);
    check(pull.ref.external_id === context.parent.external_id);
    const current = pull.state === 'open' && pull.draft === false &&
      (pull.head.commit === null || pull.head.commit === context.head) &&
      pullActionAllowed(caps, context.repository, context.actor, context.parent, context.head, 'pull.merge');
    this.methods = current ? availableMergeMethods(caps) : [];
    this.allowed = this.methods.length > 0;
  }
  private async refresh() {
    const context = this.journal.context, signal = this.journal.signal;
    if (!context || this.busy || !this.journal.live(context, signal)) return;
    this.busy = true; this.touch();
    try {
      if (this.journal.pending && !this.journal.blocked) { await this.journal.identity(context, signal); await this.deliver(context, signal, 'lookup'); }
      await this.admit(context, signal);
      if (!this.journal.pending && !this.methods.includes(this.method) && this.methods.length) this.method = this.methods[0]!;
      if (!this.journal.pending && !this.journal.blocked && !this.status) this.status = this.allowed ? '' : 'Merging is unavailable for this pull request.';
    } catch {
      if (this.journal.live(context, signal)) this.status = this.journal.pending
        ? 'The original merge command is saved. Check status after access is restored.'
        : 'Merge access could not be checked.';
    } finally { if (this.journal.live(context, signal)) { this.busy = false; this.touch(); } }
  }
  private async act(action: 'submit' | 'retry' | 'lookup' | 'cancel') {
    const context = this.journal.context, signal = this.journal.signal;
    if (!context || !this.journal.live(context, signal) || this.busy || this.journal.blocked) return;
    this.busy = true; this.status = ''; this.touch();
    let settled = false;
    try {
      if (action === 'submit' || action === 'retry') { await this.admit(context, signal); check(this.allowed); }
      else await this.journal.identity(context, signal);
      let pending = this.journal.stored(context);
      if (action === 'submit') {
        check(!pending && this.methods.includes(this.method) && validMergeMessage(this.method, noMergeMessage(this.method) ? '' : this.message));
        pending = {command: factoryCommandId(), method: this.method, message: noMergeMessage(this.method) ? '' : this.message,
          external: context.parent.external_id, head: context.head, expectedHead: true, cancel: false};
        this.journal.retain(context, pending);
      } else {
        check(pending && this.journal.pending && pending.command === this.journal.pending.command);
        check(pending.external === context.parent.external_id);
        if (action === 'retry') check(!pending.cancel && pending.head === context.head && this.methods.includes(pending.method));
        if (action === 'cancel') this.journal.requestCancel(context, pending);
      }
      settled = await this.deliver(context, signal, action === 'retry' ? 'submit' : action);
    } catch {
      if (this.journal.live(context, signal)) this.status = this.journal.pending
        ? 'No final merge outcome was confirmed. Check the saved command before retrying.'
        : 'Merge admission or saved state changed. Nothing was submitted.';
    } finally {
      if (this.journal.live(context, signal)) {
        this.busy = false; this.touch();
        if (settled) void this.refresh();
      }
    }
  }
  private async deliver(context: PullMergeContext, signal: AbortSignal, action: 'submit' | 'lookup' | 'cancel'): Promise<boolean> {
    const pending = this.journal.pending;
    check(pending);
    const base = this.path(context) + '/merges';
    const outcome = await this.journal.deliver(context, signal, action, pending, {
      endpoints: {submit: base, commands: base + '/commands'},
      body: {command_id: pending.command, method: pending.method, message: pending.message, ...(pending.expectedHead ? {expected_head: pending.head} : {})},
    });
    if (outcome.kind !== 'finished') return false;
    this.journal.settle(context, pending);
    this.status = this.journal.finished(pending, outcome, {
      committed: 'Merge recorded. Refresh to read the provider state.',
      tooLate: 'The merge was already recorded; cancellation was too late. Refresh to read the provider state.',
      cancelled: 'Merge cancelled before it was committed.', none: 'No merge was committed.',
    });
    if (outcome.effect === 'committed') this.dispatchEvent(new CustomEvent('pull-merge-saved', {detail: {repository: context.repository, pull: context.pull, command: pending.command}}));
    return true;
  }
  render() {
    if (!this.journal.context) return html``;
    return html`<section class="soda-pull-merge" aria-label="Merge pull request" aria-busy=${this.busy}>
      <h3>Merge</h3><p role="status">${this.status}</p>
      ${this.journal.pending ? html`<p>Saved ${this.journal.pending.method} merge command. Check its outcome before starting another.</p>
        <button type="button" @click=${() => this.act('lookup')} ?disabled=${this.busy || this.journal.blocked}>Check saved merge</button>
        <button type="button" @click=${() => this.act('retry')} ?disabled=${this.busy || this.journal.blocked || this.journal.pending.cancel || !this.allowed || !this.methods.includes(this.journal.pending.method)}>Retry original merge</button>
        <button type="button" @click=${() => this.act('cancel')} ?disabled=${this.busy || this.journal.blocked}>Cancel saved merge</button>`
        : html`<label>Method <select aria-label="Merge method" .value=${this.method} @change=${(event: Event) => {
            const next = (event.target as HTMLSelectElement).value;
            if (this.methods.includes(next as MergeMethod)) { this.method = next as MergeMethod; this.touch(); }
          }} ?disabled=${this.busy || !this.allowed}>
            ${this.methods.map(method => html`<option value=${method}>${mergeMethodLabel[method]}</option>`)}
          </select></label>
          ${!noMergeMessage(this.method) ? html`<label>Merge message <textarea aria-label="Merge message" rows="4" .value=${this.message} @input=${(event: Event) => { this.message = (event.target as HTMLTextAreaElement).value; this.touch(); }} ?disabled=${this.busy || !this.allowed}></textarea></label>` : ''}
          <button type="button" @click=${() => this.act('submit')} ?disabled=${this.busy || this.journal.blocked || !this.journal.durable || !this.allowed || !this.methods.includes(this.method) || !validMergeMessage(this.method, noMergeMessage(this.method) ? '' : this.message)}>Merge pull request</button>`}
    </section>`;
  }
}
customElements.define('soda-factory-pull-merge', SodaFactoryPullMerge);
