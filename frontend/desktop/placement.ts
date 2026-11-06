import {check, id, object} from '../factory/api/models.js';
import type {FactoryTransport} from '../factory/api/transport.js';

interface Placement {
  runtime: string;
  generation: string;
  state: 'active' | 'retiring' | 'inactive';
  revision: string;
}

/** Controller selection does not enable policy, funding or automatic work. */
export function mountPlacement(root: HTMLElement) {
  let epoch = 0;
  return (transport?: FactoryTransport, repository?: string) => {
    const captured = ++epoch;
    root.replaceChildren();
    root.hidden = !transport || !repository;
    if (!transport || !repository) return;
    const path = `repositories/${repository}/factory/placement`;
    const title = document.createElement('h2');
    title.textContent = 'Execution controller';
    const status = document.createElement('p');
    status.role = 'status';
    const help = document.createElement('p');
    help.textContent = 'Select one computer for this repository. Other installations must be configured separately. Selecting a controller does not enable automation.';
    const action = document.createElement('button');
    const refresh = document.createElement('button');
    action.type = refresh.type = 'button';
    refresh.textContent = 'Refresh controller';
    root.append(title, status, help, action, refresh);
    let placement: Placement | null = null, runtime = '', canSelect = false, busy = false, known = false;
    const live = () => epoch === captured;
    const render = () => {
      action.textContent = placement?.state === 'active' ? 'Retire controller' :
        placement?.state === 'retiring' ? 'Finish retirement' : 'Use this computer';
      action.disabled = busy || !known || !canSelect ||
        !!placement && placement.state !== 'inactive' && placement.runtime !== runtime;
      refresh.disabled = busy;
    };
    const read = async () => {
      const response = await transport.request(path);
      check(response.ok);
      const data = object(await response.json());
      check(typeof data.runtime === 'string' && /^[a-f0-9]{32}$/.test(data.runtime) && typeof data.can_select === 'boolean');
      let next: Placement | null = null;
      if (data.placement !== null) {
        const p = object(data.placement);
        check(p.repository === repository && /^[a-f0-9]{32}$/.test(String(p.runtime)) && id(p.generation) &&
          ['active', 'retiring', 'inactive'].includes(String(p.state)) && /^[a-f0-9]{32}$/.test(String(p.revision)));
        next = p as unknown as Placement;
      }
      if (!live()) return;
      placement = next; runtime = data.runtime; canSelect = data.can_select; known = true;
      status.textContent = !canSelect ? 'Controller changes require current repository administration.' :
        !placement || placement.state === 'inactive' ? 'No active controller.' :
        `${placement.runtime === runtime ? 'This computer' : 'Another computer'} · ${placement.state} · generation ${placement.generation}`;
    };
    const run = async (change: boolean) => {
      if (busy || !live()) return;
      busy = true; render();
      try {
        if (change) {
          check(known && canSelect);
          const state = placement?.state === 'active' ? 'retiring' : placement?.state === 'retiring' ? 'inactive' : 'active';
          const response = await transport.request(path, {method: 'PUT', body: JSON.stringify({state, expected_revision: placement?.revision ?? ''})});
          check(response.ok);
        }
        await read();
      } catch {
        if (live()) {
          known = false;
          status.textContent = 'Controller state could not be confirmed. Refresh before changing it. Existing work may still need retirement.';
        }
      } finally { if (live()) { busy = false; render(); } }
    };
    action.onclick = () => void run(true);
    refresh.onclick = () => void run(false);
    void run(false);
  };
}
