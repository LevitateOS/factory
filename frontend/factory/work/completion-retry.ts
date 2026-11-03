import {LitElement, html} from 'lit';
import type {FactoryTransport} from '../api/transport.js';
import {check, id, object, readSodaJSON} from '../api/models.js';
import {factoryCommandId} from '../api/run-response.js';
import {factoryAccess} from '../api/progress-response.js';
import {workJourney} from '../api/journey-response.js';
import type {JourneyGate} from '../api/journey-response.js';
import {
  readCompletionRetry,
  retainCompletionRetry,
  sameCompletionRetry,
  settleCompletionRetry,
} from './completion-retry-record.js';
import type {CompletionRetryAddress, PendingCompletionRetry} from './completion-retry-record.js';

export interface RetryContext {
  readonly transport: FactoryTransport;
  readonly repository: string;
  readonly issue: string;
  /** Host-admitted stable profile/provider-instance/account reference, never a credential.
   * Hosts retire the binding on identity changes; transport generations are not storage keys. */
  readonly commandScope?: (signal: AbortSignal) => Promise<string>;
}
interface RetryCommand extends PendingCompletionRetry {
  readonly confirmed: boolean;
}

export class SodaFactoryCompletionRetry extends LitElement {
  static properties = {
    gates: {state: true},
    allowed: {state: true},
    busy: {state: true},
    message: {state: true},
    pending: {state: true},
    ready: {state: true},
  };
  declare private gates: readonly JourneyGate[];
  declare private allowed: boolean;
  declare private busy: boolean;
  declare private message: string;
  declare private pending: RetryCommand | undefined;
  declare private ready: boolean;
  private address: CompletionRetryAddress | undefined;
  private current: RetryContext | undefined;
  private serial = 0;
  private generation = '';
  private lifetime = new AbortController();

