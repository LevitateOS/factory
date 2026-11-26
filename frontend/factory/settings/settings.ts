import {LitElement, html} from 'lit';
import {check, id} from '../api/models.js';
import type {FactoryTransport} from '../api/transport.js';
import {SodaFactoryRoleSettings} from '../work/role-settings.js';
import {SodaFactoryActivity} from '../work/activity.js';
import type {ActivityContext} from '../work/activity.js';
import {SodaFactoryRepair} from '../work/repair-funding.js';
import {admitHostSections, hostSectionAnchor, mountHostSections, renderHostSection} from '../app/host-section.js';
import type {FactoryHostSection, MountedHostSection} from '../app/host-section.js';
import {renderUnavailable, unavailableFeatures} from '../shared/unavailable.js';

export interface FactorySettingsContext {
  readonly transport: FactoryTransport;
  readonly repository: string;
  readonly actor?: string;
  readonly accountLabel?: string;
  readonly scope?: string;
  readonly repositoryLabel?: string;
  /** Host-only sections, placed after the shared role and automation settings. */
  readonly sections?: readonly FactoryHostSection[];
  /** A host sets a feature to false when it cannot read or save it; the page
   * then lists it as unavailable. The page sends no requests for these. */
  readonly repairFundingAvailable?: boolean;
  readonly activityAvailable?: boolean;
  /** Fresh authenticated command scope for the activity and repair saves. */
  readonly commandScope?: (signal: AbortSignal) => Promise<string>;
  readonly presentItem?: ActivityContext['presentItem'];
}

const prefix = 'settings';

/** Project settings: the shared role, limit, automation and workspace settings,
 * then any host sections. Each composed section keeps its own reads and saves. */
export class SodaFactorySettings extends LitElement {
  private readonly roles = new SodaFactoryRoleSettings();
  private readonly activity = new SodaFactoryActivity();
  private readonly repair = new SodaFactoryRepair();
  private current: FactorySettingsContext | undefined;
  private mounted: MountedHostSection[] = [];
  private lifetime = new AbortController();
  protected createRenderRoot() {
    return this;
  }
  set context(context: FactorySettingsContext | undefined) {
    if (context === this.current) return;
    if (context) {
      check(id(context.repository) && typeof context.transport.generation === 'string' && !!context.transport.generation);
      admitHostSections(context.sections);
    }
    this.lifetime.abort();
    this.lifetime = new AbortController();
    this.current = context;
    this.roles.context = context && {
      transport: context.transport,
      repository: context.repository,
      ...(context.actor === undefined ? {} : {actor: context.actor}),
      ...(context.accountLabel === undefined ? {} : {accountLabel: context.accountLabel}),
      ...(context.scope === undefined ? {} : {scope: context.scope}),
    };
    const {commandScope, presentItem} = context ?? {};
    this.activity.context = context?.activityAvailable
      ? {transport: context.transport, repository: context.repository, editable: true, ...(commandScope ? {commandScope} : {}), ...(presentItem ? {presentItem} : {})}
      : undefined;
    this.repair.context = context?.repairFundingAvailable
      ? {transport: context.transport, repository: context.repository, ...(commandScope ? {commandScope} : {})}
      : undefined;
    this.mounted = context ? mountHostSections(context.sections, this.lifetime.signal) : [];
    this.requestUpdate();
  }
  disconnectedCallback() {
    this.context = undefined;
    super.disconnectedCallback();
  }
  private jump(anchor: string) {
    this.querySelector<HTMLElement>('#' + anchor)?.scrollIntoView({block: 'start'});
  }
  protected render() {
    const context = this.current;
    if (!context) return html``;
    const unavailable = unavailableFeatures(context);
    const entries: [string, string][] = [
      [prefix + '-roles', 'Roles and automation'],
      ...(context.activityAvailable ? [[prefix + '-activity', 'Activity'] as [string, string]] : []),
      ...(context.repairFundingAvailable ? [[prefix + '-repair', 'Repair funding'] as [string, string]] : []),
      ...this.mounted.map((section): [string, string] => [hostSectionAnchor(prefix, section.id), section.title]),
      ...(unavailable.length ? [[prefix + '-unavailable', 'Unavailable in this app'] as [string, string]] : []),
    ];
    const project = context.repositoryLabel?.trim();
    return html`<section class="soda-factory-settings" aria-labelledby="settings-title">
      <header>
        <h2 id="settings-title">Settings</h2>
        ${project ? html`<p>${project}</p>` : ''}
      </header>
      <div class="soda-factory-settings-layout">
        <nav aria-label="Settings sections">
          ${entries.map(([anchor, label]) => html`<button type="button" @click=${() => this.jump(anchor)}>${label}</button>`)}
        </nav>
        <div class="soda-factory-sections">
          <div id=${prefix + '-roles'}>${this.roles}</div>
          ${context.activityAvailable ? html`<div id=${prefix + '-activity'}>${this.activity}</div>` : ''}
          ${context.repairFundingAvailable ? html`<div id=${prefix + '-repair'}>${this.repair}</div>` : ''}
          ${this.mounted.map((section) => renderHostSection(prefix, section))}
          ${renderUnavailable(prefix + '-unavailable', unavailable)}
        </div>
      </div>
    </section>`;
  }
}
customElements.define('soda-factory-settings', SodaFactorySettings);
