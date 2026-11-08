import {LitElement, html} from 'lit';
import type {FactoryTransport} from '../api/transport.js';
import type {FactoryWorkLinks} from '../app/navigation.js';
import {describeWorkJourney, renderWorkJourney} from './journey-view.js';
import {currentWorkObservation, requestWorkRefresh} from './observation.js';
import type {WorkObservation} from './observation.js';

export interface JourneyContext {
  readonly transport: FactoryTransport;
  readonly repository: string;
  readonly issue: string;
  readonly links?: FactoryWorkLinks;
  readonly observation?: WorkObservation;
}

/** @fires soda-factory-work-refresh */
export class SodaFactoryWorkJourney extends LitElement {
  static properties = {context: {attribute: false}};
  declare context: JourneyContext | undefined;
  protected createRenderRoot() {
    return this;
  }
  protected render() {
    if (!this.context) return html``;
    const observation = currentWorkObservation(this.context);
    const journey = observation?.journey;
    const message =
      observation?.state === 'ready'
        ? journey
          ? ''
          : 'No work journey has been recorded.'
        : observation?.state === 'loading'
          ? 'Loading work history…'
          : 'Current work history is unavailable. Retained history is stale.';
    return html`<p role="status">${message}</p>
      <button
        type="button"
        class="ui button"
        ?disabled=${observation?.state === 'loading'}
        @click=${() => requestWorkRefresh(this)}
      >
        Refresh journey
      </button>
      ${journey ? renderWorkJourney(describeWorkJourney(journey, this.context.links)) : ''}`;
  }
}
customElements.define('soda-factory-work-journey', SodaFactoryWorkJourney);