  constructor() {
    super();
    this.gates = [];
    this.allowed = this.busy = this.ready = false;
    this.message = '';
  }
  protected createRenderRoot() {
    return this;
  }
  set context(context: RetryContext | undefined) {
    if (context)
      check(
        id(context.repository) &&
          id(context.issue) &&
          typeof context.transport.generation === 'string' &&
          !!context.transport.generation
      );
    const prior = this.current;
    if (
      context?.transport === prior?.transport &&
      context?.repository === prior?.repository &&
      context?.transport.generation === this.generation &&
      context?.issue === prior?.issue
    ) {
      this.current = context;
      return;
    }
    this.lifetime.abort();
    this.lifetime = new AbortController();
    this.generation = context?.transport.generation ?? '';
    this.current = context;
    this.serial++;
    this.gates = [];
    this.allowed = this.busy = this.ready = false;
    this.address = undefined;
    this.message = '';
    this.pending = undefined;
    if (context) void this.load(context, this.serial);
  }
  disconnectedCallback() {
    this.context = undefined;
    super.disconnectedCallback();
  }
  private live(serial: number, context: RetryContext): boolean {
    if (serial !== this.serial || context.transport !== this.current?.transport) return false;
    if (context.transport.generation !== this.generation) {
      this.context = undefined;
      return false;
    }
    return true;
  }
  private async read(context: RetryContext, path: string) {
    const serial = this.serial;
    if (!this.live(serial, context)) throw Error('Completion context retired.');
    const response = await context.transport.request(path, {
      method: 'GET',
      signal: AbortSignal.any([this.lifetime.signal, AbortSignal.timeout(15000)]),
    });
    if (!this.live(serial, context)) throw Error('Completion context retired.');
    check(response.ok);
    return readSodaJSON(response);
  }
  private async load(context: RetryContext, serial: number) {
    if (!this.live(serial, context)) return;
    if (!this.message) this.message = 'Loading completion reviews…';
    try {
      const [accessValue, issueValue] = await Promise.all([
        this.read(context, `repositories/${context.repository}/factory/access`),
        this.read(context, `repositories/${context.repository}/factory/issues/${context.issue}`),
      ]);
      const access = factoryAccess(accessValue, context.repository);
      const journey = object(issueValue).journey;
      const gates = journey === undefined ? [] : workJourney(journey, context.repository, context.issue).gates;
      if (!this.live(serial, context)) return;
      this.allowed = access.viewer.code_write && access.viewer.session_current;
      this.gates = gates.filter((gate) => gate.state === 'attention' && gate.retryable);
      if (
        this.message.startsWith('Could not load completion reviews.') ||
        this.message === 'Loading completion reviews…'
      )
        this.message = '';
      await this.resolveIdentity(context, serial);
    } catch {
      if (this.live(serial, context)) {
        this.allowed = false;
        this.ready = false;
        this.message = 'Could not load completion reviews. Refresh the work detail.';
      }
    }
  }
  private async resolveIdentity(context: RetryContext, serial: number): Promise<boolean> {
    this.ready = false;
    try {
      if (!context.commandScope) throw Error('Missing completion retry identity');
      const scope = await context.commandScope(AbortSignal.any([this.lifetime.signal, AbortSignal.timeout(15000)]));
      if (!this.live(serial, context)) return false;
      const address: CompletionRetryAddress = {scope, repository: context.repository, issue: context.issue};
      const stored = readCompletionRetry(address);
      const confirmed =
        this.address?.scope === scope &&
        this.pending?.confirmed &&
        this.gates.some((gate) => this.sameGate(this.pending!, gate))
          ? this.pending
          : undefined;
      this.address = address;
      this.pending = stored.pending ? {...stored.pending, confirmed: false} : confirmed;
      if (stored.blocked) {
        this.message =
          'Saved completion retry is unreadable or belongs to another scope. It was preserved; no new request was sent.';
        return false;
      }
      this.ready = true;
      if (stored.pending)
        this.message =
          'An unconfirmed completion review retry is saved. Retry the same request to check its recorded result.';
      else if (!this.message.startsWith('Completion review retry queued.')) this.message = '';
      return true;
    } catch {
      if (this.live(serial, context)) {
        this.pending = undefined;
        this.message = 'Completion retry identity could not be read. Refresh completion reviews before retrying.';
      }
      return false;
    }
  }
  private async refresh() {
    const context = this.current,
      serial = this.serial;
    if (!context || !this.live(serial, context) || this.busy) return;
    this.busy = true;
    await this.load(context, serial);
    if (this.live(serial, context)) this.busy = false;
  }
  private sameGate(command: PendingCompletionRetry, gate: JourneyGate) {
    return command.event === gate.completion_id && command.cycle === gate.cycle_id;
  }
  private confirmed(context: RetryContext, command: RetryCommand) {
    this.pending = {...command, confirmed: true};
    this.message =
      'Completion review retry queued. Factory will reassess the recorded result in a new bounded attempt.';
    this.dispatchEvent(new CustomEvent('soda-completion-retry-queued', {bubbles: true, composed: true}));
    void this.load(context, this.serial);
  }
  private async retry(gate?: JourneyGate) {
    const context = this.current,
      serial = this.serial;
    if (
      !context ||
      !this.live(serial, context) ||
      !this.allowed ||
      !this.ready ||
      this.busy ||
      (gate && !this.gates.includes(gate))
    )
      return;
    if (this.pending?.confirmed || (gate && this.pending && !this.sameGate(this.pending, gate))) return;
    const shown = this.pending,
      scope = this.address?.scope;
    this.busy = true;
    try {
      // Re-admit the account immediately before dispatch. A changed record requires
      // another explicit click after the caller has seen its recovered target.
      if (!(await this.resolveIdentity(context, serial))) return;
      if (
        scope !== this.address?.scope ||
        (shown ? !this.pending || !sameCompletionRetry(shown, this.pending) : this.pending)
      ) {
        this.message = 'The saved completion retry or account changed. Review it before retrying.';
        return;
      }
      if (!gate && !this.pending) return;
      const command: RetryCommand = this.pending ?? {
        command: factoryCommandId(),
        event: gate!.completion_id,
        cycle: gate!.cycle_id,
        confirmed: false,
      };
      const address = this.address!;
      this.pending = command;
      const retained = retainCompletionRetry(address, command);
      if (retained !== 'saved') {
        this.message =
          retained === 'unavailable'
            ? 'Completion retry could not be saved. No request was sent. Retry the same request when storage is available.'
            : 'The saved completion retry changed. Refresh completion reviews before retrying.';
        return;
      }
      this.message = 'Requesting a new completion review…';
      const response = await context.transport.request(`repositories/${context.repository}/factory/actions`, {
        method: 'POST',
        headers: {'Content-Type': 'application/json'},
        signal: AbortSignal.any([this.lifetime.signal, AbortSignal.timeout(90000)]),
        body: JSON.stringify({
          command_id: command.command,
          action: 'retry_completion',
          completion: {event_id: command.event, expected_prior_cycle: command.cycle},
        }),
      });
      check(response.ok);
      const receipt = object(await readSodaJSON(response));
      check(
        receipt.command_id === command.command &&
          receipt.event_id === command.event &&
          receipt.prior_cycle === command.cycle &&
          receipt.successor_cycle === command.command &&
          receipt.repository === context.repository &&
          receipt.queued === true
      );
      if (!this.live(serial, context)) return;
      if (!settleCompletionRetry(address, command)) {
        this.ready = false;
        this.message = 'The saved completion retry changed. Refresh completion reviews to read its current state.';
        return;
      }
      this.confirmed(context, command);
    } catch {
      if (this.live(serial, context))
        this.message =
          'No retry was confirmed. The exact request remains saved. Retry this same request or refresh completion reviews.';
    } finally {
      if (this.live(serial, context)) this.busy = false;
    }
  }
  protected render() {
    if (!this.current || (!this.gates.length && !this.message && !this.pending)) return html``;
    return html`<section aria-label="Completion review needs attention">
      ${this.gates.map(
        (gate) => html`<p>
            Completion review needs attention. A retry requests another bounded assessment of the recorded result.
          </p>
          <button
            type="button"
            class="ui button"
            ?disabled=${!this.allowed || !this.ready || this.busy || (!!this.pending && (!this.sameGate(this.pending, gate) || this.pending.confirmed))}
            @click=${() => this.retry(gate)}
          >
            Retry completion review
          </button>`
      )}
      ${
        this.pending && !this.pending.confirmed && !this.gates.some((gate) => this.sameGate(this.pending!, gate))
          ? html`<p>
                An earlier completion review request is still unconfirmed. Checking it reuses its original review and
                cycle.
              </p>
              <button
                type="button"
                class="ui button"
                ?disabled=${!this.allowed || !this.ready || this.busy}
                @click=${() => this.retry()}
              >
                Retry saved completion review
              </button>`
          : ''
      }
      <p role="status">${this.message}</p>
      ${
        this.message && !this.busy
          ? html`<button type="button" class="ui button" ?disabled=${this.busy} @click=${() => this.refresh()}>
              Refresh completion reviews
            </button>`
          : ''
      }
    </section>`;
  }
}
customElements.define('soda-factory-completion-retry', SodaFactoryCompletionRetry);
