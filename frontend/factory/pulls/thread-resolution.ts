import {LitElement, html} from 'lit';
import {check, object, readSodaJSON} from '../api/models.js';
import type {FactoryTransport} from '../api/transport.js';
import type {PullRef} from '../api/collaboration.js';
import {commandOutcome} from '../api/command-response.js';
import {getJSON} from '../api/read.js';
import {factoryCommandId} from '../api/run-response.js';
import {parseHumanAnchor, type HumanAnchor} from './anchor.js';
import {pullItem} from './response.js';
import {threadRecord, threadRecordKey, type PendingThread, type ThreadAddress} from './thread-record.js';

export interface ThreadResolutionContext extends ThreadAddress {
  readonly transport: FactoryTransport;
  readonly parent: PullRef;
  commandScope(signal: AbortSignal): Promise<string>;
}
interface Selection {resolved: boolean; anchor: HumanAnchor | null; resolve: boolean; unresolve: boolean}

/** On-demand selection; provider action facts are checked again at submission. */
export class SodaFactoryThreadResolution extends LitElement {
  static properties = {version: {state: true}};
  declare private version: number;
  private current: ThreadResolutionContext | undefined;
  private binding = '';
  private generation = '';
  private lifetime = new AbortController();
  private pending: PendingThread | undefined;
  private durable = true;
  private blocked = false;
  private selection: Selection | undefined;
  private busy = false;
  private message = '';

