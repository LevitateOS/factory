import {LitElement, html} from 'lit';
import type {TemplateResult} from 'lit';
import {object} from '../api/models.js';
import type {FactoryRepositoryLinks} from '../app/navigation.js';
import {readStored, writeStored} from '../shared/drafts.js';
import {actionIcon} from './feed-actions.js';
import type {FeedQuestionContext} from './feed-question.js';
import {newIdempotencyKey} from './feed-response.js';
import type {FeedCommunication, FeedWithdrawal, FeedWithdrawn} from './feed-response.js';

// Withdrawing a post (slice 5.5; pkg/factory/feed_withdraw.go owns the rules
// and the served `withdrawn`). The original stays in the feed, dimmed and
// marked Withdrawn with the reason and a link to the correction. The person
// withdraws their own posts here: one POST factory/feed/posts carrying
// `withdraw`, retained with its idempotency key before sending so Retry
// resends the same request and a lost reply replays instead of recording twice.

/** Fired after a withdrawal settles so the feed reads the recorded state. */
export const postWithdrawn = 'soda-feed-post-withdrawn';

const postID = /^[a-f0-9]{32}$/;

// A link to another entry: the host's feed place for that post, scrolled to
// in place by the feed when the entry is already shown (data-feed-jump).
function jumpLink(post: string, label: string, places: FactoryRepositoryLinks | undefined): TemplateResult {
  return html`<a href=${places?.feed(post) ?? `#feed-post-${post}`} data-feed-jump=${post}>${label}</a>`;
}

/** The mark on a withdrawn post's card. */
export function renderWithdrawnMark(withdrawn: FeedWithdrawn, places: FactoryRepositoryLinks | undefined): TemplateResult {
  const correction = withdrawn.correction && postID.test(withdrawn.correction) ? withdrawn.correction : '';
  return html`<div class="soda-feed-withdrawn" role="note">
    <p class="soda-feed-withdrawn-mark"><strong>Withdrawn</strong>${withdrawn.by ? html`<span> by ${withdrawn.by}</span>` : ''}</p>
    ${withdrawn.reason ? html`<p class="soda-feed-withdrawn-reason">${withdrawn.reason}</p>` : ''}
    ${correction ? html`<p class="soda-feed-withdrawn-link">${jumpLink(correction, 'See the correction', places)}</p>` : ''}
  </div>`;
}

/** A withdrawal record's own card content, for an agent or the person. */
export function renderWithdrawalRecord(
  withdrawal: FeedWithdrawal,
  author: string,
  places: FactoryRepositoryLinks | undefined
): TemplateResult {
  const corrected = withdrawal.correction && postID.test(withdrawal.correction) ? withdrawal.correction : '';
  return html`<div class="soda-feed-communication" data-feed-component="withdraw" data-presentation="withdraw">
    <p class="soda-feed-component-caption">
      ${actionIcon('correction')}${author} ${corrected ? 'corrected' : 'withdrew'} an earlier post
    </p>
    <dl class="soda-feed-withdrawal">
      <div><dt>Why</dt><dd>${withdrawal.reason}</dd></div>
    </dl>
    <p class="soda-feed-withdrawn-link">
      ${postID.test(withdrawal.post) ? jumpLink(withdrawal.post, 'See the withdrawn post', places) : ''}
      ${corrected ? jumpLink(corrected, 'See the correction', places) : ''}
    </p>
  </div>`;
}

interface WithdrawPost {
  repository: string;
  idempotency_key: string;
  withdraw: {post: string; reason: string};
}

const empty = () => ({open: false, reason: '', busy: false, message: ''});

// One line of plain text, as factory.FeedWithdrawal admits it.
function admissible(reason: string): boolean {
  const text = reason.trim();
  return text !== '' && [...text].length <= 400 && ![...text].some((ch) => ch < ' ' || ch === '\u007f' || '<>`\u2028\u2029'.includes(ch));
}

export class SodaFeedWithdraw extends LitElement {
  static properties = {state: {state: true}};
  declare private state: ReturnType<typeof empty>;
  private target = '';
  private current: FeedQuestionContext | undefined;
  private serial = 0;
  constructor() {
    super();
    this.state = empty();
  }
  protected createRenderRoot() {
    return this;
  }
  get post(): string {
    return this.target;
  }
  set post(value: string) {
    if (value === this.target) return;
    this.serial++;
    this.target = value;
    this.state = empty();
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
    const scope = this.current?.scope;
    return scope && postID.test(this.target) ? `soda-feed-withdraw:v1:${scope}:${this.target}` : undefined;
  }

