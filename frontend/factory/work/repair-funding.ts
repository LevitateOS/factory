import {LitElement, html} from 'lit';
import {ScopedBinding} from '../shared/read-state.js';
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
import {PendingCommandController} from '../shared/pending-command.js';
import type {ClassifyRefusal} from '../shared/pending-command.js';

// Unchanged from the previous first-send rule: 409 and 412 on the first send refuse the change.
const classifyRefusal: ClassifyRefusal = (status) => ([409, 412].includes(status) ? 'discard' : 'keep');
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
  };
  declare private view: RepairView | undefined;
  declare private busy: boolean;
  declare private message: string;
  private cmd = new PendingCommandController<RepairChange, RepairAddress>(
    {read: readRepairChange, retain: retainRepairChange, settle: settleRepairChange, same: sameRepairChange},
    () => this.requestUpdate()
  );
  private get pending() {
    return this.cmd.pending;
  }
  private get ready() {
    return this.cmd.ready;
  }
  private bound = new ScopedBinding<RepairContext>(
    (a, b) => a.transport === b.transport && a.repository === b.repository,
    () => {
      this.context = undefined;
    }
  );
  constructor() {
    super();
    this.busy = false;
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
    if (this.bound.bind(context) === 'same') return;
    this.view = undefined;
    this.cmd.reset();
    this.message = '';
    this.busy = false;
    if (context) void this.load();
  }
  disconnectedCallback() {
    this.context = undefined;
    super.disconnectedCallback();
  }
  private async identity(context: RepairContext, serial: number): Promise<boolean> {
    this.cmd.resolving();
    try {
      if (!context.commandScope) throw Error('Missing repair identity');
      const scope = await context.commandScope(AbortSignal.any([this.bound.signal, AbortSignal.timeout(15000)]));
      if (!this.bound.live(serial, context)) return false;
      if (this.cmd.adopt({scope, repository: context.repository}) === 'blocked') {
        this.message =
          'Saved repair change is unreadable or belongs to another scope. It was preserved; no new change was sent.';
        return false;
      }
      this.message = this.pending ? 'A repair change is unconfirmed. Retry the same change before editing.' : '';
      return true;
    } catch {
      if (this.bound.live(serial, context)) {
        this.cmd.unresolve(true);
        this.message = 'Repair account identity could not be read. Refresh before saving.';
      }
      return false;
    }
  }
  private async request(context: RepairContext, serial: number) {
    const response = await context.transport.request(`repositories/${context.repository}/factory/repair`, {
      method: 'GET',
      signal: AbortSignal.any([this.bound.signal, AbortSignal.timeout(15000)]),
    });
    if (!this.bound.live(serial, context)) throw Error('Repair context retired');
    if (response.status === 401)
      this.dispatchEvent(new CustomEvent('soda-session-invalid', {bubbles: true, composed: true}));
    if (!response.ok) throw new RepairRequestError(response.status);
    return readSodaJSON(response);
  }
  private async load(change?: RepairChange) {
    const context = this.bound.current,
      serial = this.bound.serial;
    if (!context || !this.bound.live(serial, context) || this.busy) return;
    this.busy = true;
    let recorded = false;
    try {
      let admitted = true;
      if (change) {
        const readmitted = await this.cmd.readmit(() => this.identity(context, serial));
        if (!this.bound.live(serial, context) || readmitted === 'unresolved') return;
        if (readmitted === 'changed') {
          this.message = 'The saved repair change or account changed. Refresh before retrying.';
          return;
        }
      } else {
        admitted = await this.identity(context, serial);
        if (!this.bound.live(serial, context)) return;
      }
      if (change) {
        const outcome = await this.cmd.submit({
          command: change,
          live: () => this.bound.live(serial, context),
          classifyRefusal,
          send: () =>
            context.transport.request(`repositories/${context.repository}/factory/repair`, {
              method: 'PUT',
              signal: AbortSignal.any([this.bound.signal, AbortSignal.timeout(15000)]),
              headers: {'Content-Type': 'application/json'},
              body: JSON.stringify(change),
            }),
          receipt: (value) => {
            const receipt = object(value);
            check(receipt.command_id === change.command_id && receipt.revision === change.expected_revision + 1);
          },
        });
        if (outcome.kind === 'retired') return;
        if (outcome.kind === 'not-saved') {
          this.message =
            outcome.result === 'unavailable'
              ? 'Repair change could not be saved. No change was sent. Retry the same change when storage is available.'
              : 'The saved repair change was replaced. Refresh before retrying.';
          return;
        }
        if (outcome.kind === 'refused') {
          if (outcome.status === 401)
            this.dispatchEvent(new CustomEvent('soda-session-invalid', {bubbles: true, composed: true}));
          if (outcome.discarded) this.view = undefined;
          throw new RepairRequestError(outcome.status);
        }
        if (outcome.kind === 'unknown') throw outcome.error;
        if (outcome.kind === 'record-changed') {
          this.message = 'The saved repair change was replaced. Refresh before retrying.';
          return;
        }
        recorded = true;
      }
      const found = repairView(await this.request(context, serial), context.repository);
      if (!this.bound.live(serial, context)) return;
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
        check(this.cmd.settleObserved(pending));
      }
      this.view = found;
      if (admitted)
        this.message = change
          ? 'Repair grant confirmed. Recorded consumption is preserved.'
          : this.pending
            ? 'A repair change is unconfirmed. Retry the same change before editing.'
            : '';
    } catch (error) {
      if (!this.bound.live(serial, context)) return;
      const denied = error instanceof RepairRequestError && [401, 403].includes(error.status);
      // An uncertain write remains replayable; a failed read requires refresh.
      if (recorded || !(change && this.pending && !denied)) this.cmd.unresolve(true);
      if (recorded) {
        this.view = undefined;
        this.message = 'Repair change recorded. Current history could not be read; refresh repair funding.';
        return;
      }
      if (denied) this.view = undefined;
      this.message = this.pending
        ? 'A repair change is unconfirmed. Retry the same change before editing.'
        : error instanceof RepairRequestError
          ? error.message
          : 'Repair funding could not be confirmed.';
    } finally {
      if (this.bound.live(serial, context)) this.busy = false;
    }
  }
  private save = (event: SubmitEvent) => {
    event.preventDefault();
    if (
      !(event.currentTarget instanceof HTMLFormElement) ||
      !this.bound.current ||
      !this.bound.live(this.bound.serial, this.bound.current) ||
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
    if (!this.bound.current) return html``;
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
