import {html} from 'lit';

/** The Factory wordmark: the symbol (assets/branding/source/factory-symbol.svg)
 * and the name as text. The symbol's front plate follows the text colour, so
 * it is dark in the light scheme and white in the dark scheme. Styled by
 * `.soda-factory-wordmark` in styles/base.css. */
export function renderFactoryWordmark() {
  return html`<p class="soda-factory-wordmark">
    <svg viewBox="0 0 128 128" aria-hidden="true" focusable="false">
      <path class="soda-factory-mark-plate" fill-rule="evenodd" d="M40 0H128V88L88 128H0V40Z M22 83.33L48 66V82L74 64.67V110H22Z" />
      <path fill="currentColor" fill-rule="evenodd" d="M0 56L48 24V56L96 24V104L72 128H0Z M24 84L48 68V84L72 68V108H24Z" />
    </svg>Factory
  </p>`;
}
