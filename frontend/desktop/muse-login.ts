import {html} from 'lit';
import type {Core} from './transport.js';
import {ControllerView, Observed} from './controller-view.js';

interface View {
  attempt: number;
  available: boolean;
  can_control: boolean;
  checking: boolean;
  phase: 'idle' | 'requesting' | 'waiting' | 'complete' | 'cancelled' | 'failed';
  prompt: {user_code: string; verification_uri: string; expires_at: number} | null;
  error: string | null;
  tools: {
    state: 'checking' | 'downloading' | 'ready' | 'unavailable';
    upstream_version: string | null;
    selected_version: string | null;
    checked_at_unix: number | null;
    error: string | null;
  } | null;
}

/** Muse sign-in and release verification for one window. Muse keeps its own
 * login; this controller owns the public view state and every request. */
export class MuseLogin extends Observed {
  /** False until the first status refresh. */
  started = false;
  ready = false;
  busy = false;
  error: string | null = null;
  view: View | undefined;
  private epoch = -1;
  private interaction = 0;

  constructor(private readonly core: Pick<Core, 'invoke'>) {
    super();
    document.addEventListener('visibilitychange', () => {
      if (document.hidden) {
        this.interaction++;
        this.view = undefined;
        this.error = null;
        this.busy = false;
        this.notify();
      }
    });
  }
  get active() {
    return this.view?.phase === 'requesting' || this.view?.phase === 'waiting';
  }
  private async action(command: string, args?: unknown) {
    if (!this.ready || this.busy) return;
    const generation = this.epoch,
      captured = ++this.interaction;
    this.busy = true;
    this.error = null;
    this.notify();
    try {
      const next = await this.core.invoke<View>(command, args);
      if (generation === this.epoch && captured === this.interaction && command !== 'muse_sign_in_open') this.view = next;
    } catch (failure) {
      if (generation === this.epoch && captured === this.interaction)
        this.error = typeof failure === 'string' ? failure : 'Muse sign-in is unavailable. Try again.';
    } finally {
      if (generation === this.epoch && captured === this.interaction) {
        this.busy = false;
        this.notify();
      }
    }
  }
  signIn() {
    void this.action('muse_sign_in');
  }
  checkUpdate() {
    void this.action('muse_tools_refresh');
  }
  cancel() {
    void this.action('muse_sign_in_cancel', {attempt: this.view?.attempt});
  }
  open() {
    void this.action('muse_sign_in_open', {attempt: this.view?.attempt});
  }
  async refresh(available: boolean, generation: number) {
    this.started = true;
    if (this.epoch !== generation) {
      this.epoch = generation;
      this.interaction++;
      this.view = undefined;
      this.error = null;
      this.busy = false;
    }
    this.ready = available;
    this.notify();
    if (!this.ready || this.busy) return;
    const captured = this.interaction;
    try {
      const next = await this.core.invoke<View>('muse_sign_in_status');
      if (generation !== this.epoch || captured !== this.interaction || !this.ready) return;
      this.view = next;
      this.notify();
    } catch {
      if (generation === this.epoch && captured === this.interaction) {
        this.view = undefined;
        this.error = 'Muse sign-in status is unavailable. Refresh Factory and try again.';
        this.notify();
      }
    }
  }
}

/** Presentation of one MuseLogin controller. */
export class SodaDesktopMuse extends ControllerView<MuseLogin> {
  protected render() {
    const c = this.controller;
    if (!c) return html``;
    const view = c.view,
      tool = view?.tools,
      active = c.active,
      ready = c.ready;
    const toolPhase = !ready
      ? 'Muse updates are unavailable until Factory is ready.'
      : !tool || tool.state === 'checking'
        ? 'Checking the current Muse release…'
        : tool.state === 'downloading'
          ? `Downloading Muse${tool.upstream_version ? ' ' + tool.upstream_version : ''}…`
          : tool.state === 'ready'
            ? 'Muse is verified for new work.'
            : tool.error ?? 'Muse could not be verified or updated. Check again when online.';
    const toolStatus = !c.started
      ? 'Checking the current Muse release…'
      : toolPhase +
        (ready && tool
          ? [
              tool.upstream_version ? ` Latest: ${tool.upstream_version}.` : '',
              tool.selected_version ? ` Selected: ${tool.selected_version}.` : '',
              tool.checked_at_unix ? ` Last verified: ${new Date(tool.checked_at_unix * 1000).toLocaleString()}.` : '',
            ].join('')
          : '');
    const status = !c.started
      ? 'Checking Muse sign-in…'
      : !ready
        ? 'Muse sign-in is unavailable until Factory is ready.'
        : c.error ??
          view?.error ??
          (view &&
            {
              idle: view.available
                ? 'Muse keeps its own saved login. Sign in here when needed.'
                : 'Muse sign-in is unavailable until its current release is verified.',
              requesting: 'Requesting a Muse sign-in code…',
              waiting: 'Confirm the matching code on Meta to sign in.',
              complete: 'Muse sign-in finished. Refresh agent roles to use this login.',
              cancelled: 'Muse sign-in stopped.',
              failed: 'Muse sign-in could not finish. Try again.',
            }[view.phase]) ??
          'Checking Muse sign-in…';
    const prompt = ready && view?.phase === 'waiting' ? view.prompt : null;
    return html`<p id="muse-status" role="status">${status}</p>
      <p id="muse-tool-status" role="status">${toolStatus}</p>
      <button id="muse-check-update" type="button" ?disabled=${!ready || active || c.busy || !tool || !!view?.checking} @click=${() => c.checkUpdate()}>Check Muse update</button>
      <button id="muse-sign-in" type="button" ?disabled=${!ready || !view?.available || active || c.busy} @click=${() => c.signIn()}>Sign in to Muse</button>
      <div id="muse-prompt" ?hidden=${!prompt}>
        <p>Confirm this code on Meta:</p>
        <code id="muse-user-code" tabindex="0" aria-label="Muse verification code">${prompt?.user_code ?? ''}</code>
        <button id="muse-open" type="button" ?disabled=${!ready || !view?.can_control || c.busy || !prompt} @click=${() => c.open()}>Open Meta</button>
      </div>
      <button id="muse-cancel" type="button" ?hidden=${!active} ?disabled=${!ready || !view?.can_control || c.busy} @click=${() => c.cancel()}>Cancel sign-in</button>`;
  }
}
customElements.define('soda-desktop-muse', SodaDesktopMuse);
