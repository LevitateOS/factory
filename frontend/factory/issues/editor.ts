import {LitElement, html} from 'lit';
import {check, id, object} from '../api/models.js';
import type {FactoryTransport} from '../api/transport.js';
import type {IssueRef, PullRef} from '../api/collaboration.js';
import {pullActionAllowed} from '../api/capability-response.js';
import {issueDetail} from '../api/issue-response.js';
import {pullItem} from '../pulls/response.js';
import {issueEditAllowed, editBodyDigest, sameEditOriginal, scalarEditText} from '../api/issue-edit-response.js';
import type {EditOriginal} from '../api/issue-edit-response.js';
import {factoryCommandId} from '../api/run-response.js';
import {getJSON} from '../api/read.js';
import {JournalCommand} from '../shared/journal-command.js';
import {
  editorRecordKey,
  readEditorWriting,
  reloadEditorWriting,
  saveEditorWriting,
  editFields,
  validEditDraft,
  sameEditorDraft,
  samePendingEdit,
} from './editor-record.js';
import type {EditorAddress, EditorSaved, EditorWriting, EditorDraft, PendingEdit} from './editor-record.js';

export interface IssueEditorContext extends EditorAddress {
  readonly transport: FactoryTransport;
  readonly parent: IssueRef | PullRef;
  commandScope(signal: AbortSignal): Promise<string>;
}
export type IssueEditorPorts = Omit<IssueEditorContext, 'parent'>;

/** Ordinary human edits over the shared journal. A displayed draft never
 * silently adopts a newer remote base or replaces an uncertain command. */
