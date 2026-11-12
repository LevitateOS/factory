import {LitElement, html} from 'lit';
import type {FactoryTransport} from '../api/transport.js';
import {readSodaJSON} from '../api/models.js';
import {factoryCommandId} from '../api/run-response.js';
import {externalPullAdoption, sameExternalPull, type ExternalPullAdoption} from '../api/external-pull.js';
import {PendingRecord} from '../shared/retained-record.js';
import {PendingCommandController} from '../shared/pending-command.js';
import type {ClassifyRefusal} from '../shared/pending-command.js';

// Only preparation_changed proves refusal before any live or uncertain command-owned
// preparation. preparation_pending preserves that preparation's original command for
// recovery, including after reload; command_conflict also keeps the retained identity.
const classifyRefusal: ClassifyRefusal = (status, code) =>
  status === 409 && code === 'preparation_changed' ? 'discard' : 'keep';

interface Context {
  transport: FactoryTransport;
  repository: string;
  issue: string;
  available: boolean;
  commandScope?: (signal: AbortSignal) => Promise<string>;
}
interface Capture {command: string; request: ExternalPullAdoption}
function decode(raw: string): Capture | undefined {
  const value: unknown = JSON.parse(raw);
  if (!value || typeof value !== 'object' || Array.isArray(value)) return undefined;
  const row = value as Record<string, unknown>, request = externalPullAdoption(row.request);
  return Object.keys(row).length === 2 && typeof row.command === 'string' && /^[a-f0-9]{32}$/.test(row.command) && request ? {command: row.command, request} : undefined;
}

