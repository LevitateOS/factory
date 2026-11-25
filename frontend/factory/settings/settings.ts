import {LitElement, html} from 'lit';
import {check, id} from '../api/models.js';
import type {FactoryTransport} from '../api/transport.js';
import {SodaFactoryRoleSettings} from '../work/role-settings.js';
import {admitHostSections, hostSectionAnchor, mountHostSections, renderHostSection} from '../app/host-section.js';
import type {FactoryHostSection, MountedHostSection} from '../app/host-section.js';

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
}

const unavailableFeatures = [
  ['repairFundingAvailable', 'Repository repair funding', 'No repair grant is assumed.'],
  ['activityAvailable', 'Activity visibility', 'Factory activity is unavailable.'],
] as const;
const prefix = 'settings';

/** Project settings: the shared role, limit, automation and workspace settings,
 * then any host sections. Each composed section keeps its own reads and saves. */
export class SodaFactorySettings extends LitElement {
  private readonly roles = new SodaFactoryRoleSettings();
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
    const unavailable = unavailableFeatures.filter(([key]) => context[key] === false);
    const entries: [string, string][] = [
      [prefix + '-roles', 'Roles and automation'],
      ...this.mounted.map((section): [string, string] => [hostSectionAnchor(prefix, section.id), section.title]),
      ...(unavailable.length ? [[prefix + '-unavailable', 'Unavailable in this app'] as [string, string]] : []),
    ];
    const project = context.repositoryLabel?.trim();
    return html`<section class="soda-factory-settings" aria-labelledby="settings-title">
      <header>
        <h2 id="settings-title">Settings</h2>
        <p>${project ? project + ' · ' : ''}Each section saves on its own.</p>
      </header>
      <div class="soda-factory-settings-layout">
        <nav aria-label="Settings sections">
          ${entries.map(([anchor, label]) => html`<button type="button" @click=${() => this.jump(anchor)}>${label}</button>`)}
        </nav>
        <div class="soda-factory-settings-sections">
          <div id=${prefix + '-roles'}>${this.roles}</div>
          ${this.mounted.map((section) => renderHostSection(prefix, section))}
          ${unavailable.length
            ? html`<section id=${prefix + '-unavailable'} class="soda-factory-settings-unavailable"
                aria-labelledby=${prefix + '-unavailable-title'}>
                <h3 id=${prefix + '-unavailable-title'}>Unavailable in this app</h3>
                <p>These settings exist in Factory, but this app cannot read or save them yet. Nothing here is assumed to be on.</p>
                <dl>
                  ${unavailable.map(([, title, detail]) => html`<div><dt>${title}</dt><dd>${detail}</dd></div>`)}
                </dl>
              </section>`
            : ''}
        </div>
      </div>
    </section>`;
  }
}
customElements.define('soda-factory-settings', SodaFactorySettings);
