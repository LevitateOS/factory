import {LitElement, html} from 'lit';
import {check} from '../api/models.js';
import type {FactoryTransport} from '../api/transport.js';
import {admitHostSections, mountHostSections, renderHostSection} from '../app/host-section.js';
import type {FactoryHostSection, MountedHostSection} from '../app/host-section.js';

export interface FactoryAccountsContext {
  /** Binds the page to the frame's current connection; the page sends nothing. */
  readonly transport: FactoryTransport;
  /** The host's sign-in sections. Their state and requests stay with the host. */
  readonly sections: readonly FactoryHostSection[];
}

const prefix = 'accounts';

/** Account sign-ins: a page of host sections. Sign-in, saved accounts and
 * provider credentials belong to the host, so the page only places them. */
export class SodaFactoryAccounts extends LitElement {
  private current: FactoryAccountsContext | undefined;
  private mounted: MountedHostSection[] = [];
  private lifetime = new AbortController();
  protected createRenderRoot() {
    return this;
  }
  set context(context: FactoryAccountsContext | undefined) {
    if (context === this.current) return;
    if (context) {
      check(typeof context.transport.generation === 'string' && !!context.transport.generation);
      admitHostSections(context.sections);
    }
    this.lifetime.abort();
    this.lifetime = new AbortController();
    this.current = context;
    this.mounted = context ? mountHostSections(context.sections, this.lifetime.signal) : [];
    this.requestUpdate();
  }
  disconnectedCallback() {
    this.context = undefined;
    super.disconnectedCallback();
  }
  protected render() {
    if (!this.current) return html``;
    return html`<section class="soda-factory-accounts" aria-labelledby="accounts-title">
      <header>
        <h2 id="accounts-title">Accounts</h2>
        <p>Each sign-in saves on its own.</p>
      </header>
      <div class="soda-factory-sections">${this.mounted.map((section) => renderHostSection(prefix, section))}</div>
    </section>`;
  }
}
customElements.define('soda-factory-accounts', SodaFactoryAccounts);
