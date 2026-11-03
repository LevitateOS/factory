import {html} from 'lit';
import type {TemplateResult} from 'lit';
import type {FeedCommunication, FeedGraphItem, FeedSourceExcerpt} from './feed-response.js';
import {renderFeedGraph} from './feed-graph.js';

// The communication lists every action reads, present even when the server
// omitted an empty one.
export type FilledCommunication = FeedCommunication &
  Required<
    Pick<
      FeedCommunication,
      'evidenceRefs' | 'options' | 'completed' | 'remaining' | 'attempts' | 'alternatives' | 'items'
    >
  >;

export function filledCommunication(c: FeedCommunication): FilledCommunication {
  return {
    ...c,
    evidenceRefs: c.evidenceRefs ?? [],
    options: c.options ?? [],
    completed: c.completed ?? [],
    remaining: c.remaining ?? [],
    attempts: c.attempts ?? [],
    alternatives: c.alternatives ?? [],
    items: c.items ?? [],
  };
}

// These compositions explain actions using supplied facts. Icons identify the
// action; only an explicitly recorded state supplies a result or a checkmark.
export function actionIcon(action: string): TemplateResult {
  const paths: Record<string, string> = {
    investigation: 'M14 14l6 6M16 9a7 7 0 1 1-14 0 7 7 0 0 1 14 0',
    decision: 'M3 5h5v5H3zM3 14h5v5H3zM8 7h4v5h5m-9 5h4v-5m3-3 4 3-4 3',
    graph: 'M9 2h6v5H9zM2 17h6v5H2zM16 17h6v5h-6zM12 7v5H5v5m7-5h7v5',
    finding: 'M12 3 2 21h20L12 3zm0 6v5m0 3v1',
    correction: 'm4 15 5 5L20 5M3 4h8M3 8h5',
    checks: 'm3 6 2 2 4-4m3 2h9M3 15l2 2 4-4m3 2h9',
    progress: 'M3 5h18M3 12h12M3 19h7M18 10l3 2-3 2',
    blocker: 'M5 11V7a7 7 0 0 1 14 0v4M3 11h18v11H3zm9 4v3',
    question: 'M8 7a4 4 0 1 1 6 3.5c-2 1-2 2-2 3M12 18v1M3 2h18v19H3z',
    knowledge: 'M12 5C8 2 4 3 2 4v16c3-2 6-2 10 0 4-2 7-2 10 0V4c-2-1-6-2-10 1zm0 0v15',
    merge: 'M6 2v20M6 6c12 0 12 4 12 8 0 4-6 4-12 4m8-3-4 3 4 3',
    result: 'M3 4h18v15H3zm5 5 4 3 4-3m-4 3v9',
    comparison: 'M2 3h8v18H2zM14 3h8v18h-8zM6 8v8m12-8v8',
    diagnosis: 'M3 4h6v5H3zM15 15h6v5h-6zM6 9v8h5m-2-3 3 3-3 3M15 4h6M18 1v6',
    journey: 'M4 4h6v6H4zM14 14h6v6h-6zM10 7h7v7m-3-3 3 3 3-3',
  };
  return html`<svg
    class="soda-action-icon"
    viewBox="0 0 24 24"
    aria-hidden="true"
    fill="none"
    stroke="currentColor"
    stroke-width="1.5"
    stroke-linecap="round"
    stroke-linejoin="round"
  >
    <path d=${paths[action] ?? 'M4 2h12l4 4v16H4zm12 0v5h4M8 11h8M8 16h6'} />
  </svg>`;
}

export function feedItemTone(state: string | undefined): string {
  if (!state) return 'pending';
  if (
    /fail|blocked|stuck|missing|incomplete|stale|reject|disput|conflict|unknown|not\b|unverified|waiting/i.test(state)
  )
    return 'attention';
  if (/pass|verified|complete|resolved|accepted|done|succeed|finished/i.test(state)) return 'positive';
  return 'pending';
}

export function actionState(state?: string, reported = true): TemplateResult | string {
  if (!state) return '';
  const tone = feedItemTone(state);
  return html`<span class="soda-action-state" data-tone=${tone}
    ><span aria-hidden="true">${tone === 'positive' ? '✓' : tone === 'attention' ? '!' : '○'}</span
    >${reported ? 'Reported: ' : ''}${state}</span
  >`;
}

