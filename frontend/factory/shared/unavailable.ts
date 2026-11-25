import {html} from 'lit';
import type {TemplateResult} from 'lit';

/** Features a host may declare it cannot serve, by the context field it sets to false. */
export const hostFeatures = {
  repairFundingAvailable: {title: 'Repository repair funding', detail: 'No repair grant is assumed.'},
  watchAvailable: {title: 'Live run output', detail: 'Output appears in Work detail when the run ends.'},
  activityAvailable: {title: 'Activity visibility', detail: 'Factory activity is unavailable.'},
} as const;
export type HostFeature = keyof typeof hostFeatures;

/** The features a context declares unavailable. Absent means undeclared, not unavailable. */
export function unavailableFeatures(context: Partial<Record<HostFeature, boolean>>): HostFeature[] {
  return (Object.keys(hostFeatures) as HostFeature[]).filter((key) => context[key] === false);
}

/** The one "Unavailable in this app" presentation. It sends nothing and assumes nothing is on. */
export function renderUnavailable(id: string, features: readonly HostFeature[]): TemplateResult | '' {
  if (!features.length) return '';
  return html`<section id=${id} class="soda-factory-unavailable" aria-labelledby=${id + '-title'}>
    <h3 id=${id + '-title'}>Unavailable in this app</h3>
    <p>This app cannot read or save ${features.length === 1 ? 'this' : 'these'} yet. Nothing here is assumed to be on.</p>
    <dl>
      ${features.map((key) => html`<div><dt>${hostFeatures[key].title}</dt><dd>${hostFeatures[key].detail}</dd></div>`)}
    </dl>
  </section>`;
}
