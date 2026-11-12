import {LitElement, html} from 'lit';
import {check, object} from '../api/models.js';
import type {FactoryTransport} from '../api/transport.js';
import type {PullRef} from '../api/collaboration.js';
import {pullActionAllowed} from '../api/capability-response.js';
import {getJSON} from '../api/read.js';
import {commandOutcome} from '../api/command-response.js';
import {factoryCommandId} from '../api/run-response.js';
import {JournalCommand} from '../shared/journal-command.js';
import {type PullDisplayRead, pullItem, pullRef, pullReview} from './response.js';
import {
  emptyReviewWriting, readReviewWriting, reloadReviewWriting, reviewRecordKey,
  sameReviewPending, saveReviewWriting, validReviewBody,
  type ReviewAddress, type ReviewEvent, type ReviewPending, type ReviewSaved, type ReviewWriting,
} from './review-record.js';

export interface PullReviewContext extends ReviewAddress {
  readonly transport: FactoryTransport;
  readonly parent: PullRef;
  readonly head: string;
  readonly display?: PullDisplayRead;
  commandScope(signal: AbortSignal): Promise<string>;
}
export type PullReviewPorts = Omit<PullReviewContext, 'parent' | 'head'>;
const allDenied = () => ({draft: false, comment: false, approve: false, request_changes: false});

/** One retained review intent. A remount looks up its command, never resubmits it. */
export class SodaFactoryPullReview extends LitElement {
  static properties = {version: {state: true}};
  declare private version: number;
  private journal = new JournalCommand<ReviewPending, PullReviewContext>({
    key: context => JSON.stringify([context.scope, context.host, context.actor, context.repository, context.pull,
      context.parent.external_id, context.head, context.transport.generation]),
    words: {
      attention: 'The saved review needs attention. Check status.',
      completing: 'The review was recorded; provider completion is pending.',
      unconfirmed: 'The review is not confirmed. Its original command remains saved.',
    },
    say: message => { this.message = message; },
    retire: () => { this.context = undefined; },
  });
  private saved: ReviewSaved = {raw: '', writing: undefined, durable: true, blocked: false};
  private writing = emptyReviewWriting();
  private allowed = allDenied();
  private busy = false;
  private opened = false;
  private message = '';

