import {LitElement, html} from 'lit';
import type {TemplateResult} from 'lit';
import {readSodaJSON} from '../api/models.js';
import {decideIdea, decisionID, intentRevision, newDecisionID} from '../api/request-response.js';
import type {IntentRevision} from '../api/request-response.js';
import type {FactoryTransport} from '../api/transport.js';
import {readStored, writeStored} from '../shared/drafts.js';
import type {FeedAssumption, FeedCommunication} from './feed-response.js';

// An assumption challenge card (F13). The answer is the build request's own
// save: the owner's head with this idea decided, posted to factory/intents.
// The new revision's immutable ID is the server save identity, so the exact
// revision is retained before sending and a retry resends it; a lost reply
// replays instead of recording twice. A moved head refuses the save, and the
// feed then shows whatever the head records.

export interface FeedAssumptionContext {
  readonly transport: FactoryTransport;
  readonly signal: AbortSignal;
  /** The host's draft scope; answers are retained under it. */
  readonly scope: string | undefined;
  readonly viewer: string | undefined;
}

/** Fired after a save settles so the feed reads the recorded state. */
export const assumptionAnswered = 'soda-feed-assumption-answered';

const empty = () => ({busy: false, correction: '', message: ''});

export class SodaFeedAssumption extends LitElement {
  static properties = {state: {state: true}};
  declare private state: ReturnType<typeof empty>;
  private source: FeedAssumption | undefined;
  private current: FeedAssumptionContext | undefined;
  private serial = 0;
  constructor() {
    super();
    this.state = empty();
  }
  protected createRenderRoot() {
    return this;
  }
  get assumption(): FeedAssumption | undefined {
    return this.source;
  }
  set assumption(value: FeedAssumption | undefined) {
    const prior = this.source;
    if (
      value?.intent === prior?.intent &&
      value?.idea === prior?.idea &&
      value?.state === prior?.state &&
      value?.decision === prior?.decision &&
      value?.correction === prior?.correction
    )
      return;
    if (value?.intent !== prior?.intent || value?.idea !== prior?.idea) {
      this.serial++;
      this.state = empty();
    }
    this.source = value ? {...value} : undefined;
    this.requestUpdate();
  }
  get context(): FeedAssumptionContext | undefined {
    return this.current;
  }
  set context(value: FeedAssumptionContext | undefined) {
    const prior = this.current;
    if (
      value?.transport === prior?.transport &&
      value?.signal === prior?.signal &&
      value?.scope === prior?.scope &&
      value?.viewer === prior?.viewer
    )
      return;
    this.serial++;
    this.state = {...this.state, busy: false};
    this.current = value ? {...value} : undefined;
    this.requestUpdate();
  }
  disconnectedCallback() {
    this.serial++;
    super.disconnectedCallback();
  }

  private storageKey(): string | undefined {
    const a = this.source,
      scope = this.current?.scope;
    return a && scope ? `soda-feed-assumption:v1:${scope}:${a.intent}:${a.idea}` : undefined;
  }

  // retained reads the exact unconfirmed answer, if one is kept.
  private retained(key: string | undefined): IntentRevision | undefined {
    const raw = key ? readStored(key) : '';
    if (!raw) return undefined;
    try {
      const revision = intentRevision(JSON.parse(raw));
      const idea = revision.ideas.find((row) => row.id === this.source?.idea);
      if (
        decisionID(revision.id) &&
        revision.intent === this.source?.intent &&
        revision.predecessor !== undefined &&
        idea !== undefined &&
        idea.decision !== 'pending'
      )
        return revision;
    } catch {
      // An unreadable copy is dropped below; nothing was confirmed from it.
    }
    writeStored(key!, '');
    return undefined;
  }

  private async answer(decision: 'accept' | 'edit') {
    const a = this.source,
      context = this.current,
      key = this.storageKey();
    if (!a || !context || this.state.busy || a.state !== 'open' || context.viewer !== a.owner) return;
    if (!key) {
      this.state = {...this.state, message: 'Answers cannot be kept here. Open the request to answer.'};
      return;
    }
    let revision = this.retained(key);
    const correction = this.state.correction.trim().slice(0, 4000);
    if (!revision && decision === 'edit' && !correction) return;
    const serial = ++this.serial;
    const live = () => serial === this.serial && !context.signal.aborted;
    this.state = {...this.state, busy: true, message: ''};
    const signal = AbortSignal.any([context.signal, AbortSignal.timeout(30000)]);
    try {
      if (!revision) {
        const read = await context.transport.request('factory/intents/' + a.intent, {method: 'GET', signal});
        if (!read.ok) {
          void read.body?.cancel().catch(() => {});
          throw Error('The request could not be read. Nothing was sent.');
        }
        const head = intentRevision(await readSodaJSON(read, 1024 * 1024));
        if (!live()) return;
        const idea = head.ideas.find((row) => row.id === a.idea);
        if (head.state !== 'draft' || idea?.decision !== 'pending') {
          this.settle('The request already records a different state. Showing it now.');
          return;
        }
        revision = {
          ...head,
          id: newDecisionID(),
          predecessor: head.id,
          ideas: head.ideas.map((row) =>
            row.id === a.idea ? decideIdea(row, decision, a.owner, decision === 'edit' ? correction : undefined) : row
          ),
        };
        writeStored(key, JSON.stringify(revision));
        if (readStored(key) !== JSON.stringify(revision)) throw Error('The answer could not be kept. Nothing was sent.');
      }
      const saved = await context.transport.request('factory/intents', {
        method: 'POST',
        headers: {'Content-Type': 'application/json'},
        body: JSON.stringify(revision),
        signal,
      });
      void saved.body?.cancel().catch(() => {});
      if (!live()) return;
      if (saved.status === 400) {
        writeStored(key, '');
        throw Error('The request refused this answer and nothing was recorded. Open the request to answer it.');
      }
      if (saved.ok || saved.status === 412) {
        writeStored(key, '');
        this.settle(
          saved.ok
            ? 'Answer recorded.'
            : 'The request changed before this answer was saved. Showing what it records now.'
        );
        return;
      }
      throw Error('The answer was not confirmed. Retry sends the same answer.');
    } catch (error) {
      if (live())
        this.state = {
          ...this.state,
          busy: false,
          message: error instanceof Error && error.name !== 'AbortError' && error.name !== 'TimeoutError'
            ? error.message
            : 'The answer was not confirmed. Retry sends the same answer.',
        };
    }
  }

