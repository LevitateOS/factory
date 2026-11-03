import {html} from 'lit';
import type {TemplateResult} from 'lit';
import type {FeedCommunication} from './feed-response.js';
import type {FeedArtifactContext} from './feed-artifact.js';
import {
  actionIcon,
  actionList,
  communicationAction,
  commitTrail,
  filledCommunication,
  renderFeedAction,
  sourceExcerpt,
} from './feed-actions.js';
import type {FactoryRepositoryLinks} from '../app/navigation.js';
import './feed-artifact.js';

/** One card's real links: hrefs come only from the repository's SodaOS places. */
export interface FeedCardLinks {
  places?: FactoryRepositoryLinks;
  /** The viewer's native user id: Open request shows only on their own requests. */
  viewer?: string;
  /** The entry's verified delivery id (the result card's event identity). */
  delivery?: string;
  /** Origin of a Ready app the viewer may open for that delivery. */
  appOrigin?: string;
}

// Controller-written Factory events carry their event kind (stored as
// component_id). Each has its presentation, its caption in the reader's words
// and its level in the thread. Worker reports are presented by their kind.
export interface FactoryEvent {
  readonly presentation: string;
  readonly caption: string;
  readonly level: 'post' | 'update' | 'reply';
}
const factoryEvents: Readonly<Record<string, FactoryEvent>> = {
  F01: {presentation: 'checks', caption: 'Preparation', level: 'reply'},
  F02: {presentation: 'progress', caption: 'Work started', level: 'update'},
  F03: {presentation: 'decision', caption: 'Why this work is next', level: 'reply'},
  F04: {presentation: 'decision', caption: 'JEV assessment', level: 'post'},
  F05: {presentation: 'result', caption: 'Work created', level: 'post'},
  F06: {presentation: 'result', caption: 'Merged', level: 'post'},
  F07: {presentation: 'progress', caption: 'Ready', level: 'reply'},
  F08: {presentation: 'progress', caption: 'Pause, stop and takeover', level: 'post'},
  F09: {presentation: 'blocker', caption: 'Run problem', level: 'post'},
  F10: {presentation: 'result', caption: 'App ready', level: 'post'},
  // Drafts are captioned as proposals below; recorded F11 posts are decisions.
  F11: {presentation: 'decision', caption: 'Decision', level: 'post'},
  F12: {presentation: 'blocker', caption: 'Waiting on allowance', level: 'post'},
};

export function factoryEvent(id: string | undefined): FactoryEvent | undefined {
  return id !== undefined && Object.hasOwn(factoryEvents, id) ? factoryEvents[id] : undefined;
}

const defaultPresentation: Record<FeedCommunication['kind'], string> = {
  ask: 'question',
  answer: 'question',
  reply: 'discussion',
  flag: 'finding',
  learned: 'knowledge',
  suggest: 'proposal',
  partial: 'progress',
  blocked: 'blocker',
  stuck: 'diagnosis',
  record_decision: 'decision',
  finish_incomplete: 'incomplete',
  system: 'brief',
  library_maintenance: 'maintenance',
  artifact: 'artifact',
  journey: 'journey',
  diagnosis: 'diagnosis',
  repair_draft: 'comparison',
};

function communicationCaption(c: FeedCommunication): string {
  if (c.intentSource?.state === 'draft') return 'Proposal (draft) · not accepted';
  return (
    factoryEvent(c.componentId)?.caption ?? defaultPresentation[c.kind].replace(/^./, (letter) => letter.toUpperCase())
  );
}

// A summary that is only a machine code (e.g. "start-unconfirmed") is the
// recorded cause, not prose: keep it, collapsed under Technical details.
const machineCode = /^[a-z0-9]+(?:[-_.:/][a-z0-9]+)+$/;
function componentSummary(summary: string): TemplateResult {
  if (!machineCode.test(summary.trim())) return html`<p class="soda-feed-component-summary">${summary}</p>`;
  return html`<details class="soda-feed-technical-details">
    <summary>Technical details</summary>
    <p>${summary}</p>
  </details>`;
}

