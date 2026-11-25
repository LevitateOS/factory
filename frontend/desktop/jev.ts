import {html, nothing} from 'lit';
import type {Core} from './transport.js';
import {ControllerView, Observed} from './controller-view.js';

export type JevState = 'none' | 'connected' | 'refused' | 'locked';
export interface JevModel {
  name: string;
  description: string;
  release_date: string;
}
/** Rust's key-free JEV status. */
export interface JevStatus {
  state: JevState;
  last4?: string;
  added_at: number;
  checked_at: number;
  refused_at?: number;
  default_model: string;
  models: JevModel[];
}
type Phase = 'idle' | 'checking' | 'replacing' | 'confirm-remove';

/** The TypeSafe key for JEV on this Mac. Rust keeps the key; this controller
 * owns the public status and every request, and a key typed into the view goes
 * straight to `jev_connect` without being kept here. Reading status never
 * contacts TypeSafe: only Connect, Check now and replacing a key do. */
export class JevAccount extends Observed {
  started = false;
  ready = false;
  busy = false;
  phase: Phase = 'idle';
  status: JevStatus | undefined;
  error: string | null = null;
  private epoch = -1;
  private interaction = 0;

  constructor(private readonly core: Pick<Core, 'invoke'>) {
    super();
  }
  get state(): JevState | undefined {
    return this.status?.state;
  }
  private set(next: JevStatus | undefined) {
    this.status = next;
  }
  private async action(command: string, args?: Record<string, unknown>, checking = false) {
    if (!this.ready || this.busy) return false;
    const generation = this.epoch,
      captured = ++this.interaction;
    this.busy = true;
    this.error = null;
    if (checking) this.phase = 'checking';
    this.notify();
    try {
      const next = await this.core.invoke<JevStatus>(command, args);
      if (generation !== this.epoch || captured !== this.interaction) return false;
      this.set(next);
      this.phase = 'idle';
      return true;
    } catch (failure) {
      if (generation === this.epoch && captured === this.interaction) {
        this.error = typeof failure === 'string' ? failure : 'This JEV action could not finish. Try again.';
        if (this.phase === 'checking') this.phase = this.status?.state === 'none' ? 'idle' : 'replacing';
      }
      return false;
    } finally {
      if (generation === this.epoch && captured === this.interaction) {
        this.busy = false;
        this.notify();
      }
    }
  }
  /** Sends the pasted key once. The caller clears its field afterwards. */
  connect(key: string) {
    const trimmed = key.trim();
    if (!trimmed) {
      this.error = 'Paste your TypeSafe API key first.';
      this.notify();
      return Promise.resolve(false);
    }
    return this.action('jev_connect', {key: trimmed}, true);
  }
  check() {
    void this.action('jev_check');
  }
  setModel(model: string) {
    void this.action('jev_set_model', {model});
  }
  replace() {
    this.phase = 'replacing';
    this.error = null;
    this.notify();
  }
  confirmRemove() {
    this.phase = 'confirm-remove';
    this.error = null;
    this.notify();
  }
  back() {
    this.phase = 'idle';
    this.error = null;
    this.notify();
  }
  remove() {
    void this.action('jev_remove');
  }
  open(page: 'keys' | 'rate_limits') {
    void this.core.invoke('jev_open_console', {page}).catch(() => {
      this.error = 'Your browser could not open. Visit console.typesafe.ai.';
      this.notify();
    });
  }
  async refresh(available: boolean, generation: number) {
    this.started = true;
    if (this.epoch !== generation) {
      this.epoch = generation;
      this.interaction++;
      this.set(undefined);
      this.error = null;
      this.busy = false;
      this.phase = 'idle';
    }
    this.ready = available;
    this.notify();
    if (!this.ready || this.busy) return;
    const captured = this.interaction;
    try {
      const next = await this.core.invoke<JevStatus>('jev_status');
      if (generation !== this.epoch || captured !== this.interaction || !this.ready) return;
      this.set(next);
      this.notify();
    } catch (failure) {
      if (generation === this.epoch && captured === this.interaction) {
        this.set(undefined);
        this.error = typeof failure === 'string' ? failure : 'JEV status is unavailable. Refresh Factory and try again.';
        this.notify();
      }
    }
  }
}

const day = (seconds: number) =>
  seconds ? new Date(seconds * 1000).toLocaleDateString(undefined, {month: 'short', day: 'numeric', year: 'numeric'}) : '—';
