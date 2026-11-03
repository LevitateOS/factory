import {html} from 'lit';
import {repeat} from 'lit/directives/repeat.js';
export const view = html`<input
  .value=${repeat(
    ['a'],
    (value) => value,
    (value) => html`<span>${value}</span>`
  )}
/>`;
