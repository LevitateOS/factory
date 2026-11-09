import {SodaFactoryApp} from '../factory/app/browser.js';
import {mountAccounts} from './accounts.js';
import {mountMuseLogin} from './muse-login.js';
import {mountFountainLogin} from './fountain-login.js';
import {mountConnections} from './connections.js';
import type {Core} from './transport.js';
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
const connections = mountConnections(core, app);
const refreshAccounts = mountAccounts(core, (accounts) => connections.accounts(accounts));
const refreshMuse = mountMuseLogin(core);
const refreshFountain = mountFountainLogin(core);
const status = document.querySelector<HTMLElement>('#runtime-status')!;
const retry = document.querySelector<HTMLButtonElement>('#retry')!;
let reading = false,
  epoch = -1;
async function refresh() {
  if (reading) return;
  reading = true;
  try {
    const value = await core.invoke<RuntimeStatus>('runtime_status');
    if (value.generation !== epoch) {
      epoch = value.generation;
      app.context = undefined;
    }
    status.textContent =
      value.phase === 'ready'
        ? value.execution_error ? 'Ready for browsing. Agent execution is unavailable.' : 'Ready'
        : value.phase === 'starting'
          ? 'Opening Factory…'
          : value.phase === 'stopping'
            ? 'Closing Factory…'
            : 'Factory could not start. Retry when the local runtime is available.';
    retry.hidden = value.phase !== 'failed' && !(value.phase === 'ready' && value.execution_error);
    retry.textContent = value.phase === 'ready' ? 'Retry execution' : 'Retry startup';
    connections.refresh(value.phase === 'ready', value.generation, value.profile);
    await Promise.all([refreshAccounts(value.phase === 'ready', value.generation), refreshMuse(value.phase === 'ready', value.generation), refreshFountain(value.phase === 'ready', value.generation)]);
  } catch {
    status.textContent = 'Factory is unavailable. Your saved work is kept.';
    app.context = undefined;
    connections.refresh(false, epoch, null);
    await Promise.all([refreshAccounts(false, epoch), refreshMuse(false, epoch)]);
  } finally {
    reading = false;
  }
}
for (const [id, command] of [
  ['refresh', 'runtime_status'],
  ['retry', 'runtime_retry'],
  ['new-window', 'open_window'],
  ['close', 'close_window'],
  ['quit', 'quit_factory'],
] as const) {
  document.querySelector('#' + id)!.addEventListener('click', () => {
    void core
      .invoke(command)
      .then(() => refresh())
      .catch(() => {
        status.textContent = 'This action could not finish. Your saved work is kept.';
      });
  });
}
document.addEventListener('visibilitychange', () => {
  if (!document.hidden) void refresh();
});
setInterval(() => {
  if (!document.hidden) void refresh();
}, 2000);
void refresh();