const moment = (seconds: number) =>
  new Date(seconds * 1000).toLocaleString(undefined, {month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit'});

/** Presentation of one JevAccount controller, for Accounts and first run. */
export class SodaDesktopJev extends ControllerView<JevAccount> {
  /** First run offers "Skip for now" through this callback. */
  skip: (() => void) | undefined;
  private shown = false;

  // The one view moves between first run and Accounts; only first run offers Skip.
  connectedCallback() {
    super.connectedCallback();
    this.requestUpdate();
  }
  private async submit(event: Event) {
    event.preventDefault();
    const c = this.controller;
    const input = this.querySelector<HTMLInputElement>('#jev-key');
    if (!c || !input) return;
    const ok = await c.connect(input.value);
    if (ok) {
      input.value = '';
      this.shown = false;
    }
    this.requestUpdate();
  }
  private entry(replacing: boolean) {
    const c = this.controller!;
    const checking = c.phase === 'checking';
    return html`<form id="jev-entry" class="jev-entry" @submit=${(event: Event) => void this.submit(event)}>
      <label for="jev-key">${replacing ? 'New TypeSafe API key' : 'TypeSafe API key'}</label>
      <div class="jev-key-row">
        <input
          id="jev-key"
          type=${this.shown ? 'text' : 'password'}
          autocomplete="off"
          spellcheck="false"
          placeholder="Paste your key"
          ?disabled=${!c.ready || checking}
        />
        <button type="button" aria-pressed=${this.shown} @click=${() => { this.shown = !this.shown; this.requestUpdate(); }}>${this.shown ? 'Hide' : 'Show'}</button>
      </div>
      <div class="jev-actions">
        <button type="submit" class="jev-primary" ?disabled=${!c.ready || c.busy}>
          ${checking ? 'Checking with TypeSafe…' : replacing ? 'Check and replace' : 'Connect JEV'}
        </button>
        ${replacing ? html`<button type="button" ?disabled=${c.busy} @click=${() => c.back()}>Keep the current key</button>` : nothing}
        ${!replacing && this.skip && this.closest('soda-desktop-first-run') ? html`<button type="button" class="jev-skip" ?disabled=${c.busy} @click=${() => this.skip?.()}>Skip for now</button>` : nothing}
        <button type="button" class="jev-link" @click=${() => c.open('keys')}>Get a key from the TypeSafe console</button>
      </div>
      <p class="jev-help">
        Factory checks the key with TypeSafe before saving it. The check is free. The key is kept in this Mac’s Keychain and is
        sent only to TypeSafe.${replacing ? ' The current key stays in use until the new one passes the check.' : ''}
      </p>
    </form>`;
  }
  protected render() {
    const c = this.controller;
    if (!c) return html``;
    const s = c.status;
    const state = s?.state;
    const badge = !c.started
      ? 'Opening…'
      : !c.ready
        ? 'Unavailable'
        : state === 'connected'
          ? 'Connected'
          : state === 'refused'
            ? 'Key refused'
            : state === 'locked'
              ? 'Keyring locked'
              : 'Not connected';
    const models = s?.models.length ? s.models : [{name: 'jev-latest', description: '', release_date: ''}];
    return html`<div class="jev" data-state=${state ?? 'unknown'}>
      <p class="jev-intro">
        <span class="jev-badge" data-state=${state ?? 'unknown'} role="status">${badge}</span>
        JEV makes the small calls that keep work moving: which agent a post needs, what to work on next, whether a plan is ready and
        whether finished work can be delivered. It runs on TypeSafe with your own API key.
      </p>
      ${c.error ? html`<p class="jev-alert" role="alert">${c.error}</p>` : nothing}
      ${state === 'refused'
        ? html`<p class="jev-alert" role="alert">
            TypeSafe stopped accepting the saved key${s?.refused_at ? ` on ${moment(s.refused_at)}` : ''}. Decisions wait until a key
            TypeSafe accepts is saved.
          </p>`
        : nothing}
      ${state === 'locked' ? html`<p class="jev-alert" role="alert">Unlock your desktop keyring so Factory can read the JEV key.</p>` : nothing}
      ${!c.ready || state === undefined || state === 'locked'
        ? nothing
        : state === 'none'
          ? this.entry(false)
          : c.phase === 'replacing' || (c.phase === 'checking' && c.busy)
            ? this.entry(true)
            : html`<dl class="jev-details">
                  <div><dt>Key</dt><dd><code class="jev-key">•••• •••• ${s?.last4 ?? ''}</code> Added ${day(s!.added_at)} · Checked ${day(s!.checked_at)}</dd></div>
                  <div>
                    <dt><label for="jev-model">Model</label></dt>
                    <dd>
                      <select id="jev-model" ?disabled=${c.busy || state !== 'connected'} @change=${(event: Event) => c.setModel((event.target as HTMLSelectElement).value)}>
                        ${models.map(
                          (model) => html`<option value=${model.name} ?selected=${model.name === s!.default_model}>
                            ${model.name === 'jev-latest' ? 'Latest JEV (jev-latest), recommended' : model.name}
                          </option>`
                        )}
                      </select>
                    </dd>
                  </div>
                </dl>
                ${c.phase === 'confirm-remove'
                  ? html`<div class="jev-confirm" role="alertdialog" aria-labelledby="jev-confirm-title">
                      <p id="jev-confirm-title"><strong>Remove the JEV key from this Mac?</strong></p>
                      <p>New posts and plans will wait until a key is connected again. The key keeps working at TypeSafe until you delete it there.</p>
                      <div class="jev-actions">
                        <button type="button" class="jev-primary" ?disabled=${c.busy} @click=${() => c.remove()}>Remove from Factory</button>
                        <button type="button" ?disabled=${c.busy} @click=${() => c.back()}>Keep it</button>
                      </div>
                    </div>`
                  : html`<div class="jev-actions">
                      <button type="button" ?disabled=${c.busy} @click=${() => c.replace()}>Replace key</button>
                      <button type="button" ?disabled=${c.busy} @click=${() => c.check()}>${c.busy ? 'Checking…' : 'Check now'}</button>
                      <button type="button" ?disabled=${c.busy} @click=${() => c.confirmRemove()}>Remove from Factory</button>
                      <button type="button" class="jev-link" @click=${() => c.open('rate_limits')}>Rate limits in TypeSafe</button>
                    </div>`}`}
      <details class="jev-sent">
        <summary>What Factory sends to TypeSafe</summary>
        <p>
          Post titles and text, role names and descriptions, request and plan text, candidate titles, and the file paths and short
          excerpts Factory picks for a decision. Never your credentials, GitHub tokens or whole repositories. TypeSafe states that it
          does not train on customer requests.
        </p>
      </details>
    </div>`;
  }
}
customElements.define('soda-desktop-jev', SodaDesktopJev);
