import {LitElement, html} from 'lit';
import {check, object} from '../factory/api/models.js';
import type {Core} from './transport.js';
import dark from '../../assets/branding/icons/app/factory-dark.png';
import light from '../../assets/branding/icons/app/factory-light.png';

type Icon = 'dark' | 'light';
interface View {
  icon: Icon;
  available: boolean;
}
const choices: [Icon, string, string][] = [
  ['dark', 'Dark', dark],
  ['light', 'Light', light],
];

function admit(value: unknown): View {
  const view = object(value);
  check((view.icon === 'dark' || view.icon === 'light') && typeof view.available === 'boolean');
  return view as unknown as View;
}

/** The Dock icon picker. The app saves the choice in its profile and shows it
 * at once; this only reads and sends it. */
export class SodaDesktopAppIcon extends LitElement {
  static properties = {view: {state: true}, status: {state: true}};
  declare view: View | undefined;
  declare status: string;
  constructor(private readonly core: Pick<Core, 'invoke'>) {
    super();
    this.status = '';
    void this.send('app_icon_status');
  }
  protected createRenderRoot() {
    return this;
  }
  private async send(command: string, args?: Record<string, unknown>) {
    try {
      this.view = admit(await this.core.invoke(command, args));
      this.status = '';
    } catch {
      this.status = command === 'app_icon_status' ? 'The app icon setting could not be read.' : 'The app icon could not be changed.';
    }
  }
  protected render() {
    const view = this.view;
    if (!view) return html`<p role="status">${this.status}</p>`;
    if (!view.available) return html`<p>This system shows the icon it installed with Factory.</p>`;
    return html`<fieldset class="soda-desktop-app-icon">
        <legend>Shown in the Dock</legend>
        ${choices.map(
          ([icon, label, src]) => html`<label>
            <input type="radio" name="app-icon" .checked=${view.icon === icon} @change=${() => this.send('app_icon_select', {icon})} />
            <img src=${src} alt="" width="64" height="64" />
            ${label}
          </label>`
        )}
      </fieldset>
      <p role="status">${this.status}</p>`;
  }
}
customElements.define('soda-desktop-app-icon', SodaDesktopAppIcon);
