import {LitElement, html} from 'lit';
import type {TemplateResult} from 'lit';
import {object} from '../api/models.js';
import type {FactoryTransport} from '../api/transport.js';
import {readStored, writeStored} from '../shared/drafts.js';
import {newIdempotencyKey} from './feed-response.js';
import type {FeedCommunication, FeedQuestionAnswer, FeedQuestionForm} from './feed-response.js';

// A typed question's answer controls (slice 5.1), rendered from the ask's
// declared form; no agent markup is shown. The answer is an ordinary reply
// (POST factory/feed/posts with `answer` instead of a body): the server checks
// it against the form and records its plain-text rendering. The exact request,
// with its idempotency key, is retained before sending so Retry resends it and
// a lost reply replays instead of recording twice. Free-text replies stay in
// the card's reply box.

export interface FeedQuestionContext {
  readonly transport: FactoryTransport;
  readonly signal: AbortSignal;
  /** The host's draft scope; an unconfirmed answer is retained under it. */
  readonly scope: string | undefined;
  readonly repository: string;
}

export interface FeedTypedQuestion {
  readonly post: string;
  readonly form: FeedQuestionForm;
  readonly answered: FeedCommunication['answered'];
}

/** Fired after an answer settles so the feed reads the recorded state. */
export const questionAnswered = 'soda-feed-question-answered';

interface AnswerPost {
  repository: string;
  parent_id: string;
  idempotency_key: string;
  answer: FeedQuestionAnswer;
}

const empty = () => ({busy: false, picks: [] as string[], value: '', message: ''});

const kindWords: Record<FeedQuestionForm['kind'], string> = {
  pick_one: 'pick one',
  yes_no: 'yes or no',
  score: 'score',
  pick_several: 'pick several',
  value: 'set a value',
};

