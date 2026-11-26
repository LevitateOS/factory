import {html} from 'lit';
import type {TemplateResult} from 'lit';

export interface MoreLink {
  readonly label: string;
  readonly href: string;
}

/** Drops links that go to the same place, keeping the first label. */
export function distinctLinks(links: readonly MoreLink[]): MoreLink[] {
  const seen = new Set<string>();
  return links.filter((link) => !seen.has(link.href) && !!seen.add(link.href));
}

/** One ⋯ menu for a card's secondary destinations; nothing when there are none. */
export function renderMoreMenu(label: string, links: readonly MoreLink[]): TemplateResult | string {
  const items = distinctLinks(links);
  if (!items.length) return '';
  const close = (event: Event) => ((event.currentTarget as HTMLElement).closest('details')!.open = false);
  return html`<details class="soda-more-menu">
    <summary aria-label=${label} title="More">
      <svg viewBox="0 0 16 16" aria-hidden="true"><circle cx="3" cy="8" r="1.3" /><circle cx="8" cy="8" r="1.3" /><circle cx="13" cy="8" r="1.3" /></svg>
    </summary>
    <div role="menu" aria-label=${label}>
      ${items.map((link) => html`<a role="menuitem" href=${link.href} @click=${close}>${link.label}</a>`)}
    </div>
  </details>`;
}
