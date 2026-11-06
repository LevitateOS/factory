import {LitElement, html} from 'lit';
import {check, readSodaJSON} from '../api/models.js';
import type {ChangedFile, PullRef, ReviewAnchor} from '../api/collaboration.js';
import type {FactoryTransport} from '../api/transport.js';
import {pullActionAllowed} from '../api/capability-response.js';
import {commandOutcome} from '../api/command-response.js';
import {getJSON} from '../api/read.js';
import {factoryCommandId} from '../api/run-response.js';
import type {DiffThread, ReviewDraft} from '../review/diff.js';
import '../review/diff.js';
import {humanAnchor} from './anchor.js';
import {pullItem, type PullThreadView} from './response.js';
import {
  inlineRecordKey, newInlineDraft, readInlineWriting, reloadInlineWriting, sameInlinePending,
  saveInlineWriting, validInlineBody, validInlineDraftBody, type InlineAddress, type InlineSaved, type InlineWriting,
} from './inline-record.js';

export interface PullInlineContext extends InlineAddress {
  readonly transport: FactoryTransport;
  readonly head: string;
  commandScope(signal: AbortSignal): Promise<string>;
}
const sameFile = (anchor: ReviewAnchor, file: ChangedFile) =>
  anchor.diff.base === file.diff.base && anchor.diff.head === file.diff.head &&
  anchor.diff.pull.external_id === file.diff.pull.external_id &&
  anchor.old_path === file.old_path && anchor.new_path === file.new_path;

/** File diffs and one durable inline comment draft share the exact selected anchor. */
export class SodaFactoryPullInline extends LitElement {
  static properties = {version: {state: true}, files: {attribute: false}, threads: {attribute: false}};
  declare private version: number;
  declare files: readonly ChangedFile[];
  declare threads: readonly PullThreadView[];
  private current: PullInlineContext | undefined;
  private binding = '';
  private generation = '';
  private lifetime = new AbortController();
  private saved: InlineSaved = {raw: '', writing: undefined, durable: true, blocked: false};
  private writing: InlineWriting = {};
  private allowed = false;
  private busy = false;
  private message = '';

