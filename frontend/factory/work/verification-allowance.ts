import {LitElement, html} from 'lit';
import {ScopedBinding} from '../shared/read-state.js';
import {check, id, object, readSodaJSON} from '../api/models.js';
import type {FactoryTransport} from '../api/transport.js';
import {factoryCommandId} from '../api/run-response.js';
import {decisionID} from '../api/request-response.js';
import {verificationAllowance} from '../api/verification-allowance.js';
import type {VerificationAllowance, VerificationAllowanceChange} from '../api/verification-allowance.js';
import {
  readAllowanceChange,
  retainAllowanceChange,
  sameAllowanceChange,
  settleAllowanceChange,
} from './allowance-record.js';
import type {AllowanceAddress} from './allowance-record.js';
import {PendingCommandController} from '../shared/pending-command.js';
import type {ClassifyRefusal} from '../shared/pending-command.js';

// Unchanged from the previous first-send rule: a 409 on the first send refuses the change.
const classifyRefusal: ClassifyRefusal = (status) => (status === 409 ? 'discard' : 'keep');

export interface VerificationAllowanceContext {
  readonly transport: FactoryTransport;
  readonly repository: string;
  readonly intent: string;
  /** Stable admitted profile/provider/account reference; never a credential. */
  readonly commandScope?: (signal: AbortSignal) => Promise<string>;
}
/** An explicit owner limit change never refunds usage or starts verification. */
export class SodaFactoryVerificationAllowance extends LitElement {
  static properties = {
    allowance: {state: true},
    message: {state: true},
    busy: {state: true},
    runs: {state: true},
    minutes: {state: true},
  };
  declare private allowance: VerificationAllowance | undefined;
  declare private message: string;
  declare private busy: boolean;
  declare private runs: number;
  declare private minutes: number;
  private cmd = new PendingCommandController<VerificationAllowanceChange, AllowanceAddress>(
    {read: readAllowanceChange, retain: retainAllowanceChange, settle: settleAllowanceChange, same: sameAllowanceChange},
    () => this.requestUpdate()
  );
  private get pending() {
    return this.cmd.pending;
  }
  private get ready() {
    return this.cmd.ready;
  }
  private bound = new ScopedBinding<VerificationAllowanceContext>(
    (a, b) => a.transport === b.transport && a.repository === b.repository && a.intent === b.intent,
    () => {
      this.context = undefined;
    }
  );
  constructor() {
    super();
    this.message = '';
    this.busy = false;
    this.runs = 2;
    this.minutes = 60;
  }
  protected createRenderRoot() {
    return this;
  }
  set context(context: VerificationAllowanceContext | undefined) {
    if (context)
      check(
        id(context.repository) &&
          decisionID(context.intent) &&
          typeof context.transport.generation === 'string' &&
          !!context.transport.generation
      );
    if (this.bound.bind(context) === 'same') return;
    this.allowance = undefined;
    this.cmd.reset();
    this.message = '';
    this.busy = false;
    if (context) void this.refresh();
  }
  disconnectedCallback() {
    this.context = undefined;
    super.disconnectedCallback();
  }
  private path(context: VerificationAllowanceContext) {
    return `factory/intents/${context.intent}/verification-allowance`;
  }
  private async resolveIdentity(context: VerificationAllowanceContext, serial: number): Promise<boolean> {
    this.cmd.resolving();
    try {
      if (!context.commandScope) throw Error('Missing allowance identity');
      const scope = await context.commandScope(AbortSignal.any([this.bound.signal, AbortSignal.timeout(15000)]));
      if (!this.bound.live(serial, context)) return false;
      if (this.cmd.adopt({scope, repository: context.repository, intent: context.intent}) === 'blocked') {
        this.message =
          'Saved verification limit change is unreadable or belongs to another scope. It was preserved; no new change was sent.';
        return false;
      }
      const response = await context.transport.request(`factory/intents/${context.intent}`, {
        signal: AbortSignal.any([this.bound.signal, AbortSignal.timeout(15000)]),
      });
      check(response.ok);
      const request = object(await readSodaJSON(response));
      if (!this.bound.live(serial, context)) return false;
      check(request.intent === context.intent && request.repository === context.repository);
      this.message = this.cmd.pending
        ? 'An unconfirmed verification limit change is saved. Retry the same change to check its recorded result.'
        : '';
      return true;
    } catch {
      if (this.bound.live(serial, context)) {
        this.cmd.unresolve(true);
        this.message = 'Allowance identity or request scope could not be read. Refresh before increasing limits.';
      }
      return false;
    }
  }
  async refresh() {
    const context = this.bound.current,
      serial = this.bound.serial;
    if (!context || !this.bound.live(serial, context) || this.busy) return;
    this.busy = true;
    try {
      await this.resolveIdentity(context, serial);
      if (!this.bound.live(serial, context)) return;
      const response = await context.transport.request(this.path(context), {
        signal: AbortSignal.any([this.bound.signal, AbortSignal.timeout(15000)]),
      });
      if (!this.bound.live(serial, context)) return;
      if (response.status === 401 || response.status === 403) this.allowance = undefined;
      check(response.ok);
      const found = verificationAllowance(await readSodaJSON(response), context.intent);
      if (!this.bound.live(serial, context)) return;
      this.allowance = found;
      this.runs = found.limits.runs;
      this.minutes = found.limits.active_minutes;
      if (this.ready && this.pending && found.revision === this.pending.command_id) {
        check(
          found.limits.runs === this.pending.limits.runs &&
            found.limits.active_minutes === this.pending.limits.active_minutes
        );
        if (!this.cmd.settleObserved(this.pending)) throw Error('Saved change replaced');
        this.message = 'Verification limits confirmed. Previous usage is retained.';
      }
    } catch {
      if (this.bound.live(serial, context)) {
        this.cmd.unresolve(true);
        this.message = 'Could not read verification limits. Refresh to try again.';
      }
    } finally {
      if (this.bound.live(serial, context)) this.busy = false;
    }
  }
  private async save(event: SubmitEvent) {
    event.preventDefault();
    const context = this.bound.current,
      current = this.allowance,
      serial = this.bound.serial;
    if (!context || !this.bound.live(serial, context) || !this.ready || !current?.can_increase || this.busy) return;
    const limits = {runs: this.runs, active_minutes: this.minutes};
    if (
      !this.pending &&
      (![limits.runs, limits.active_minutes].every(
        (value) => Number.isSafeInteger(value) && value > 0 && value <= 2147483647
      ) ||
        limits.runs < current.limits.runs ||
        limits.active_minutes < current.limits.active_minutes ||
        (limits.runs === current.limits.runs && limits.active_minutes === current.limits.active_minutes))
    ) {
      this.message = 'Increase at least one limit; previous usage stays recorded.';
      return;
    }
    this.busy = true;
    try {
      const admitted = await this.cmd.readmit(() => this.resolveIdentity(context, serial));
      if (admitted === 'unresolved') return;
      if (admitted === 'changed') {
        this.message = 'The saved limit change or account changed. Refresh before retrying.';
        return;
      }
      const pending = this.pending ?? {command_id: factoryCommandId(), expected_revision: current.revision, limits};
      let confirmed: VerificationAllowance | undefined;
      const outcome = await this.cmd.submit({
        command: pending,
        live: () => this.bound.live(serial, context),
        classifyRefusal,
        send: () =>
          context.transport.request(this.path(context), {
            method: 'POST',
            headers: {'Content-Type': 'application/json'},
            body: JSON.stringify(pending),
            signal: AbortSignal.any([this.bound.signal, AbortSignal.timeout(15000)]),
          }),
        receipt: (body) => {
          confirmed = verificationAllowance(body, context.intent);
          check(
            confirmed.command_id === pending.command_id &&
              confirmed.limits.runs >= pending.limits.runs &&
              confirmed.limits.active_minutes >= pending.limits.active_minutes
          );
        },
      });
      if (outcome.kind === 'retired') return;
      if (outcome.kind === 'not-saved')
        this.message =
          outcome.result === 'unavailable'
            ? 'Verification limit change could not be saved. No change was sent. Retry the same change when storage is available.'
            : 'The saved limit change changed. Refresh before retrying.';
      else if (outcome.kind === 'record-changed') this.message = 'The saved limit change changed. Refresh before retrying.';
      else if (outcome.kind === 'refused' && outcome.discarded) {
        this.allowance = undefined;
        this.message = 'Verification limits changed. Refresh before retrying.';
      } else if (outcome.kind === 'confirmed' && confirmed) {
        this.allowance = confirmed;
        this.runs = confirmed.limits.runs;
        this.minutes = confirmed.limits.active_minutes;
        this.message = 'Verification limits saved. Previous usage is retained.';
      } else this.message = 'The current limits were not confirmed. Retry the same change or refresh.';
    } finally {
      if (this.bound.live(serial, context)) this.busy = false;
    }
  }
  protected render() {
    if (!this.bound.current) return html``;
    const current = this.allowance,
      disabled = !this.ready || this.busy || this.pending !== undefined;
    return html`<section aria-label="Verification allowance">
      ${current ? html`<p>${current.consumption.runs} of ${current.limits.runs} verification runs used; ${current.consumption.active_seconds} active seconds used of ${current.limits.active_minutes} minutes.</p>` : ''}
      ${
        current?.can_increase
          ? html`<form @submit=${this.save}>
              <label
                >Total verification runs
                <input
                  name="verification-runs"
                  type="number"
                  min=${current.limits.runs}
                  step="1"
                  .value=${String(this.pending?.limits.runs ?? this.runs)}
                  ?disabled=${disabled}
                  @input=${(event: Event) => {
                    this.runs = Number((event.target as HTMLInputElement).value);
                  }}
              /></label>
              <label
                >Total active minutes
                <input
                  name="verification-minutes"
                  type="number"
                  min=${current.limits.active_minutes}
                  step="1"
                  .value=${String(this.pending?.limits.active_minutes ?? this.minutes)}
                  ?disabled=${disabled}
                  @input=${(event: Event) => {
                    this.minutes = Number((event.target as HTMLInputElement).value);
                  }}
              /></label>
              <p>
                Previous usage stays recorded. Each run is still capped at 30 minutes; repository and connection funding
                also apply.
              </p>
              <button type="submit" class="ui button" ?disabled=${!this.ready || this.busy}>
                ${this.pending ? 'Retry same limit change' : 'Increase verification limits'}
              </button>
            </form>`
          : ''
      }
      <p role="status">${this.message}</p>
      <button
        type="button"
        class="ui button"
        ?disabled=${this.busy}
        @click=${() => {
          void this.refresh();
        }}
      >
        Refresh verification limits
      </button>
    </section>`;
  }
}
customElements.define('soda-factory-verification-allowance', SodaFactoryVerificationAllowance);