export function actionList(title: string, values: readonly string[], className = ''): TemplateResult | string {
  if (!values.length) return '';
  return html`<section class=${`soda-action-list ${className}`}>
    <h4>${title}<span class="soda-action-count">${values.length}</span></h4>
    <ul>
      ${values.slice(0, 4).map((value) => html`<li>${value}</li>`)}
    </ul>
    ${
      values.length > 4
        ? html`<details>
            <summary>${values.length - 4} more</summary>
            <ul>
              ${values.slice(4).map((value) => html`<li>${value}</li>`)}
            </ul>
          </details>`
        : ''
    }
  </section>`;
}

export function actionItems(items: readonly FeedGraphItem[], mode = 'tasks', reported = true): TemplateResult | string {
  if (!items.length) return '';
  const render = (rows: readonly FeedGraphItem[], offset = 0) =>
    html`<ol class="soda-action-items" data-mode=${mode}>
      ${rows.map(
        (item, index) => html`<li>
          <span class="soda-action-item-mark" aria-hidden="true"
            >${mode === 'files' ? actionIcon('investigation') : mode === 'checks' ? actionIcon('checks') : String(index + offset + 1).padStart(2, '0')}</span
          >
          <div>
            <strong class=${mode === 'files' ? 'soda-action-path' : ''}>${item.label}</strong
            >${item.detail ? html`<p>${item.detail}</p>` : ''}${actionState(item.state, reported)}
          </div>
        </li>`
      )}
    </ol>`;
  return html`${render(items.slice(0, 4))}${
    items.length > 4
      ? html`<details class="soda-action-more">
          <summary>${items.length - 4} more ${mode === 'files' ? 'sources' : 'items'}</summary>
          ${render(items.slice(4), 4)}
        </details>`
      : ''
  }`;
}

export function actionComparison(
  before: string | undefined,
  after: string | undefined,
  labels: readonly [string, string] = ['Before', 'After']
): TemplateResult | string {
  if (before === undefined && after === undefined) return '';
  return html`<div class="soda-action-comparison">
    <section data-side="before">
      <h4>${labels[0]}</h4>
      <p>${before ?? 'Not supplied'}</p>
    </section>
    <span class="soda-action-arrow" aria-hidden="true">→</span>
    <section data-side="after">
      <h4>${labels[1]}</h4>
      <p>${after ?? 'Not supplied'}</p>
    </section>
  </div>`;
}

export function sourceExcerpt(source: FeedSourceExcerpt): TemplateResult {
  const lines = source.code.split('\n');
  const excerpt = (code: string, corrected: boolean) =>
    html`<pre
      tabindex="0"
      aria-label=${corrected ? 'Changed source excerpt' : 'Source excerpt'}
    ><code>${code.split('\n').map((line, index) => html`<span class="soda-source-line" data-change=${corrected ? (!lines.includes(line) ? 'added' : '') : (source.highlightLines ?? []).includes(source.startLine + index) ? 'flagged' : ''}><span class="soda-source-number" aria-hidden="true">${source.startLine + index}</span><span>${line || ' '}</span></span>`)}</code></pre>`;
  return html`<section class="soda-action-source">
    <header>
      <span>${actionIcon('investigation')}<strong>${source.path}</strong></span
      >${source.revision ? html`<code title=${source.revision}>${source.revision.slice(0, 12)}</code>` : ''}
    </header>
    <div class="soda-source-panes">
      ${
        source.after !== undefined
          ? html`<section>
                <h4>Before</h4>
                ${excerpt(source.code, false)}
              </section>
              <section>
                <h4>Changed excerpt</h4>
                ${excerpt(source.after, true)}
              </section>`
          : excerpt(source.code, false)
      }
    </div>
    <p class="soda-source-attribution">Source supplied with this report</p>
  </section>`;
}

function workBalance(c: FilledCommunication): TemplateResult | string {
  if (!c.completed.length && !c.remaining.length) return '';
  return html`<div class="soda-action-balance">
    ${actionList('Completed', c.completed, 'soda-action-done')}${actionList(c.kind === 'finish_incomplete' ? 'Handing over' : 'Still needed', c.remaining, 'soda-action-pending')}
  </div>`;
}

function nextStep(c: FilledCommunication, label = 'Next step'): TemplateResult | string {
  return c.resolution
    ? html`<div class="soda-action-next">
        <span aria-hidden="true">↳</span>
        <div>
          <h4>${label}</h4>
          <p>${c.resolution}</p>
        </div>
      </div>`
    : '';
}

