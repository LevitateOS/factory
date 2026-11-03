interface Account {
  connection: string;
  provider: string;
  origin: string;
  account: string;
  label?: string;
  state: 'stored' | 'expired' | 'refresh_pending' | 'locked' | 'withdrawn';
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

export function mountAccounts(core: Core) {
  const status = document.querySelector<HTMLElement>('#github-status')!;
  const begin = document.querySelector<HTMLButtonElement>('#github-sign-in')!;
  const cancel = document.querySelector<HTMLButtonElement>('#github-cancel')!;
  const open = document.querySelector<HTMLButtonElement>('#github-open')!;
  const prompt = document.querySelector<HTMLElement>('#github-prompt')!;
  const code = document.querySelector<HTMLElement>('#github-user-code')!;
  const expiry = document.querySelector<HTMLElement>('#github-expiry')!;
  const list = document.querySelector<HTMLUListElement>('#github-accounts')!;
  const rows = new Map<string, {row: HTMLLIElement; name: HTMLSpanElement; remove: HTMLButtonElement}>();
  let generation = -1,
    ready = false,
    reading = false,
    busy = false;
  let view: SignIn = {attempt: 0, phase: 'idle', prompt: null, error: null};
  let accounts: Account[] = [];
  let localError: string | null = null;
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
    else if (localError ?? view.error) status.textContent = (localError ?? view.error)!;
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
          remove = document.createElement('button');
        row.dataset.connection = account.connection;
        remove.type = 'button';
        remove.textContent = 'Remove from Factory';
        remove.addEventListener(
          'click',
          () =>
            void action(async () => {
              await core.invoke('credential_withdraw', {connection: account.connection});
            })
        );
        row.append(name, remove);
        list.append(row);
        value = {row, name, remove};
        rows.set(account.connection, value);
      }
      const state = {
        stored: 'Saved',
        expired: 'Sign in again',
        refresh_pending: 'Sign in again',
        locked: 'Unlock your desktop keyring',
        withdrawn: 'Removed',
      }[account.state];
      value.name.textContent = `${account.label ?? account.account} · ${state}`;
      value.remove.disabled = busy || !ready;
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
      view = next;
      accounts = saved.filter((account) => account.provider === 'github');
      render();
    } catch {
      if (captured === generation && capturedInteraction === interaction)
        status.textContent = 'Saved accounts are unavailable. Unlock your desktop keyring and refresh.';
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
  return async (available: boolean, epoch: number) => {
    ready = available;
    if (generation !== epoch) {
      generation = epoch;
      accounts = [];
      localError = null;
      view = {attempt: 0, phase: 'idle', prompt: null, error: null};
    }
    render();
    await read();
  };
}
