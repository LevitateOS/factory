import {html} from 'lit';
import type {TemplateResult} from 'lit';
import {feedRolePortraits, postRole, roleLabel} from './roles.js';

// roleChip shows a role named on a post (an ask's "Addressed to", a post's
// "Read first by"): its portrait and its name. Anything that is not a
// canonical post role renders nothing.
export function roleChip(value: unknown, assetBase = ''): TemplateResult | string {
  const role = postRole(value);
  if (!role) return '';
  const label = roleLabel(role) ?? role;
  const portrait = feedRolePortraits[label];
  return html`<span
    class="soda-role-chip"
    data-role=${role}
    style=${`--soda-role-chip-color: var(--soda-role-${role.replaceAll('_', '-')})`}
    ><span class="soda-role-chip-face" aria-hidden="true"
      >${portrait ? html`<img src=${assetBase + portrait} alt="" width="20" height="20" loading="lazy" />` : ''}</span
    >${label}</span
  >`;
}

/** The plain name of a role named on a post, or undefined. */
export function namedRoleLabel(value: unknown): string | undefined {
  const role = postRole(value);
  return role && roleLabel(role);
}
