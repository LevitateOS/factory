import type {Core} from './transport.js';
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
export function mountMuseLogin(core: Core) {
  const status = document.querySelector<HTMLElement>('#muse-status')!;
  const begin = document.querySelector<HTMLButtonElement>('#muse-sign-in')!;
  const toolStatus = document.querySelector<HTMLElement>('#muse-tool-status')!;
  const checkUpdate = document.querySelector<HTMLButtonElement>('#muse-check-update')!;
  const cancel = document.querySelector<HTMLButtonElement>('#muse-cancel')!;
  const open = document.querySelector<HTMLButtonElement>('#muse-open')!;
  const prompt = document.querySelector<HTMLElement>('#muse-prompt')!;
  const code = document.querySelector<HTMLElement>('#muse-user-code')!;
  let epoch = -1, interaction = 0, ready = false, busy = false;
  let error: string | null = null;
  let view: View | undefined;
  function render() {
    const active = view?.phase === 'requesting' || view?.phase === 'waiting';
    begin.disabled = !ready || !view?.available || active || busy;
    const tool = view?.tools;
    checkUpdate.disabled = !ready || active || busy || !tool || !!view?.checking;
    const toolPhase = !ready ? 'Muse updates are unavailable until Factory is ready.'
      : !tool || tool.state === 'checking' ? 'Checking the current Muse release…'
      : tool.state === 'downloading' ? `Downloading Muse${tool.upstream_version ? ' ' + tool.upstream_version : ''}…`
      : tool.state === 'ready' ? 'Muse is verified for new work.'
      : tool.error ?? 'Muse could not be verified or updated. Check again when online.';
    toolStatus.textContent = toolPhase + (ready && tool ? [
      tool.upstream_version ? ` Latest: ${tool.upstream_version}.` : '',
      tool.selected_version ? ` Selected: ${tool.selected_version}.` : '',
      tool.checked_at_unix ? ` Last verified: ${new Date(tool.checked_at_unix * 1000).toLocaleString()}.` : '',
    ].join('') : '');
    cancel.hidden = !active;
    cancel.disabled = !ready || !view?.can_control || busy;
    prompt.hidden = !ready || view?.phase !== 'waiting' || !view.prompt;
    code.textContent = prompt.hidden ? '' : view!.prompt!.user_code;
    open.disabled = !ready || !view?.can_control || busy || prompt.hidden;
    status.textContent = !ready
      ? 'Muse sign-in is unavailable until Factory is ready.'
      : error ?? view?.error ?? (view && {
          idle: view.available ? 'Muse keeps its own saved login. Sign in here when needed.' : 'Muse sign-in is unavailable until its current release is verified.',
          requesting: 'Requesting a Muse sign-in code…',
          waiting: 'Confirm the matching code on Meta to sign in.',
          complete: 'Muse sign-in finished. Refresh agent roles to use this login.',
          cancelled: 'Muse sign-in stopped.',
          failed: 'Muse sign-in could not finish. Try again.',
        }[view.phase]) ?? 'Checking Muse sign-in…';
  }
  async function action(command: string, args?: unknown) {
    if (!ready || busy) return;
    const generation = epoch, captured = ++interaction;
    busy = true;
    error = null;
    render();
    try {
      const next = await core.invoke<View>(command, args);
      if (generation === epoch && captured === interaction && command !== 'muse_sign_in_open') view = next;
    } catch (failure) {
      if (generation === epoch && captured === interaction) error = typeof failure === 'string' ? failure : 'Muse sign-in is unavailable. Try again.';
    } finally {
      if (generation === epoch && captured === interaction) { busy = false; render(); }
    }
  }
  begin.addEventListener('click', () => void action('muse_sign_in'));
  checkUpdate.addEventListener('click', () => void action('muse_tools_refresh'));
  cancel.addEventListener('click', () => void action('muse_sign_in_cancel', {attempt: view?.attempt}));
  open.addEventListener('click', () => void action('muse_sign_in_open', {attempt: view?.attempt}));
  document.addEventListener('visibilitychange', () => {
    if (document.hidden) {
      interaction++;
      view = undefined;
      error = null;
      busy = false;
      render();
    }
  });
  return async (available: boolean, generation: number) => {
    if (epoch !== generation) {
      epoch = generation;
      interaction++;
      view = undefined;
      error = null;
      busy = false;
    }
    ready = available;
    render();
    if (!ready || busy) return;
    const captured = interaction;
    try {
      const next = await core.invoke<View>('muse_sign_in_status');
      if (generation !== epoch || captured !== interaction || !ready) return;
      view = next;
      render();
    } catch {
      if (generation === epoch && captured === interaction) {
        view = undefined;
        error = 'Muse sign-in status is unavailable. Refresh Factory and try again.';
        render();
      }
    }
  };
}
