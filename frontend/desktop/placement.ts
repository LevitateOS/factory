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
    const permission = document.createElement('button');
    const permissionStatus = document.createElement('p');
    permissionStatus.role = 'status';
    const limitLabel = document.createElement('label');
    limitLabel.textContent = 'Maximum simultaneous agent runs ';
    const limit = document.createElement('input');
    limit.type = 'number'; limit.min = '1'; limit.max = '64'; limit.step = '1'; limit.required = true;
    limitLabel.append(limit);
    const saveLimit = document.createElement('button');
    saveLimit.textContent = 'Save run limit';
    const limitStatus = document.createElement('p');
    limitStatus.role = 'status';
    action.type = refresh.type = environment.type = permission.type = saveLimit.type = 'button';
    refresh.textContent = 'Refresh controller';
    root.append(title, status, help, action, refresh, limitLabel, saveLimit, limitStatus,
      permissionStatus, permission, environmentStatus, environment);
    let placement: Placement | null = null, runtime = '', canSelect = false, busy = false, known = false;
    let environmentRevision = 0, environmentActive = false;
    let capacityRevision = 0, operatorRevision = 0, operatorActive = false;
    const live = () => epoch === captured;
    const render = () => {
      action.textContent = placement?.state === 'active' ? 'Retire controller' :
        placement?.state === 'retiring' ? 'Finish retirement' : 'Use this computer';
      action.disabled = busy || !known || !canSelect ||
        !!placement && placement.state !== 'inactive' && placement.runtime !== runtime;
      refresh.disabled = busy;
      limit.disabled = saveLimit.disabled = busy || !known || !canSelect;
      permission.textContent = operatorActive ? 'Withdraw repository run permission' : 'Allow this repository to use this computer';
      permission.disabled = busy || !known || !canSelect || (!operatorActive && (placement?.state !== 'active' || placement.runtime !== runtime));
      environment.textContent = environmentActive ? 'Withdraw workspace permission' : 'Allow isolated workspaces';
      environment.disabled = busy || !known || !canSelect || (!environmentActive && (placement?.state !== 'active' || placement.runtime !== runtime));
    };
    const read = async () => {
      const [response, envResponse, settingsResponse] = await Promise.all([
        transport.request(path),
        transport.request(`repositories/${repository}/factory/environment-grant`),
        transport.request(`repositories/${repository}/factory`),
      ]);
      check(response.ok && envResponse.ok && settingsResponse.ok);
      const data = object(await response.json());
      check(typeof data.runtime === 'string' && /^[a-f0-9]{32}$/.test(data.runtime) && typeof data.can_select === 'boolean');
      let next: Placement | null = null;
      if (data.placement !== null) {
        const p = object(data.placement);
        check(p.repository === repository && /^[a-f0-9]{32}$/.test(String(p.runtime)) && id(p.generation) &&
          ['active', 'retiring', 'inactive'].includes(String(p.state)) && /^[a-f0-9]{32}$/.test(String(p.revision)));
        next = p as unknown as Placement;
      }
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
      const settings = object(await settingsResponse.json());
      check(settings.repository === repository);
      let capacityRev = 0, runs = 1, operatorRev = 0, permitted = false;
      if (settings.capacity !== null) {
        const capacity = object(settings.capacity);
        check(Number.isSafeInteger(capacity.revision) && Number(capacity.revision) > 0 &&
          Number.isSafeInteger(capacity.max_concurrent_runs) && Number(capacity.max_concurrent_runs) >= 1 && Number(capacity.max_concurrent_runs) <= 64);
        capacityRev = Number(capacity.revision); runs = Number(capacity.max_concurrent_runs);
      }
      if (settings.operator_grant !== null) {
        const operator = object(settings.operator_grant);
        check(operator.repository === repository && Number.isSafeInteger(operator.revision) && Number(operator.revision) > 0 && typeof operator.active === 'boolean');
        operatorRev = Number(operator.revision); permitted = operator.active;
      }
      if (!live()) return;
      placement = next; runtime = data.runtime; canSelect = data.can_select; known = true;
      environmentRevision = revision; environmentActive = active;
      capacityRevision = capacityRev; operatorRevision = operatorRev; operatorActive = permitted;
      limit.value = String(runs);
      limitStatus.textContent = capacityRev ? `Run limit: ${runs}. This limit is shared by repositories in this Factory installation.` : 'No run limit is saved. Choose and save a limit before running agents.';
      permissionStatus.textContent = permitted ? 'This repository may use local run capacity. Policy, funding and admitted workers are still required.' : 'This repository does not have local run permission.';
      environmentStatus.textContent = active ? 'Isolated workspace permission is recorded. Execution still requires current policy, funding and worker admission.' : 'Isolated workspaces are not enabled.';
      status.textContent = !canSelect ? 'Local execution settings are unavailable for this connection.' :
        !placement || placement.state === 'inactive' ? 'No active controller.' :
        `${placement.runtime === runtime ? 'This computer' : 'Another computer'} · ${placement.state} · generation ${placement.generation}`;
    };
    const run = async (change: 'controller' | 'environment' | 'capacity' | 'permission' | false) => {
      if (busy || !live()) return;
      if (change === 'capacity' && !limit.reportValidity()) return;
      busy = true; render();
      try {
        if (change === 'controller') {
          check(known && canSelect);
          const state = placement?.state === 'active' ? 'retiring' : placement?.state === 'retiring' ? 'inactive' : 'active';
          const response = await transport.request(path, {method: 'PUT', body: JSON.stringify({state, expected_revision: placement?.revision ?? ''})});
          check(response.ok);
        } else if (change === 'capacity') {
          check(known && canSelect);
          const response = await transport.request('/api/factory/capacity', {method: 'PUT',
            body: JSON.stringify({max_concurrent_runs: Number(limit.value), expected_revision: capacityRevision})});
          check(response.ok);
        } else if (change === 'permission') {
          check(known && canSelect);
          const response = await transport.request(`repositories/${repository}/factory/operator-grant`, {method: 'PUT',
            body: JSON.stringify({active: !operatorActive, expected_revision: operatorRevision})});
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
          status.textContent = 'Execution settings could not be confirmed. Refresh before changing them. Existing work may still need retirement.';
        }
      } finally { if (live()) { busy = false; render(); } }
    };
    action.onclick = () => void run('controller');
    environment.onclick = () => void run('environment');
    saveLimit.onclick = () => void run('capacity');
    permission.onclick = () => void run('permission');
    refresh.onclick = () => void run(false);
    void run(false);
  };
}
