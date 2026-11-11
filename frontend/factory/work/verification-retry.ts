import {LitElement, html} from 'lit';
import type {FactoryTransport} from '../api/transport.js';
import {check, id, object, readSodaJSON} from '../api/models.js';
import {factoryCommandId, factoryRunID, factoryRunStatusResponse} from '../api/run-response.js';
import type {DeliveryRepairProgress, FactoryRunDetail, RepairState} from '../api/run-response.js';
import {deliveryRepairDecision, retryDecision} from '../api/work-response.js';
import {PendingCommandController} from '../shared/pending-command.js';
import type {ClassifyRefusal} from '../shared/pending-command.js';
import {
  readVerificationRequest,
  retainVerificationRequest,
  sameVerificationRequest,
  settleVerificationRequest,
} from './verification-record.js';
import type {PendingVerificationRequest, VerificationRequestAddress} from './verification-record.js';

/** Host-selected canonical repository and recorded issue-zero runs.
 * Retire the binding on profile/account changes; this view does not discover custody. */
export interface VerificationRetryContext {
  readonly transport: FactoryTransport;
  readonly repository: string;
  readonly runs: readonly string[];
  /** Stable admitted profile/provider-instance/account reference, never a credential. */
  readonly commandScope?: (signal: AbortSignal) => Promise<string>;
}

// Refusals returned before the retry command is recorded: pkg/factory/control/lifecycle_retry.go
// (RetryRun admission; command_conflict means a record exists) and the rolled-back transactions in
// pkg/store/factory_explicit_retry.go and factory_delivery_retry.go. Delivery repair has no command
// record to prove absence from, so it keeps every refusal.
const classifyRetryRefusal: ClassifyRefusal = (status, code) =>
  status === 409 && (code === 'retry_ineligible' || code === 'authority_ineffective') ? 'discard' : 'keep';