  constructor() { super(); this.version = 0; this.files = []; this.threads = []; }
  protected createRenderRoot() { return this; }
  get context() { return this.current; }
  private key(context: PullInlineContext) {
    return JSON.stringify([context.scope, context.host, context.actor, context.repository, context.pull,
      context.parent.external_id, context.head, context.transport.generation]);
  }
  set context(context: PullInlineContext | undefined) {
    const key = context && this.key(context);
    if (context && key === this.binding && this.current) return;
    this.lifetime.abort(); this.lifetime = new AbortController();
    this.current = undefined; this.binding = ''; this.allowed = false; this.busy = false; this.message = ''; this.writing = {};
    if (context) {
      inlineRecordKey(context);
      check(!!context.parent.external_id.trim() && /^[a-f0-9]{40}(?:[a-f0-9]{24})?$/i.test(context.head));
      check(typeof context.commandScope === 'function' && !!context.transport.generation);
      this.current = context; this.binding = key!; this.generation = context.transport.generation;
      this.restore(context); void this.refresh();
    }
    this.touch();
  }
  disconnectedCallback() { this.context = undefined; super.disconnectedCallback(); }
  private touch() { this.version++; }
  private live(context: PullInlineContext, signal: AbortSignal) {
    if (this.current !== context || this.lifetime.signal !== signal || signal.aborted || context.transport.generation !== this.generation) return false;
    if (this.key(context) !== this.binding) { this.context = undefined; return false; }
    return true;
  }
  private restore(context: PullInlineContext, reload = false) {
    this.saved = reload ? reloadInlineWriting(context) : readInlineWriting(context);
    this.writing = this.saved.writing ?? {};
    if (this.saved.blocked) this.message = 'Saved inline writing changed or could not be read. It was preserved.';
    else if (!this.saved.durable) this.message = 'Inline writing is only saved in this window. Submission waits for durable storage.';
  }
  private path(context: PullInlineContext) {
    return `factory/collaboration/repositories/${context.repository}/pulls/${context.pull}`;
  }
  private async identity(context: PullInlineContext, signal: AbortSignal) {
    const scope = await context.commandScope(AbortSignal.any([signal, AbortSignal.timeout(15000)]));
    check(this.live(context, signal) && scope === context.scope);
  }
  private async admit(context: PullInlineContext, signal: AbortSignal) {
    this.allowed = false;
    await this.identity(context, signal);
    const [raw, caps] = await Promise.all([
      getJSON(context.transport, this.path(context), signal),
      getJSON(context.transport, this.path(context) + '/capabilities', signal),
    ]);
    check(this.live(context, signal));
    const pull = pullItem(raw, context.host, context.repository, context.pull);
    check(pull.ref.external_id === context.parent.external_id);
    this.allowed = pull.state === 'open' && pull.head.commit === context.head &&
      pullActionAllowed(caps, context.repository, context.actor, context.parent, context.head, 'pull.review.comment');
  }
  private async refresh() {
    const context = this.current, signal = this.lifetime.signal;
    if (!context || this.busy || !this.live(context, signal)) return;
    this.busy = true; this.touch();
    try {
      if (this.writing.pending && !this.saved.blocked) { await this.identity(context, signal); await this.deliver(context, signal, 'lookup'); }
      await this.admit(context, signal);
      if (!this.writing.pending && !this.saved.blocked && !this.message) this.message = this.allowed ? '' : 'Inline commenting is unavailable for this account.';
    } catch {
      if (this.live(context, signal)) this.message = this.writing.pending
        ? 'The original inline comment is saved. Check status after access is restored.'
        : 'Inline comment access could not be checked. Your writing was preserved.';
    } finally { if (this.live(context, signal)) { this.busy = false; this.touch(); } }
  }
  private write(context: PullInlineContext, next: InlineWriting) {
    const result = saveInlineWriting(context, this.saved.raw, next);
    if (result === 'conflict') { this.saved = {...this.saved, blocked: true}; this.message = 'Saved inline writing changed. Reload before submitting.'; }
    else { this.saved = readInlineWriting(context); this.writing = next;
      if (result === 'unavailable') this.message = 'Inline writing is only saved in this window. No submission was sent.'; }
    this.touch();
    return result;
  }
  private changeDraft(event: CustomEvent<ReviewDraft | null>) {
    const context = this.current;
    if (!context || !this.live(context, this.lifetime.signal) || this.busy || !this.allowed || this.writing.pending || this.saved.blocked) return;
    event.stopPropagation();
    try {
      const selected = event.detail;
      const existing = this.writing.draft;
      if (selected && existing && existing.body !== '' &&
          JSON.stringify(selected.anchor) !== JSON.stringify(existing.anchor)) {
        this.message = 'Finish or discard the existing inline draft before starting another.';
        this.touch();
        return;
      }
      check(!selected || validInlineDraftBody(selected.body));
      const draft = selected ? newInlineDraft(selected.anchor, selected.body) : undefined;
      if (draft) humanAnchor(draft.anchor, context.parent);
      const next: InlineWriting = draft ? {draft} : {};
      if (this.write(context, next) === 'saved') this.message = '';
    } catch {
      this.message = 'This selection or draft could not be saved. Choose a current file and try again.';
      this.touch();
    }
  }
  private async act(action: 'submit' | 'retry' | 'lookup' | 'cancel', draft = false) {
    const context = this.current, signal = this.lifetime.signal;
    if (!context || !this.live(context, signal) || this.busy || this.saved.blocked) return;
    this.busy = true; this.message = ''; this.touch();
    try {
      if (action === 'submit' || action === 'retry') { await this.admit(context, signal); check(this.allowed); }
      else await this.identity(context, signal);
      const latest = readInlineWriting(context), shown = this.writing.pending;
      check(!latest.blocked && latest.raw === this.saved.raw);
      if (shown) check(latest.writing?.pending && sameInlinePending(shown, latest.writing.pending));
      let pending = shown;
      if (action === 'submit') {
        const source = this.writing.draft;
        check(!pending && source && validInlineBody(source.body) && source.anchor.diff.head === context.head);
        humanAnchor(source.anchor, context.parent);
        pending = {command: factoryCommandId(), source, draft, cancel: false};
      } else check(pending);
      if (action === 'retry') check(!pending.cancel && pending.source.anchor.diff.head === context.head);
      if (action === 'cancel') pending = {...pending, cancel: true};
      if (action === 'submit' || action === 'cancel') {
        if (this.write(context, {...this.writing, pending}) !== 'saved') return;
      }
      await this.deliver(context, signal, action === 'retry' ? 'submit' : action);
    } catch {
      if (this.live(context, signal)) this.message = this.writing.pending
        ? 'No final inline comment outcome was confirmed. Check the saved command before retrying.'
        : 'Inline comment access or saved writing changed. Nothing was submitted.';
    } finally { if (this.live(context, signal)) { this.busy = false; this.touch(); } }
  }
  private async deliver(context: PullInlineContext, signal: AbortSignal, action: 'submit' | 'lookup' | 'cancel') {
    const pending = this.writing.pending;
    check(pending && this.live(context, signal));
    const base = this.path(context) + '/inline-comments', command = base + '/commands/' + pending.command;
    const response = await context.transport.request(action === 'submit' ? base : command + (action === 'cancel' ? '/cancel' : ''), {
      method: action === 'lookup' ? 'GET' : 'POST', headers: {'Content-Type': 'application/json'},
      signal: AbortSignal.any([signal, AbortSignal.timeout(35000)]),
      ...(action === 'lookup' ? {} : {body: JSON.stringify(action === 'submit'
        ? {command_id: pending.command, anchor: humanAnchor(pending.source.anchor, context.parent), draft: pending.draft, body: pending.source.body} : {})}),
    });
    check(this.live(context, signal) && response.ok);
    const outcome = commandOutcome(await readSodaJSON(response), pending.command);
    check(this.live(context, signal));
    if (!outcome.finished) {
      this.message = outcome.completion === 'needs_intervention' ? 'The saved inline comment needs attention. Check status.'
        : outcome.effect === 'committed' ? 'Inline comment recorded; completion is pending.'
          : 'The inline comment is not confirmed. Its original command remains saved.';
      return;
    }
    const latest = readInlineWriting(context);
    check(!latest.blocked && latest.writing?.pending && sameInlinePending(latest.writing.pending, pending));
    const next: InlineWriting = {};
    if (latest.writing.draft && (outcome.effect !== 'committed' || latest.writing.draft.revision !== pending.source.revision)) next.draft = latest.writing.draft;
    const stored = saveInlineWriting(context, latest.raw, next);
    check(stored !== 'conflict');
    this.saved = readInlineWriting(context); this.writing = next;
    this.message = outcome.effect === 'committed' ? pending.draft ? 'Draft inline comment saved.' : 'Inline comment posted.'
      : pending.cancel ? 'Inline comment cancelled; writing preserved.' : 'No inline comment was posted; writing preserved.';
    if (stored !== 'saved') this.message += ' Local saved state still needs storage.';
    if (outcome.effect === 'committed') this.dispatchEvent(new CustomEvent('pull-inline-saved', {detail: {repository: context.repository, pull: context.pull, command: pending.command}}));
  }
  private reload() {
    const context = this.current;
    if (!context || this.busy) return;
    this.restore(context, true); this.touch(); void this.refresh();
  }
  private discardDraft() {
    const context = this.current;
    if (!context || !this.live(context, this.lifetime.signal) || this.busy || this.writing.pending || this.saved.blocked) return;
    if (this.write(context, {}) === 'saved') this.message = 'Unsent inline draft discarded.';
  }
  render() {
    const context = this.current;
    if (!context) return html``;
    const draft = this.writing.draft, pending = this.writing.pending;
    const available = this.allowed && !this.busy && !pending && !this.saved.blocked && this.saved.durable;
    return html`<section class="soda-pull-inline" aria-label="Changed files and inline comments" aria-busy=${this.busy}>
      <p role="status">${this.message}</p>
      ${this.saved.blocked ? html`<button type="button" @click=${this.reload}>Reload saved inline writing</button>` : ''}
      ${this.files.map(file => {
        const threads: DiffThread[] = this.threads.filter(thread => {
          const anchor = thread.currentAnchor ?? thread.anchor;
          return anchor ? anchor.old_path === file.old_path && anchor.new_path === file.new_path
            : thread.recordedLocation?.path === file.old_path || thread.recordedLocation?.path === file.new_path;
        }).map(thread => ({external_id: thread.externalID, outdated: thread.outdated, resolution: thread.resolution,
          anchor: thread.currentAnchor ?? thread.anchor, recorded_location: thread.recordedLocation,
          comments: {items: thread.comments.map(comment => ({comment: {body: comment.body}})), next: thread.commentsNext}}));
        return html`<soda-factory-diff .file=${file} .threads=${threads}
          .draft=${draft && sameFile(draft.anchor, file) ? {anchor: draft.anchor, body: draft.body} : null}
          .allowDraft=${available && (!draft || sameFile(draft.anchor, file))} @factory-review-draft-change=${this.changeDraft}></soda-factory-diff>`;
      })}
      ${draft && !this.files.some(file => sameFile(draft.anchor, file)) ? html`<section aria-label="Retained inline draft">
        <p>Draft retained at its original diff revision and path. Open that file to edit it.</p>
        <p>${draft.anchor.old_path || draft.anchor.new_path} · ${draft.anchor.diff.head}</p>
        <textarea aria-label="Retained inline comment" readonly .value=${draft.body}></textarea>
        ${!pending ? html`<button type="button" @click=${this.discardDraft}
          ?disabled=${this.busy || this.saved.blocked || !this.saved.durable}>Discard unsent inline draft</button>` : ''}
      </section>` : ''}
      ${draft && !pending ? html`<div class="soda-pull-inline-actions">
        <button type="button" @click=${() => this.act('submit', true)} ?disabled=${!available || !validInlineBody(draft.body) || draft.anchor.diff.head !== context.head}>Add to draft review</button>
        <button type="button" @click=${() => this.act('submit', false)} ?disabled=${!available || !validInlineBody(draft.body) || draft.anchor.diff.head !== context.head}>Post inline comment</button>
      </div>` : ''}
      ${pending ? html`<p>Saved inline comment command. Check its outcome before another comment.</p>
        <button type="button" @click=${() => this.act('lookup')} ?disabled=${this.busy || this.saved.blocked}>Check saved inline comment</button>
        <button type="button" @click=${() => this.act('retry')} ?disabled=${this.busy || this.saved.blocked || pending.cancel || !this.allowed}>Retry original inline comment</button>
        <button type="button" @click=${() => this.act('cancel')} ?disabled=${this.busy || this.saved.blocked}>Cancel saved inline comment</button>` : ''}
    </section>`;
  }
}
customElements.define('soda-factory-pull-inline', SodaFactoryPullInline);