export class SodaFeedQuestion extends LitElement {
  static properties = {state: {state: true}};
  declare private state: ReturnType<typeof empty>;
  private source: FeedTypedQuestion | undefined;
  private current: FeedQuestionContext | undefined;
  private serial = 0;
  constructor() {
    super();
    this.state = empty();
  }
  protected createRenderRoot() {
    return this;
  }
  get question(): FeedTypedQuestion | undefined {
    return this.source;
  }
  set question(value: FeedTypedQuestion | undefined) {
    const prior = this.source;
    if (value?.post === prior?.post && JSON.stringify(value?.answered) === JSON.stringify(prior?.answered)) return;
    if (value?.post !== prior?.post) {
      this.serial++;
      this.state = empty();
    }
    this.source = value;
    this.requestUpdate();
  }
  get context(): FeedQuestionContext | undefined {
    return this.current;
  }
  set context(value: FeedQuestionContext | undefined) {
    const prior = this.current;
    if (
      value?.transport === prior?.transport &&
      value?.signal === prior?.signal &&
      value?.scope === prior?.scope &&
      value?.repository === prior?.repository
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
    const q = this.source,
      scope = this.current?.scope;
    return q && scope ? `soda-feed-question:v1:${scope}:${q.post}` : undefined;
  }

  // retained reads the exact unconfirmed answer request, if one is kept.
  private retained(key: string | undefined): AnswerPost | undefined {
    const raw = key ? readStored(key) : '';
    if (!raw) return undefined;
    try {
      const saved = object(JSON.parse(raw));
      const answer = object(saved.answer);
      if (
        saved.repository === this.current?.repository &&
        saved.parent_id === this.source?.post &&
        typeof saved.idempotency_key === 'string' &&
        /^[A-Za-z0-9_-]{1,64}$/.test(saved.idempotency_key) &&
        answer.kind === this.source?.form.kind
      )
        return saved as unknown as AnswerPost;
    } catch {
      // An unreadable copy is dropped below; nothing was confirmed from it.
    }
    writeStored(key!, '');
    return undefined;
  }

  // chosen builds the answer from the current selection, or nothing while
  // the selection does not yet fit the form.
  private chosen(): FeedQuestionAnswer | undefined {
    const form = this.source?.form;
    const {picks, value} = this.state;
    const one = picks.length === 1 ? picks[0] : undefined;
    switch (form?.kind) {
      case 'pick_one':
        return one !== undefined ? {kind: form.kind, choice: one} : undefined;
      case 'yes_no':
        return one !== undefined ? {kind: form.kind, yes: one === 'yes'} : undefined;
      case 'score':
        return one !== undefined ? {kind: form.kind, score: Number(one)} : undefined;
      case 'pick_several': {
        const [least, most] = this.bounds(form);
        return picks.length >= least && picks.length <= most
          ? {kind: form.kind, choices: (form.options ?? []).map((o) => o.id).filter((id) => picks.includes(id))}
          : undefined;
      }
      case 'value': {
        const text = value.trim();
        if (!text || text.length > 200) return undefined;
        if (form.number && (text.length > 32 || !Number.isFinite(Number(text)))) return undefined;
        return {kind: form.kind, value: text};
      }
    }
    return undefined;
  }

  private bounds(form: FeedQuestionForm): [number, number] {
    const options = form.options?.length ?? 0;
    return [form.minPicks || 1, form.maxPicks || options];
  }

  private async send() {
    const q = this.source,
      context = this.current,
      key = this.storageKey();
    if (!q || !context || this.state.busy || q.answered) return;
    if (!key) {
      this.state = {...this.state, message: 'Answers cannot be kept here. Reply in words below instead.'};
      return;
    }
    let post = this.retained(key);
    if (!post) {
      const answer = this.chosen();
      if (!answer) return;
      post = {repository: context.repository, parent_id: q.post, idempotency_key: newIdempotencyKey(), answer};
      writeStored(key, JSON.stringify(post));
      if (readStored(key) !== JSON.stringify(post)) {
        this.state = {...this.state, message: 'The answer could not be kept. Nothing was sent.'};
        return;
      }
    }
    const serial = ++this.serial;
    const live = () => serial === this.serial && !context.signal.aborted;
    this.state = {...this.state, busy: true, message: ''};
    try {
      const saved = await context.transport.request('factory/feed/posts', {
        method: 'POST',
        headers: {'Content-Type': 'application/json'},
        body: JSON.stringify(post),
        signal: AbortSignal.any([context.signal, AbortSignal.timeout(30000)]),
      });
      void saved.body?.cancel().catch(() => {});
      if (!live()) return;
      if (saved.status === 400) {
        writeStored(key, '');
        throw Error('The question refused this answer and nothing was recorded. Reply in words below instead.');
      }
      if (saved.ok || saved.status === 409) {
        writeStored(key, '');
        this.state = {...empty(), message: saved.ok ? 'Answer recorded.' : 'This question already has a recorded answer. Showing it now.'};
        this.dispatchEvent(new CustomEvent(questionAnswered, {bubbles: true}));
        return;
      }
      throw Error('The answer was not confirmed. Retry sends the same answer.');
    } catch (error) {
      if (live())
        this.state = {
          ...this.state,
          busy: false,
          message:
            error instanceof Error && error.name !== 'AbortError' && error.name !== 'TimeoutError'
              ? error.message
              : 'The answer was not confirmed. Retry sends the same answer.',
        };
    }
  }

  private pick(value: string, several = false) {
    const picks = several
      ? this.state.picks.includes(value)
        ? this.state.picks.filter((row) => row !== value)
        : [...this.state.picks, value]
      : [value];
    this.state = {...this.state, picks, message: ''};
  }

  // The recorded answer's selection, so answered controls show what was chosen.
  private recordedPicks(answer: FeedQuestionAnswer): string[] {
    if (answer.choice) return [answer.choice];
    if (answer.choices) return answer.choices;
    if (answer.yes !== undefined) return [answer.yes ? 'yes' : 'no'];
    if (answer.score !== undefined) return [String(answer.score)];
    return [];
  }

  private renderControls(form: FeedQuestionForm, picks: string[], value: string, locked: boolean): TemplateResult {
    const name = `soda-question-${this.source!.post}`;
    switch (form.kind) {
      case 'pick_one':
      case 'pick_several': {
        const several = form.kind === 'pick_several';
        const [least, most] = this.bounds(form);
        return html`<fieldset class="soda-feed-typed-options" ?disabled=${locked}>
          <legend class=${several ? '' : 'soda-feed-typed-quiet'}>
            ${several ? (least === most ? `Pick ${least}` : `Pick ${least} to ${most}`) : 'Pick one'}
          </legend>
          ${(form.options ?? []).map(
            (option) => html`<label class="soda-feed-typed-option">
              <input
                type=${several ? 'checkbox' : 'radio'}
                name=${name}
                .value=${option.id}
                .checked=${picks.includes(option.id)}
                @change=${() => this.pick(option.id, several)}
              />
              <span><strong>${option.label}</strong>${option.detail ? html`<span>${option.detail}</span>` : ''}</span>
            </label>`
          )}
        </fieldset>`;
      }
      case 'yes_no':
        return html`${form.yesMeans || form.noMeans
            ? html`<div class="soda-feed-typed-means">
                ${form.yesMeans ? html`<p><strong>Yes means</strong> ${form.yesMeans}</p>` : ''}
                ${form.noMeans ? html`<p><strong>No means</strong> ${form.noMeans}</p>` : ''}
              </div>`
            : ''}
          <div class="soda-feed-typed-segments" role="group" aria-label="Yes or no">
            ${(['yes', 'no'] as const).map(
              (side) => html`<button
                type="button"
                aria-pressed=${picks.includes(side) ? 'true' : 'false'}
                ?disabled=${locked}
                @click=${() => this.pick(side)}
              >
                ${side === 'yes' ? 'Yes' : 'No'}
              </button>`
            )}
          </div>`;
      case 'score': {
        const low = form.low ?? 0,
          high = form.high ?? 10;
        const levels = Array.from({length: high - low + 1}, (_, index) => low + index);
        return html`<fieldset class="soda-feed-typed-score" ?disabled=${locked}>
          <legend>Score from ${low} to ${high}</legend>
          <div class="soda-feed-typed-segments">
            ${levels.map(
              (level) => html`<label>
                <input
                  type="radio"
                  name=${name}
                  .value=${String(level)}
                  .checked=${picks.includes(String(level))}
                  @change=${() => this.pick(String(level))}
                />
                <span>${level}</span>
              </label>`
            )}
          </div>
          ${form.lowLabel || form.highLabel
            ? html`<p class="soda-feed-typed-ends" aria-hidden="true">
                <span>${form.lowLabel ? `${low} · ${form.lowLabel}` : ''}</span><span
                  >${form.highLabel ? `${high} · ${form.highLabel}` : ''}</span
                >
              </p>`
            : ''}
        </fieldset>`;
      }
      case 'value':
        return html`<div class="soda-feed-typed-value">
          <label for=${name}>${form.label}</label>
          <span>
            <input
              id=${name}
              type="text"
              inputmode=${form.number ? 'decimal' : 'text'}
              maxlength=${form.number ? 32 : 200}
              .value=${value}
              ?disabled=${locked}
              @input=${(event: Event) => {
                const node = event.target;
                if (node instanceof HTMLInputElement) this.state = {...this.state, value: node.value, message: ''};
              }}
              @keydown=${(event: KeyboardEvent) => {
                if (event.key === 'Enter') {
                  event.preventDefault();
                  void this.send();
                }
              }}
            />${form.unit ? html`<span class="soda-feed-typed-unit">${form.unit}</span>` : ''}
          </span>
        </div>`;
    }
  }

  protected render() {
    const q = this.source;
    if (!q) return html``;
    const answered = q.answered;
    const retained = answered ? undefined : this.retained(this.storageKey());
    const shown = answered?.answer ?? retained?.answer;
    const picks = shown ? this.recordedPicks(shown) : this.state.picks;
    const value = shown ? (shown.value ?? '') : this.state.value;
    const busy = this.state.busy;
    return html`<section class="soda-feed-typed" data-question-kind=${q.form.kind} data-answered=${answered ? 'true' : 'false'}>
      <p class="soda-feed-typed-kind">Question · ${kindWords[q.form.kind]}</p>
      ${this.renderControls(q.form, picks, value, !!shown || busy)}
      ${answered
        ? html`<p class="soda-feed-typed-reply"><span>Posted as your reply</span>${answered.text}</p>`
        : busy
          ? html`<p class="soda-feed-typed-note" role="status">Sending your answer…</p>`
          : retained
          ? html`<p class="soda-feed-typed-note">An answer is saved but not confirmed.</p>
              <div class="soda-feed-typed-actions">
                <button type="button" class="ui primary button" @click=${() => void this.send()}>
                  Retry saved answer
                </button>
              </div>`
          : html`<div class="soda-feed-typed-actions">
              <button type="button" class="ui primary button" ?disabled=${!this.chosen()} @click=${() => void this.send()}>
                Send answer
              </button>
              <span>Or reply in words below.</span>
            </div>`}
      ${this.state.message ? html`<p class="soda-feed-typed-note" role="status">${this.state.message}</p>` : ''}
    </section>`;
  }
}
if (!customElements.get('soda-feed-question')) customElements.define('soda-feed-question', SodaFeedQuestion);

/** The typed controls for an ask that declares an answer form. */
export function renderTypedQuestion(
  c: FeedCommunication,
  entryKey: string,
  context: FeedQuestionContext | undefined
): TemplateResult | string {
  const post = entryKey.slice(entryKey.indexOf(':') + 1);
  if (!c.answerForm || !/^[a-f0-9]{32}$/.test(post)) return '';
  const question: FeedTypedQuestion = {post, form: c.answerForm, answered: c.answered};
  return html`<soda-feed-question .question=${question} .context=${context}></soda-feed-question>`;
}
