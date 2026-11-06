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
    const environment = document.createElement('button');
    const environmentStatus = document.createElement('p');
    environmentStatus.role = 'status';
    action.type = refresh.type = environment.type = 'button';
    refresh.textContent = 'Refresh controller';
    root.append(title, status, help, action, refresh, environmentStatus, environment);
    let placement: Placement | null = null, runtime = '', canSelect = false, busy = false, known = false;
    let environmentRevision = 0, environmentActive = false;
    const live = () => epoch === captured;
    const render = () => {
      action.textContent = placement?.state === 'active' ? 'Retire controller' :
        placement?.state === 'retiring' ? 'Finish retirement' : 'Use this computer';
      action.disabled = busy || !known || !canSelect ||
        !!placement && placement.state !== 'inactive' && placement.runtime !== runtime;
      refresh.disabled = busy;
      environment.textContent = environmentActive ? 'Withdraw workspace permission' : 'Allow isolated workspaces';
      environment.disabled = busy || !known || !canSelect || (!environmentActive && (placement?.state !== 'active' || placement.runtime !== runtime));
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
      const envResponse = await transport.request(`repositories/${repository}/factory/environment-grant`);
      check(envResponse.ok);
      const env = object(await envResponse.json()).environment;
      let revision = 0, active = false;
      if (env !== null) {
        const grant = object(env);
        check(grant.repository === repository && Number.isSafeInteger(grant.revision) && Number(grant.revision) > 0);
        revision = Number(grant.revision);
        if (grant.native) {
          const owner = object(grant.native);
          check(/^[a-f0-9]{32}$/.test(String(owner.runtime)) && id(owner.generation) && typeof grant.active === 'boolean');
          active = owner.runtime === data.runtime && grant.active;
        }
      }
      if (!live()) return;
      placement = next; runtime = data.runtime; canSelect = data.can_select; known = true;
      environmentRevision = revision; environmentActive = active;
      environmentStatus.textContent = active ? 'Isolated workspace permission is recorded. Execution still requires current policy, funding and worker admission.' : 'Isolated workspaces are not enabled.';
      status.textContent = !canSelect ? 'Controller changes require current repository administration.' :
        !placement || placement.state === 'inactive' ? 'No active controller.' :
        `${placement.runtime === runtime ? 'This computer' : 'Another computer'} · ${placement.state} · generation ${placement.generation}`;
    };
    const run = async (change: 'controller' | 'environment' | false) => {
      if (busy || !live()) return;
      busy = true; render();
      try {
        if (change === 'controller') {
          check(known && canSelect);
          const state = placement?.state === 'active' ? 'retiring' : placement?.state === 'retiring' ? 'inactive' : 'active';
          const response = await transport.request(path, {method: 'PUT', body: JSON.stringify({state, expected_revision: placement?.revision ?? ''})});
          check(response.ok);
        } else if (change === 'environment') {
          check(known && canSelect);
          const response = await transport.request(`repositories/${repository}/factory/environment-grant`, {
            method: 'PUT', body: JSON.stringify({native: true, active: !environmentActive, expected_revision: environmentRevision}),
          });
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
    action.onclick = () => void run('controller');
    environment.onclick = () => void run('environment');
    refresh.onclick = () => void run(false);
    void run(false);
  };
}
