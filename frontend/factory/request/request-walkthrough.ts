import {html, svg} from 'lit';
import type {
  AssumptionChallenge,
  ConsolidatedProposal,
  Exploration,
  ProductIdea,
  ProposalAlternative,
  ProposalStep,
} from '../api/request-response.js';
import type {IntentView} from './request-view.js';

function short(text: string, limit: number): string {
  return text.length > limit ? text.slice(0, limit - 1) + '…' : text;
}
function flowBranches(step: ProposalStep) {
  if (!step.branches?.length) return '';
  return html`<ul>
    ${step.branches.map((branch) => html`<li><strong>${branch.outcome}:</strong> ${branch.result}</li>`)}
  </ul>`;
}
function flowSteps(steps: ProposalStep[]) {
  return html`<ol>
    ${steps.map(
      (step) => html`<li>
        <p><strong>${step.actor}</strong> ${step.action}</p>
        <p>${step.result}</p>
        ${flowBranches(step)}
      </li>`
    )}
  </ol>`;
}
function figureRows(alternative: ProposalAlternative): {label: string; detail: string}[] {
  return alternative.steps.flatMap((step) => [
    {label: `${step.actor} — ${step.action}`, detail: step.result},
    ...(step.branches || []).map((branch) => ({label: `If ${branch.outcome}`, detail: branch.result})),
  ]);
}
function flowFigure(alternative: ProposalAlternative) {
  const rows = figureRows(alternative);
  const height = rows.length * 56 + 24;
  return html`<figure>
    <svg
      role="img"
      aria-label=${'Illustrated flow: ' + alternative.name}
      viewBox=${'0 0 600 ' + height}
      width="100%"
      height=${height}
      preserveAspectRatio="xMidYMin meet"
    >
      ${rows.map(
        (row, index) => svg`
          <g>
            <rect x="8" y=${16 + index * 56} width="584" height="44" fill="none" stroke="currentColor" />
            <text x="20" y=${34 + index * 56} font-size="13">${short(row.label, 72)}</text>
            <text x="20" y=${50 + index * 56} font-size="12">${short(row.detail, 76)}</text>
            ${
              index < rows.length - 1
                ? svg`<line x1="300" y1=${60 + index * 56} x2="300" y2=${72 + index * 56} stroke="currentColor" />`
                : ''
            }
          </g>`
      )}
    </svg>
    <figcaption>Illustrated flow: ${alternative.name}. The numbered list beside it carries the same steps.</figcaption>
  </figure>`;
}
function recommendationFlow(proposal: ConsolidatedProposal) {
  const other = proposal.alternative;
  return html`<section aria-label="Recommended product flow">
    <h3>Here is how it would work</h3>
    <p>${proposal.alternative.name}</p>
    ${other.audience ? html`<p>Who will use it: ${other.audience}</p>` : ''} ${flowSteps(other.steps)}
    ${other.visibility ? html`<p>Who sees each result: ${other.visibility}</p>` : ''}
    <details>
      <summary>Flow diagram</summary>
      ${flowFigure(other)}
    </details>
    <h4>Outcomes</h4>
    <ul>
      ${proposal.outcomes.map((outcome) => html`<li>${outcome}</li>`)}
    </ul>
    ${
      proposal.constraints.length
        ? html`<h4>Limits</h4>
            <ul>
              ${proposal.constraints.map((constraint) => html`<li>${constraint}</li>`)}
            </ul>`
        : ''
    }
    <details>
      <summary>Delivery milestones</summary>
      <ol>
        ${proposal.milestones.map((milestone) => html`<li>${milestone.name}: ${milestone.result}</li>`)}
      </ol>
    </details>
  </section>`;
}
function comparedApproaches(exploration: Exploration) {
  const alternatives = exploration.content.alternatives;
  if (!alternatives?.length) return '';
  const recommended = exploration.recommendation?.alternative.name;
  return html`<section aria-label="Compared approaches">
    <h3>Compared approaches</h3>
    <p>
      ${exploration.content.producer === 'soda-planning-agent-v1' ? (recommended ? `JEV recommends ${recommended}; you can correct it before acceptance.` : 'JEV will compare these alternatives when you check the walkthrough; you can correct them before acceptance.') : 'Starter comparison; no vote is needed. Correct anything wrong instead.'}
    </p>
    ${alternatives.map(
      (other) => html`<details>
        <summary>${other.name}</summary>
        ${other.audience ? html`<p>Audience: ${other.audience}</p>` : ''}
        ${other.summary ? html`<p>${other.summary}</p>` : ''}
        ${other.evidence ? html`<p>Evidence: ${other.evidence}</p>` : ''}
        ${exploration.content.producer === 'soda-planning-agent-v1' ? flowSteps(other.steps) : ''}
        <h4>Outcomes</h4>
        <ul>
          ${other.outcomes.map((outcome) => html`<li>${outcome}</li>`)}
        </ul>
      </details>`
    )}
  </section>`;
}
function unknownList(exploration: Exploration) {
  return html`<section aria-label="Still to establish">
    <h3>Still to establish</h3>
    <ul>
      ${exploration.content.unknowns.map((unknown) => html`<li>${unknown}</li>`)}
    </ul>
  </section>`;
}
function consistencyNote(exploration: Exploration) {
  const report = exploration.consistency;
  if (!report) return '';
  if (!report.mismatches.length) return html`<p>The walkthrough was checked against the saved direction.</p>`;
  return html`<section aria-label="Consistency check">
    <h3 tabindex="-1">Needs attention before acceptance</h3>
    <ul>
      ${report.mismatches.map((finding) => html`<li>${finding.detail}</li>`)}
    </ul>
  </section>`;
}
function challengeIdea(view: IntentView, challenge: AssumptionChallenge): ProductIdea | undefined {
  return view.revision?.ideas.find((idea) => idea.id === challenge.idea);
}
function challengeCorrection(view: IntentView, challenge: AssumptionChallenge, idea: ProductIdea | undefined) {
  if (!idea) return html`<p>This question no longer applies to the current revision.</p>`;
  return html`<label
      >Explain a correction<textarea
        rows="2"
        maxlength="4000"
        aria-label=${'Explain a correction to ' + challenge.assumption}
        .value=${idea.decision === 'edit' ? idea.correction || '' : ''}
        ?disabled=${view.busy || view.blocked}
        @input=${(event: Event) => {
          const node = event.target;
          if (node instanceof HTMLTextAreaElement) view.decide(idea.id, 'edit', node.value);
        }}
      ></textarea>
    </label>
    <button
      aria-label=${'Use recommended resolution for ' + challenge.assumption}
      ?disabled=${view.busy || view.blocked || idea.decision === 'accept'}
      @click=${() => view.decide(challenge.idea, 'accept')}
    >
      Use recommended resolution
    </button>`;
}
function challengeDecision(idea: ProductIdea | undefined): string {
  if (idea?.decision === 'accept') return 'Answered: using the recommended resolution.';
  if (idea?.decision === 'decline') return 'Answered: declined; update the proposal to recompose without it.';
  if (idea?.decision === 'edit' && idea.correction?.trim()) return 'Answered with a correction.';
  return 'Needs your answer.';
}
export function assumptionChallenges(view: IntentView, exploration: Exploration) {
  if (!exploration.challenges.length) return '';
  return html`<section aria-label="Questions needing your answer">
    <h3>Questions needing your answer</h3>
    ${exploration.challenges.map((challenge) => {
      const idea = challengeIdea(view, challenge);
      return html`<article>
        <p>${challenge.assumption}</p>
        <p class="consequence">${challenge.consequence}</p>
        <p>Recommended resolution: ${challenge.recommendation}</p>
        <p>${challengeDecision(idea)}</p>
        ${challengeCorrection(view, challenge, idea)}
      </article>`;
    })}
  </section>`;
}
export function changeSomething(view: IntentView) {
  if (!view.changeOpen)
    return html`<button ?disabled=${view.busy || view.blocked} @click=${() => view.toggleChange(true)}>
      Change something
    </button>`;
  return html`<div class="change-field">
    <label
      >What should change?<textarea
        rows="3"
        maxlength="4000"
        .value=${view.changeText}
        ?disabled=${view.busy || view.blocked}
        @input=${(event: Event) => {
          const node = event.target;
          if (node instanceof HTMLTextAreaElement) view.set('changeText', node.value);
        }}
      ></textarea>
    </label>
    <button ?disabled=${view.busy || view.blocked} @click=${() => view.toggleChange(false)}>Cancel</button>
    <button
      class="primary"
      ?disabled=${view.busy || view.blocked || !view.changeText.trim()}
      @click=${() => view.correctProposal(view.changeText)}
    >
      Submit change
    </button>
  </div>`;
}
export function explorationReview(view: IntentView, exploration: Exploration) {
  const recommendation = exploration.recommendation;
  return html`${
    view.explorationStale ? html`<p>The walkthrough below is out of date. Update the proposal to refresh it.</p>` : ''
  }
  ${
    recommendation
      ? recommendationFlow(recommendation)
      : html`<p>
          ${exploration.content.supported ? 'The authored alternatives below are awaiting review.' : 'No authored comparison covers this objective yet. Answer the questions below instead.'}
        </p>`
  }
  ${comparedApproaches(exploration)} ${unknownList(exploration)} ${consistencyNote(exploration)}
  ${changeSomething(view)} ${assumptionChallenges(view, exploration)}`;
}