  private settle(message: string) {
    this.state = {busy: false, correction: '', message};
    this.dispatchEvent(new CustomEvent(assumptionAnswered, {bubbles: true}));
  }

  private renderAnswer(a: FeedAssumption): TemplateResult {
    if (a.state === 'answered') {
      if (a.decision === 'edit')
        return html`<div class="soda-feed-assumption-answer">
          <h4>Answered with a correction</h4>
          <p>${a.correction}</p>
        </div>`;
      return html`<p class="soda-feed-assumption-answer">
        ${a.decision === 'decline' ? 'Answered: declined.' : 'Answered: it holds.'}
      </p>`;
    }
    if (a.state !== 'open') return html`<p class="soda-feed-assumption-answer">This question no longer applies to the current request.</p>`;
    if (this.current?.viewer !== a.owner)
      return html`<p class="soda-feed-assumption-answer">Waiting for the request owner to answer.</p>`;
    const busy = this.state.busy;
    if (this.retained(this.storageKey()))
      return html`<p class="soda-feed-assumption-answer">An answer is saved but not confirmed.</p>
        <div class="soda-feed-assumption-actions">
          <button type="button" class="ui primary button" ?disabled=${busy} @click=${() => void this.answer('accept')}>
            Retry saved answer
          </button>
        </div>`;
    return html`<div class="soda-feed-assumption-actions">
      <button type="button" class="ui primary button" ?disabled=${busy} @click=${() => void this.answer('accept')}>
        It holds
      </button>
      <input
        type="text"
        maxlength="4000"
        aria-label="Explain the correction"
        placeholder="Or explain the correction…"
        .value=${this.state.correction}
        ?disabled=${busy}
        @input=${(event: Event) => {
          const node = event.target;
          if (node instanceof HTMLInputElement) this.state = {...this.state, correction: node.value};
        }}
        @keydown=${(event: KeyboardEvent) => {
          if (event.key === 'Enter') {
            event.preventDefault();
            void this.answer('edit');
          }
        }}
      />
      <button
        type="button"
        class="ui button"
        ?disabled=${busy || !this.state.correction.trim()}
        @click=${() => void this.answer('edit')}
      >
        Send correction
      </button>
    </div>`;
  }

  protected render() {
    const a = this.source;
    if (!a) return html``;
    return html`<div class="soda-feed-assumption" data-assumption-state=${a.state ?? 'closed'}>
      <div><h4>Assumed</h4><p>${a.assumption}</p></div>
      <div><h4>If that is wrong</h4><p>${a.consequence}</p></div>
      <div class="soda-feed-assumption-recommended">
        <span aria-hidden="true">↳</span>
        <div><h4>Recommended</h4><p>${a.recommendation}</p></div>
      </div>
      ${this.renderAnswer(a)}
      ${this.state.message ? html`<p class="soda-feed-assumption-message" role="status">${this.state.message}</p>` : ''}
      ${a.state === 'open' ? html`<p class="soda-feed-assumption-hold">Work that depends on this is held. Everything else continues.</p>` : ''}
    </div>`;
  }
}
if (!customElements.get('soda-feed-assumption')) customElements.define('soda-feed-assumption', SodaFeedAssumption);

/** The F13 card body: caption, the title served for its state, and the challenge. */
export function renderFeedAssumption(c: FeedCommunication, context: FeedAssumptionContext | undefined): TemplateResult {
  const a = c.assumption!;
  return html`<div class="soda-feed-communication" data-feed-component="F13" data-presentation="question">
    <p class="soda-feed-component-caption">Assumption challenge${c.scope ? html`<span> · ${c.scope}</span>` : ''}</p>
    <h3>${c.title}</h3>
    <soda-feed-assumption .assumption=${a} .context=${context}></soda-feed-assumption>
  </div>`;
}
