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
interface Core {
  invoke<T>(command: string, args?: unknown): Promise<T>;
}

/** Undefined means unobserved status; an empty list means observed account removal. */
export function mountAccounts(core: Core, changed: (accounts: readonly Account[] | undefined) => void = () => {}) {
  const status = document.querySelector<HTMLElement>('#github-status')!;
  const begin = document.querySelector<HTMLButtonElement>('#github-sign-in')!;
  const cancel = document.querySelector<HTMLButtonElement>('#github-cancel')!;
  const open = document.querySelector<HTMLButtonElement>('#github-open')!;
  const prompt = document.querySelector<HTMLElement>('#github-prompt')!;
  const code = document.querySelector<HTMLElement>('#github-user-code')!;
  const expiry = document.querySelector<HTMLElement>('#github-expiry')!;
  const list = document.querySelector<HTMLUListElement>('#github-accounts')!;
  const rows = new Map<string, {row: HTMLLIElement; name: HTMLSpanElement; refresh: HTMLButtonElement; remove: HTMLButtonElement}>();
  let generation = -1,
    ready = false,
    reading = false,
    busy = false;
  let view: SignIn = {attempt: 0, phase: 'idle', prompt: null, error: null};
  let accounts: Account[] = [];
  let accountsCurrent = false;
  let localError: string | null = null;
  let readError: string | null = null;
  let interaction = 0;

  function render() {
    const active = ['requesting', 'waiting', 'verifying', 'saving'].includes(view.phase);
    begin.disabled = !ready || busy || active;
    begin.textContent = accounts.length ? 'Add GitHub account' : 'Sign in to GitHub';
    cancel.hidden = !active;
    cancel.disabled = busy || view.phase === 'saving';
    prompt.hidden = !ready || view.phase !== 'waiting' || !view.prompt;
    open.disabled = busy || prompt.hidden;
    if (!prompt.hidden && view.prompt) {
      if (code.textContent !== view.prompt.user_code) code.textContent = view.prompt.user_code;
      const seconds = Math.max(0, view.prompt.expires_at - Math.floor(Date.now() / 1000));
      expiry.textContent = seconds ? `Code expires in ${Math.ceil(seconds / 60)} minutes.` : 'This code has expired.';
    } else code.textContent = '';
    if (!ready) status.textContent = 'Saved accounts are unavailable until Factory is ready.';
    else if (readError ?? localError ?? view.error) status.textContent = (readError ?? localError ?? view.error)!;
    else
      status.textContent = {
        idle: accounts.length ? 'Your saved GitHub accounts.' : 'Sign in to access your GitHub repositories.',
        requesting: 'Requesting a GitHub sign-in code…',
        waiting: 'Approve sign-in on GitHub to continue.',
        verifying: 'Verifying your GitHub account…',
        saving: 'Saving your account…',
        complete: 'GitHub account saved.',
        cancelled: 'GitHub sign-in cancelled.',
        failed: 'GitHub sign-in could not finish. Try again.',
      }[view.phase];
    const keep = new Set(accounts.map((account) => account.connection));
    for (const [connection, value] of rows)
      if (!keep.has(connection)) {
        value.row.remove();
        rows.delete(connection);
      }
    for (const account of accounts) {
      let value = rows.get(account.connection);
      if (!value) {
        const row = document.createElement('li'),
          name = document.createElement('span'),
          refresh = document.createElement('button'),
          remove = document.createElement('button');
        row.dataset.connection = account.connection;
        remove.type = 'button';
        refresh.type = 'button';
        refresh.textContent = 'Refresh login';
        refresh.addEventListener('click', () => void action(async () => {
          view = await core.invoke<SignIn>('github_refresh', {connection: account.connection});
        }));
        remove.textContent = 'Remove from Factory';
        remove.addEventListener(
          'click',
          () =>
            void action(async () => {
              changed(accounts.filter((value) => value.connection !== account.connection));
              await core.invoke('credential_withdraw', {connection: account.connection});
            })
        );
        row.append(name, refresh, remove);
        list.append(row);
        value = {row, name, refresh, remove};
        rows.set(account.connection, value);
      }
      const state = {
        stored: 'Saved',
        expired: 'Expired',
        refresh_pending: 'Refresh unconfirmed',
        locked: 'Unlock your desktop keyring',
        withdrawn: 'Removed',
      }[account.state];
      const workflow = account.grant?.scope === undefined
        ? 'Workflow permission not recorded'
        : account.grant.scope.split(/[,\s]+/).includes('workflow')
          ? 'Workflow edits allowed by saved grant'
          : 'Sign in again to allow workflow edits';
      value.name.textContent = `${account.label ?? account.account} · ${state} · ${workflow}`;
      value.remove.disabled = busy || !ready || !accountsCurrent;
      value.refresh.hidden = !account.grant?.refresh_available;
      value.refresh.disabled = busy || !ready || !accountsCurrent || active || !['stored','expired'].includes(account.state);
    }
  }
  async function read() {
    if (reading || !ready) return;
    reading = true;
    const captured = generation;
    const capturedInteraction = interaction;
    try {
      const [next, saved] = await Promise.all([
        core.invoke<SignIn>('github_sign_in_status'),
        core.invoke<Account[]>('credential_status'),
      ]);
      if (captured !== generation || capturedInteraction !== interaction || !ready) return;
      if (next.phase !== view.phase) localError = null;
      readError = null;
      view = next;
      accounts = saved.filter((account) => account.provider === 'github');
      accountsCurrent = true;
      changed(saved.filter((account) => account.provider === 'github' || account.provider === 'fountain'));
      render();
    } catch (error) {
      if (captured === generation && capturedInteraction === interaction) {
        readError =
          typeof error === 'string'
            ? error
            : 'Could not read saved account status. Refresh Factory status and try again.';
        accountsCurrent = false;
        changed(undefined);
        render();
      }
    } finally {
      reading = false;
    }
  }
  async function action(work: (captured: number) => Promise<void>) {
    if (busy || !ready) return;
    busy = true;
    interaction++;
    localError = null;
    render();
    const captured = generation;
    try {
      await work(captured);
    } catch (error) {
      if (captured === generation)
        localError = typeof error === 'string' ? error : 'This account action could not finish. Try again.';
    } finally {
      busy = false;
      render();
      void read();
    }
  }
  begin.addEventListener(
    'click',
    () =>
      void action(async (captured) => {
        const next = await core.invoke<SignIn>('github_sign_in');
        if (captured === generation && ready) view = next;
      })
  );
  cancel.addEventListener(
    'click',
    () =>
      void action(async (captured) => {
        const next = await core.invoke<SignIn>('github_sign_in_cancel', {attempt: view.attempt});
        if (captured === generation && ready) view = next;
      })
  );
  open.addEventListener(
    'click',
    () =>
      void action(async () => {
        await core.invoke('github_sign_in_open', {attempt: view.attempt});
      })
  );
  document.addEventListener('visibilitychange', () => {
    if (document.hidden) {
      interaction++;
      accountsCurrent = false;
      changed(undefined);
      render();
    }
  });
  return async (available: boolean, epoch: number) => {
    ready = available;
    if (generation !== epoch) {
      generation = epoch;
      accounts = [];
      localError = null;
      readError = null;
      view = {attempt: 0, phase: 'idle', prompt: null, error: null};
      accountsCurrent = false;
      changed(undefined);
    }
    if (!ready) {
      accountsCurrent = false;
      changed(undefined);
    }
    render();
    await read();
  };
}