  // retained reads the exact unconfirmed withdrawal, if one is kept.
  private retained(key: string | undefined): WithdrawPost | undefined {
    const raw = key ? readStored(key) : '';
    if (!raw) return undefined;
    try {
      const saved = object(JSON.parse(raw));
      const withdraw = object(saved.withdraw);
      if (
        saved.repository === this.current?.repository &&
        typeof saved.idempotency_key === 'string' &&
        /^[A-Za-z0-9_-]{1,64}$/.test(saved.idempotency_key) &&
        withdraw.post === this.target &&
        typeof withdraw.reason === 'string' &&
        admissible(withdraw.reason)
      )
        return saved as unknown as WithdrawPost;
    } catch {
      // An unreadable copy is dropped below; nothing was confirmed from it.
    }
    writeStored(key!, '');
    return undefined;
  }

  private async send() {
    const context = this.current,
      key = this.storageKey();
    if (!context || this.state.busy) return;
    if (!key) {
      this.state = {...this.state, message: 'A withdrawal cannot be kept here, so nothing was sent.'};
      return;
    }
    let post = this.retained(key);
    if (!post) {
      if (!admissible(this.state.reason)) return;
      post = {
        repository: context.repository,
        idempotency_key: newIdempotencyKey(),
        withdraw: {post: this.target, reason: this.state.reason.trim()},
      };
      writeStored(key, JSON.stringify(post));
      if (readStored(key) !== JSON.stringify(post)) {
        this.state = {...this.state, message: 'The withdrawal could not be kept. Nothing was sent.'};
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
      if (saved.ok || saved.status === 409) {
        writeStored(key, '');
        this.state = {...empty(), message: saved.ok ? 'Post withdrawn.' : 'This post was already withdrawn.'};
        this.dispatchEvent(new CustomEvent(postWithdrawn, {bubbles: true}));
        return;
      }
      if (saved.status === 400 || saved.status === 403 || saved.status === 404) {
        writeStored(key, '');
        throw Error(
          saved.status === 403
            ? 'Only the post’s author can withdraw it. Nothing was recorded.'
            : 'The withdrawal was refused and nothing was recorded.'
        );
      }
      throw Error('The withdrawal was not confirmed. Retry sends the same withdrawal.');
    } catch (error) {
      if (live())
        this.state = {
          ...this.state,
          busy: false,
          message:
            error instanceof Error && error.name !== 'AbortError' && error.name !== 'TimeoutError'
              ? error.message
              : 'The withdrawal was not confirmed. Retry sends the same withdrawal.',
        };
    }
  }

  protected render() {
    if (!postID.test(this.target) || !this.current) return html``;
    const retained = this.retained(this.storageKey());
    const {open, reason, busy, message} = this.state;
    const status = message ? html`<p class="soda-feed-withdraw-note" role="status">${message}</p>` : '';
    if (busy) return html`<div class="soda-feed-withdraw"><p class="soda-feed-withdraw-note" role="status">Withdrawing…</p></div>`;
    if (retained)
      return html`<div class="soda-feed-withdraw" data-withdraw="retained">
        <p class="soda-feed-withdraw-note">A withdrawal is saved but not confirmed: “${retained.withdraw.reason}”</p>
        <div class="soda-feed-withdraw-actions">
          <button type="button" class="ui primary button" @click=${() => void this.send()}>Retry withdrawal</button>
        </div>
        ${status}
      </div>`;
    if (!open)
      return html`<div class="soda-feed-withdraw">
        <button type="button" class="ui button" data-withdraw-open @click=${() => (this.state = {...this.state, open: true, message: ''})}>
          Withdraw
        </button>
        ${status}
      </div>`;
    const name = `soda-withdraw-${this.target}`;
    return html`<div class="soda-feed-withdraw" data-withdraw="open">
      <label for=${name}>Why are you withdrawing this post?</label>
      <input
        id=${name}
        type="text"
        maxlength="400"
        .value=${reason}
        @input=${(event: Event) => {
          const node = event.target;
          if (node instanceof HTMLInputElement) this.state = {...this.state, reason: node.value, message: ''};
        }}
      />
      <p class="soda-feed-withdraw-note">It stays in the feed, marked withdrawn with your reason. This cannot be undone.</p>
      <div class="soda-feed-withdraw-actions">
        <button type="button" class="ui primary button" ?disabled=${!admissible(reason)} @click=${() => void this.send()}>
          Withdraw post
        </button>
        <button type="button" class="ui button" @click=${() => (this.state = empty())}>Cancel</button>
      </div>
      ${status}
    </div>`;
  }
}
if (!customElements.get('soda-feed-withdraw')) customElements.define('soda-feed-withdraw', SodaFeedWithdraw);

/** The Withdraw action on the person's own post. */
export function renderWithdrawAction(post: string, context: FeedQuestionContext | undefined): TemplateResult | string {
  return postID.test(post) && context ? html`<soda-feed-withdraw .post=${post} .context=${context}></soda-feed-withdraw>` : '';
}

/** A withdrawn post's communication without its answer controls. */
export function withdrawnCommunication(communication: FeedCommunication): FeedCommunication {
  const copy = {...communication};
  delete copy.answerForm;
  delete copy.options;
  if (copy.kind === 'ask') copy.questionState = 'withdrawn';
  return copy;
}
