import {LitElement, html} from 'lit';
import type {TemplateResult} from 'lit';
import {repeat} from 'lit/directives/repeat.js';
function action(label: string, disabled: boolean, onClick: (event: MouseEvent) => void): TemplateResult {
  return html`<button ?disabled=${disabled} @click=${onClick}>${label}</button>`;
}
export class CheckedControl extends LitElement {
  static properties = {count: {type: Number}, active: {type: Boolean}};
  declare count: number;
  declare active: boolean;
  protected render() {
    return html`<input .value=${String(this.count)} ?disabled=${!this.active} /> ${action('Run', false, () => {})}
      ${repeat(
        ['a', 'b'],
        (value) => value,
        (value) => html`<span>${value}</span>`
      )}`;
  }
}
customElements.define('checked-control', CheckedControl);
export const usage = html`<checked-control .count=${2} .active=${true}></checked-control>
  <details @toggle=${(_event: Event) => {}}>
    <summary>Details</summary>
    Content
  </details>`;
