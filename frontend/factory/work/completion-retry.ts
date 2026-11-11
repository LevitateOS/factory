import {LitElement, html} from 'lit';
import type {FactoryTransport} from '../api/transport.js';
import {check, id, object} from '../api/models.js';
import {factoryCommandId} from '../api/run-response.js';
import {PendingCommandController} from '../shared/pending-command.js';
import type {ClassifyRefusal} from '../shared/pending-command.js';
import {currentWorkObservation, requestWorkRefresh} from './observation.js';
import type {WorkObservation} from './observation.js';
import type {JourneyGate} from '../api/journey-response.js';
import {
  readCompletionRetry,
  retainCompletionRetry,
  sameCompletionRetry,
  settleCompletionRetry,
} from './completion-retry-record.js';
import type {CompletionRetryAddress, PendingCompletionRetry} from './completion-retry-record.js';

export interface RetryContext {
  readonly observation?: WorkObservation;
  readonly transport: FactoryTransport;
  readonly repository: string;
  readonly issue: string;
  /** Host-admitted stable profile/provider-instance/account reference, never a credential.
   * Hosts retire the binding on identity changes; transport generations are not storage keys. */
  readonly commandScope?: (signal: AbortSignal) => Promise<string>;
}
// These refusals are returned before the command is recorded: pkg/factory/control/completion_retry.go
// (eligibility and authority checks) and the single rolled-back transaction in
// pkg/store/factory_completion_retry.go. command_conflict means a record exists and is never discarded.
const classifyRefusal: ClassifyRefusal = (status, code) =>
  status === 409 && (code === 'completion_retry_ineligible' || code === 'authority_ineffective') ? 'discard' : 'keep';

/** @fires soda-factory-work-refresh */
export class SodaFactoryCompletionRetry extends LitElement {
  static properties = {
    gates: {state: true},
    allowed: {state: true},
    busy: {state: true},
    message: {state: true},
  };
  declare private gates: readonly JourneyGate[];
  declare private allowed: boolean;
  declare private busy: boolean;
  declare private message: string;
  private cmd = new PendingCommandController<PendingCompletionRetry, CompletionRetryAddress>(
    {read: readCompletionRetry, retain: retainCompletionRetry, settle: settleCompletionRetry, same: sameCompletionRetry},
    () => this.requestUpdate()
  );
  private current: RetryContext | undefined;
  private serial = 0;
  private generation = '';
  private lifetime = new AbortController();