export class SodaFactoryIssueEditor extends LitElement {
  static properties = {version: {state: true}};
  declare private version: number;
  private journal = new JournalCommand<PendingEdit, IssueEditorContext>({
    key: (context) =>
      JSON.stringify([
        context.scope,
        context.host,
        context.actor,
        context.repository,
        context.issue,
        context.kind,
        context.parent.kind,
        context.parent.repository,
        context.parent.number,
        context.parent.external_id,
        context.transport.generation,
      ]),
    words: {
      attention: 'The saved edit needs provider attention. Check status before taking another action.',
      completing: 'The edit was recorded; provider completion is still pending. Check status.',
      unconfirmed: 'The edit is not confirmed. Its original writing remains saved.',
    },
    say: (message) => {
      this.message = message;
    },
    retire: () => {
      this.context = undefined;
    },
    byObject: true,
  });
  private saved: EditorSaved = {raw: '', writing: undefined, blocked: false, durable: true};
  private writing: EditorWriting = {};
  private latest: EditOriginal | undefined;
  private allowed = false;
  private busy = false;
  private opened = false;
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
  set context(context: IssueEditorContext | undefined) {
    if (!this.journal.rebind(context)) return;
    this.writing = {};
    this.latest = undefined;
    this.allowed = false;
    this.busy = false;
    this.opened = false;
    this.message = '';
    this.noEffect = '';
    if (context) {
      editorRecordKey(context);
      check(
        context.parent.kind === (context.kind === 'pull' ? 'pull' : 'issue') &&
          context.parent.repository === context.repository &&
          context.parent.number === context.issue &&
          id(context.issue)
      );
      check(
        typeof context.parent.external_id === 'string' &&
          !!context.parent.external_id.trim()
      );
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
  private subjectLabel() { return this.journal.context?.kind === 'pull' ? 'pull request' : 'issue'; }
  private restore(context: IssueEditorContext, reload = false) {
    this.saved = reload ? reloadEditorWriting(context) : readEditorWriting(context);
    this.writing = this.saved.writing ?? {};
    this.opened = !!this.writing.draft || !!this.writing.pending || this.saved.blocked;
    if (this.saved.blocked)
      this.message =
        `Saved ${this.subjectLabel()} edits are unreadable or changed in another window. They were preserved. Reload saved writing to check again.`;
    else if (!this.saved.durable)
      this.message = `${this.subjectLabel()} edits are only saved in this window. Saving waits for durable storage.`;
  }
  private path(context: IssueEditorContext) {
    return `factory/collaboration/repositories/${context.repository}/${context.kind === 'pull' ? 'pulls' : 'issues'}/${context.issue}`;
  }
  private async readCurrent(context: IssueEditorContext, signal: AbortSignal) {
    this.allowed = false;
    await this.journal.identity(context, signal);
    const [value, capabilities] = await Promise.all([
      getJSON(context.transport, this.path(context), signal),
      getJSON(context.transport, this.path(context) + '/capabilities', signal),
    ]);
    check(this.journal.live(context, signal));
    const issue = context.kind === 'pull'
      ? pullItem(value, context.host, context.repository, context.issue)
      : issueDetail(value, context.host, context.repository, context.issue);
    check(
      issue.ref.external_id === context.parent.external_id &&
        scalarEditText(issue.title) &&
        new TextEncoder().encode(issue.title).length <= 65536
    );
    const digest = await editBodyDigest(issue.body);
    check(this.journal.live(context, signal));
    this.latest = {external: issue.ref.external_id, title: issue.title, body: issue.body, digest};
    if (context.kind === 'pull') {
      check(context.parent.kind === 'pull');
      check('head' in issue);
      const head = object(capabilities).head;
      check(head === null || typeof head === 'string' && /^[a-f0-9]{40}(?:[a-f0-9]{24})?$/i.test(head));
      this.allowed = (issue.head.commit === null || issue.head.commit === head) &&
        pullActionAllowed(capabilities, context.repository, context.actor, context.parent, head as string | null, 'pull.edit');
    } else {
      check(context.parent.kind === 'issue');
      this.allowed = issueEditAllowed(capabilities, context.repository, context.actor, context.parent);
    }
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
        try {
          await this.readCurrent(context, signal);
        } catch {
          this.allowed = false;
        }
        check(this.journal.live(context, signal));
        await this.deliver(context, signal, 'lookup');
      } else {
        await this.readCurrent(context, signal);
        if (!this.saved.blocked)
          this.message = !this.saved.durable
            ? `${this.subjectLabel()} edits are only saved in this window.`
            : !this.allowed
              ? 'Editing is not available for this account.'
              : this.stale()
                ? `The ${this.subjectLabel()} changed. Review the current version before choosing a new base.`
                : '';
      }
    } catch {
      if (this.journal.live(context, signal))
        this.message = this.writing.pending
          ? 'No final outcome was confirmed. The original edit remains saved. Check status before retrying.'
          : `${this.subjectLabel()} edit access could not be checked. Your writing was preserved.`;
    } finally {
      if (this.journal.live(context, signal)) {
        this.busy = false;
        this.touch();
      }
    }
  }
  private stale() {
    return !!this.writing.draft && !!this.latest && !sameEditOriginal(this.writing.draft.original, this.latest);
  }
  private write(context: IssueEditorContext, next: EditorWriting) {
    const result = saveEditorWriting(context, this.saved.raw, next);
    if (result !== 'conflict') {
      this.saved = readEditorWriting(context);
      this.writing = next;
    } else {
      this.saved = {...this.saved, blocked: true};
      this.message = 'Saved writing changed. Your text is still shown; reload saved writing before saving.';
    }
    if (result === 'unavailable') this.message = `${this.subjectLabel()} edits are only saved in this window. No edit was submitted.`;
    return result;
  }
  private async begin() {
    const context = this.journal.context,
      signal = this.journal.signal;
    if (!context || this.busy || !this.journal.live(context, signal)) return;
    this.busy = true;
    this.opened = true;
    this.touch();
    try {
      await this.readCurrent(context, signal);
      check(this.allowed && this.latest);
      if (!this.writing.draft && !this.writing.pending && !this.saved.blocked) {
        this.write(context, {
          draft: {
            revision: factoryCommandId(),
            title: this.latest.title,
            body: this.latest.body,
            original: {...this.latest},
          },
        });
      }
    } catch {
      if (this.journal.live(context, signal))
        this.message = `The current ${this.subjectLabel()} could not be opened for editing. Saved writing was preserved.`;
    } finally {
      if (this.journal.live(context, signal)) {
        this.busy = false;
        this.touch();
      }
    }
  }
  private input(event: Event, field: 'title' | 'body') {
    const context = this.journal.context,
      draft = this.writing.draft;
    if (!context || !draft || this.writing.pending || this.saved.blocked || !this.journal.live(context, this.journal.signal))
      return;
    const next: EditorWriting = {
      draft: {...draft, revision: factoryCommandId(), [field]: (event.target as HTMLInputElement).value},
    };
    const result = this.write(context, next);
    if (result === 'conflict') this.writing = next;
    else if (result === 'saved') this.message = '';
    this.touch();
  }
  private useCurrent() {
    const context = this.journal.context,
      latest = this.latest,
      draft = this.writing.draft;
    if (
      !context ||
      !latest ||
      !draft ||
      this.busy ||
      this.writing.pending ||
      this.saved.blocked ||
      !this.journal.live(context, this.journal.signal)
    )
      return;
    if (draft.original.external !== context.parent.external_id) return;
    const next: EditorDraft = {
      revision: factoryCommandId(),
      title: draft.title === draft.original.title ? latest.title : draft.title,
      body: draft.body === draft.original.body ? latest.body : draft.body,
      original: {...latest},
    };
    if (this.write(context, {draft: next}) === 'saved')
      this.message =
        `Your changed fields were kept. Unchanged fields now use the current ${this.subjectLabel()}. Nothing was submitted.`;
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
      this.noEffect === this.writing.pending?.command
    )
      return;
    this.busy = true;
    this.message = '';
    this.touch();
    try {
      const shown = this.writing.pending;
      if (action === 'submit') {
        await this.readCurrent(context, signal);
        check(this.allowed && (!shown || !shown.cancel));
        if (!shown && this.stale()) {
          this.message =
            `The ${this.subjectLabel()} changed. Your draft was preserved. Review the current version before choosing a new base.`;
          return;
        }
        check(shown ? validEditDraft(shown.source) : this.writing.draft && validEditDraft(this.writing.draft));
      } else await this.journal.identity(context, signal);
      check(this.journal.live(context, signal));
      const current = readEditorWriting(context),
        same = shown
          ? !!current.writing?.pending && samePendingEdit(shown, current.writing.pending)
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
        check(action === 'submit' && this.writing.draft);
        pending = {command: factoryCommandId(), source: this.writing.draft, cancel: false};
      }
      check(pending.source.original.external === context.parent.external_id);
      if (action === 'cancel') pending = {...pending, cancel: true};
      if (this.write(context, {...this.writing, pending}) !== 'saved') return;
      await this.deliver(context, signal, action);
    } catch {
      if (this.journal.live(context, signal))
        this.message = this.writing.pending
          ? 'No final outcome was confirmed. The original edit remains saved. Check status before retrying.'
          : `${this.subjectLabel()} access or saved writing changed. Nothing was submitted; check access and reload saved writing.`;
    } finally {
      if (this.journal.live(context, signal)) {
        this.busy = false;
        this.touch();
      }
    }
  }
  private async deliver(context: IssueEditorContext, signal: AbortSignal, action: 'submit' | 'lookup' | 'cancel') {
    const pending = this.writing.pending;
    check(pending && pending.source.original.external === context.parent.external_id);
    const original = pending.source.original;
    check((await editBodyDigest(original.body)) === original.digest && this.journal.live(context, signal));
    const base = this.path(context) + '/edits';
    const outcome = await this.journal.deliver(context, signal, action, pending, {
      endpoints: {submit: base, commands: base + '/commands'},
      body: {
        command_id: pending.command,
        ...editFields(pending.source),
        expected: {external_id: original.external, title: original.title, body_digest: original.digest},
      },
    });
    if (outcome.kind !== 'finished') return;
    const latest = readEditorWriting(context);
    check(!latest.blocked && latest.writing?.pending && samePendingEdit(latest.writing.pending, pending));
    const draft = latest.writing.draft;
    if (
      outcome.effect === 'not_committed' &&
      draft &&
      (draft.title !== pending.source.title ||
        draft.body !== pending.source.body ||
        !sameEditOriginal(draft.original, pending.source.original))
    ) {
      this.saved = latest;
      this.writing = latest.writing;
      this.noEffect = pending.command;
      this.message = 'No changes were saved. Your original edit and a newer draft are both preserved.';
      return;
    }
    const next: EditorWriting =
      outcome.effect === 'not_committed'
        ? {draft: draft ?? pending.source}
        : draft && !sameEditorDraft(draft, pending.source)
          ? {draft}
          : {};
    const stored = saveEditorWriting(context, latest.raw, next);
    check(stored !== 'conflict');
    this.saved = readEditorWriting(context);
    this.writing = next;
    this.opened = !!next.draft;
    this.message = this.journal.finished(pending, outcome, {
      committed: `${this.subjectLabel()} changes saved.`,
      cancelled: 'Edit submission cancelled. Your draft was preserved.',
      none: `No changes were saved. Your draft was preserved. Check the current ${this.subjectLabel()} before retrying.`,
    });
    if (stored !== 'saved') this.message += ' The local saved state still needs storage.';
    if (outcome.effect === 'committed') {
      const detail = {host: context.host, actor: context.actor, scope: context.scope,
        parent: {...context.parent}, command: pending.command};
      if (context.kind === 'pull') this.dispatchEvent(new CustomEvent('pull-edit-saved', {detail}));
      else this.dispatchEvent(new CustomEvent('issue-edit-saved', {detail}));
    }
  }
  private continueWriting() {
    const context = this.journal.context,
      pending = this.writing.pending;
    if (
      !context ||
      !pending ||
      this.noEffect !== pending.command ||
      this.busy ||
      !this.journal.live(context, this.journal.signal)
    )
      return;
    const latest = readEditorWriting(context);
    if (latest.blocked || !latest.writing?.pending || !samePendingEdit(pending, latest.writing.pending)) {
      this.saved = {...this.saved, blocked: true};
      this.message = 'Saved writing changed. Reload it before choosing a draft.';
    } else {
      this.saved = latest;
      if (this.write(context, latest.writing.draft ? {draft: latest.writing.draft} : {}) !== 'conflict') {
        this.noEffect = '';
        this.message = 'The newer draft is ready. Nothing was submitted.';
      }
    }
    this.touch();
  }
  render() {
    if (!this.journal.context) return html``;
    const subject = this.subjectLabel();
    if (!this.opened)
      return html`<section class="soda-issue-editor" aria-label=${`${subject} editor`} aria-busy=${this.busy}>
        <button type="button" ?disabled=${this.busy || !this.allowed} @click=${this.begin}>Edit ${subject}</button>
        <p role="status">${this.message}</p>
      </section>`;
    const pending = this.writing.pending,
      draft = pending?.source ?? this.writing.draft,
      blocked = this.saved.blocked;
    return html`<section class="soda-issue-editor" aria-label=${`${subject} editor`} aria-busy=${this.busy}>
      <h3>Edit ${subject}</h3>
      ${
        draft
          ? html`<label
                >Title<input
                  aria-label=${`Edit ${subject} title`}
                  .value=${draft.title}
                  ?readonly=${!!pending || blocked}
                  ?disabled=${this.busy}
                  @input=${(event: Event) => this.input(event, 'title')}
              /></label>
              <p>${new TextEncoder().encode(draft.title).length} / 255 title bytes</p>
              <label
                >Description Markdown<textarea
                  aria-label=${`Edit ${subject} Markdown`}
                  rows="8"
                  .value=${draft.body}
                  ?readonly=${!!pending || blocked}
                  ?disabled=${this.busy}
                  @input=${(event: Event) => this.input(event, 'body')}
                ></textarea>
              </label>
              <p>${new TextEncoder().encode(draft.body).length} / 65536 description bytes</p>`
          : ''
      }
      <p role="status">${this.message}</p>
      ${
        this.noEffect === pending?.command
          ? html`<button type="button" ?disabled=${this.busy || blocked} @click=${this.continueWriting}>
              Discard original edit and continue with newer draft
            </button>`
          : pending
            ? html`
                <button type="button" ?disabled=${this.busy || blocked} @click=${() => this.act('lookup')}>
                  Check edit status
                </button>
                <button
                  type="button"
                  ?disabled=${this.busy || blocked || (!pending.cancel && !this.allowed)}
                  @click=${() => this.act(pending.cancel ? 'cancel' : 'submit')}
                >
                  ${pending.cancel ? 'Retry cancellation' : 'Retry saved edit'}
                </button>
                ${!pending.cancel ? html`<button type="button" ?disabled=${this.busy || blocked} @click=${() => this.act('cancel')}>Cancel edit submission</button>` : ''}
              `
            : html`<button
                type="button"
                ?disabled=${this.busy || blocked || !this.allowed || !draft || !validEditDraft(draft) || this.stale()}
                @click=${() => this.act('submit')}
              >
                Save ${subject}
              </button>`
      }
      ${
        pending && this.writing.draft && !sameEditorDraft(this.writing.draft, pending.source)
          ? html`<details>
              <summary>Newer saved edit draft</summary>
              <input aria-label="Newer edit title" readonly .value=${this.writing.draft.title} /><textarea
                aria-label="Newer edit Markdown"
                readonly
                rows="4"
                .value=${this.writing.draft.body}
              ></textarea>
            </details>`
          : ''
      }
      ${
        !pending && draft && this.latest && this.stale()
          ? html`<details open>
              <summary>Review current ${subject}</summary>
              <p>Keep your changed fields and use current values for fields you left unchanged.</p>
              <input aria-label=${`Current ${subject} title`} readonly .value=${this.latest.title} /><textarea
                aria-label=${`Current ${subject} Markdown`}
                readonly
                rows="4"
                .value=${this.latest.body}
              ></textarea
              ><button
                type="button"
                ?disabled=${this.busy || blocked || !this.allowed || draft.original.external !== this.journal.context.parent.external_id}
                @click=${this.useCurrent}
              >
                Use current ${subject} as base
              </button>
            </details>`
          : ''
      }
      <button type="button" ?disabled=${this.busy} @click=${this.refresh}>Check ${subject} edit access</button>
      ${
        !pending
          ? html`<button
              type="button"
              ?disabled=${this.busy}
              @click=${() => {
                this.opened = false;
                this.touch();
              }}
            >
              Close editor
            </button>`
          : ''
      }
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
customElements.define('soda-factory-issue-editor', SodaFactoryIssueEditor);
export class SodaFactoryPullEditor extends SodaFactoryIssueEditor {}
customElements.define('soda-factory-pull-editor', SodaFactoryPullEditor);
