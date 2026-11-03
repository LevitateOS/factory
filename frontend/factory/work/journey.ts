import {LitElement, html} from 'lit';
import type {FactoryTransport} from '../api/transport.js';
import type {FactoryWorkLinks} from '../app/navigation.js';
import {check, id, object, readSodaJSON} from '../api/models.js';
import {workJourney} from '../api/journey-response.js';
import type {WorkJourney} from '../api/journey-response.js';
import {describeWorkJourney, renderWorkJourney} from './journey-view.js';
import type {WorkJourneyModel} from './journey-view.js';

export interface JourneyContext {
  readonly transport: FactoryTransport;
  readonly repository: string;
  readonly links?: FactoryWorkLinks;
  readonly issue: string;
}

export class SodaFactoryWorkJourney extends LitElement {
  static properties = {model: {state: true}, message: {state: true}, busy: {state: true}};
  declare private model: WorkJourneyModel | undefined;
  declare private message: string;
  declare private busy: boolean;
  private current: JourneyContext | undefined;
  private serial = 0;
  private generation = '';
  private recorded: WorkJourney | undefined;
  private request: AbortController | undefined;

  constructor() {
    super();
    this.message = '';
    this.busy = false;
  }
  protected createRenderRoot() {
    return this;
  }
  set context(context: JourneyContext | undefined) {
    if (context)
      check(
        id(context.repository) &&
          id(context.issue) &&
          typeof context.transport.generation === 'string' &&
          context.transport.generation !== ''
      );
    const prior = this.current;
    const generation = context?.transport.generation ?? '';
    if (
      context?.transport === prior?.transport &&
      context?.repository === prior?.repository &&
      generation === this.generation &&
      context?.issue === prior?.issue
    ) {
      this.current = context;
      // Navigation ports may change while the recorded source stays bound.
      // Refresh those destinations without another read or duplicate history.
      if (this.recorded && context && context.links !== prior?.links)
        this.model = describeWorkJourney(this.recorded, context.links);
      return;
    }
    this.current = context;
    this.serial++;
    this.request?.abort();
    this.request = undefined;
    this.generation = generation;
    this.recorded = undefined;
    this.model = undefined;
    this.message = '';
    this.busy = false;
    if (context) void this.load();
  }
  disconnectedCallback() {
    this.context = undefined;
    super.disconnectedCallback();
  }
  private isLive(serial: number, context: JourneyContext): boolean {
    if (serial !== this.serial) return false;
    if (context.transport.generation !== this.generation) {
      this.context = undefined;
      return false;
    }
    return true;
  }
  private async load() {
    const context = this.current,
      serial = this.serial;
    if (!context || this.busy) return;
    if (!this.isLive(serial, context)) return;
    const request = new AbortController();
    this.request = request;
    this.busy = true;
    this.message = this.model ? 'Refreshing work history…' : 'Loading work history…';
    try {
      const response = await context.transport.request(
        `repositories/${context.repository}/factory/issues/${context.issue}`,
        {method: 'GET', signal: AbortSignal.any([request.signal, AbortSignal.timeout(15000)])}
      );
      if (!this.isLive(serial, context)) return;
      if (response.status === 401 || response.status === 403) {
        this.recorded = undefined;
        this.model = undefined;
      }
      check(response.ok);
      const raw = object(await readSodaJSON(response));
      check(raw.repository === context.repository && raw.issue === context.issue);
      const recorded =
        raw.journey === undefined ? undefined : workJourney(raw.journey, context.repository, context.issue);
      if (!this.isLive(serial, context)) return;
      const model = recorded ? describeWorkJourney(recorded, this.current?.links) : undefined;
      this.recorded = recorded;
      this.model = model;
      this.message = model ? '' : 'No work journey has been recorded.';
    } catch {
      if (this.isLive(serial, context))
        this.message = this.model
          ? 'Could not refresh. The last loaded work history is still shown.'
          : 'Could not load the work journey. Refresh to try again.';
    } finally {
      if (this.isLive(serial, context)) this.busy = false;
      if (this.request === request) this.request = undefined;
    }
  }
  protected render() {
    if (!this.current) return html``;
    return html`<p role="status">${this.message}</p>
      <button type="button" class="ui button" ?disabled=${this.busy} @click=${() => this.load()}>
        ${this.busy ? 'Loading…' : 'Refresh journey'}
      </button>
      ${this.model ? renderWorkJourney(this.model) : ''}`;
  }
}
customElements.define('soda-factory-work-journey', SodaFactoryWorkJourney);
