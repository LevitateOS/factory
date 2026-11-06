import {LitElement, html} from 'lit';
import {check, object, readSodaJSON} from '../api/models.js';
import type {PullRef} from '../api/collaboration.js';
import type {FactoryTransport} from '../api/transport.js';
import {pullActionAllowed} from '../api/capability-response.js';
import {commandOutcome} from '../api/command-response.js';
import {getJSON} from '../api/read.js';
import {factoryCommandId} from '../api/run-response.js';
import {pullItem, pullReviewRequests, pullReviewerLookup} from './response.js';
import {reviewerRecord, sameReviewer, type PendingReviewer, type ReviewerAddress, type ReviewerTarget} from './reviewer-record.js';

export interface PullReviewersContext extends ReviewerAddress {
  readonly transport: FactoryTransport;
  readonly parent: PullRef;
  commandScope(signal: AbortSignal): Promise<string>;
}
const targetKey = (target: ReviewerTarget) => `${target.kind}:${target.id}`;

/** Current requests plus canonical participants; one retained change at a time. */
export class SodaFactoryPullReviewers extends LitElement {
  static properties = {version: {state: true}, participants: {attribute: false}};
  declare private version: number;
  declare participants: readonly ReviewerTarget[];
  private current: PullReviewersContext | undefined;
  private binding = '';
  private lifetime = new AbortController();
  private pending: PendingReviewer | undefined;
  private blocked = false;
  private durable = true;
  private busy = false;
  private allowed = false;
  private message = '';
  private items: ReturnType<typeof pullReviewRequests>['items'] = [];
  private limits: ReturnType<typeof pullReviewRequests>['limits'] = null;
  private known = new Map<string, ReviewerTarget>();

