import {html} from 'lit';
import type {TemplateResult} from 'lit';
import type {FeedCommunication} from './feed-response.js';
import type {FactoryRepositoryLinks} from '../app/navigation.js';
import {actionIcon} from './feed-actions.js';

// An agent's request for more allowance or for access (slice 5.4;
// pkg/factory/feed_request.go owns validation and the served state). The card
// says who asks, what, how much, why and what waits, and links to the
// setting. It has no grant control and sends nothing: the person changes the
// setting in Settings.

/** Names of the canonical settings (factory.Setting*) a request may name. */
const settingNames: Readonly<Record<string, string>> = {
  policy: 'Automation policy',
  operator_grant: 'Operator grant',
  capacity: 'Run capacity',
  sponsorships: 'Usage sponsorship',
  environment_grant: 'Environment grant',
};

const units: Readonly<Record<string, [string, string]>> = {
  runs: ['concurrent run', 'concurrent runs'],
  minutes: ['minute', 'minutes'],
};

export function renderFeedRequest(
  c: FeedCommunication,
  author: string | undefined,
  places: FactoryRepositoryLinks | undefined
): TemplateResult {
  const r = c.request!;
  const name = settingNames[r.setting] ?? r.setting;
  const state = r.state ?? 'open';
  const [one, many] = units[r.unit ?? ''] ?? [r.unit ?? '', r.unit ?? ''];
  const asked = r.kind === 'allowance' ? `${r.amount ?? 0} ${r.amount === 1 ? one : many} in total` : 'Access';
  const href = places?.settings?.(r.setting);
  return html`<div class="soda-feed-communication" data-feed-component="request" data-presentation="request" data-request-state=${state}>
    <p class="soda-feed-component-caption">
      ${actionIcon('blocker')}${author ?? 'An agent'} asks for ${r.kind === 'allowance' ? 'more allowance' : 'access'}${c.scope ? html`<span> · ${c.scope}</span>` : ''}
    </p>
    ${c.summary && c.summary !== c.title ? html`<p class="soda-feed-component-summary">${c.summary}</p>` : ''}
    <dl class="soda-feed-request">
      <div><dt>Setting</dt><dd>${name}</dd></div>
      <div><dt>Asking for</dt><dd>${asked}</dd></div>
      <div><dt>Why</dt><dd>${r.reason}</dd></div>
      ${r.blocks ? html`<div><dt>Waiting on it</dt><dd>${r.blocks}</dd></div>` : ''}
    </dl>
    ${
      state === 'met'
        ? html`<p class="soda-feed-request-state">${name} now meets this request.</p>`
        : state === 'replied'
          ? html`<p class="soda-feed-request-state">Someone replied, so this no longer waits for you.</p>`
          : ''
    }
    <p class="soda-feed-request-link">
      ${href ? html`<a href=${href}>Open ${name} in Settings</a>` : html`<strong>${name}</strong> · Change it in Settings.`}
      <span>Nothing is granted from this post.</span>
    </p>
  </div>`;
}
