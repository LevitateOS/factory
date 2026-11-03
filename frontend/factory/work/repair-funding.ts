import {LitElement, html} from 'lit';
import {check, id, object, readSodaJSON} from '../api/models.js';
import type {FactoryTransport} from '../api/transport.js';
import {factoryCommandId} from '../api/run-response.js';
import {repairView} from '../api/repair-response.js';
import type {RepairChange, RepairView} from '../api/repair-response.js';
import {
  validRepairChange,
  readRepairChange,
  retainRepairChange,
  sameRepairChange,
  settleRepairChange,
} from './repair-record.js';
import type {RepairAddress} from './repair-record.js';
export interface RepairContext {
  readonly transport: FactoryTransport;
  readonly repository: string;
  /** Stable admitted profile/provider/account reference. Hosts retire identity changes. */
  readonly commandScope?: (signal: AbortSignal) => Promise<string>;
}
class RepairRequestError extends Error {
  constructor(readonly status: number) {
    super(
      status === 403
        ? 'The current repository owner or administrator is required.'
        : status === 409 || status === 412
          ? 'Repair authority changed. Refresh before saving again.'
          : 'Repair funding could not be confirmed.'
    );
  }
}
export class SodaFactoryRepair extends LitElement {
  static properties = {
    view: {state: true},
    busy: {state: true},
    message: {state: true},
    pending: {state: true},
    ready: {state: true},
  };
  declare private view: RepairView | undefined;
  declare private busy: boolean;
  declare private message: string;
  declare private pending: RepairChange | undefined;
  declare private ready: boolean;
  private current: RepairContext | undefined;
  private address: RepairAddress | undefined;
  private serial = 0;
  private generation = '';
  private lifetime = new AbortController();
  private attempted = false;
  constructor() {
    super();
    this.busy = this.ready = false;
    this.message = '';
  }
  protected createRenderRoot() {
    return this;
  }
  set context(context: RepairContext | undefined) {
    if (context)
      check(
        id(context.repository) && typeof context.transport.generation === 'string' && !!context.transport.generation
      );
    if (
      context?.transport === this.current?.transport &&
      context?.repository === this.current?.repository &&
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
    this.view = this.pending = undefined;
    this.address = undefined;
    this.message = '';
    this.busy = this.ready = this.attempted = false;
    if (context) void this.load();
  }
  disconnectedCallback() {
    this.context = undefined;
    super.disconnectedCallback();
  }
  private live(serial: number, context: RepairContext): boolean {
    if (serial !== this.serial || context.transport !== this.current?.transport) return false;
    if (context.transport.generation !== this.generation) {
      this.context = undefined;
      return false;
    }
    return true;
  }
  private async identity(context: RepairContext, serial: number): Promise<boolean> {
    this.ready = false;
    try {
      if (!context.commandScope) throw Error('Missing repair identity');
      const scope = await context.commandScope(AbortSignal.any([this.lifetime.signal, AbortSignal.timeout(15000)]));
      if (!this.live(serial, context)) return false;
      const address = {scope, repository: context.repository},
        stored = readRepairChange(address);
      this.attempted = stored.pending
        ? this.address?.scope === scope && this.pending && sameRepairChange(stored.pending, this.pending)
          ? this.attempted
          : true
        : false;
      this.address = address;
      this.pending = stored.pending;
      if (stored.blocked) {
        this.message =
          'Saved repair change is unreadable or belongs to another scope. It was preserved; no new change was sent.';
        return false;
      }
      this.ready = true;
      this.message = stored.pending ? 'A repair change is unconfirmed. Retry the same change before editing.' : '';
      return true;
    } catch {
      if (this.live(serial, context))
        this.message = 'Repair account identity could not be read. Refresh before saving.';
      return false;
    }
  }
  private async request(context: RepairContext, serial: number, change?: RepairChange) {
    const response = await context.transport.request(`repositories/${context.repository}/factory/repair`, {
      method: change ? 'PUT' : 'GET',
      signal: AbortSignal.any([this.lifetime.signal, AbortSignal.timeout(15000)]),
      ...(change ? {headers: {'Content-Type': 'application/json'}, body: JSON.stringify(change)} : {}),
    });
    if (!this.live(serial, context)) throw Error('Repair context retired');
    if (response.status === 401)
      this.dispatchEvent(new CustomEvent('soda-session-invalid', {bubbles: true, composed: true}));
    if (!response.ok) throw new RepairRequestError(response.status);
    return readSodaJSON(response);
  }
  private async load(change?: RepairChange) {
    const context = this.current,
      serial = this.serial;
    if (!context || !this.live(serial, context) || this.busy) return;
    const shown = this.pending,
      scope = this.address?.scope;
    this.busy = true;
    let recorded = false;
    try {
      const admitted = await this.identity(context, serial);
      if (!this.live(serial, context)) return;
      if (change) {
        if (!admitted) return;
        if (
          scope !== this.address?.scope ||
          (shown ? !this.pending || !sameRepairChange(shown, this.pending) : this.pending)
        ) {
          this.ready = false;
          this.message = 'The saved repair change or account changed. Refresh before retrying.';
          return;
        }
        const address = this.address!;
        this.pending = change;
        const saved = retainRepairChange(address, change);
        if (saved !== 'saved') {
          this.message =
            saved === 'unavailable'
              ? 'Repair change could not be saved. No change was sent. Retry the same change when storage is available.'
              : 'The saved repair change was replaced. Refresh before retrying.';
          if (saved === 'conflict') this.ready = false;
          return;
        }
        const firstAttempt = !this.attempted;
        this.attempted = true;
        let value: unknown;
        try {
          value = await this.request(context, serial, change);
        } catch (error) {
          if (!this.live(serial, context)) return;
          if (
            firstAttempt &&
            error instanceof RepairRequestError &&
            [409, 412].includes(error.status) &&
            settleRepairChange(address, change)
          ) {
            this.pending = undefined;
            this.view = undefined;
            this.ready = false;
          }
          throw error;
        }
        if (!this.live(serial, context)) return;
        const receipt = object(value);
        check(receipt.command_id === change.command_id && receipt.revision === change.expected_revision + 1);
        if (this.address?.scope !== address.scope || !settleRepairChange(address, change)) {
          this.ready = false;
          this.message = 'The saved repair change was replaced. Refresh before retrying.';
          return;
        }
        this.pending = undefined;
        recorded = true;
      }
      const found = repairView(await this.request(context, serial), context.repository);
      if (!this.live(serial, context)) return;
      if (this.ready && this.pending && found.grant?.id === this.pending.command_id) {
        const pending = this.pending,
          grant = found.grant;
        check(
          found.revision === pending.expected_revision + 1 &&
            grant.active === pending.active &&
            grant.effect === pending.effect &&
            grant.investigator_runs === pending.investigator_runs &&
            grant.proposal_judgments === pending.proposal_judgments &&
            grant.active_minutes === pending.active_minutes
        );
        check(settleRepairChange(this.address!, pending));
        this.pending = undefined;
      }
      this.view = found;
      if (admitted)
        this.message = change
          ? 'Repair grant confirmed. Recorded consumption is preserved.'
          : this.pending
            ? 'A repair change is unconfirmed. Retry the same change before editing.'
            : '';
    } catch (error) {
      if (!this.live(serial, context)) return;
      this.ready = false;
      if (recorded) {
        this.view = undefined;
        this.message = 'Repair change recorded. Current history could not be read; refresh repair funding.';
        return;
      }
      if (error instanceof RepairRequestError && [401, 403].includes(error.status)) this.view = undefined;
      // An uncertain write remains replayable; a failed read requires refresh.
      if (change && this.pending && !(error instanceof RepairRequestError && [401, 403].includes(error.status)))
        this.ready = true;
      this.message = this.pending
        ? 'A repair change is unconfirmed. Retry the same change before editing.'
        : error instanceof RepairRequestError
          ? error.message
          : 'Repair funding could not be confirmed.';
    } finally {
      if (this.live(serial, context)) this.busy = false;
    }
  }
  private save = (event: SubmitEvent) => {
    event.preventDefault();
    if (
      !(event.currentTarget instanceof HTMLFormElement) ||
      !this.current ||
      !this.live(this.serial, this.current) ||
      !this.ready ||
      !this.view ||
      this.busy ||
      this.pending
    )
      return;
    const form = event.currentTarget;
    if (!form.reportValidity()) return;
    const data = new FormData(form),
      effect = String(data.get('effect') ?? '').trim();
    const change: RepairChange = {
      command_id: factoryCommandId(),
      expected_revision: this.view.revision,
      investigator_runs: Number(data.get('investigator_runs')),
      proposal_judgments: Number(data.get('proposal_judgments')),
      active_minutes: Number(data.get('active_minutes')),
      active: data.has('active'),
      effect,
    };
    try {
      validRepairChange(change);
    } catch {
      this.message = 'Enter whole numbers within the displayed repair limits.';
      return;
    }
    const prior = this.view.grant;
    if (
      prior &&
      (change.investigator_runs > prior.investigator_runs ||
        change.proposal_judgments > prior.proposal_judgments ||
        change.active_minutes > prior.active_minutes) &&
      !effect
    ) {
      this.message = 'Describe the effect of increasing a repair limit.';
      return;
    }
    void this.load(change);
  };
  protected render() {
    if (!this.current) return html``;
    return html`<section aria-label="Repository repair funding">
      <h3>Repository repair funding</h3>
      <p>
        Authorize a finite allowance for Strategist stall investigations and proposal judgments. Existing charges remain
        spent after failures, retries and grant changes. Each episode permits at most two cycles; a second cycle
        requires new factual evidence.
      </p>
      <p data-repair-message aria-live="polite">${this.message}</p>
      <button type="button" class="ui button" ?disabled=${this.busy} @click=${() => this.load()}>
        Refresh repair funding
      </button>
      ${this.pending ? html`<button type="button" class="ui button" ?disabled=${!this.ready || this.busy} @click=${() => this.load(this.pending)}>Retry same repair change</button>` : ''}
      ${this.view ? this.renderView(this.view) : ''}
    </section>`;
  }
  private renderView(view: RepairView) {
    const grant = view.grant;
    const runs = view.charges.filter((charge) => charge.kind === 'run').length;
    const judgments = view.charges.filter((charge) => charge.kind === 'judgment').length;
    const minutes = view.charges.reduce((total, charge) => total + charge.active_minutes, 0);
    return html`<p>
        ${grant ? (grant.active ? 'Repair grant active.' : 'Repair grant withdrawn.') : 'No repair grant has been authorized.'}
        Recorded usage: ${runs} investigator runs, ${judgments} proposal judgments, ${minutes} active minutes reserved.
      </p>
      <form @submit=${this.save}>
        <fieldset ?disabled=${!this.ready || this.busy || !!this.pending}>
          <label
            >Investigator run limit
            <input
              type="number"
              name="investigator_runs"
              min="0"
              max="4"
              step="1"
              required
              .value=${String(this.pending?.investigator_runs ?? grant?.investigator_runs ?? 0)}
          /></label>
          <label
            >Proposal judgment limit
            <input
              type="number"
              name="proposal_judgments"
              min="0"
              max="4"
              step="1"
              required
              .value=${String(this.pending?.proposal_judgments ?? grant?.proposal_judgments ?? 0)}
          /></label>
          <label
            >Active minute limit
            <input
              type="number"
              name="active_minutes"
              min="0"
              max="120"
              step="1"
              required
              .value=${String(this.pending?.active_minutes ?? grant?.active_minutes ?? 0)}
          /></label>
          <label
            ><input type="checkbox" name="active" .checked=${this.pending?.active ?? grant?.active ?? false} />
            Authorize repository repair spending</label
          >
          <label
            >Effect of this change
            <textarea name="effect" maxlength="4000" .value=${this.pending?.effect ?? ''}></textarea>
          </label>
          <button type="submit" class="ui button">Save repair grant</button>
        </fieldset>
      </form>
      <details>
        <summary>Recorded repair charges (${view.charges.length})</summary>
        <ul>
          ${view.charges.map((charge) => html`<li>${charge.kind}, episode ${charge.episode_id}, cycle ${charge.cycle}: ${charge.active_minutes} active minutes reserved${charge.failed ? ', failed' : ''}. Charge ${charge.id}.</li>`)}
        </ul>
      </details>
      <details>
        <summary>Repair episodes (${view.episodes.length})</summary>
        ${view.episodes.map(
          (episode) =>
            html`<p>Episode ${episode.episode_id}: ${episode.reason}</p>
              <ul>
                ${episode.cycles.map((cycle) => html`<li>Cycle ${cycle.number}: ${cycle.state}${cycle.reason ? ` · ${cycle.reason}` : ''}${cycle.run_id ? ` · Run ${cycle.run_id}` : ''}</li>`)}
              </ul>`
        )}
      </details>`;
  }
}
customElements.define('soda-factory-repair', SodaFactoryRepair);