function reason(c: FilledCommunication, label = 'Why'): TemplateResult | string {
  return c.rationale
    ? html`<div class="soda-action-reason">
        <h4>${label}</h4>
        <p>${c.rationale}</p>
      </div>`
    : '';
}

function decision(c: FilledCommunication): TemplateResult {
  const jev = c.componentId === 'F03' || c.componentId === 'F04';
  // A draft build request is a proposal, not a recorded decision.
  const draft = c.intentSource?.state === 'draft';
  return html`<div class="soda-action-decision">
    ${
      c.items.length
        ? html`<section class="soda-decision-inputs">
            <h4>Considered</h4>
            ${actionItems(c.items, 'options', c.kind !== 'system')}
          </section>`
        : c.evidenceRefs.length
          ? html`<section class="soda-decision-inputs">${actionList('Evidence considered', c.evidenceRefs)}</section>`
          : ''
    }
    <div class="soda-decision-route">
      <span aria-hidden="true">↓</span><strong>${jev ? 'JEV' : draft ? 'Proposal' : 'Decision'}</strong
      ><span aria-hidden="true">↓</span>
    </div>
    <section class="soda-decision-choice">
      <h4>${c.kind === 'suggest' || draft ? 'Proposed choice' : 'Recorded choice'}</h4>
      <p>${c.decision ?? 'No choice recorded'}</p>
      ${reason(c)}
    </section>
    ${
      c.alternatives.length
        ? html`<details class="soda-action-more">
            <summary>Other options considered · ${c.alternatives.length}</summary>
            ${actionList('Alternatives', c.alternatives)}
          </details>`
        : ''
    }
    ${nextStep(c)}
  </div>`;
}

function question(c: FilledCommunication, entryKey: string): TemplateResult {
  const ask = c.kind === 'ask';
  return html`<section class="soda-action-question">
    <div class="soda-question-mark" aria-hidden="true">?</div>
    <div>
      <h4>
        ${ask ? (c.questionState === 'answered' ? 'Answer recorded' : c.questionState === 'replied' ? 'Reply received' : 'Your input is needed') : 'Question and answer'}
      </h4>
      <p>${c.question ?? c.title}</p>
      ${
        ask && c.options.length
          ? html`<div class="soda-feed-answer-options" aria-label="Suggested answers">
                ${c.options.map((option, index) => html`<button type="button" data-feed-answer=${entryKey} data-feed-answer-text=${option}><span class="soda-question-option" aria-hidden="true">${index + 1}</span>${option}</button>`)}
              </div>
              <p class="soda-feed-block-note">Choose an option to start a reply, or write your own below.</p>`
          : ''
      }
      ${nextStep(c, ask && c.questionState !== 'answered' ? 'What this affects' : 'Answer')}${reason(c)}
    </div>
  </section>`;
}

// An ordinary question reads as conversation: the question once, any
// suggested answers, and what it affects. The card puts the reply box under it.
export function questionPrompt(communication: FeedCommunication, entryKey: string): TemplateResult {
  const c = filledCommunication(communication);
  // Each distinct line once: a summary or question that repeats the title is dropped.
  const lines = [...new Set([c.summary, c.question].filter((line): line is string => !!line && line !== c.title))];
  return html`<div class="soda-feed-question">
    <h3 class="soda-feed-question-text">${c.title}</h3>
    ${lines.map((line) => html`<p>${line}</p>`)}
    ${
      c.options.length
        ? html`<div class="soda-feed-answer-options" aria-label="Suggested answers">
            ${c.options.map((option) => html`<button type="button" data-feed-answer=${entryKey} data-feed-answer-text=${option}>${option}</button>`)}
          </div>`
        : ''
    }
    ${nextStep(c, 'What this affects')}${reason(c)}
  </div>`;
}

function finding(c: FilledCommunication): TemplateResult {
  return html`<div class="soda-action-finding">
    ${c.source ? sourceExcerpt(c.source) : ''}
    <section class="soda-finding-observation">
      <span class="soda-finding-mark" aria-hidden="true">!</span>
      <div>
        <h4>${c.severity === 'critical' ? 'Critical finding' : 'Finding'}</h4>
        ${actionComparison(c.before, c.after, ['Expected', 'Observed'])}${reason(c, 'Why it matters')}${actionItems(c.items, 'findings')}
      </div>
    </section>
    ${nextStep(c, 'Suggested correction')}
  </div>`;
}

