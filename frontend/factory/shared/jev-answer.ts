import {html} from 'lit';
import type {TemplateResult} from 'lit';

// JEV's typed answers drawn as what they are: a Choice is a set of weighed
// options with the pick marked, a NOUL is a yes/no balance, and a Score is a
// position on its ordered levels. Each shows how sure JEV was.

export interface JevChoiceOption {
  readonly key: string;
  readonly label: string;
  readonly detail?: string;
  readonly probability: number;
}

const pct = (value: number) => `${Math.round(Math.max(0, Math.min(1, value)) * 100)}%`;

function finite(value: unknown): number | undefined {
  return typeof value === 'number' && Number.isFinite(value) ? value : undefined;
}

function text(value: unknown): string {
  if (typeof value === 'string') return value;
  if (value === undefined || value === null) return '';
  return JSON.stringify(value);
}

function humanKey(key: string): string {
  const words = key.replaceAll(/[_-]+/g, ' ').trim();
  return words.charAt(0).toUpperCase() + words.slice(1);
}

function renderSure(confidence: number | undefined): TemplateResult | string {
  if (confidence === undefined) return '';
  return html`<p class="soda-jev-sure">JEV is <strong>${pct(confidence)} sure</strong></p>`;
}

/** A Choice: every option with its share, the selected one marked. */
export function renderJevChoice(
  options: readonly JevChoiceOption[],
  selected: string,
  confidence?: number
): TemplateResult {
  const ranked = [...options].sort((a, b) => b.probability - a.probability);
  const shown = ranked.filter((option) => option.key === selected || option.probability >= 0.01).slice(0, 6);
  const hidden = ranked.length - shown.length;
  return html`<div class="soda-jev-answer soda-jev-choice" data-kind="choice">
    <ol>
      ${shown.map((option) => {
        const picked = option.key === selected;
        return html`<li data-picked=${picked ? 'true' : 'false'}>
          <span class="soda-jev-option"
            ><span class="soda-jev-mark" aria-hidden="true">${picked ? '✓' : ''}</span
            ><span class="soda-jev-label" title=${option.detail ?? ''}>${option.label}</span
            >${picked ? html`<span class="soda-visually-hidden">(chosen)</span>` : ''}<span class="soda-jev-pc"
              >${pct(option.probability)}</span
            ></span
          >
          <span class="soda-jev-track" aria-hidden="true"><b style=${`width:${pct(option.probability)}`}></b></span>
        </li>`;
      })}
    </ol>
    ${hidden > 0 ? html`<p class="soda-jev-more">${hidden === 1 ? '1 other option' : `${hidden} other options`} under 1%</p>` : ''}
    ${renderSure(confidence)}
  </div>`;
}

/** A NOUL: the probability of yes against no, with what each side means. */
export function renderJevNoul(probability: number, criteria?: unknown): TemplateResult {
  const sides = criteria && typeof criteria === 'object' ? (criteria as Record<string, unknown>) : {};
  const yes = Math.max(0, Math.min(1, probability));
  const leaning = yes >= 0.5 ? 'yes' : 'no';
  return html`<div class="soda-jev-answer soda-jev-noul" data-kind="noul" data-leaning=${leaning}>
    <p class="soda-jev-noul-head">
      <strong>${leaning === 'yes' ? pct(yes) : pct(1 - yes)} ${leaning}</strong>
    </p>
    <div class="soda-jev-balance" role="img" aria-label=${`Probability of yes ${pct(yes)}, no ${pct(1 - yes)}`}>
      <b class="soda-jev-yes" style=${`width:${pct(yes)}`}></b><b class="soda-jev-no"></b><i aria-hidden="true"></i>
    </div>
    <p class="soda-jev-sides">
      <span><strong>Yes ${pct(yes)}</strong>${text(sides.true) ? html` · ${text(sides.true)}` : ''}</span>
      <span><strong>No ${pct(1 - yes)}</strong>${text(sides.false) ? html` · ${text(sides.false)}` : ''}</span>
    </p>
  </div>`;
}

/** A Score: where the answer sits on its ordered levels, lowest to highest. */
export function renderJevScore(
  score: number,
  levels: readonly string[],
  probabilities: Readonly<Record<string, number>> = {},
  confidence?: number
): TemplateResult {
  const top = Math.max(1, levels.length - 1);
  const position = Math.max(0, Math.min(top, score));
  const nearest = Math.round(position);
  return html`<div class="soda-jev-answer soda-jev-score" data-kind="score">
    <p class="soda-jev-score-head">
      <strong>${Number.isInteger(position) ? position : position.toFixed(1)}</strong><span> of ${top}</span>
      ${levels[nearest] ? html`<span class="soda-jev-score-level">· ${levels[nearest]}</span>` : ''}
    </p>
    <div class="soda-jev-scale" role="img" aria-label=${`Score ${position.toFixed(1)} of ${top}`}>
      ${levels.map(
        (_, index) =>
          html`<span data-on=${index === nearest ? 'true' : 'false'}
            ><b style=${`height:${pct(probabilities[String(index)] ?? (index === nearest ? 1 : 0))}`}></b
          ></span>`
      )}
      <i style=${`left:${((position + 0.5) / levels.length) * 100}%`}></i>
    </div>
    <ol class="soda-jev-levels">
      ${levels.map(
        (level, index) => html`<li data-on=${index === nearest ? 'true' : 'false'}><span>${index}</span>${level}</li>`
      )}
    </ol>
    ${renderSure(confidence)}
  </div>`;
}

/**
 * One recorded JEV answer by its type. Falls back to the literal value when
 * the shape is not one JEV returns.
 */
export function renderJevAnswer(value: unknown, criteria?: unknown): TemplateResult {
  const answer = value && typeof value === 'object' ? (value as Record<string, unknown>) : {};
  const confidence = finite(answer.confidence);
  const probabilities =
    answer.probabilities && typeof answer.probabilities === 'object'
      ? (answer.probabilities as Record<string, number>)
      : {};
  if (answer.type === 'noul' && finite(answer.noul) !== undefined)
    return renderJevNoul(answer.noul as number, criteria);
  if (answer.type === 'choice' && typeof answer.choice === 'string') {
    const named =
      criteria && typeof criteria === 'object' && !Array.isArray(criteria) ? (criteria as Record<string, unknown>) : {};
    const keys = new Set([...Object.keys(named), ...Object.keys(probabilities), answer.choice]);
    const options = [...keys].map((key) => ({
      key,
      label: humanKey(key),
      detail: text(named[key]),
      probability:
        finite(probabilities[key]) ?? (key === answer.choice && Object.keys(probabilities).length === 0 ? 1 : 0),
    }));
    return renderJevChoice(options, answer.choice, confidence);
  }
  if (answer.type === 'score' && finite(answer.score) !== undefined) {
    const legend = answer.legend && typeof answer.legend === 'object' ? (answer.legend as Record<string, unknown>) : {};
    const listed = Array.isArray(criteria) ? criteria.map(text) : [];
    const count = Math.max(listed.length, Object.keys(legend).length, 2);
    const levels = Array.from(
      {length: count},
      (_, index) => text(legend[String(index)]) || listed[index] || `Level ${index}`
    );
    return renderJevScore(answer.score as number, levels, probabilities, confidence);
  }
  return html`<pre class="soda-jev-raw">${text(value)}</pre>`;
}