  constructor() { super(); this.version = 0; this.participants = []; }
  protected createRenderRoot() { return this; }
  get context() { return this.current; }
  set context(context: PullReviewersContext | undefined) {
    const key = context && JSON.stringify([context.scope, context.host, context.actor, context.repository, context.pull,
      context.parent.external_id, context.transport.generation]);
    if (context && key === this.binding) return;
    this.lifetime.abort(); this.lifetime = new AbortController();
    this.current = undefined; this.binding = ''; this.pending = undefined; this.items = []; this.limits = null; this.known.clear();
    this.blocked = false; this.durable = true; this.busy = false; this.allowed = false; this.message = '';
    if (context) {
      reviewerRecord(context);
      check(context.parent.kind === 'pull' && context.parent.repository === context.repository && context.parent.number === context.pull);
      check(!!context.parent.external_id.trim() && !!context.transport.generation && typeof context.commandScope === 'function');
      this.current = context; this.binding = key!; this.restore(context); void this.refresh();
    }
    this.touch();
  }
  disconnectedCallback() { this.context = undefined; super.disconnectedCallback(); }
  private touch() { this.version++; }
  private live(context: PullReviewersContext, signal: AbortSignal) {
    return this.current === context && this.lifetime.signal === signal && !signal.aborted &&
      this.binding === JSON.stringify([context.scope, context.host, context.actor, context.repository, context.pull,
        context.parent.external_id, context.transport.generation]);
  }
  private restore(context: PullReviewersContext) {
    const saved = reviewerRecord(context).read();
    this.pending = saved.pending; this.blocked = saved.blocked; this.durable = saved.durable;
    if (saved.blocked) this.message = 'Saved reviewer command could not be read. It was preserved.';
    else if (!saved.durable) this.message = 'Reviewer command storage is unavailable. No new change can be sent.';
  }
  private path(context: PullReviewersContext) { return `factory/collaboration/repositories/${context.repository}/pulls/${context.pull}`; }
  private async identity(context: PullReviewersContext, signal: AbortSignal) {
    const scope = await context.commandScope(AbortSignal.any([signal, AbortSignal.timeout(15000)]));
    check(this.live(context, signal) && scope === context.scope);
  }
  private async read(context: PullReviewersContext, signal: AbortSignal) {
    this.allowed = false;
    await this.identity(context, signal);
    const path = this.path(context);
    const [raw, caps, requested] = await Promise.all([getJSON(context.transport, path, signal),
      getJSON(context.transport, path + '/capabilities', signal), getJSON(context.transport, path + '/review-requests', signal)]);
    check(this.live(context, signal));
    const pull = pullItem(raw, context.host, context.repository, context.pull);
    const snapshot = pullReviewRequests(requested, context.host, context.repository, context.pull);
    check(pull.ref.external_id === context.parent.external_id && snapshot.pull.external_id === context.parent.external_id);
    this.items = snapshot.items;
    this.limits = snapshot.limits;
    for (const item of this.items) if (item.target) this.known.set(targetKey(item.target), item.target);
    const head = object(caps).head;
    check(head === null || typeof head === 'string' && /^[a-f0-9]{40}(?:[a-f0-9]{24})?$/i.test(head));
    this.allowed = pull.state === 'open' && snapshot.allTargetsVisible &&
      pullActionAllowed(caps, context.repository, context.actor, context.parent, head as string | null, 'pull.review');
    if (!snapshot.allTargetsVisible) this.message = 'Some reviewer identities are hidden. Reviewer changes are unavailable.';
  }
  private async refresh() {
    const context = this.current, signal = this.lifetime.signal;
    if (!context || this.busy || !this.live(context, signal)) return;
    this.busy = true; this.touch();
    try {
      if (this.pending && !this.blocked) { await this.identity(context, signal); await this.deliver(context, signal, 'lookup'); }
      await this.read(context, signal);
    } catch { if (this.live(context, signal)) this.message = 'Review requests could not be refreshed. Saved commands were preserved.'; }
    finally { if (this.live(context, signal)) { this.busy = false; this.touch(); } }
  }
  private async act(action: 'submit' | 'retry' | 'lookup' | 'cancel', target?: ReviewerTarget, requested?: boolean) {
    const context = this.current, signal = this.lifetime.signal;
    if (!context || this.busy || this.blocked || !this.live(context, signal)) return;
    this.busy = true; this.message = ''; this.touch();
    try {
      if (action === 'submit' || action === 'retry') { await this.read(context, signal); check(this.allowed); }
      else await this.identity(context, signal);
      const saved = reviewerRecord(context).read();
      check(!saved.blocked && saved.durable);
      if (this.pending) check(saved.pending && sameReviewer(this.pending, saved.pending));
      let pending = saved.pending;
      if (action === 'submit') {
        check(!pending && target && requested !== undefined && !(requested && target.kind === 'user' && target.id === context.actor));
        check(!requested || this.requestSupported(target));
        check(this.targets().some(item => targetKey(item) === targetKey(target)));
        check(this.items.some(item => item.target && targetKey(item.target) === targetKey(target)) !== requested);
        pending = {command: factoryCommandId(), target, requested, external: context.parent.external_id, cancel: false};
      } else check(pending && pending.external === context.parent.external_id);
      if (action === 'retry') check(!pending.cancel);
      if (action === 'cancel') pending = {...pending, cancel: true};
      if (action === 'submit' || action === 'cancel') check(reviewerRecord(context).retain(pending) === 'saved');
      this.pending = pending;
      await this.deliver(context, signal, action === 'retry' ? 'submit' : action);
      if (!this.pending) await this.read(context, signal);
    } catch { if (this.live(context, signal)) this.message = this.pending
      ? 'No final reviewer outcome was confirmed. Check the saved command before retrying.'
      : 'Reviewer access or selection changed. Nothing was submitted.'; }
    finally { if (this.live(context, signal)) { this.busy = false; this.touch(); } }
  }
  private async lookup(event: SubmitEvent) {
    event.preventDefault();
    const context = this.current, signal = this.lifetime.signal;
    if (!context || this.busy || this.pending || this.blocked || !this.live(context, signal) || !(event.currentTarget instanceof HTMLFormElement)) return;
    const form = new FormData(event.currentTarget), kind = form.get('kind'), name = String(form.get('name') ?? '').trim();
    if ((kind !== 'user' && kind !== 'team') || !name) return;
    this.busy = true; this.message = ''; this.touch();
    try {
      await this.identity(context, signal);
      const query = new URLSearchParams({kind, name});
      const raw = await getJSON(context.transport, this.path(context) + '/review-requests/lookup?' + query, signal);
      check(this.live(context, signal));
      const found = pullReviewerLookup(raw, context.host, context.repository, context.pull, kind);
      check(found.pull.external_id === context.parent.external_id);
      this.known.set(targetKey(found.target), found.target);
      this.message = kind === 'user' && found.target.id === context.actor
        ? 'You cannot request your own review.'
        : kind === 'team' && this.limits && !this.limits.teams
          ? 'Team review requests are unavailable for this repository.'
          : !this.requestSupported(found.target) ? 'This repository’s reviewer limit has been reached.'
            : `Found ${found.target.label}. Choose Request review to send a request.`;
    } catch { if (this.live(context, signal)) this.message = 'The reviewer could not be found with this account. No request was sent.'; }
    finally { if (this.live(context, signal)) { this.busy = false; this.touch(); } }
  }
  private async deliver(context: PullReviewersContext, signal: AbortSignal, action: 'submit' | 'lookup' | 'cancel') {
    const pending = this.pending;
    check(pending && pending.external === context.parent.external_id && this.live(context, signal));
    const base = this.path(context) + '/review-requests', command = base + '/commands/' + pending.command;
    const response = await context.transport.request(action === 'submit' ? base : command + (action === 'cancel' ? '/cancel' : ''), {
      method: action === 'lookup' ? 'GET' : 'POST', headers: {'Content-Type': 'application/json'},
      signal: AbortSignal.any([signal, AbortSignal.timeout(35000)]),
      ...(action === 'lookup' ? {} : {body: JSON.stringify(action === 'submit'
        ? {command_id: pending.command, reviewer_kind: pending.target.kind, reviewer_id: pending.target.id, requested: pending.requested} : {})}),
    });
    check(this.live(context, signal) && response.ok);
    const outcome = commandOutcome(await readSodaJSON(response), pending.command);
    check(this.live(context, signal));
    if (!outcome.finished) {
      this.message = outcome.completion === 'needs_intervention'
        ? 'The saved reviewer change needs attention. Its original command remains saved.'
        : 'The reviewer change is not complete. Its original command remains saved.';
      return;
    }
    check(reviewerRecord(context).settle(pending));
    if (outcome.effect === 'committed') this.known.set(targetKey(pending.target), pending.target);
    this.restore(context);
    this.message = outcome.effect === 'committed' ? 'Reviewer request changed.' : 'No reviewer change was committed.';
  }
  private targets() {
    const targets = new Map(this.known);
    for (const target of this.participants) targets.set(targetKey(target), target);
    return [...targets.values()];
  }
  private requestSupported(target: ReviewerTarget) {
    return !this.limits || (target.kind !== 'team' || this.limits.teams) &&
      this.items.filter(item => !item.asCodeOwner).length < this.limits.maximum;
  }
  render() {
    if (!this.current) return html``;
    const pending = this.pending, unavailable = this.busy || this.blocked || !this.durable || !this.allowed;
    const requested = new Set(this.items.flatMap(item => item.target ? [targetKey(item.target)] : []));
    return html`<section class="soda-pull-reviewers" aria-label="Review requests" aria-busy=${this.busy}>
      <h3>Reviewers</h3><p role="status">${this.message}</p>
      ${!pending ? html`<form @submit=${this.lookup}>
        <label>Reviewer type <select name="kind" ?disabled=${this.busy || this.blocked}><option value="user">User</option><option value="team">Team</option></select></label>
        <label>Username or team slug <input name="name" maxlength="512" required ?disabled=${this.busy || this.blocked}></label>
        <button type="submit" ?disabled=${this.busy || this.blocked}>Find reviewer</button>
      </form>` : ''}
      <ul>${this.items.map(item => html`<li>${item.label}${!pending && item.target ? html`<button type="button"
        ?disabled=${unavailable} @click=${() => this.act('submit', item.target!, false)}>Remove request for ${item.label}</button>` : ''}</li>`)}</ul>
      ${pending ? html`<p>Saved ${pending.requested ? 'request' : 'removal'} for ${pending.target.label}.</p>
        <button type="button" ?disabled=${this.busy || this.blocked} @click=${() => this.act('lookup')}>Check saved reviewer change</button>
        <button type="button" ?disabled=${unavailable || pending.cancel} @click=${() => this.act('retry')}>Retry original reviewer change</button>
        <button type="button" ?disabled=${this.busy || this.blocked || pending.cancel} @click=${() => this.act('cancel')}>Cancel saved reviewer change</button>`
        : this.targets().filter(target => !requested.has(targetKey(target)) && !(target.kind === 'user' && target.id === this.current?.actor)).map(target => html`<button type="button"
          ?disabled=${unavailable || !this.requestSupported(target)} @click=${() => this.act('submit', target, true)}>Request review from ${target.label}</button>`)}
      <button type="button" ?disabled=${this.busy} @click=${() => this.refresh()}>Refresh reviewers</button>
    </section>`;
  }
}
customElements.define('soda-factory-pull-reviewers', SodaFactoryPullReviewers);
