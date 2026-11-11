import {LitElement, html} from 'lit';
import {check, object} from '../api/models.js';
import type {FactoryTransport} from '../api/transport.js';
import type {PullRef} from '../api/collaboration.js';
import {getJSON} from '../api/read.js';
import {factoryCommandId} from '../api/run-response.js';
import {JournalCommand} from '../shared/journal-command.js';
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
  private journal = new JournalCommand<PendingThread, ThreadResolutionContext>({
    key: context => JSON.stringify([context.scope, context.host, context.actor, context.repository, context.pull,
      context.threadID, context.parent.external_id, context.transport.generation]),
    words: {
      attention: 'The saved thread change needs attention. Check status.',
      completing: 'Thread change recorded; completion is pending.',
      unconfirmed: 'The thread change is not confirmed. Its original command remains saved.',
    },
    say: message => { this.message = message; },
    retire: () => { this.context = undefined; },
    store: context => threadRecord(context),
  });
  private selection: Selection | undefined;
  private busy = false;
  private message = '';

  constructor() { super(); this.version = 0; }
  protected createRenderRoot() { return this; }
  get context() { return this.journal.context; }
  set context(context: ThreadResolutionContext | undefined) {
    if (!this.journal.rebind(context)) return;
    this.selection = undefined;
    this.busy = false; this.message = '';
    if (context) {
      threadRecordKey(context);
      check(context.parent.kind === 'pull' && context.parent.repository === context.repository && context.parent.number === context.pull);
      check(!!context.parent.external_id.trim());
      this.journal.attach(context);
      this.restore(context);
      if (this.journal.pending && !this.journal.blocked) void this.lookup();
    }
    this.touch();
  }
  disconnectedCallback() { this.context = undefined; super.disconnectedCallback(); }
  private touch() { this.version++; }
  private restore(context: ThreadResolutionContext) {
    const saved = this.journal.restore(context);
    if (saved.blocked) this.message = 'Saved thread command could not be read. It was preserved.';
    else if (!saved.durable) this.message = 'Thread command storage is unavailable. No new change can be sent.';
  }
  private path(context: ThreadResolutionContext) {
    return `factory/collaboration/repositories/${context.repository}/pulls/${context.pull}`;
  }
  private async admit(context: ThreadResolutionContext, signal: AbortSignal): Promise<Selection> {
    await this.journal.identity(context, signal);
    const [raw, selectedRaw] = await Promise.all([
      getJSON(context.transport, this.path(context), signal),
      getJSON(context.transport, this.path(context) + '/threads/' + encodeURIComponent(context.threadID) + '/selection', signal),
    ]);
    check(this.journal.live(context, signal));
    const pull = pullItem(raw, context.host, context.repository, context.pull);
    check(pull.ref.external_id === context.parent.external_id);
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
    const context = this.journal.context, signal = this.journal.signal;
    if (!context || this.busy || !this.journal.live(context, signal)) return;
    this.busy = true; this.message = ''; this.touch();
    try {
      const selected = await this.admit(context, signal);
      if (!selected.resolve && !selected.unresolve) this.message = 'Changing this thread is unavailable.';
    } catch { if (this.journal.live(context, signal)) this.message = 'Thread resolution could not be checked.'; }
    finally { if (this.journal.live(context, signal)) { this.busy = false; this.touch(); } }
  }
  private async lookup() { await this.act('lookup'); }
  private async act(action: 'submit' | 'retry' | 'lookup' | 'cancel', desired?: boolean) {
    const context = this.journal.context, signal = this.journal.signal;
    if (!context || !this.journal.live(context, signal) || this.busy || this.journal.blocked) return;
    this.busy = true; this.message = ''; this.touch();
    try {
      const selected = action === 'submit' || action === 'retry' ? await this.admit(context, signal) : undefined;
      if (!selected) await this.journal.identity(context, signal);
      let pending = this.journal.stored(context);
      if (action === 'submit') {
        check(selected && !pending && desired !== undefined && desired !== selected.resolved);
        check(desired ? selected.resolve : selected.unresolve);
        pending = {command: factoryCommandId(), resolved: desired, anchor: selected.anchor, cancel: false};
        this.journal.retain(context, pending);
      } else {
        check(pending && this.journal.pending && pending.command === this.journal.pending.command);
        if (action === 'retry') check(!pending.cancel && selected && pending.resolved !== selected.resolved &&
          (pending.resolved ? selected.resolve : selected.unresolve) &&
          JSON.stringify(pending.anchor) === JSON.stringify(selected.anchor));
        if (action === 'cancel') this.journal.requestCancel(context, pending);
      }
      await this.deliver(context, signal, action === 'retry' ? 'submit' : action);
    } catch {
      if (this.journal.live(context, signal)) this.message = this.journal.pending
        ? 'No final thread outcome was confirmed. Check the saved command before retrying.'
        : 'Thread resolution access or saved state changed. Nothing was submitted.';
    } finally { if (this.journal.live(context, signal)) { this.busy = false; this.touch(); } }
  }
  private async deliver(context: ThreadResolutionContext, signal: AbortSignal, action: 'submit' | 'lookup' | 'cancel') {
    const pending = this.journal.pending;
    check(pending);
    const base = this.path(context) + '/thread-resolutions';
    const outcome = await this.journal.deliver(context, signal, action, pending, {
      endpoints: {submit: base, commands: base + '/commands'},
      body: {command_id: pending.command, thread_id: context.threadID, resolved: pending.resolved, anchor: pending.anchor},
    });
    if (outcome.kind !== 'finished') return;
    this.journal.settle(context, pending);
    this.selection = undefined;
    this.message = this.journal.finished(pending, outcome, {
      committed: 'Thread resolution changed. Refresh to read its current state.',
      tooLate: 'The thread change was already recorded; cancellation was too late. Refresh to read its current state.',
      cancelled: 'Thread change cancelled.', none: 'No thread change was committed.',
    });
    if (outcome.effect === 'committed') this.dispatchEvent(new CustomEvent('pull-thread-saved', {detail: {repository: context.repository, pull: context.pull, thread: context.threadID}}));
  }
  render() {
    if (!this.journal.context) return html``;
    const selected = this.selection;
    return html`<section class="soda-pull-thread-resolution" aria-label="Thread resolution" aria-busy=${this.busy}>
      <p role="status">${this.message}</p>
      ${this.journal.pending ? html`<p>Saved ${this.journal.pending.resolved ? 'resolve' : 'unresolve'} command.</p>
        <button type="button" @click=${() => this.act('lookup')} ?disabled=${this.busy || this.journal.blocked}>Check saved thread change</button>
        <button type="button" @click=${() => this.act('retry')} ?disabled=${this.busy || this.journal.blocked || this.journal.pending.cancel}>Retry original thread change</button>
        <button type="button" @click=${() => this.act('cancel')} ?disabled=${this.busy || this.journal.blocked}>Cancel saved thread change</button>`
        : selected ? html`<p>Current state: ${selected.resolved ? 'resolved' : 'unresolved'}</p>
          <button type="button" @click=${() => this.act('submit', !selected.resolved)}
            ?disabled=${this.busy || this.journal.blocked || !this.journal.durable || !(selected.resolved ? selected.unresolve : selected.resolve)}>
            ${selected.resolved ? 'Reopen discussion' : 'Resolve discussion'}
          </button>`
          : html`<button type="button" @click=${this.open} ?disabled=${this.busy || this.journal.blocked}>Manage resolution</button>`}
    </section>`;
  }
}
customElements.define('soda-factory-thread-resolution', SodaFactoryThreadResolution);
