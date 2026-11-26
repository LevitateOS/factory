import {html} from 'lit';
import {repeat} from 'lit/directives/repeat.js';
import type {Core} from './transport.js';
import {ControllerView, Observed} from './controller-view.js';

export interface Account {
  connection: string;
  provider: string;
  origin: string;
  account: string;
  label?: string;
  state: 'stored' | 'expired' | 'refresh_pending' | 'locked' | 'withdrawn';
  grant?: {refresh_available: boolean; scope?: string};
}
interface SignIn {
  attempt: number;
  phase: 'idle' | 'requesting' | 'waiting' | 'verifying' | 'saving' | 'complete' | 'failed' | 'cancelled';
  prompt: {user_code: string; verification_uri: string; expires_at: number} | null;
  error: string | null;
}
const idle = (): SignIn => ({attempt: 0, phase: 'idle', prompt: null, error: null});

/** GitHub device-code sign-in and saved accounts for one window. Rust keeps the
 * credentials; this controller owns the public view state and every request, and
 * views only render it. `changed` receives undefined for unobserved status and
 * an empty list for observed account removal. */
export class GitHubAccounts extends Observed {
  /** False until the first status refresh. */
  started = false;
  ready = false;
  busy = false;
  view: SignIn = idle();
  accounts: Account[] = [];
  accountsCurrent = false;
  localError: string | null = null;
  readError: string | null = null;
  private generation = -1;
  private reading = false;
  private interaction = 0;

  constructor(
    private readonly core: Pick<Core, 'invoke'>,
    private readonly changed: (accounts: readonly Account[] | undefined) => void = () => {}
  ) {
    super();
  }
  get active() {
    return ['requesting', 'waiting', 'verifying', 'saving'].includes(this.view.phase);
  }
  private async read() {
    if (this.reading || !this.ready) return;
    this.reading = true;
    const captured = this.generation;
    const capturedInteraction = this.interaction;
    try {
      // A sign-in in progress shows its code even when saved accounts can't be read.
      const [signIn, read] = await Promise.allSettled([
        this.core.invoke<SignIn>('github_sign_in_status'),
        this.core.invoke<Account[]>('credential_status'),
      ]);
      if (captured !== this.generation || capturedInteraction !== this.interaction || !this.ready) return;
      if (signIn.status === 'fulfilled') {
        if (signIn.value.phase !== this.view.phase) this.localError = null;
        this.view = signIn.value;
      }
      if (read.status === 'rejected') throw read.reason;
      if (signIn.status === 'rejected') throw signIn.reason;
      const saved = read.value;
      this.readError = null;
      this.accounts = saved.filter((account) => account.provider === 'github');
      this.accountsCurrent = true;
      this.changed(saved.filter((account) => account.provider === 'github' || account.provider === 'fountain'));
      this.notify();
    } catch (error) {
      if (captured === this.generation && capturedInteraction === this.interaction) {
        this.readError =
          typeof error === 'string' ? error : 'Could not read saved account status.';
        this.accountsCurrent = false;
        this.changed(undefined);
        this.notify();
      }
    } finally {
      this.reading = false;
    }
  }
  private async action(work: (captured: number) => Promise<void>) {
    if (this.busy || !this.ready) return;
    this.busy = true;
    this.interaction++;
    this.localError = null;
    this.notify();
    const captured = this.generation;
    try {
      await work(captured);
    } catch (error) {
      if (captured === this.generation)
        this.localError = typeof error === 'string' ? error : 'This account action could not finish. Try again.';
    } finally {
      this.busy = false;
      this.notify();
      void this.read();
    }
  }
  signIn() {
    void this.action(async (captured) => {
      const next = await this.core.invoke<SignIn>('github_sign_in');
      if (captured === this.generation && this.ready) this.view = next;
    });
  }
  cancel() {
    void this.action(async (captured) => {
      const next = await this.core.invoke<SignIn>('github_sign_in_cancel', {attempt: this.view.attempt});
      if (captured === this.generation && this.ready) this.view = next;
    });
  }
  open() {
    void this.action(async () => {
      await this.core.invoke('github_sign_in_open', {attempt: this.view.attempt});
    });
  }
  refreshLogin(connection: string) {
    void this.action(async (captured) => {
      const next = await this.core.invoke<SignIn>('github_refresh', {connection});
      if (captured === this.generation && this.ready) this.view = next;
    });
  }
  remove(connection: string) {
    void this.action(async () => {
      this.changed(this.accounts.filter((value) => value.connection !== connection));
      await this.core.invoke('credential_withdraw', {connection});
    });
  }
  async refresh(available: boolean, epoch: number) {
    this.started = true;
    this.ready = available;
    if (this.generation !== epoch) {
      this.generation = epoch;
      this.accounts = [];
      this.localError = null;
      this.readError = null;
      this.view = idle();
      this.accountsCurrent = false;
      this.changed(undefined);
    }
    if (!this.ready) {
      this.accountsCurrent = false;
      this.changed(undefined);
    }
    this.notify();
    await this.read();
  }
}