// Only the host-selected recorded issue-zero runs are read. The server's
// exact delivery eligibility owns this control; a native exit alone is no verdict.
export class SodaFactoryVerificationRetry extends LitElement {
  static properties = {
    entries: {state: true},
    message: {state: true},
    busy: {state: true},
    reading: {state: true},
    recoveryMessage: {state: true},
  };
  declare private entries: FactoryRunDetail[];
  declare private message: string;
  declare private busy: boolean;
  private current: VerificationRetryContext | undefined;
  private generation = '';
  private lifetime = new AbortController();
  private serial = 0;
  declare private recoveryMessage: string;
  private cmd = new PendingCommandController<PendingVerificationRequest, VerificationRequestAddress>(
    {
      read: readVerificationRequest,
      retain: retainVerificationRequest,
      settle: settleVerificationRequest,
      same: sameVerificationRequest,
    },
    () => this.requestUpdate()
  );
  /** Which transient notice `message` currently holds; set only where it is written. */
  private notice: 'loading' | 'read-failed' | undefined;
  private get pending() {
    return this.cmd.pending;
  }
  private get ready() {
    return this.cmd.ready;
  }
  private repairs = new Map<string, {cause: string; cycle: string}>();
  private refreshTimer: ReturnType<typeof setTimeout> | undefined;
  declare private reading: boolean;
  private unavailable = new Set<string>();
  constructor() {
    super();
    this.entries = [];
    this.message = '';
    this.busy = false;
    this.reading = false;
    this.recoveryMessage = '';
  }
  protected createRenderRoot() {
    return this;
  }
  set context(context: VerificationRetryContext | undefined) {
    if (context)
      check(
        id(context.repository) &&
          Array.isArray(context.runs) &&
          context.runs.length <= 64 &&
          context.runs.every(factoryRunID) &&
          new Set(context.runs).size === context.runs.length &&
          typeof context.transport.generation === 'string' &&
          !!context.transport.generation
      );
    if (
      context?.transport === this.current?.transport &&
      context?.repository === this.current?.repository &&
      context?.transport.generation === this.generation &&
      context?.runs.join('|') === this.current?.runs.join('|')
    ) {
      this.current = context;
      return;
    }
    this.lifetime.abort();
    this.lifetime = new AbortController();
    this.generation = context?.transport.generation ?? '';
    this.current = context;
    clearTimeout(this.refreshTimer);
    this.serial++;
    this.entries = [];
    this.message = '';
    this.cmd.reset();
    this.notice = undefined;
    this.recoveryMessage = '';
    this.repairs.clear();
    this.unavailable.clear();
    this.reading = false;
    this.busy = false;
    if (context) {
      void this.load(context, this.serial);
    }
  }
  disconnectedCallback() {
    this.context = undefined;
    super.disconnectedCallback();
  }
  private live(serial: number, context: VerificationRetryContext): boolean {
    if (serial !== this.serial || context.transport !== this.current?.transport) return false;
    if (context.transport.generation !== this.generation) {
      this.context = undefined;
      return false;
    }
    return true;
  }
  refresh() {
    if (this.current && !this.busy) return this.load(this.current, this.serial);
  }
  private async resolveIdentity(context: VerificationRetryContext, serial: number): Promise<boolean> {
    this.cmd.resolving();
    try {
      if (!context.commandScope) throw Error('Missing verification identity');
      const scope = await context.commandScope(AbortSignal.any([this.lifetime.signal, AbortSignal.timeout(15000)]));
      if (!this.live(serial, context)) return false;
      const blocked = this.cmd.adopt({scope, repository: context.repository}) === 'blocked';
      this.recoveryMessage = blocked
        ? 'Saved verification request is unreadable or belongs to another scope. It was preserved; no new request was sent.'
        : this.cmd.pending
          ? 'An unconfirmed verification request is saved. Retry the same request to check its recorded result.'
          : '';
      return !blocked;
    } catch {
      if (this.live(serial, context)) {
        this.cmd.unresolve();
        this.recoveryMessage = 'Verification identity could not be read. Refresh verification before retrying.';
      }
      return false;
    }
  }
  private async load(context: VerificationRetryContext, serial: number, progressOnly = false) {
    if (!this.live(serial, context) || this.reading) return;
    this.reading = true;
    clearTimeout(this.refreshTimer);
    if (!this.entries.length && !this.message) {
      this.message = 'Loading verification and repair progress…';
      this.notice = 'loading';
    }
    if (!progressOnly) await this.resolveIdentity(context, serial);
    if (!this.live(serial, context)) return;
    const selected = [
      ...new Set([
        ...context.runs,
        ...(this.pending ? [this.pending.run] : []),
        ...this.repairs.keys(),
        ...this.entries.filter((entry) => this.unsettled(entry.delivery_repair?.progress)).map((entry) => entry.id),
      ]),
    ];
    const runs = progressOnly
      ? selected.filter(
          (run) =>
            this.repairs.has(run) ||
            this.entries.some((entry) => entry.id === run && this.unsettled(entry.delivery_repair?.progress))
        )
      : selected;
    const reads = await Promise.allSettled(
      runs.map(async (run) => {
        const response = await context.transport.request(`factory/runs/${run}`, {
          signal: AbortSignal.any([this.lifetime.signal, AbortSignal.timeout(15000)]),
        });
        if (!this.live(serial, context)) throw Error('Verification context retired.');
        if (response.status === 401 || response.status === 403)
          this.entries = this.entries.filter((entry) => entry.id !== run);
        check(response.ok);
        const value = await readSodaJSON(response);
        if (!this.live(serial, context)) throw Error('Verification context retired.');
        check(object(object(value).run).id === run);
        check(object(object(value).view).issue === undefined);
        const rawRepair = object(value).delivery_repair;
        if (rawRepair && object(rawRepair).progress !== undefined) {
          const progress = object(object(rawRepair).progress);
          check(
            progress.failed_run === run &&
              factoryRunID(progress.cause_id) &&
              factoryRunID(progress.cycle_id) &&
              [
                'collecting',
                'ready',
                'investigating',
                'assessing',
                'applying',
                'adopted',
                'attention',
                'stale',
              ].includes(String(progress.state))
          );
        }
        const entry = factoryRunStatusResponse(value, run, context.repository);
        if (this.pending?.run === run) check(entry.reconciled);
        const acknowledged = this.repairs.get(run),
          progress = entry.delivery_repair?.progress;
        if (acknowledged && progress)
          check(progress.cause_id === acknowledged.cause && progress.cycle_id === acknowledged.cycle);
        if (acknowledged && !progress) entry.delivery_repair = {available: false};
        return entry;
      })
    );
    if (!this.live(serial, context)) return;
    this.reading = false;
    for (const [index, read] of reads.entries()) {
      if (read.status === 'rejected') this.unavailable.add(runs[index]!);
      else this.unavailable.delete(runs[index]!);
    }
    const entries = reads.flatMap((read) =>
      read.status === 'fulfilled' &&
      read.value.reconciled &&
      read.value.view?.repository === context.repository &&
      read.value.view.issue === undefined &&
      (read.value.verification_retry?.available ||
        read.value.delivery_repair?.available ||
        read.value.delivery_repair?.progress ||
        this.repairs.has(read.value.id) ||
        this.pending?.run === read.value.id)
        ? [read.value]
        : []
    );
    const retained = progressOnly
      ? this.entries
      : this.entries.filter((entry) => reads[runs.indexOf(entry.id)]?.status === 'rejected');
    const current = new Map(retained.map((entry) => [entry.id, entry]));
    for (const entry of entries) current.set(entry.id, entry);
    this.entries = [...current.values()];
    for (const entry of entries) if (entry.delivery_repair?.progress) this.repairs.delete(entry.id);
    if (reads.some((read) => read.status === 'rejected')) {
      this.message =
        'Could not read current verification or repair progress. Last confirmed states are shown; refresh verification to try again.';
      this.notice = 'read-failed';
      return;
    }
    if (
      entries.some((entry) => entry.delivery_repair?.progress) ||
      this.notice !== undefined
    ) {
      this.message = '';
      this.notice = undefined;
    }
    if (this.repairs.size || this.entries.some((entry) => this.unsettled(entry.delivery_repair?.progress)))
      this.refreshTimer = setTimeout(() => {
        if (this.live(serial, context) && this.isConnected) void this.load(context, serial, true);
      }, 4000);
  }
  private unsettled(progress: DeliveryRepairProgress | undefined) {
    return progress !== undefined && !['adopted', 'attention', 'stale'].includes(progress.state);
  }
  private async requestAction(run: string, action: 'retry' | 'repair_delivery') {
    const context = this.current;
    if (
      !context ||
      !this.live(this.serial, context) ||
      !this.ready ||
      this.busy ||
      this.reading ||
      this.unavailable.has(run)
    )
      return;
    const entry = this.entries.find((entry) => entry.id === run);
    if (
      !entry ||
      (!this.pending && !(action === 'retry' ? entry.verification_retry?.available : entry.delivery_repair?.available))
    )
      return;
    const shown = this.pending;
    if (shown && (shown.run !== run || shown.action !== action)) return;
    const serial = this.serial;
    this.busy = true;
    clearTimeout(this.refreshTimer);
    try {
      const admitted = await this.cmd.readmit(() => this.resolveIdentity(context, serial));
      if (admitted === 'unresolved') return;
      if (admitted === 'changed') {
        this.recoveryMessage =
          'The saved verification request or account changed. Refresh verification before retrying.';
        return;
      }
      const pending = this.pending ?? {run, command: action === 'retry' ? factoryCommandId() : '', action};
      let repair: ReturnType<typeof deliveryRepairDecision> | undefined;
      const outcome = await this.cmd.submit({
        command: pending,
        live: () => this.live(serial, context),
        classifyRefusal: action === 'retry' ? classifyRetryRefusal : () => 'keep',
        send: () =>
          context.transport.request(`factory/runs/${run}/actions`, {
            method: 'POST',
            headers: {'Content-Type': 'application/json'},
            body: JSON.stringify({action: pending.action, ...(pending.command ? {command_id: pending.command} : {})}),
            signal: AbortSignal.any([this.lifetime.signal, AbortSignal.timeout(90000)]),
          }),
        receipt: (value) => {
          if (action === 'retry') {
            const receipt = object(value);
            check(receipt.command_id === pending.command && receipt.prior_run === run && receipt.queued === true);
            retryDecision(value, pending.command, run);
          } else {
            repair = deliveryRepairDecision(value, run);
            check(factoryRunID(repair.cause_id) && factoryRunID(repair.cycle_id) && object(value).state === 'queued');
          }
        },
      });
      if (outcome.kind === 'retired') return;
      if (outcome.kind === 'not-saved') {
        this.recoveryMessage =
          outcome.result === 'unavailable'
            ? 'Verification request could not be saved. No request was sent. Retry the same request when storage is available.'
            : 'The saved verification request changed. Refresh verification before retrying.';
      } else if (outcome.kind === 'record-changed') {
        this.recoveryMessage =
          'The saved verification request changed. Refresh verification to read its current state.';
      } else if (outcome.kind === 'confirmed') {
        this.recoveryMessage = '';
        this.confirmed(run, action, repair);
      } else if (outcome.kind === 'refused' && outcome.discarded) {
        this.notice = undefined;
        this.message =
          'The verification request is no longer available. Refresh verification before requesting again.';
      } else {
        this.notice = undefined;
        this.message =
          action === 'retry'
            ? 'No Retry was confirmed. Retry this same request or refresh the verification status.'
            : 'No repair request was confirmed. Retry this same request or refresh the verification status.';
      }
    } finally {
      if (this.live(serial, context)) this.busy = false;
    }
  }
  private confirmed(
    run: string,
    action: 'retry' | 'repair_delivery',
    repair?: ReturnType<typeof deliveryRepairDecision>
  ) {
    this.notice = undefined;
    if (repair) {
      this.repairs.set(run, {cause: repair.cause_id, cycle: repair.cycle_id});
      this.entries = this.entries.map((entry) =>
        entry.id === run ? {...entry, delivery_repair: {available: false}} : entry
      );
    } else this.entries = this.entries.filter((entry) => entry.id !== run);
    this.message =
      action === 'retry'
        ? 'Verification Retry queued. The original result and spending are retained.'
        : 'Application repair requested. Diagnosis is queued; the original result and spending are retained.';
    if (repair && this.current) void this.load(this.current, this.serial);
  }
  private renderRepair(progress: DeliveryRepairProgress) {
    const states: Record<RepairState, string> = {
      collecting: 'Collecting the recorded failure and current work.',
      ready: 'Strategist diagnosis is queued.',
      investigating: 'Strategist diagnosis is in progress.',
      assessing: 'Assessing the Strategist proposal.',
      applying: 'Adopting the qualified repair graph.',
      adopted: 'Repair graph adopted. Fresh readiness and application verification are still required.',
      attention: 'Application repair needs attention.',
      stale: 'Application repair inputs changed; the retained diagnosis is stale.',
      unknown: 'This repair state is not recognized by this view.',
    };
    return html`<section aria-label="Application repair progress">
      <p role="status">${states[progress.state]}</p>
      <p>
        Cause ${progress.cause_id.slice(0, 8)} · failed verification ${progress.failed_run.slice(0, 8)} · source
        ${progress.source_commit.slice(0, 8)}
      </p>
      ${progress.run_id ? html`<p>Strategist run ${progress.run_id.slice(0, 8)}</p>` : ''}
      <ul>
        ${progress.failures.map((failure) => html`<li>${failure.check_ref}: ${failure.detail}</li>`)}
      </ul>
      ${progress.failure_count > progress.failures.length ? html`<p>Showing ${progress.failures.length} of ${progress.failure_count} recorded failures.</p>` : ''}
      ${progress.judgment ? html`<p>Judgment: ${progress.judgment.option}; acceptance probability ${progress.judgment.accept_probability}; confidence ${progress.judgment.confidence}.</p>` : ''}
      ${progress.reason ? html`<p>${progress.reason}</p>` : ''}
    </section>`;
  }
  protected render() {
    if (!this.current || (!this.entries.length && !this.message && !this.recoveryMessage)) return html``;
    return html`<section aria-label="Application verification needs attention">
      ${this.entries.length ? html`<p>Previous verification results and spending stay recorded.</p>` : ''}
      ${this.entries.map(
        (entry) => html`<div>
          Verification run ${entry.id.slice(0, 8)}
          ${entry.verification_retry?.available || (this.pending?.run === entry.id && this.pending.action === 'retry') ? html`<button type="button" class="ui button" ?disabled=${!this.ready || this.busy || this.reading || this.unavailable.has(entry.id) || (!!this.pending && (this.pending.run !== entry.id || this.pending.action !== 'retry'))} @click=${() => this.requestAction(entry.id, 'retry')}>${this.pending?.run === entry.id && this.pending.action === 'retry' ? 'Retry same verification request' : 'Retry application verification'}</button>` : ''}
          ${entry.delivery_repair?.available || (this.pending?.run === entry.id && this.pending.action === 'repair_delivery') ? html`<button type="button" class="ui button" ?disabled=${!this.ready || this.busy || this.reading || this.unavailable.has(entry.id) || (!!this.pending && (this.pending.run !== entry.id || this.pending.action !== 'repair_delivery'))} @click=${() => this.requestAction(entry.id, 'repair_delivery')}>${this.pending?.run === entry.id && this.pending.action === 'repair_delivery' ? 'Retry same repair request' : 'Request application repair'}</button>` : ''}
          ${entry.delivery_repair?.progress ? this.renderRepair(entry.delivery_repair.progress) : ''}
        </div>`
      )}
      <p role="status">${this.message}</p>
      <p role="status">${this.recoveryMessage}</p>
      <button
        type="button"
        class="ui button"
        ?disabled=${this.busy || this.reading}
        @click=${() => {
          if (this.current) void this.load(this.current, this.serial);
        }}
      >
        Refresh verification
      </button>
    </section>`;
  }
}
customElements.define('soda-factory-verification-retry', SodaFactoryVerificationRetry);