  constructor() { super(); this.version = 0; }
  protected createRenderRoot() { return this; }
  get context() { return this.current; }
  private key(context: ThreadResolutionContext) {
    return JSON.stringify([context.scope, context.host, context.actor, context.repository, context.pull,
      context.threadID, context.parent.external_id, context.transport.generation]);
  }
  set context(context: ThreadResolutionContext | undefined) {
    const key = context && this.key(context);
    if (context && key === this.binding && this.current) return;
    this.lifetime.abort(); this.lifetime = new AbortController();
    this.current = undefined; this.binding = ''; this.pending = undefined; this.selection = undefined;
    this.durable = true; this.blocked = false; this.busy = false; this.message = '';
    if (context) {
      threadRecordKey(context);
      check(context.parent.kind === 'pull' && context.parent.repository === context.repository && context.parent.number === context.pull);
      check(!!context.parent.external_id.trim() && typeof context.commandScope === 'function' && !!context.transport.generation);
      this.current = context; this.binding = key!; this.generation = context.transport.generation;
      this.restore(context);
      if (this.pending && !this.blocked) void this.lookup();
    }
    this.touch();
  }
  disconnectedCallback() { this.context = undefined; super.disconnectedCallback(); }
  private touch() { this.version++; }
  private live(context: ThreadResolutionContext, signal: AbortSignal) {
    if (this.current !== context || this.lifetime.signal !== signal || signal.aborted || context.transport.generation !== this.generation) return false;
    if (this.key(context) !== this.binding) { this.context = undefined; return false; }
    return true;
  }
  private restore(context: ThreadResolutionContext) {
    const saved = threadRecord(context).read();
    this.pending = saved.pending; this.blocked = saved.blocked; this.durable = saved.durable;
    if (saved.blocked) this.message = 'Saved thread command could not be read. It was preserved.';
    else if (!saved.durable) this.message = 'Thread command storage is unavailable. No new change can be sent.';
  }
  private path(context: ThreadResolutionContext) {
    return `factory/collaboration/repositories/${context.repository}/pulls/${context.pull}`;
  }
  private async identity(context: ThreadResolutionContext, signal: AbortSignal) {
    const scope = await context.commandScope(AbortSignal.any([signal, AbortSignal.timeout(15000)]));
    check(this.live(context, signal) && scope === context.scope);
  }
  private async admit(context: ThreadResolutionContext, signal: AbortSignal): Promise<Selection> {
    await this.identity(context, signal);
    const [raw, selectedRaw] = await Promise.all([
      getJSON(context.transport, this.path(context), signal),
      getJSON(context.transport, this.path(context) + '/threads/' + encodeURIComponent(context.threadID) + '/selection', signal),
    ]);
    check(this.live(context, signal));
    const pull = pullItem(raw, context.host, context.repository, context.pull);
    check(pull.ref.external_id === context.parent.external_id && pull.state === 'open');
    const selected = object(selectedRaw), resolve = object(selected.resolve), unresolve = object(selected.unresolve);
    check(selected.host === context.host && selected.actor === context.actor && selected.repository === context.repository &&
      selected.number === context.pull && selected.thread_id === context.threadID && typeof selected.resolved === 'boolean');
    check(['allowed', 'denied', 'unsupported', 'unavailable', 'unknown'].includes(String(resolve.state)) &&
      ['allowed', 'denied', 'unsupported', 'unavailable', 'unknown'].includes(String(unresolve.state)));
    const selection: Selection = {resolved: selected.resolved,
      anchor: selected.original_anchor === null ? null : parseHumanAnchor(selected.original_anchor),
      resolve: resolve.state === 'allowed', unresolve: unresolve.state === 'allowed'};
    this.selection = selection;
    return selection;
  }
  private async open() {
    const context = this.current, signal = this.lifetime.signal;
    if (!context || this.busy || !this.live(context, signal)) return;
    this.busy = true; this.message = ''; this.touch();
    try {
      const selected = await this.admit(context, signal);
      if (!selected.resolve && !selected.unresolve) this.message = 'Changing this thread is unavailable.';
    } catch { if (this.live(context, signal)) this.message = 'Thread resolution could not be checked.'; }
    finally { if (this.live(context, signal)) { this.busy = false; this.touch(); } }
  }
  private async lookup() { await this.act('lookup'); }
  private async act(action: 'submit' | 'retry' | 'lookup' | 'cancel', desired?: boolean) {
    const context = this.current, signal = this.lifetime.signal;
    if (!context || !this.live(context, signal) || this.busy || this.blocked) return;
    this.busy = true; this.message = ''; this.touch();
    try {
      const selected = action === 'submit' || action === 'retry' ? await this.admit(context, signal) : undefined;
      if (!selected) await this.identity(context, signal);
      const saved = threadRecord(context).read();
      check(!saved.blocked && saved.durable);
      let pending = saved.pending;
      if (action === 'submit') {
        check(selected && !pending && desired !== undefined && desired !== selected.resolved);
        check(desired ? selected.resolve : selected.unresolve);
        pending = {command: factoryCommandId(), resolved: desired, anchor: selected.anchor};
        check(threadRecord(context).retain(pending) === 'saved');
        this.pending = pending;
      } else {
        check(pending && this.pending && pending.command === this.pending.command);
        if (action === 'retry') check(selected && pending.resolved !== selected.resolved &&
          (pending.resolved ? selected.resolve : selected.unresolve) &&
          JSON.stringify(pending.anchor) === JSON.stringify(selected.anchor));
      }
      await this.deliver(context, signal, action === 'retry' ? 'submit' : action);
    } catch {
      if (this.live(context, signal)) this.message = this.pending
        ? 'No final thread outcome was confirmed. Check the saved command before retrying.'
        : 'Thread resolution access or saved state changed. Nothing was submitted.';
    } finally { if (this.live(context, signal)) { this.busy = false; this.touch(); } }
  }
  private async deliver(context: ThreadResolutionContext, signal: AbortSignal, action: 'submit' | 'lookup' | 'cancel') {
    const pending = this.pending;
    check(pending && this.live(context, signal));
    const base = this.path(context) + '/thread-resolutions', command = base + '/commands/' + pending.command;
    const response = await context.transport.request(action === 'submit' ? base : command + (action === 'cancel' ? '/cancel' : ''), {
      method: action === 'lookup' ? 'GET' : 'POST', headers: {'Content-Type': 'application/json'},
      signal: AbortSignal.any([signal, AbortSignal.timeout(35000)]),
      ...(action === 'lookup' ? {} : {body: JSON.stringify(action === 'submit'
        ? {command_id: pending.command, thread_id: context.threadID, resolved: pending.resolved, anchor: pending.anchor} : {})}),
    });
    check(this.live(context, signal) && response.ok);
    const outcome = commandOutcome(await readSodaJSON(response), pending.command);
    check(this.live(context, signal));
    if (!outcome.finished) {
      this.message = outcome.completion === 'needs_intervention' ? 'The saved thread change needs attention. Check status.'
        : outcome.effect === 'committed' ? 'Thread change recorded; completion is pending.'
          : 'The thread change is not confirmed. Its original command remains saved.';
      return;
    }
    check(threadRecord(context).settle(pending));
    this.restore(context); this.selection = undefined;
    this.message = outcome.effect === 'committed' ? 'Thread resolution changed. Refresh to read its current state.'
      : action === 'cancel' ? 'Thread change cancelled.' : 'No thread change was committed.';
    if (outcome.effect === 'committed') this.dispatchEvent(new CustomEvent('pull-thread-saved', {detail: {repository: context.repository, pull: context.pull, thread: context.threadID}}));
  }
  render() {
    if (!this.current) return html``;
    const selected = this.selection;
    return html`<section class="soda-pull-thread-resolution" aria-label="Thread resolution" aria-busy=${this.busy}>
      <p role="status">${this.message}</p>
      ${this.pending ? html`<p>Saved ${this.pending.resolved ? 'resolve' : 'unresolve'} command.</p>
        <button type="button" @click=${() => this.act('lookup')} ?disabled=${this.busy || this.blocked}>Check saved thread change</button>
        <button type="button" @click=${() => this.act('retry')} ?disabled=${this.busy || this.blocked}>Retry original thread change</button>
        <button type="button" @click=${() => this.act('cancel')} ?disabled=${this.busy || this.blocked}>Cancel saved thread change</button>`
        : selected ? html`<p>Current state: ${selected.resolved ? 'resolved' : 'unresolved'}</p>
          <button type="button" @click=${() => this.act('submit', !selected.resolved)}
            ?disabled=${this.busy || this.blocked || !this.durable || !(selected.resolved ? selected.unresolve : selected.resolve)}>
            ${selected.resolved ? 'Reopen discussion' : 'Resolve discussion'}
          </button>`
          : html`<button type="button" @click=${this.open} ?disabled=${this.busy || this.blocked}>Manage resolution</button>`}
    </section>`;
  }
}
customElements.define('soda-factory-thread-resolution', SodaFactoryThreadResolution);