export function communicationAction(c: FeedCommunication, presentation: string): string {
  if (c.componentId === 'F06') return 'merge';
  if (c.componentId === 'F07') return 'unlock';
  if (c.source?.after !== undefined) return 'correction';
  if (presentation === 'evidence') return 'investigation';
  return presentation;
}

export function commitTrail(commits: NonNullable<FeedCommunication['commits']>): TemplateResult {
  return html`<section class="soda-action-checkpoint">
    <header>
      ${actionIcon('merge')}
      <h4>Saved changes</h4>
      <span>${commits.length} commits</span>
    </header>
    <ul class="soda-action-commits">
      ${commits.map((commit) => html`<li><code title=${commit.revision}>${commit.revision.slice(0, 8)}</code><span>${commit.summary}</span></li>`)}
    </ul>
    <p class="soda-feed-block-note">Recorded commits · review and checks remain separate.</p>
  </section>`;
}

export function renderFeedAction(c: FilledCommunication, presentation: string, entryKey: string): TemplateResult {
  const action = communicationAction(c, presentation);
  let body: TemplateResult | string;
  switch (action) {
    case 'question':
      body = question(c, entryKey);
      break;
    case 'decision':
      body = decision(c);
      break;
    case 'finding':
      body = finding(c);
      break;
    case 'correction':
    case 'comparison':
      body = html`${c.source ? sourceExcerpt(c.source) : ''}${actionComparison(c.before, c.after)}${reason(c, 'Reason for the change')}${actionItems(c.items, 'changes')}${nextStep(c)}`;
      break;
    case 'investigation':
      body = html`<div class="soda-action-investigation">
        <header>
          ${actionIcon('investigation')}
          <h4>Evidence examined</h4>
        </header>
        ${actionItems(c.items, 'files')}${c.source ? sourceExcerpt(c.source) : ''}${
          c.rationale || c.resolution
            ? html`<section class="soda-investigation-insight">
                <h4>What this tells us</h4>
                <p>${c.resolution ?? c.rationale}</p>
              </section>`
            : ''
        }
      </div>`;
      break;
    case 'graph':
      body = html`<section class="soda-action-map">${renderFeedGraph(c.items)}${reason(c)}${nextStep(c)}</section>`;
      break;
    case 'progress':
    case 'incomplete':
      body = html`<section class="soda-action-work">
        ${workBalance(c)}${actionItems(c.items, 'tasks')}${reason(c)}${nextStep(c, action === 'incomplete' ? 'Continue from here' : 'Next step')}
      </section>`;
      break;
    case 'blocker':
      body = html`<section class="soda-action-blocker">
        <div class="soda-blocker-stop">
          ${actionIcon('blocker')}<strong>${c.kind === 'flag' ? 'Attention needed' : 'Work is waiting'}</strong>
        </div>
        ${actionItems(c.items, 'obstacles')}${reason(c)}${nextStep(c, 'Can continue when')}${workBalance(c)}
      </section>`;
      break;
    case 'diagnosis':
      body = html`<section class="soda-action-diagnosis">
        <h4>${actionIcon('diagnosis')} What the agent investigated</h4>
        ${actionList('Already tried', c.attempts, 'soda-action-attempts')}${actionItems(c.items, 'hypotheses')}${reason(c, 'Current explanation')}${nextStep(c, 'What to investigate next')}
      </section>`;
      break;
    case 'checks':
      body = html`<section class="soda-action-checksheet">
        <header>
          ${actionIcon('checks')}
          <h4>${c.kind === 'system' ? 'Recorded checks' : 'Reported checks'}</h4>
          <span>${c.items.length} listed</span>
        </header>
        ${actionItems(c.items, 'checks', c.kind !== 'system')}${actionComparison(c.before, c.after)}${reason(c)}${nextStep(c)}
      </section>`;
      break;
    case 'journey':
      body = html`<section class="soda-action-journey">
        <h4>${actionIcon('journey')} Following the experience</h4>
        ${actionItems(c.items, 'journey')}${actionComparison(c.before, c.after, ['Expected', 'Observed'])}${reason(c)}${nextStep(c)}
      </section>`;
      break;
    case 'knowledge':
      body = html`<section class="soda-action-notebook">
        <div class="soda-notebook-label">
          ${actionIcon('knowledge')}<strong>Project knowledge</strong>${c.scope ? html`<span>${c.scope}</span>` : ''}
        </div>
        <blockquote>${c.decision ?? c.summary}</blockquote>
        ${reason(c, 'What we learned')}${actionItems(c.items, 'notes')}${nextStep(c, 'Apply this when')}
      </section>`;
      break;
    case 'proposal':
      body = html`<section class="soda-action-proposal">
        <header>
          ${actionIcon('decision')}
          <h4>Proposed change</h4>
          <span>Awaiting a decision</span>
        </header>
        ${actionComparison(c.before, c.after, ['Current approach', 'Suggested approach'])}${reason(c, 'Why this would help')}${actionItems(c.items, 'options')}${actionList('Alternatives', c.alternatives)}${nextStep(c)}
      </section>`;
      break;
    case 'merge':
      body = html`<section class="soda-action-merge">
        ${mergeDrawing()}
        <div>
          <h4>Integration recorded</h4>
          ${actionItems(c.items, 'changes', false)}${workBalance(c)}${nextStep(c)}
        </div>
      </section>`;
      break;
    case 'unlock':
      body = html`<section class="soda-action-unlock">
        <div class="soda-unlock-source">
          ${actionIcon('graph')}<strong>Prerequisites resolved</strong
          >${actionItems(c.items, 'tasks', false)}${actionList('Completed work', c.completed)}
        </div>
        <span class="soda-unlock-arrow" aria-hidden="true">↓</span>
        <div>
          <h4>Ready for selection</h4>
          <p>${c.title}</p>
          ${nextStep(c)}
        </div>
      </section>`;
      break;
    case 'result':
      body = html`<section class="soda-action-result">
        <div class="soda-result-seal" aria-hidden="true">${actionIcon('result')}</div>
        <div>
          <h4>${c.kind === 'system' ? 'Recorded outcome' : 'Work handed over'}</h4>
          <p>${c.summary}</p>
          ${c.decision ? html`<p>${c.decision}</p>` : ''}${workBalance(c)}${actionItems(c.items, 'outcomes', c.kind !== 'system')}${reason(c)}${nextStep(c, 'What follows')}
        </div>
      </section>`;
      break;
    case 'discussion':
      body = html`<section class="soda-action-discussion">
        <span class="soda-discussion-mark" aria-hidden="true">“</span>
        <div>
          <blockquote>${c.question ?? c.summary}</blockquote>
          ${c.resolution ? html`<p>${c.resolution}</p>` : ''}${reason(c)}${actionItems(c.items, 'notes')}
        </div>
      </section>`;
      break;
    case 'maintenance':
      body = html`<section class="soda-action-maintenance">
        <header>
          ${actionIcon('knowledge')}
          <h4>Library change</h4>
        </header>
        ${actionComparison(c.before, c.after, ['Previous entry', 'Current entry'])}${actionItems(c.items, 'changes')}${reason(c, 'Reason recorded')}${nextStep(c)}
      </section>`;
      break;
    case 'artifact':
      body = html`${actionItems(c.items, 'artifacts')}${reason(c)}${nextStep(c)}`;
      break;
    default:
      body = html`<section class="soda-action-brief">
        <div class="soda-brief-corner" aria-hidden="true"></div>
        <p>${c.summary}</p>
        ${c.scope ? html`<p class="soda-brief-scope">${c.scope}</p>` : ''}${c.question ? html`<blockquote>${c.question}</blockquote>` : ''}${reason(c, 'Approach')}${actionItems(c.items, 'tasks')}${workBalance(c)}${nextStep(c)}
      </section>`;
  }
  return html`<div class="soda-feed-action" data-action=${action}>${body}</div>`;
}

export function mergeDrawing(): TemplateResult {
  return html`<svg
    class="soda-merge-drawing"
    viewBox="0 0 240 90"
    role="img"
    aria-label="A change joins the target branch"
  >
    <path class="soda-merge-main" d="M12 68H228" />
    <path class="soda-merge-branch" d="M36 68C65 68 40 22 80 22H160C195 22 175 68 208 68" />
    <circle cx="36" cy="68" r="5" />
    <circle cx="105" cy="22" r="5" />
    <circle cx="150" cy="22" r="5" />
    <circle class="soda-merge-join" cx="208" cy="68" r="7" />
    <text x="12" y="87">Target branch</text>
    <text x="86" y="12">Change</text>
  </svg>`;
}