  constructor() { super(); this.version = 0; }
  protected createRenderRoot() { return this; }
  get context() { return this.journal.context; }
  set context(context: PullReviewContext | undefined) {
    if (!this.journal.rebind(context)) return;
    this.allowed = allDenied(); this.busy = false; this.message = '';
    this.writing = emptyReviewWriting();
    if (context) {
      reviewRecordKey(context);
      check(context.parent.kind === 'pull' && context.parent.repository === context.repository && context.parent.number === context.pull);
      check(!!context.parent.external_id.trim() && /^[a-f0-9]{40}(?:[a-f0-9]{24})?$/i.test(context.head));
      this.journal.attach(context);
      this.restore(context);
      this.opened = false;
    }
    this.touch();
  }
  disconnectedCallback() { this.context = undefined; super.disconnectedCallback(); }
  private touch() { this.version++; }
  private restore(context: PullReviewContext, reload = false) {
    this.saved = reload ? reloadReviewWriting(context) : readReviewWriting(context);
    this.writing = this.saved.writing ?? emptyReviewWriting();
    if (this.saved.blocked) this.message = 'Saved review writing changed or could not be read. It was preserved; reload before submitting.';
    else if (!this.saved.durable) this.message = 'Review writing is only saved in this window. Submission waits for durable storage.';
  }
  private path(context: PullReviewContext) {
    return `factory/collaboration/repositories/${context.repository}/pulls/${context.pull}`;
  }
  private async admit(context: PullReviewContext, signal: AbortSignal, refresh = false) {
    this.allowed = allDenied();
    await this.journal.identity(context, signal);
    const display = context.display ? await context.display(signal, refresh) : undefined;
    const [raw, caps] = display ? [display.pull, display.capabilities] : await Promise.all([
      getJSON(context.transport, this.path(context), signal),
      getJSON(context.transport, this.path(context) + '/capabilities', signal),
    ]);
    check(this.journal.live(context, signal));
    const pull = display ? display.pull : pullItem(raw, context.host, context.repository, context.pull);
    check(pull.ref.external_id === context.parent.external_id &&
      (pull.head.commit === null || pull.head.commit === context.head) && pull.state === 'open');
    this.allowed = {
      draft: pullActionAllowed(caps, context.repository, context.actor, context.parent, context.head, 'pull.review.comment'),
      comment: pullActionAllowed(caps, context.repository, context.actor, context.parent, context.head, 'pull.review.comment'),
      approve: pullActionAllowed(caps, context.repository, context.actor, context.parent, context.head, 'pull.review.approve'),
      request_changes: pullActionAllowed(caps, context.repository, context.actor, context.parent, context.head, 'pull.review.request_changes'),
    };
    const draft = object(caps).review_draft;
    if (draft !== undefined) {
      const fact = object(draft);
      check(['allowed', 'denied', 'unsupported', 'unavailable', 'unknown'].includes(String(fact.state)));
      this.allowed.draft &&= fact.state === 'allowed';
    }
  }
  private async refresh() {
    const context = this.journal.context, signal = this.journal.signal;
    if (!context || this.busy || !this.journal.live(context, signal)) return;
    this.busy = true; this.touch();
    try {
      if (this.writing.pending && !this.saved.blocked) {
        await this.journal.identity(context, signal);
        await this.deliver(context, signal, 'lookup');
      }
      await this.admit(context, signal, true);
      if (!this.writing.pending && !this.saved.blocked) this.message = Object.values(this.allowed).some(Boolean)
        ? this.saved.durable ? '' : 'Review writing is only saved in this window.'
        : 'Review actions are unavailable for this account.';
    } catch {
      if (this.journal.live(context, signal)) this.message = this.writing.pending
        ? 'The original review is saved. Check status after access is restored.'
        : 'Review access could not be checked. Your writing was preserved.';
    } finally { if (this.journal.live(context, signal)) { this.busy = false; this.touch(); } }
  }
  private write(context: PullReviewContext, next: ReviewWriting) {
    const result = saveReviewWriting(context, this.saved.raw, next);
    if (result === 'conflict') {
      this.saved = {...this.saved, blocked: true};
      this.message = 'Saved review writing changed. Reload it before submitting.';
    } else {
      this.saved = readReviewWriting(context); this.writing = next;
      if (result === 'unavailable') this.message = 'Review writing is only saved in this window. No submission was sent.';
    }
    return result;
  }
  private input(event: Event) {
    const context = this.journal.context;
    if (!context || this.busy || this.writing.pending || this.saved.blocked || !this.journal.live(context, this.journal.signal)) return;
    const body = (event.target as HTMLTextAreaElement).value;
    const next: ReviewWriting = {draft: {revision: factoryCommandId(), body}};
    const result = this.write(context, next);
    if (result === 'conflict') this.writing = next;
    else if (result === 'saved') this.message = '';
    this.touch();
  }
  private async act(action: 'submit' | 'retry' | 'lookup' | 'cancel', event?: ReviewEvent) {
    const context = this.journal.context, signal = this.journal.signal;
    if (!context || !this.journal.live(context, signal) || this.busy || this.saved.blocked) return;
    this.busy = true; this.message = ''; this.touch();
    try {
      if (action === 'submit' || action === 'retry') {
        await this.admit(context, signal);
        check(event ? this.allowed[event] : this.writing.pending && this.allowed[this.writing.pending.event]);
        check(action !== 'retry' || !this.writing.pending?.cancel);
      } else await this.journal.identity(context, signal);
      const current = readReviewWriting(context), shown = this.writing.pending;
      check(!current.blocked && current.raw === this.saved.raw);
      if (shown) check(current.writing?.pending && sameReviewPending(shown, current.writing.pending));
      let pending = shown;
      if (action === 'submit') {
        check(!pending && event && validReviewBody(this.writing.draft.body));
        check(event !== 'request_changes' || !!this.writing.draft.body.trim());
        pending = {command: factoryCommandId(), event, body: this.writing.draft.body,
          external: context.parent.external_id, head: context.head, draft: this.writing.draft.revision, cancel: false, expectedHead: true};
      } else check(pending);
      check(pending.external === context.parent.external_id);
      if (action === 'submit' || action === 'retry') check(pending.head === context.head);
      if (action === 'cancel') pending = {...pending, cancel: true};
      if (action === 'submit' || action === 'cancel') {
        if (this.write(context, {...this.writing, pending}) !== 'saved') return;
      }
      await this.deliver(context, signal, action === 'retry' ? 'submit' : action);
    } catch {
      if (this.journal.live(context, signal)) this.message = this.writing.pending
        ? 'No final outcome was confirmed. The original review remains saved; check status before retrying.'
        : 'Review access or saved writing changed. Nothing was submitted.';
    } finally { if (this.journal.live(context, signal)) { this.busy = false; this.touch(); } }
  }
  private async deliver(context: PullReviewContext, signal: AbortSignal, action: 'submit' | 'lookup' | 'cancel') {
    const pending = this.writing.pending;
    check(pending);
    const base = this.path(context) + '/reviews';
    const outcome = await this.journal.deliver(context, signal, action, pending, {
      endpoints: {submit: base, commands: base + '/commands'},
      body: {command_id: pending.command, event: pending.event, body: pending.body, ...(pending.expectedHead ? {expected_head: pending.head} : {})},
      parse: (body, command) => {
        const {review, ...fields} = object(body);
        if (review !== undefined && review !== null) {
          const ref = object(review);
          check(pullRef(ref.pull, context.repository, context.pull).external_id === context.parent.external_id);
          check(typeof ref.external_id === 'string' && !!ref.external_id.trim());
        }
        return commandOutcome(fields, command);
      },
    });
    if (outcome.kind !== 'finished') return;
    const latest = readReviewWriting(context);
    check(!latest.blocked && latest.writing?.pending && sameReviewPending(latest.writing.pending, pending));
    const next: ReviewWriting = {draft: latest.writing.draft};
    if (outcome.effect === 'committed' && next.draft.revision === pending.draft && next.draft.body === pending.body)
      next.draft = {revision: factoryCommandId(), body: pending.event === 'draft' ? pending.body : ''};
    const stored = saveReviewWriting(context, latest.raw, next);
    check(stored !== 'conflict');
    this.saved = readReviewWriting(context); this.writing = next;
    this.message = this.journal.finished(pending, outcome, {
      committed: pending.event === 'draft' ? 'Draft review saved. Edit it here or choose a submission action.' : 'Review submitted.',
      cancelled: 'Review cancelled; writing preserved.', none: 'No review was submitted; writing preserved.',
    });
    if (stored !== 'saved') this.message += ' Local saved state still needs storage.';
    if (outcome.effect === 'committed') this.dispatchEvent(new CustomEvent('pull-review-saved', {detail: {repository: context.repository, pull: context.pull, command: pending.command}}));
  }
  private reload() {
    const context = this.journal.context;
    if (!context || this.busy) return;
    this.restore(context, true); this.touch(); void this.refresh();
  }
  async loadDraft(externalID: string) {
    this.opened = true;
    const context = this.journal.context, signal = this.journal.signal;
    if (!context || this.busy || this.writing.pending || this.saved.blocked || !this.journal.live(context, signal)) return;
    this.busy = true; this.message = ''; this.touch();
    try {
      await this.admit(context, signal);
      const raw = await getJSON(context.transport, this.path(context) + '/reviews/' + encodeURIComponent(externalID), signal);
      check(this.journal.live(context, signal));
      const review = pullReview(raw, context.repository, context.pull);
      check(review.externalID === externalID && review.pull.external_id === context.parent.external_id &&
        review.authorID === context.actor && review.state === 'pending' && validReviewBody(review.body));
      if (this.writing.draft.body && this.writing.draft.body !== review.body) {
        this.message = 'Your local review writing was kept. Copy or clear it before loading the saved draft.';
        return;
      }
      if (this.write(context, {draft: {revision: factoryCommandId(), body: review.body}}) === 'saved')
        this.message = 'Saved provider draft loaded. Edit it or choose a submission action.';
    } catch {
      if (this.journal.live(context, signal)) this.message = 'The saved draft could not be loaded. Your local writing was preserved.';
    } finally { if (this.journal.live(context, signal)) { this.busy = false; this.touch(); } }
  }
  render() {
    const context = this.journal.context;
    if (!context) return html``;
    if (!this.opened) return html`<button type="button" @click=${() => { this.opened = true; this.touch(); void this.refresh(); }}>Write review</button>`;
    const pending = this.writing.pending;
    return html`<section class="soda-pull-review" aria-label="Write pull request review" aria-busy=${this.busy}>
      <h3>Review</h3><p role="status">${this.message}</p>
      <button type="button" @click=${this.refresh} ?disabled=${this.busy}>Refresh review actions</button>
      ${this.saved.blocked ? html`<button type="button" @click=${this.reload}>Reload saved review writing</button>` : ''}
      <label>Review Markdown <textarea aria-label="Review Markdown" .value=${this.writing.draft.body} @input=${this.input} ?disabled=${this.busy || !!pending || this.saved.blocked}></textarea></label>
      ${!this.allowed.draft && this.allowed.comment ? html`<p>Your summary is saved on this device. Submit it with your pending comments when ready.</p>` : ''}
      ${pending ? html`<p>Saved ${pending.event.replace('_', ' ')} review command. Check its outcome before starting another.</p>
        <button type="button" @click=${() => this.act('lookup')} ?disabled=${this.busy || this.saved.blocked}>Check saved review</button>
        <button type="button" @click=${() => this.act('retry')} ?disabled=${this.busy || this.saved.blocked || pending.cancel || !this.allowed[pending.event]}>Retry original review</button>
        <button type="button" @click=${() => this.act('cancel')} ?disabled=${this.busy || this.saved.blocked}>Cancel saved review</button>`
        : html`<div class="soda-pull-review-actions">
          <button type="button" @click=${() => this.act('submit', 'draft')} ?disabled=${this.busy || this.saved.blocked || !this.saved.durable || !this.allowed.draft}>Save draft review</button>
          <button type="button" @click=${() => this.act('submit', 'comment')} ?disabled=${this.busy || this.saved.blocked || !this.saved.durable || !this.allowed.comment}>Submit comment</button>
          <button type="button" @click=${() => this.act('submit', 'approve')} ?disabled=${this.busy || this.saved.blocked || !this.saved.durable || !this.allowed.approve}>Approve</button>
          <button type="button" @click=${() => this.act('submit', 'request_changes')} ?disabled=${this.busy || this.saved.blocked || !this.saved.durable || !this.allowed.request_changes || !this.writing.draft.body.trim()}>Request changes</button>
        </div>`}
    </section>`;
  }
}
customElements.define('soda-factory-pull-review', SodaFactoryPullReview);
