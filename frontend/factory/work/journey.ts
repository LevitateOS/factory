import {LitElement, html} from 'lit';
import type {FactoryTransport} from '../api/transport.js';
import type {FactoryWorkLinks} from '../app/navigation.js';
import {describeWorkJourney, renderWorkJourney} from './journey-view.js';
import {currentWorkObservation} from './observation.js';
import type {WorkObservation} from './observation.js';

export interface JourneyContext {
  readonly transport: FactoryTransport;
  readonly repository: string;
  readonly issue: string;
  readonly links?: FactoryWorkLinks;
  readonly observation?: WorkObservation;
}

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
    // A shown journey stays put while it re-reads.
    const message = journey
      ? ''
      : observation?.state === 'ready'
        ? 'Nothing has happened here yet.'
        : observation?.state === 'loading'
          ? 'Loading work history…'
          : 'Work history is unavailable right now.';
    return html`<header class="soda-journey-head">
        <h2>Journey</h2>
      </header>
      <p role="status">${message}</p>
      ${journey ? renderWorkJourney(describeWorkJourney(journey, this.context.links)) : ''}`;
  }
}
customElements.define('soda-factory-work-journey', SodaFactoryWorkJourney);
