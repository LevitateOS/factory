import {LitElement, html} from 'lit';
import {object, readSodaJSON} from '../api/models.js';
import {factoryCommandId} from '../api/run-response.js';
import type {FactoryTransport} from '../api/transport.js';
import {
  retainedCandidateFunding,
  type RetainedCandidateFunding,
  type FactoryIssueDetail,
} from '../api/work-response.js';
import {PendingRecord} from '../shared/retained-record.js';

interface PublicationContext {
  transport: FactoryTransport;
  repository: string;
  issue: string;
  detail?: FactoryIssueDetail;
  commandScope?: (signal: AbortSignal) => Promise<string>;
}
interface Capture {
  funding?: RetainedCandidateFunding;
  command: string;
  publication: string;
  run: string;
  candidate: string;
}
function valid(value: unknown): value is Capture {
  if (!value || typeof value !== 'object' || Array.isArray(value)) return false;
  const r = value as Record<string, unknown>;
  return (
    Object.keys(r).length === (r.funding === undefined ? 4 : 5) &&
    (r.funding === undefined ||
      (retainedCandidateFunding(r.funding) !== undefined &&
        r.publication === (r.funding as RetainedCandidateFunding).publication &&
        r.candidate === (r.funding as RetainedCandidateFunding).candidate)) &&
    ['command', 'publication', 'run'].every((k) => typeof r[k] === 'string' && /^[a-f0-9]{32}$/.test(r[k] as string)) &&
    typeof r.candidate === 'string' &&
    /^[a-f0-9]{40,64}$/.test(r.candidate)
  );
}