  constructor() {
    super();
    this.gates = [];
    this.allowed = this.busy = false;
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
      this.current = context && {...context};
      if (context?.observation !== prior?.observation && context) this.observe(context);
      return;
    }
    this.lifetime.abort();
    this.lifetime = new AbortController();
    this.generation = context?.transport.generation ?? '';
    this.current = context && {...context};
    this.serial++;
    this.gates = [];
    this.allowed = this.busy = false;
    this.cmd.reset();
    this.message = '';
    if (context) this.observe(context);
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
  private observe(context: RetryContext) {
    const observation = currentWorkObservation(context);
    this.allowed =
      observation?.state === 'ready' &&
      !!observation.access?.viewer.code_write &&
      !!observation.access.viewer.session_current;
    this.gates = observation?.journey?.gates.filter((gate) => gate.state === 'attention' && gate.retryable) ?? [];
    if (!this.allowed) this.message = 'Current completion review authority is unavailable. Refresh the work detail.';
    else if (!this.busy) void this.resolveIdentity(context, this.serial);
  }
  private async resolveIdentity(context: RetryContext, serial: number): Promise<boolean> {
    this.cmd.resolving();
    try {
      if (!context.commandScope) throw Error('Missing completion retry identity');
      const scope = await context.commandScope(AbortSignal.any([this.lifetime.signal, AbortSignal.timeout(15000)]));
      if (!this.live(serial, context)) return false;
      const address: CompletionRetryAddress = {scope, repository: context.repository, issue: context.issue};
      if (this.cmd.adopt(address) === 'blocked') {
        this.message =
          'Saved completion retry is unreadable or belongs to another scope. It was preserved; no new request was sent.';
        return false;
      }
      if (this.cmd.pending)
        this.message =
          'An unconfirmed completion review retry is saved. Retry the same request to check its recorded result.';
      else if (this.cmd.state.kind !== 'confirmed') this.message = '';
      return true;
    } catch {
      if (this.live(serial, context)) {
        this.cmd.unresolve();
        this.message = 'Completion retry identity could not be read. Refresh completion reviews before retrying.';
      }
      return false;
    }
  }
  private sameGate(command: PendingCompletionRetry, gate: JourneyGate) {
    return command.event === gate.completion_id && command.cycle === gate.cycle_id;
  }
  /** The confirmed command, while its gate is still listed. */
  private get confirmedShown(): PendingCompletionRetry | undefined {
    const command = this.cmd.confirmed;
    return command && this.gates.some((gate) => this.sameGate(command, gate)) ? command : undefined;
  }
  private confirmed() {
    this.message =
      'Completion review retry queued. Factory will reassess the recorded result in a new bounded attempt.';
    this.dispatchEvent(new CustomEvent('soda-completion-retry-queued', {bubbles: true, composed: true}));
  }
  private async retry(gate?: JourneyGate) {
    const context = this.current,
      serial = this.serial;
    if (
      !context ||
      !this.live(serial, context) ||
      !this.allowed ||
      !this.cmd.ready ||
      this.busy ||
      (gate && !this.gates.includes(gate))
    )
      return;
    if (this.confirmedShown || (gate && this.cmd.pending && !this.sameGate(this.cmd.pending, gate))) return;
    this.busy = true;
    try {
      // Re-admit the account immediately before dispatch. A changed record requires
      // another explicit click after the caller has seen its recovered target.
      const admitted = await this.cmd.readmit(() => this.resolveIdentity(context, serial));
      if (admitted === 'unresolved') return;
      if (admitted === 'changed') {
        this.message = 'The saved completion retry or account changed. Review it before retrying.';
        return;
      }
      if (!this.allowed || !this.live(serial, context)) return;
      if (!this.cmd.pending && gate && !this.gates.includes(gate)) return;
      if (!gate && !this.cmd.pending) return;
      const command: PendingCompletionRetry = this.cmd.pending ?? {
        command: factoryCommandId(),
        event: gate!.completion_id,
        cycle: gate!.cycle_id,
      };
      this.message = 'Requesting a new completion review…';
      const outcome = await this.cmd.submit({
        command,
        live: () => this.live(serial, context),
        classifyRefusal,
        send: () =>
          context.transport.request(`repositories/${context.repository}/factory/actions`, {
            method: 'POST',
            headers: {'Content-Type': 'application/json'},
            signal: AbortSignal.any([this.lifetime.signal, AbortSignal.timeout(90000)]),
            body: JSON.stringify({
              command_id: command.command,
              action: 'retry_completion',
              completion: {event_id: command.event, expected_prior_cycle: command.cycle},
            }),
          }),
        receipt: (body) => {
          const receipt = object(body);
          check(
            receipt.command_id === command.command &&
              receipt.event_id === command.event &&
              receipt.prior_cycle === command.cycle &&
              receipt.successor_cycle === command.command &&
              receipt.repository === context.repository &&
              receipt.queued === true
          );
        },
      });
      if (outcome.kind === 'retired') return;
      if (outcome.kind === 'not-saved')
        this.message =
          outcome.result === 'unavailable'
            ? 'Completion retry could not be saved. No request was sent. Retry the same request when storage is available.'
            : 'The saved completion retry changed. Refresh completion reviews before retrying.';
      else if (outcome.kind === 'record-changed')
        this.message = 'The saved completion retry changed. Refresh completion reviews to read its current state.';
      else if (outcome.kind === 'confirmed') this.confirmed();
      else if (outcome.kind === 'refused' && outcome.discarded)
        this.message = 'The completion review changed and can no longer be retried. Refresh completion reviews.';
      else
        this.message =
          'No retry was confirmed. The exact request remains saved. Retry this same request or refresh completion reviews.';
    } finally {
      if (this.live(serial, context)) this.busy = false;
    }
  }
  protected render() {
    const shown = this.cmd.pending ?? this.confirmedShown;
    if (!this.current || (!this.gates.length && !this.message && !shown)) return html``;
    return html`<section aria-label="Completion review needs attention">
      ${this.gates.map(
        (gate) => html`<p>
            Completion review needs attention. A retry requests another bounded assessment of the recorded result.
          </p>
          <button
            type="button"
            class="ui button"
            ?disabled=${!this.allowed || !this.cmd.ready || this.busy || (!!shown && (!this.sameGate(shown, gate) || !!this.confirmedShown))}
            @click=${() => this.retry(gate)}
          >
            Retry completion review
          </button>`
      )}
      ${
        this.cmd.pending && !this.gates.some((gate) => this.sameGate(this.cmd.pending!, gate))
          ? html`<p>
                An earlier completion review request is still unconfirmed. Checking it reuses its original review and
                cycle.
              </p>
              <button
                type="button"
                class="ui button"
                ?disabled=${!this.allowed || !this.cmd.ready || this.busy}
                @click=${() => this.retry()}
              >
                Retry saved completion review
              </button>`
          : ''
      }
      <p role="status">${this.message}</p>
      ${
        this.message && !this.busy
          ? html`<button
              type="button"
              class="ui button"
              ?disabled=${this.busy}
              @click=${() => requestWorkRefresh(this)}
            >
              Refresh completion reviews
            </button>`
          : ''
      }
    </section>`;
  }
}
customElements.define('soda-factory-completion-retry', SodaFactoryCompletionRetry);