const accountState = {
  stored: 'Saved',
  expired: 'Expired',
  refresh_pending: 'Renewal unconfirmed',
  locked: 'Unlock your desktop keyring',
  withdrawn: 'Removed',
} as const;
const phaseStatus = (saved: boolean) =>
  ({
    idle: saved ? 'Your saved GitHub accounts.' : 'Sign in to access your GitHub repositories.',
    requesting: 'Requesting a GitHub sign-in code…',
    waiting: 'Approve sign-in on GitHub to continue.',
    verifying: 'Verifying your GitHub account…',
    saving: 'Saving your account…',
    complete: 'GitHub account saved.',
    cancelled: 'GitHub sign-in cancelled.',
    failed: 'GitHub sign-in could not finish. Try again.',
  }) as const;

/** Presentation of one GitHubAccounts controller; it sends nothing of its own. */
export class SodaDesktopGitHub extends ControllerView<GitHubAccounts> {
  protected render() {
    const c = this.controller;
    if (!c) return html``;
    const view = c.view,
      active = c.active;
    const prompt = c.ready && view.phase === 'waiting' ? view.prompt : null;
    const seconds = prompt ? Math.max(0, prompt.expires_at - Math.floor(Date.now() / 1000)) : 0;
    const status = !c.started
      ? 'Opening saved accounts…'
      : !c.ready
        ? 'Saved accounts are unavailable until Factory is ready.'
        : (c.readError ?? c.localError ?? view.error) || phaseStatus(c.accounts.length > 0)[view.phase];
    return html`<p id="github-status" role="status">${status}</p>
      <button id="github-sign-in" type="button" ?disabled=${!c.ready || c.busy || active} @click=${() => c.signIn()}>${c.accounts.length ? 'Add GitHub account' : 'Sign in to GitHub'}</button>
      <div id="github-prompt" ?hidden=${!prompt}>
        <p>Enter this code at <span>github.com/login/device</span>:</p>
        <code id="github-user-code" role="status" tabindex="0" aria-label="GitHub verification code">${prompt?.user_code ?? ''}</code>
        <p id="github-expiry" role="status">${prompt ? (seconds ? `Code expires in ${Math.ceil(seconds / 60)} minutes.` : 'This code has expired.') : ''}</p>
        <p>GitHub will ask for repository and profile access.</p>
        <button id="github-open" type="button" ?disabled=${c.busy || !prompt} @click=${() => c.open()}>Open GitHub</button>
      </div>
      <button id="github-cancel" type="button" ?hidden=${!active} ?disabled=${c.busy || view.phase === 'saving'} @click=${() => c.cancel()}>Cancel sign-in</button>
      <ul id="github-accounts" aria-label="Saved GitHub accounts">${repeat(
        c.accounts,
        (account) => account.connection,
        (account) => {
          const workflow =
            account.grant?.scope === undefined
              ? 'Workflow permission not recorded'
              : account.grant.scope.split(/[,\s]+/).includes('workflow')
                ? 'Workflow edits allowed by saved grant'
                : 'Sign in again to allow workflow edits';
          // Row order (name, refresh, remove) is what the native sign-in check drives.
          const refreshable = !c.busy && c.ready && c.accountsCurrent && !active && ['stored', 'expired'].includes(account.state);
          return html`<li data-connection=${account.connection}><span role="status"><strong>${account.label ?? account.account}</strong> · ${accountState[account.state]} · ${workflow}</span><button type="button" ?hidden=${!account.grant?.refresh_available} ?disabled=${!refreshable} @click=${() => c.refreshLogin(account.connection)}>Renew sign-in</button><button type="button" ?disabled=${c.busy || !c.ready || !c.accountsCurrent} @click=${() => c.remove(account.connection)}>Remove from Factory</button></li>`;
        }
      )}</ul>`;
  }
}
customElements.define('soda-desktop-github', SodaDesktopGitHub);
