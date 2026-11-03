import {LitElement, html} from 'lit';
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
    pending: {state: true},
    ready: {state: true},
  };
  declare private allowance: VerificationAllowance | undefined;
  declare private message: string;
  declare private busy: boolean;
  declare private runs: number;
  declare private minutes: number;
  declare private pending: VerificationAllowanceChange | undefined;
  declare private ready: boolean;
  private current: VerificationAllowanceContext | undefined;
  private address: AllowanceAddress | undefined;
  private attempted = false;
  private serial = 0;
  private generation = '';
  private lifetime = new AbortController();
  constructor() {
    super();
    this.message = '';
    this.busy = this.ready = false;
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
    if (
      context?.transport === this.current?.transport &&
      context?.repository === this.current?.repository &&
      context?.intent === this.current?.intent &&
      context?.transport.generation === this.generation
    ) {
      this.current = context;
      return;
    }
    this.lifetime.abort();
    this.lifetime = new AbortController();
    this.current = context;
    this.generation = context?.transport.generation ?? '';
    this.serial++;
    this.allowance = this.pending = undefined;
    this.address = undefined;
    this.attempted = false;
    this.message = '';
    this.busy = this.ready = false;
    if (context) void this.refresh();
  }
  disconnectedCallback() {
    this.context = undefined;
    super.disconnectedCallback();
  }
  private live(serial: number, context: VerificationAllowanceContext): boolean {
    if (serial !== this.serial || context.transport !== this.current?.transport) return false;
    if (context.transport.generation !== this.generation) {
      this.context = undefined;
      return false;
    }
    return true;
  }
  private path(context: VerificationAllowanceContext) {
    return `factory/intents/${context.intent}/verification-allowance`;
  }
  private async resolveIdentity(context: VerificationAllowanceContext, serial: number): Promise<boolean> {
    this.ready = false;
    try {
      if (!context.commandScope) throw Error('Missing allowance identity');
      const scope = await context.commandScope(AbortSignal.any([this.lifetime.signal, AbortSignal.timeout(15000)]));
      if (!this.live(serial, context)) return false;
      const address = {scope, repository: context.repository, intent: context.intent};
      const stored = readAllowanceChange(address);
      this.attempted = stored.pending
        ? this.address?.scope === scope && this.pending && sameAllowanceChange(this.pending, stored.pending)
          ? this.attempted
          : true
        : false;
      this.address = address;
      this.pending = stored.pending;
      if (stored.blocked) {
        this.message =
          'Saved verification limit change is unreadable or belongs to another scope. It was preserved; no new change was sent.';
        return false;
      }
      const response = await context.transport.request(`factory/intents/${context.intent}`, {
        signal: AbortSignal.any([this.lifetime.signal, AbortSignal.timeout(15000)]),
      });
      check(response.ok);
      const request = object(await readSodaJSON(response));
      if (!this.live(serial, context)) return false;
      check(request.intent === context.intent && request.repository === context.repository);
      this.ready = true;
      this.message = stored.pending
        ? 'An unconfirmed verification limit change is saved. Retry the same change to check its recorded result.'
        : '';
      return true;
    } catch {
      if (this.live(serial, context))
        this.message = 'Allowance identity or request scope could not be read. Refresh before increasing limits.';
      return false;
    }
  }
  async refresh() {
    const context = this.current,
      serial = this.serial;
    if (!context || !this.live(serial, context) || this.busy) return;
    this.busy = true;
    try {
      await this.resolveIdentity(context, serial);
      if (!this.live(serial, context)) return;
      const response = await context.transport.request(this.path(context), {
        signal: AbortSignal.any([this.lifetime.signal, AbortSignal.timeout(15000)]),
      });
      if (!this.live(serial, context)) return;
      if (response.status === 401 || response.status === 403) this.allowance = undefined;
      check(response.ok);
      const found = verificationAllowance(await readSodaJSON(response), context.intent);
      if (!this.live(serial, context)) return;
      this.allowance = found;
      this.runs = found.limits.runs;
      this.minutes = found.limits.active_minutes;
      if (this.ready && this.pending && found.revision === this.pending.command_id) {
        check(
          found.limits.runs === this.pending.limits.runs &&
            found.limits.active_minutes === this.pending.limits.active_minutes
        );
        if (!settleAllowanceChange(this.address!, this.pending)) throw Error('Saved change replaced');
        this.pending = undefined;
        this.message = 'Verification limits confirmed. Previous usage is retained.';
      }
    } catch {
      if (this.live(serial, context)) {
        this.ready = false;
        this.message = 'Could not read verification limits. Refresh to try again.';
      }
    } finally {
      if (this.live(serial, context)) this.busy = false;
    }
  }
  private async save(event: SubmitEvent) {
    event.preventDefault();
    const context = this.current,
      current = this.allowance,
      serial = this.serial;
    if (!context || !this.live(serial, context) || !this.ready || !current?.can_increase || this.busy) return;
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
    const shown = this.pending,
      scope = this.address?.scope;
    this.busy = true;
    try {
      if (!(await this.resolveIdentity(context, serial))) return;
      if (
        scope !== this.address?.scope ||
        (shown ? !this.pending || !sameAllowanceChange(shown, this.pending) : this.pending)
      ) {
        this.ready = false;
        this.message = 'The saved limit change or account changed. Refresh before retrying.';
        return;
      }
      const pending = this.pending ?? {command_id: factoryCommandId(), expected_revision: current.revision, limits};
      const address = this.address!;
      this.pending = pending;
      const retained = retainAllowanceChange(address, pending);
      if (retained !== 'saved') {
        this.message =
          retained === 'unavailable'
            ? 'Verification limit change could not be saved. No change was sent. Retry the same change when storage is available.'
            : 'The saved limit change changed. Refresh before retrying.';
        if (retained === 'conflict') this.ready = false;
        return;
      }
      const firstAttempt = !this.attempted;
      this.attempted = true;
      const response = await context.transport.request(this.path(context), {
        method: 'POST',
        headers: {'Content-Type': 'application/json'},
        body: JSON.stringify(pending),
        signal: AbortSignal.any([this.lifetime.signal, AbortSignal.timeout(15000)]),
      });
      if (!this.live(serial, context)) return;
      if (response.status === 409 && firstAttempt && settleAllowanceChange(address, pending)) {
        this.pending = undefined;
        this.allowance = undefined;
        this.ready = false;
        this.message = 'Verification limits changed. Refresh before retrying.';
        return;
      }
      check(response.ok);
      const confirmed = verificationAllowance(await readSodaJSON(response), context.intent);
      if (!this.live(serial, context)) return;
      check(
        confirmed.command_id === pending.command_id &&
          confirmed.limits.runs >= pending.limits.runs &&
          confirmed.limits.active_minutes >= pending.limits.active_minutes
      );
      if (address.scope !== this.address?.scope || !settleAllowanceChange(address, pending)) {
        this.ready = false;
        this.message = 'The saved limit change changed. Refresh before retrying.';
        return;
      }
      this.allowance = confirmed;
      this.pending = undefined;
      this.runs = confirmed.limits.runs;
      this.minutes = confirmed.limits.active_minutes;
      this.message = 'Verification limits saved. Previous usage is retained.';
    } catch {
      if (this.live(serial, context))
        this.message = 'The current limits were not confirmed. Retry the same change or refresh.';
    } finally {
      if (this.live(serial, context)) this.busy = false;
    }
  }
  protected render() {
    if (!this.current) return html``;
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