export function renderFeedCommunication(
  communication: FeedCommunication,
  entryKey: string,
  artifacts?: FeedArtifactContext,
  links: FeedCardLinks = {}
): TemplateResult {
  const c = filledCommunication(communication);
  const {places} = links;
  const ownRequest =
    c.intentSource !== undefined && links.viewer !== undefined && c.intentSource.owner === links.viewer;
  const event = factoryEvent(c.componentId);
  const presentation = event?.presentation ?? defaultPresentation[c.kind];
  const action = communicationAction(c, presentation);
  const extraBalance =
    !['progress', 'incomplete', 'blocker', 'result', 'merge', 'brief'].includes(action) &&
    (c.completed.length > 0 || c.remaining.length > 0);
  return html`<div
    class="soda-feed-communication"
    data-feed-component=${(event && c.componentId) || c.kind}
    data-presentation=${presentation}
    data-communication=${c.kind}
  >
    <p class="soda-feed-component-caption">
      ${actionIcon(action)}${communicationCaption(c)}${c.scope && action !== 'knowledge' ? html`<span> · ${c.scope}</span>` : ''}
    </p>
    <h3>${c.title}</h3>
    ${['brief', 'knowledge', 'discussion', 'result'].includes(action) || c.summary === c.title ? '' : componentSummary(c.summary)}
    ${c.severity && action !== 'finding' ? html`<p class="soda-feed-severity" data-severity=${c.severity}>${c.severity === 'critical' ? 'Critical concern' : c.severity === 'warning' ? 'Attention needed' : 'For your information'}</p>` : ''}
    ${renderFeedAction(c, presentation, entryKey)}
    ${c.dispositionReason ? html`<p class="soda-feed-disposition-reason" data-post-resolution=${c.disposition ?? ''}>${c.dispositionReason}</p>` : ''}
    ${c.source && !['finding', 'correction', 'comparison', 'investigation'].includes(action) ? html`<div class="soda-feed-action">${sourceExcerpt(c.source)}</div>` : ''}
    ${c.commits?.length ? html`<div class="soda-feed-action" data-action="commits">${commitTrail(c.commits)}</div>` : ''}
    ${c.artifacts?.length ? html`<div class="soda-feed-artifacts">${c.artifacts.map((artifact) => html`<soda-feed-artifact .artifact=${artifact} .context=${artifacts}></soda-feed-artifact>`)}</div>` : ''}
    ${
      extraBalance
        ? html`<details class="soda-action-more">
            <summary>Work completed and remaining</summary>
            <div class="soda-action-balance">
              ${actionList('Completed', c.completed)}${actionList('Still needed', c.remaining)}
            </div>
          </details>`
        : ''
    }
    ${action !== 'diagnosis' ? actionList('Already tried', c.attempts) : ''}
    ${action !== 'question' ? actionList('Options', c.options) : ''}
    ${
      (action !== 'decision' || c.items.length > 0) && c.evidenceRefs.length
        ? html`<details class="soda-feed-evidence-refs">
            <summary>
              Evidence · ${c.evidenceRefs.length} ${c.evidenceRefs.length === 1 ? 'reference' : 'references'}
            </summary>
            <ul>
              ${c.evidenceRefs.map((ref) => html`<li><code>${ref}</code></li>`)}
            </ul>
          </details>`
        : ''
    }
    ${c.libraryItemId && places?.knowledge ? html`<p class="soda-feed-library-reference"><a href=${places.knowledge(c.libraryItemId)}>Open in Knowledge</a>${c.libraryRevision ? ` · revision ${c.libraryRevision}` : ''}</p>` : ''}
    ${
      c.intentSource
        ? html`<div class="soda-feed-source-reference">
            ${c.intentSource.state === 'draft' ? 'Proposed choice' : 'Recorded choice'} ·
            ${c.intentSource.state}${ownRequest && places ? html`<br /><a href=${places.request(c.intentSource.intent)}>Open request</a>` : ''}
            <details>
              <summary>Source revision</summary>
              <code>${c.intentSource.revision}</code
              >${c.intentSource.predecessor ? html`<p>Previous revision: <code>${c.intentSource.predecessor}</code></p>` : ''}
            </details>
          </div>`
        : ''
    }
    ${
      c.planSource
        ? html`<details class="soda-feed-source-reference">
            <summary>Recorded plan · generation ${c.planSource.generation ?? 0}</summary>
            <p>Plan: <code>${c.planSource.plan}</code></p>
            <p>Assignment: <code>${c.planSource.attemptRoot}</code></p>
            <p>Source commit: <code>${c.planSource.sourceCommit}</code></p>
            <p>Plan digest: <code>${c.planSource.digest}</code></p>
            <p>Recording this revision does not itself admit or implement the plan.</p>
          </details>`
        : ''
    }
    ${
      c.componentId === 'F10'
        ? html`<p class="soda-feed-app-links">
            ${links.appOrigin ? html`<a class="ui primary button" href=${links.appOrigin} target="_blank" rel="noopener" data-feed-open-app=${entryKey}>Open app ↗</a>` : ''}
            ${places?.app && links.delivery ? html`<a href=${places.app(links.delivery)}>App versions</a>` : ''}
          </p>`
        : ''
    }
  </div>`;
}