// Selection is a read; only the explicit funded command below creates work.
// The existing retained-command owner fences a lost API reply across reloads.
export class SodaFactoryExternalPull extends LitElement {
  static properties = {busy: {state: true}, message: {state: true}, offer: {state: true}, forkApproved: {state: true}};
  declare private busy: boolean;
  declare private message: string;
  declare private offer: ExternalPullAdoption | undefined;
  declare private forkApproved: boolean;
  private cmd = new PendingCommandController<Capture, {scope: string}>(
    {
      read: () => this.record?.read() ?? {pending: undefined, blocked: true, durable: false},
      retain: (_, pending) => this.record?.retain(pending) ?? 'unavailable',
      settle: (_, pending) => this.record?.settle(pending) ?? false,
      same: (a, b) => a.command === b.command && sameExternalPull(a.request, b.request),
    },
    () => this.requestUpdate()
  );
  private get pending() {
    return this.cmd.pending;
  }
  private get ready() {
    return this.cmd.ready;
  }
  private current: Context | undefined;
  private generation = '';
  private serial = 0;
  private lifetime = new AbortController();
  private scope = '';
  private record: PendingRecord<Capture> | undefined;
  constructor() { super(); this.busy = false; this.message = ''; this.forkApproved = false; }
  protected createRenderRoot() { return this; }
  set context(context: Context | undefined) {
    const prior = this.current; this.current = context;
    if (context?.transport === prior?.transport && context?.transport.generation === this.generation && context?.repository === prior?.repository && context?.issue === prior?.issue && context?.commandScope === prior?.commandScope) { this.requestUpdate(); return; }
    this.generation = context?.transport.generation ?? '';
    this.lifetime.abort(); this.lifetime = new AbortController(); this.serial++;
    this.record = undefined; this.scope = ''; this.offer = undefined; this.forkApproved = false; this.cmd.reset(); this.busy = false; this.message = '';
    if (context) void this.recover(context, this.serial);
  }
  disconnectedCallback() { super.disconnectedCallback(); this.lifetime.abort(); this.serial++; }
  connectedCallback() { super.connectedCallback(); if (this.lifetime.signal.aborted) { this.lifetime = new AbortController(); if (this.current) void this.recover(this.current, ++this.serial); } }
  private live(serial: number) { return serial === this.serial && !this.lifetime.signal.aborted; }
  private async recover(context: Context, serial: number) {
    try {
      const scope = await context.commandScope?.(this.lifetime.signal);
      if (!this.live(serial)) return;
      if (!scope || scope.length > 1024 || /[\u0000-\u001f\u007f]/.test(scope)) throw Error('scope');
      this.scope = scope;
      this.record = new PendingRecord<Capture>(`soda-external-pull:v1:${encodeURIComponent(scope)}:${context.repository}:${context.issue}`, 8192, decode, capture => JSON.stringify(capture),
        (a, b) => a.command === b.command && sameExternalPull(a.request, b.request));
      const blocked = this.cmd.adopt({scope}) === 'blocked', saved = this.cmd.pending;
      if (blocked || saved && (saved.request.repository !== context.repository || saved.request.issue !== context.issue)) throw Error('capture');
    } catch { this.cmd.unresolve(); if (this.live(serial)) this.message = 'The retained adoption request is unavailable. No new request was sent.'; }
  }
  private async post(context: Context, body: unknown, signal: AbortSignal) {
    const response = await context.transport.request(`repositories/${context.repository}/factory/actions`, {method: 'POST', headers: {'Content-Type': 'application/json'}, body: JSON.stringify(body), signal: AbortSignal.any([signal, AbortSignal.timeout(90000)])});
    const value: unknown = await readSodaJSON(response);
    if (!response.ok) throw Error('request');
    return value;
  }
  private async inspect(event: SubmitEvent) {
    event.preventDefault();
    const context = this.current, serial = this.serial, signal = this.lifetime.signal;
    if (!context?.available || !this.ready || this.busy || this.pending) return;
    const form = event.currentTarget as HTMLFormElement, number = new FormData(form).get('number');
    if (typeof number !== 'string' || !/^[1-9][0-9]{0,9}$/.test(number)) return;
    this.busy = true; this.offer = undefined; this.forkApproved = false; this.message = '';
    try {
      const value = await this.post(context, {action: 'inspect-external-pr', external_selection: {issue: context.issue, number}}, signal);
      if (!this.live(serial)) return;
      const offer = externalPullAdoption(value);
      if (!offer || offer.repository !== context.repository || offer.issue !== context.issue || offer.pr_number !== number) throw Error('scope');
      this.offer = offer;
    } catch { if (this.live(serial)) this.message = 'PR selection is unavailable. It needs current accepted inputs, permissions and an approved source preparation at the PR base.'; }
    finally { if (this.live(serial)) this.busy = false; }
  }
  private async adopt() {
    const context = this.current, record = this.record, serial = this.serial, signal = this.lifetime.signal;
    if (!context?.available || !this.ready || !record || this.busy || !this.pending && !this.offer) return;
    if (!this.pending && this.offer?.fork_consent && !this.forkApproved) return;
    this.busy = true; this.message = '';
    try {
      if (await context.commandScope?.(signal) !== this.scope || !this.live(serial)) return;
      const capture = this.pending ?? {command: factoryCommandId(), request: this.offer!};
      let planned = false;
      const outcome = await this.cmd.submit({command: capture, live: () => this.live(serial), classifyRefusal, forgetUnsaved: true,
        send: () => context.transport.request(`repositories/${context.repository}/factory/actions`, {method: 'POST', headers: {'Content-Type': 'application/json'}, body: JSON.stringify({action: 'adopt-external-pr', command_id: capture.command, external_pull: capture.request}), signal: AbortSignal.any([signal, AbortSignal.timeout(90000)])}),
        receipt: (value) => {
          const admission = value as Record<string, unknown>;
          const admittedRequest = externalPullAdoption(admission?.request);
          if (!admission || admission.command_id !== capture.command || !admittedRequest || !sameExternalPull(admittedRequest,capture.request)) throw Error('receipt');
          if (admission.planning !== undefined) {
            const planning = admission.planning as Record<string, unknown>;
            if (capture.request.plan_assignment !== '' || admission.publication !== undefined || !planning || planning.assignment !== capture.command || typeof planning.run !== 'string' || !/^[a-f0-9]{32}$/.test(planning.run) || !['assigned','finished'].includes(String(planning.stage))) throw Error('receipt');
            planned = true;
            return;
          }
          const row = admission.publication as Record<string, unknown>, origin = row?.external as Record<string, unknown> | undefined;
          if (!row || row.id !== capture.command || row.assignment_id !== capture.command || row.repository !== context.repository || row.issue !== context.issue || row.acceptance !== capture.request.acceptance || !origin || origin.command_id !== capture.command) throw Error('receipt');
          const confirmed = externalPullAdoption({...capture.request, repository: row.repository, issue: row.issue, acceptance: row.acceptance, pr_number: String(row.pr_number), pr_id: String(row.pr_id), target: origin.target});
          const authority = row.authority as Record<string, unknown> | undefined;
          if (!confirmed || !sameExternalPull(confirmed, capture.request) || authority?.sponsorship_connection !== capture.request.connection) throw Error('receipt');
        }});
      if (outcome.kind === 'retired') return;
      if (outcome.kind === 'confirmed') {
        this.offer = undefined;
        this.message = planned ? 'Planning command admitted. Work retains its run, charges and subsequent PR adoption; no initial Builder is started.' : 'External PR adopted with the captured source and finite review/correction allowance.';
        this.dispatchEvent(new CustomEvent('soda-factory-work-refresh', {bubbles: true, composed: true}));
        return;
      }
      if (outcome.kind === 'refused' && outcome.discarded) { this.offer = undefined; this.message = 'The adoption request was refused before it was recorded. Inspect the PR again.'; return; }
      this.message = this.pending ? 'The original request remains retained. Recover it before selecting another PR; no replacement command was issued.' : 'Adoption was not sent because its captured account or local record is unavailable.';
    } catch { if (this.live(serial)) this.message = this.pending ? 'The original request remains retained. Recover it before selecting another PR; no replacement command was issued.' : 'Adoption was not sent because its captured account or local record is unavailable.'; }
    finally { if (this.live(serial)) this.busy = false; }
  }
  protected render() {
    if (!this.current?.available && !this.pending) return html``;
    const offer = this.pending?.request ?? this.offer;
    return html`<details><summary>Adopt an external pull request</summary>
      <p>Review an existing PR for this accepted issue. A fresh Architect pass is funded when no current plan can be reused. Fork PRs require distinct head and base branch names. Corrections require permission to push to the original PR branch.</p>
      <form @submit=${(event: SubmitEvent) => this.inspect(event)}><label>Pull request number <input name="number" inputmode="numeric" pattern="[1-9][0-9]*" required ?disabled=${this.busy || !!this.pending}></label>
        <button type="submit" ?disabled=${this.busy || !this.ready || !!this.pending || !this.current?.available}>Inspect PR</button></form>
      ${offer ? html`<p>PR #${offer.pr_number}, head ${offer.target.head_oid}, base ${offer.target.base_oid}. ${offer.plan_assignment === '' ? 'Includes fresh planning.' : 'Reuses the completed issue plan.'} Funding source: ${offer.connection}. Up to ${offer.limits.active_minutes} minutes per run, ${offer.limits.review_rounds} reviews and ${offer.limits.correction_cycles} corrections.</p>
        ${offer.fork_consent && !this.pending ? html`<label><input type="checkbox" name="fork_consent" .checked=${this.forkApproved} @change=${(event: Event) => { this.forkApproved = (event.target as HTMLInputElement).checked; }} ?disabled=${this.busy}>Allow Factory to execute this fork's code at the captured commit using the selected funding source.</label>` : ''}
        <button type="button" @click=${() => this.adopt()} ?disabled=${this.busy || !this.ready || !this.current?.available || !this.pending && offer.fork_consent && !this.forkApproved}>${this.pending ? 'Recover original adoption' : offer.plan_assignment === '' ? 'Plan, adopt and fund review/correction' : 'Adopt and fund review/correction'}</button>` : ''}
      <p role="status">${this.message}</p></details>`;
  }
}
customElements.define('soda-factory-external-pull', SodaFactoryExternalPull);
