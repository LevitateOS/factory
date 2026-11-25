import {LitElement, html} from 'lit';
import {check, id, object} from '../factory/api/models.js';
import type {FactoryTransport} from '../factory/api/transport.js';

interface Placement {
  runtime: string;
  generation: string;
  state: 'active' | 'retiring' | 'inactive';
  revision: string;
}
type Change = 'controller' | 'environment' | 'capacity' | 'permission' | false;

/** Execution controller state for one connection and repository. It reads once
 * when created and again after each explicit action; views only render it.
 * Controller selection does not enable policy, funding or automatic work. */
export class PlacementController {
  placement: Placement | null = null;
  runtime = '';
  canSelect = false;
  canConfigure = false;
  busy = false;
  known = false;
  environmentActive = false;
  operatorActive = false;
  runs = 1;
  /** Successful reads; a view resets its run-limit input when this changes. */
  reads = 0;
  status = '';
  limitStatus = '';
  permissionStatus = '';
  environmentStatus = '';
  private environmentRevision = 0;
  private capacityRevision = 0;
  private operatorRevision = 0;
  private retired = false;
  private readonly listeners = new Set<() => void>();
  private readonly path: string;
  private readonly first: Promise<void>;

  constructor(
    private readonly transport: FactoryTransport,
    private readonly repository: string
  ) {
    this.path = `repositories/${repository}/factory/placement`;
    this.first = this.run(false);
  }
  /** New project: the same steps as this computer's Execution controller
   * buttons, taken once because the person asked for agents to work here. True
   * when this computer controls the repository and both permissions are recorded. */
  async useThisComputer() {
    await this.first;
    const ours = () => this.placement?.state === 'active' && this.placement.runtime === this.runtime;
    if (this.known && this.canSelect && (!this.placement || this.placement.state === 'inactive')) await this.run('controller');
    if (this.known && this.canConfigure && !this.capacityRevision) await this.run('capacity', 1);
    if (this.known && this.canConfigure && !this.operatorActive && ours()) await this.run('permission');
    if (this.known && this.canConfigure && !this.environmentActive && ours()) await this.run('environment');
    return this.known && ours() && this.operatorActive && this.environmentActive;
  }
  retire() {
    this.retired = true;
    this.listeners.clear();
  }
  subscribe(listener: () => void) {
    if (!this.retired) this.listeners.add(listener);
    return () => this.listeners.delete(listener);
  }
  private changed() {
    for (const listener of this.listeners) listener();
  }
  private async read() {
    const {transport, repository} = this;
    const [response, envResponse, settingsResponse] = await Promise.all([
      transport.request(this.path),
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
        active = next?.state === 'active' && owner.runtime === data.runtime &&
          owner.runtime === next.runtime && owner.generation === next.generation && grant.active;
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
    if (this.retired) return;
    const placement = next;
    this.placement = placement; this.runtime = data.runtime; this.canSelect = data.can_select;
    this.canConfigure = data.can_configure_local === undefined ? this.canSelect : data.can_configure_local === true; this.known = true;
    this.environmentRevision = revision; this.environmentActive = active;
    this.capacityRevision = capacityRev; this.operatorRevision = operatorRev; this.operatorActive = permitted;
    this.runs = runs; this.reads++;
    this.limitStatus = capacityRev ? `Run limit: ${runs}. This limit is shared by repositories in this Factory installation.` : 'No run limit is saved. Choose and save a limit before running agents.';
    this.permissionStatus = permitted ? 'This repository may use local run capacity. Policy, funding and admitted workers are still required.' : 'This repository does not have local run permission.';
    this.environmentStatus = active ? 'Isolated workspace permission is recorded. Execution still requires current policy, funding and worker admission.' : 'Isolated workspaces are not enabled.';
    this.status = !this.canSelect ? (this.canConfigure ? 'Controller selection belongs to Fountain. Local workspace and capacity permissions remain separate.' : 'Local execution settings are unavailable for this connection.') :
      !placement || placement.state === 'inactive' ? 'No active controller.' :
      `${placement.runtime === this.runtime ? 'This computer' : 'Another computer'} · ${placement.state} · generation ${placement.generation}`;
  }
  /** `limit` is the run limit the acting view validated, used only for 'capacity'. */
  async run(change: Change, limit?: number) {
    if (this.busy || this.retired) return;
    this.busy = true; this.changed();
    const {transport, repository} = this;
    try {
      if (change === 'controller') {
        check(this.known && this.canSelect);
        const placement = this.placement;
        const state = placement?.state === 'active' ? 'retiring' : placement?.state === 'retiring' ? 'inactive' : 'active';
        const response = await transport.request(this.path, {method: 'PUT', body: JSON.stringify({state, expected_revision: placement?.revision ?? ''})});
        check(response.ok);
      } else if (change === 'capacity') {
        check(this.known && this.canConfigure);
        const response = await transport.request('/api/factory/capacity', {method: 'PUT',
          body: JSON.stringify({max_concurrent_runs: limit, expected_revision: this.capacityRevision})});
        check(response.ok);
      } else if (change === 'permission') {
        check(this.known && this.canConfigure);
        const response = await transport.request(`repositories/${repository}/factory/operator-grant`, {method: 'PUT',
          body: JSON.stringify({active: !this.operatorActive, expected_revision: this.operatorRevision})});
        check(response.ok);
      } else if (change === 'environment') {
        check(this.known && this.canConfigure);
        const response = await transport.request(`repositories/${repository}/factory/environment-grant`, {
          method: 'PUT', body: JSON.stringify({native: true, active: !this.environmentActive, expected_revision: this.environmentRevision}),
        });
        check(response.ok);
      }
      await this.read();
    } catch {
      if (!this.retired) {
        this.known = false;
        this.status = 'Execution settings could not be confirmed. Refresh before changing them. Existing work may still need retirement.';
      }
    } finally {
      if (!this.retired) { this.busy = false; this.changed(); }
    }
  }
}

/** Presentation of one PlacementController. Several views of the same
 * controller show the same state and send nothing of their own. */
export class SodaDesktopPlacement extends LitElement {
  static properties = {controller: {attribute: false}};
  declare controller: PlacementController | undefined;
  private unsubscribe: (() => void) | undefined;
  private appliedReads = -1;
  protected createRenderRoot() {
    return this;
  }
  connectedCallback() {
    super.connectedCallback();
    this.subscribe();
  }
  disconnectedCallback() {
    this.unsubscribe?.();
    this.unsubscribe = undefined;
    super.disconnectedCallback();
  }
  protected willUpdate(changed: Map<PropertyKey, unknown>) {
    if (!changed.has('controller')) return;
    this.appliedReads = -1;
    if (this.isConnected) this.subscribe();
  }
  private subscribe() {
    this.unsubscribe?.();
    this.unsubscribe = this.controller?.subscribe(() => this.requestUpdate());
  }
  protected updated() {
    const controller = this.controller, limit = this.querySelector('input');
    if (!controller || !limit || controller.reads === this.appliedReads) return;
    this.appliedReads = controller.reads;
    limit.value = controller.reads ? String(controller.runs) : '';
  }
  private saveLimit() {
    const limit = this.querySelector('input');
    if (!limit?.reportValidity()) return;
    void this.controller?.run('capacity', Number(limit.value));
  }
  protected render() {
    const c = this.controller;
    if (!c) return html``;
    const placement = c.placement, ours = placement?.state === 'active' && placement.runtime === c.runtime;
    const configurable = !c.busy && c.known && c.canConfigure;
    return html`<p role="status">${c.status}</p>
      <p>Select one computer for this repository. Other installations must be configured separately. Selecting a controller does not enable automation.</p>
      <button type="button" ?disabled=${c.busy || !c.known || !c.canSelect || (!!placement && placement.state !== 'inactive' && placement.runtime !== c.runtime)}
        @click=${() => void c.run('controller')}>${placement?.state === 'active' ? 'Retire controller' : placement?.state === 'retiring' ? 'Finish retirement' : 'Use this computer'}</button>
      <button type="button" ?disabled=${c.busy} @click=${() => void c.run(false)}>Refresh controller</button>
      <label>Maximum simultaneous agent runs <input type="number" min="1" max="64" step="1" required ?disabled=${!configurable} /></label>
      <button type="button" ?disabled=${!configurable} @click=${() => this.saveLimit()}>Save run limit</button>
      <p role="status">${c.limitStatus}</p>
      <p role="status">${c.permissionStatus}</p>
      <button type="button" ?disabled=${!configurable || (!c.operatorActive && !ours)}
        @click=${() => void c.run('permission')}>${c.operatorActive ? 'Withdraw repository run permission' : 'Allow this repository to use this computer'}</button>
      <p role="status">${c.environmentStatus}</p>
      <button type="button" ?disabled=${!configurable || (!c.environmentActive && !ours)}
        @click=${() => void c.run('environment')}>${c.environmentActive ? 'Withdraw workspace permission' : 'Allow isolated workspaces'}</button>`;
  }
}
customElements.define('soda-desktop-placement', SodaDesktopPlacement);

export function placementView(controller: PlacementController): SodaDesktopPlacement {
  const view = new SodaDesktopPlacement();
  view.controller = controller;
  return view;
}
