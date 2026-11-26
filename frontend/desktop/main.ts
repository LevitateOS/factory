import {SodaFactoryApp} from '../factory/app/browser.js';
import {mountSignIn} from './sign-in.js';
import {mountConnections} from './connections.js';
import type {Core} from './transport.js';
import {mountDeveloper} from './developer.js';
interface RuntimeStatus {
  phase: 'starting' | 'ready' | 'failed' | 'stopping';
  profile: string | null;
  instance: string | null;
  generation: number;
  execution_error?: string | null;
}
const core = (window as unknown as {__TAURI__: {core: Core}}).__TAURI__.core;
const app = new SodaFactoryApp();
document.querySelector('#application')!.replaceChildren(app);
const panel = document.querySelector<HTMLElement>('#connection-panel')!;
const signIn = mountSignIn(document.querySelector<HTMLElement>('#sign-in')!, panel);
const connections = mountConnections(core, app, signIn.placement, panel);
const refreshSignIn = signIn.start(core, (accounts) => connections.accounts(accounts));
const status = document.querySelector<HTMLElement>('#runtime-status')!;
const retry = document.querySelector<HTMLButtonElement>('#retry')!;
let reading = false,
  epoch = -1;
async function refresh() {
  if (reading) return;
  reading = true;
  try {
    const value = await core.invoke<RuntimeStatus>('runtime_status');
    // connections.refresh retires the frame for a new generation or failure.
    epoch = value.generation;
    status.textContent =
      value.phase === 'ready'
        ? value.execution_error ? 'Ready for browsing. Agent execution is unavailable.' : ''
        : value.phase === 'starting'
          ? 'Opening Factory…'
          : value.phase === 'stopping'
            ? 'Closing Factory…'
            : 'Factory could not start. Retry when the local runtime is available.';
    retry.hidden = value.phase !== 'failed' && !(value.phase === 'ready' && value.execution_error);
    retry.textContent = value.phase === 'ready' ? 'Retry execution' : 'Retry startup';
    status.hidden = !status.textContent;
    connections.refresh(value.phase === 'ready', value.generation, value.profile);
    await refreshSignIn(value.phase === 'ready', value.generation);
  } catch {
    status.textContent = 'Factory is unavailable. Your saved work is kept.';
    status.hidden = false;
    connections.refresh(false, epoch, null);
    await refreshSignIn(false, epoch);
  } finally {
    reading = false;
  }
}
// New window, Close window and Quit live in the native menu.
retry.addEventListener('click', () => {
  void core
    .invoke('runtime_retry')
    .then(() => refresh())
    .catch(() => {
      status.textContent = 'This action could not finish. Your saved work is kept.';
      status.hidden = false;
    });
});
// On macOS the window has no title bar: the traffic lights sit over the page,
// and the empty top strip of the sidebar, content header or setup page moves the window.
if (/Mac/.test(navigator.platform)) {
  document.documentElement.dataset.shell = 'mac';
  const handles = '.soda-factory-sidebar, .soda-factory-content > header, .soda-factory-project, .soda-factory-project nav, #sign-in, soda-desktop-first-run';
  document.addEventListener('mousedown', (event) => {
    if (event.button !== 0 || event.clientY > 44 || !(event.target instanceof Element) || !event.target.matches(handles)) return;
    void core.invoke(event.detail === 2 ? 'plugin:window|toggle_maximize' : 'plugin:window|start_dragging').catch(() => {});
  });
}
// Local status only; a covered or minimized window keeps its connection current.
setInterval(() => void refresh(), 2000);
void refresh();
void mountDeveloper(core).catch(() => {});