/** A fresh command reuses the candidate; a lost reply reuses this same command. */
export class SodaFactoryRetainedPublication extends LitElement {
  static properties = {busy: {state: true}, ready: {state: true}, message: {state: true}, pending: {state: true}};
  declare private busy: boolean;
  declare private ready: boolean;
  declare private message: string;
  declare private pending: Capture | undefined;
  private current: PublicationContext | undefined;
  private generation = '';
  private serial = 0;
  private lifetime = new AbortController();
  private record: PendingRecord<Capture> | undefined;
  constructor() {
    super();
    this.busy = this.ready = false;
    this.message = '';
  }
  protected createRenderRoot() {
    return this;
  }
  set context(context: PublicationContext | undefined) {
    const prior = this.current;
    this.current = context;
    if (
      context?.transport === prior?.transport &&
      context?.transport.generation === this.generation &&
      context?.repository === prior?.repository &&
      context?.issue === prior?.issue
    ) {
      this.requestUpdate();
      return;
    }
    this.lifetime.abort();
    this.lifetime = new AbortController();
    this.generation = context?.transport.generation ?? '';
    this.serial++;
    this.record = undefined;
    this.pending = undefined;
    this.ready = this.busy = false;
    this.message = '';
    if (context) void this.recover(context, this.serial);
  }
  disconnectedCallback() {
    super.disconnectedCallback();
    this.lifetime.abort();
    this.serial++;
  }
  connectedCallback() {
    super.connectedCallback();
    if (this.lifetime.signal.aborted) {
      this.lifetime = new AbortController();
      if (this.current) void this.recover(this.current, ++this.serial);
    }
  }
  private live(serial: number) {
    return serial === this.serial && !this.lifetime.signal.aborted;
  }
  private async recover(context: PublicationContext, serial: number) {
    try {
      const scope = await context.commandScope?.(this.lifetime.signal);
      if (!this.live(serial)) return;
      if (!scope || scope.length > 1024 || /[\u0000-\u001f\u007f]/.test(scope)) throw Error('scope');
      this.record = new PendingRecord<Capture>(
        `soda-retained-publication:v1:${encodeURIComponent(scope)}:${context.repository}:${context.issue}`,
        4096,
        (raw) => {
          const value: unknown = JSON.parse(raw);
          return valid(value) ? value : undefined;
        },
        (value) => JSON.stringify(value),
        (a, b) => JSON.stringify(a) === JSON.stringify(b)
      );
      const saved = this.record.read();
      if (saved.blocked) throw Error('saved record');
      this.pending = saved.pending;
      this.ready = true;
    } catch {
      if (this.live(serial)) this.message = 'The saved publication request is unavailable. No new request was sent.';
    }
  }
  private async publish() {
    const context = this.current,
      record = this.record,
      serial = this.serial;
    if (!context || !record || !this.ready || this.busy) return;
    const publication = context.detail?.publication;
    const funding = context.detail?.capabilities.fund_retained;
    if (
      !this.pending &&
      (!publication || (!context.detail?.capabilities.publish_retained?.available && !funding?.available))
    )
      return;
    const captured = record.read();
    if (captured.blocked) {
      this.ready = false;
      return;
    }
    const pending =
      captured.pending ??
      this.pending ??
      (publication && {
        command: factoryCommandId(),
        publication: publication.id,
        run: publication.run,
        candidate: publication.candidate,
        ...(funding?.available ? {funding: structuredClone(funding.funding)} : {}),
      });
    if (!pending || !valid(pending) || record.retain(pending) !== 'saved') {
      this.message = 'The publication identity could not be saved. No request was sent.';
      return;
    }
    this.pending = pending;
    this.busy = true;
    this.message = pending.funding
      ? 'Funding requested; waiting for confirmation.'
      : 'Publication requested; waiting for confirmation.';
    try {
      const response = await context.transport.request(`factory/runs/${pending.run}/actions`, {
        method: 'POST',
        headers: {'Content-Type': 'application/json'},
        body: JSON.stringify({
          action: pending.funding ? 'fund-retained-candidate' : 'publish-retained-candidate',
          ...(pending.funding ? {funding: pending.funding} : {}),
          command_id: pending.command,
          publication: pending.publication,
        }),
        signal: AbortSignal.any([this.lifetime.signal, AbortSignal.timeout(90000)]),
      });
      const receipt = object(await readSodaJSON(response));
      if (!this.live(serial)) return;
      if (
        !response.ok ||
        (pending.funding
          ? receipt.command_id !== pending.command ||
            JSON.stringify(receipt.funding) !== JSON.stringify(pending.funding)
          : receipt.id !== pending.command ||
            receipt.assignment_id !== pending.command ||
            receipt.run !== pending.run ||
            receipt.candidate !== pending.candidate)
      )
        throw Error('unconfirmed');
      const saved = record.read();
      if (saved.blocked || saved.pending?.command !== pending.command) throw Error('changed record');
      record.settle(pending);
      this.pending = undefined;
      this.message = pending.funding
        ? 'Finite review/correction funding recorded on the selected source. Earlier charges and publication authority remain unchanged.'
        : 'Fresh publication recorded. Review and correction require separate funding.';
      this.dispatchEvent(new CustomEvent('soda-factory-work-refresh', {bubbles: true, composed: true}));
    } catch {
      if (this.live(serial))
        this.message = `${pending.funding ? 'Funding' : 'Publication'} was not confirmed. The saved request is preserved; check the same request to recover its result.`;
    } finally {
      if (this.live(serial)) this.busy = false;
    }
  }
  protected render() {
    const publication = this.current?.detail?.publication;
    const offer = this.current?.detail?.capabilities.fund_retained;
    const funding = this.pending?.funding ?? offer?.funding;
    if (!this.pending && publication?.stage !== 'fenced' && !offer?.available) return html``;
    return html`<section aria-label=${funding ? 'Fund retained candidate review' : 'Publish retained candidate'}>
      <p>
        ${funding ? `${funding.previous_root ? 'Start a fresh review continuation for the published PR. The earlier attempt stays closed. ' : ''}Fund review and correction on source ${funding.connection}: ${funding.limits.active_minutes} minutes per run, ${funding.limits.review_rounds} reviews and ${funding.limits.correction_cycles} corrections. Earlier charges and spent review rounds are preserved. Planning, initial Builder and completed publication do not run again.` : 'Publish the completed candidate on a fresh branch. The earlier uncertain publication remains preserved. No planning or Builder work runs again.'}
      </p>
      <button
        class="ui button"
        type="button"
        ?disabled=${this.busy || !this.ready || (!this.pending && !this.current?.detail?.capabilities.publish_retained?.available && !offer?.available)}
        @click=${() => this.publish()}
      >
        ${this.pending ? 'Check saved request' : funding ? 'Fund review and correction' : 'Publish retained candidate'}
      </button>
      <p role="status">${this.message}</p>
    </section>`;
  }
}
customElements.define('soda-factory-retained-publication', SodaFactoryRetainedPublication);
