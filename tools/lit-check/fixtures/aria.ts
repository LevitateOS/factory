import {html} from 'lit';
export const view = (busy: boolean) => html`<div aria-busy=${String(busy)}></div>`;
