import {LitElement, html} from 'lit';
import {check, object, readSodaJSON} from '../api/models.js';
import type {FactoryTransport} from '../api/transport.js';
import type {PullRef} from '../api/collaboration.js';
import {pullActionAllowed} from '../api/capability-response.js';
import {commandOutcome} from '../api/command-response.js';
import {getJSON} from '../api/read.js';
import {factoryCommandId} from '../api/run-response.js';
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
  private current: PullMergeContext | undefined;
  private binding = '';
  private generation = '';
  private lifetime = new AbortController();
  private pending: PendingMerge | undefined;
  private durable = true;
  private blocked = false;
  private allowed = false;
  private methods: MergeMethod[] = [];
  private busy = false;
  private method: MergeMethod = 'merge';
  private message = '';
  private status = '';

  constructor() { super(); this.version = 0; }
  protected createRenderRoot() { return this; }
  get context() { return this.current; }
  private key(context: PullMergeContext) {
    return JSON.stringify([context.scope, context.host, context.actor, context.repository, context.pull,
      context.parent.external_id, context.head, context.transport.generation]);
  }
  set context(context: PullMergeContext | undefined) {
    const key = context && this.key(context);
    if (context && key === this.binding && this.current) return;
    this.lifetime.abort(); this.lifetime = new AbortController();
    this.current = undefined; this.binding = ''; this.allowed = false; this.methods = []; this.busy = false; this.method = 'merge'; this.message = ''; this.status = '';
    this.pending = undefined; this.blocked = false; this.durable = true;
    if (context) {
      mergeRecordKey(context);
      check(context.parent.kind === 'pull' && context.parent.repository === context.repository && context.parent.number === context.pull);
      check(!!context.parent.external_id.trim() && /^[a-f0-9]{40}(?:[a-f0-9]{24})?$/i.test(context.head));
      check(typeof context.commandScope === 'function' && !!context.transport.generation);
      this.current = context; this.binding = key!; this.generation = context.transport.generation;
      this.restore(context); void this.refresh();
    }
    this.touch();
  }
  disconnectedCallback() { this.context = undefined; super.disconnectedCallback(); }
  private touch() { this.version++; }
  private live(context: PullMergeContext, signal: AbortSignal) {
    if (this.current !== context || this.lifetime.signal !== signal || signal.aborted || context.transport.generation !== this.generation) return false;
    if (this.key(context) !== this.binding) { this.context = undefined; return false; }
    return true;
  }
  private restore(context: PullMergeContext) {
    const saved = mergeRecord(context).read();
    this.pending = saved.pending; this.blocked = saved.blocked; this.durable = saved.durable;
    if (saved.blocked) this.status = 'Saved merge command could not be read. It was preserved.';
    else if (!saved.durable) this.status = 'Merge command storage is unavailable. No new merge can be sent.';
  }
  private path(context: PullMergeContext) {
    return `factory/collaboration/repositories/${context.repository}/pulls/${context.pull}`;
  }
  private async identity(context: PullMergeContext, signal: AbortSignal) {
    const scope = await context.commandScope(AbortSignal.any([signal, AbortSignal.timeout(15000)]));
    check(this.live(context, signal) && scope === context.scope);
  }
  private async admit(context: PullMergeContext, signal: AbortSignal) {
    this.allowed = false; this.methods = [];
    await this.identity(context, signal);
    const [raw, caps] = await Promise.all([
      getJSON(context.transport, this.path(context), signal),
      getJSON(context.transport, this.path(context) + '/capabilities', signal),
    ]);
    check(this.live(context, signal));
    const pull = pullItem(raw, context.host, context.repository, context.pull);
    check(pull.ref.external_id === context.parent.external_id);
    const current = pull.state === 'open' && pull.draft === false &&
      (pull.head.commit === null || pull.head.commit === context.head) &&
      pullActionAllowed(caps, context.repository, context.actor, context.parent, context.head, 'pull.merge');
    this.methods = current ? availableMergeMethods(caps) : [];
    this.allowed = this.methods.length > 0;
  }
  private async refresh() {
    const context = this.current, signal = this.lifetime.signal;
    if (!context || this.busy || !this.live(context, signal)) return;
    this.busy = true; this.touch();
    try {
      if (this.pending && !this.blocked) { await this.identity(context, signal); await this.deliver(context, signal, 'lookup'); }
      await this.admit(context, signal);
      if (!this.pending && !this.methods.includes(this.method) && this.methods.length) this.method = this.methods[0]!;
      if (!this.pending && !this.blocked && !this.status) this.status = this.allowed ? '' : 'Merging is unavailable for this pull request.';
    } catch {
      if (this.live(context, signal)) this.status = this.pending
        ? 'The original merge command is saved. Check status after access is restored.'
        : 'Merge access could not be checked.';
    } finally { if (this.live(context, signal)) { this.busy = false; this.touch(); } }
  }
  private async act(action: 'submit' | 'retry' | 'lookup' | 'cancel') {
    const context = this.current, signal = this.lifetime.signal;
    if (!context || !this.live(context, signal) || this.busy || this.blocked) return;
    this.busy = true; this.status = ''; this.touch();
    let settled = false;
    try {
      if (action === 'submit' || action === 'retry') { await this.admit(context, signal); check(this.allowed); }
      else await this.identity(context, signal);
      const saved = mergeRecord(context).read();
      check(!saved.blocked && saved.durable);
      let pending = saved.pending;
      if (action === 'submit') {
        check(!pending && this.methods.includes(this.method) && validMergeMessage(this.method, noMergeMessage(this.method) ? '' : this.message));
        pending = {command: factoryCommandId(), method: this.method, message: noMergeMessage(this.method) ? '' : this.message,
          external: context.parent.external_id, head: context.head, expectedHead: true, cancel: false};
        check(mergeRecord(context).retain(pending) === 'saved');
        this.pending = pending;
      } else {
        check(pending && this.pending && pending.command === this.pending.command);
        check(pending.external === context.parent.external_id);
        if (action === 'retry') check(!pending.cancel && pending.head === context.head && this.methods.includes(pending.method));
        if (action === 'cancel') {
          pending = {...pending, cancel: true};
          check(mergeRecord(context).retain(pending) === 'saved');
          this.pending = pending;
        }
      }
      settled = await this.deliver(context, signal, action === 'retry' ? 'submit' : action);
    } catch {
      if (this.live(context, signal)) this.status = this.pending
        ? 'No final merge outcome was confirmed. Check the saved command before retrying.'
        : 'Merge admission or saved state changed. Nothing was submitted.';
    } finally {
      if (this.live(context, signal)) {
        this.busy = false; this.touch();
        if (settled) void this.refresh();
      }
    }
  }
  private async deliver(context: PullMergeContext, signal: AbortSignal, action: 'submit' | 'lookup' | 'cancel'): Promise<boolean> {
    const pending = this.pending;
    check(pending && this.live(context, signal));
    const base = this.path(context) + '/merges', command = base + '/commands/' + pending.command;
    const response = await context.transport.request(action === 'submit' ? base : command + (action === 'cancel' ? '/cancel' : ''), {
      method: action === 'lookup' ? 'GET' : 'POST', headers: {'Content-Type': 'application/json'},
      signal: AbortSignal.any([signal, AbortSignal.timeout(35000)]),
      ...(action === 'lookup' ? {} : {body: JSON.stringify(action === 'submit'
        ? {command_id: pending.command, method: pending.method, message: pending.message, ...(pending.expectedHead ? {expected_head: pending.head} : {})} : {})}),
    });
    check(this.live(context, signal) && response.ok);
    const outcome = commandOutcome(await readSodaJSON(response), pending.command);
    check(this.live(context, signal));
    if (!outcome.finished) {
      this.status = outcome.completion === 'needs_intervention' ? 'The saved merge needs attention. Check status.'
        : outcome.effect === 'committed' ? 'Merge effect recorded; completion is pending.'
          : 'The merge is not confirmed. Its original command remains saved.';
      return false;
    }
    check(mergeRecord(context).settle(pending));
    this.restore(context);
    this.status = outcome.effect === 'committed'
      ? pending.cancel ? 'The merge was already recorded; cancellation was too late. Refresh to read the provider state.' : 'Merge recorded. Refresh to read the provider state.'
      : pending.cancel ? 'Merge cancelled before it was committed.' : 'No merge was committed.';
    if (outcome.effect === 'committed') this.dispatchEvent(new CustomEvent('pull-merge-saved', {detail: {repository: context.repository, pull: context.pull, command: pending.command}}));
    return true;
  }
  render() {
    if (!this.current) return html``;
    return html`<section class="soda-pull-merge" aria-label="Merge pull request" aria-busy=${this.busy}>
      <h3>Merge</h3><p role="status">${this.status}</p>
      ${this.pending ? html`<p>Saved ${this.pending.method} merge command. Check its outcome before starting another.</p>
        <button type="button" @click=${() => this.act('lookup')} ?disabled=${this.busy || this.blocked}>Check saved merge</button>
        <button type="button" @click=${() => this.act('retry')} ?disabled=${this.busy || this.blocked || this.pending.cancel || !this.allowed || !this.methods.includes(this.pending.method)}>Retry original merge</button>
        <button type="button" @click=${() => this.act('cancel')} ?disabled=${this.busy || this.blocked}>Cancel saved merge</button>`
        : html`<label>Method <select aria-label="Merge method" .value=${this.method} @change=${(event: Event) => {
            const next = (event.target as HTMLSelectElement).value;
            if (this.methods.includes(next as MergeMethod)) { this.method = next as MergeMethod; this.touch(); }
          }} ?disabled=${this.busy || !this.allowed}>
            ${this.methods.map(method => html`<option value=${method}>${mergeMethodLabel[method]}</option>`)}
          </select></label>
          ${!noMergeMessage(this.method) ? html`<label>Merge message <textarea aria-label="Merge message" rows="4" .value=${this.message} @input=${(event: Event) => { this.message = (event.target as HTMLTextAreaElement).value; this.touch(); }} ?disabled=${this.busy || !this.allowed}></textarea></label>` : ''}
          <button type="button" @click=${() => this.act('submit')} ?disabled=${this.busy || this.blocked || !this.durable || !this.allowed || !this.methods.includes(this.method) || !validMergeMessage(this.method, noMergeMessage(this.method) ? '' : this.message)}>Merge pull request</button>`}
    </section>`;
  }
}
customElements.define('soda-factory-pull-merge', SodaFactoryPullMerge);
