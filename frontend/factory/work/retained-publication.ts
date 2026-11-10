import {LitElement, html} from 'lit';
import {object, readSodaJSON} from '../api/models.js';
import {factoryCommandId} from '../api/run-response.js';
import type {FactoryTransport} from '../api/transport.js';
import {
  retainedCandidateFunding,
  mergePreparation,
  reviewRetry,
  type ReviewRetry,
  type MergePreparation,
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
  reviewRetry?: ReviewRetry;
  preparation?: MergePreparation;
  funding?: RetainedCandidateFunding;
  command: string;
  publication: string;
  run: string;
  candidate: string;
}
function sameFields(left: object, right: object): boolean {
  const a = left as Record<string, unknown>, b = right as Record<string, unknown>;
  return Object.keys(a).length === Object.keys(b).length &&
    Object.keys(a).every(key => Object.hasOwn(b, key) && a[key] === b[key]);
}
function sameFunding(left: RetainedCandidateFunding, value: unknown): boolean {
  const right = retainedCandidateFunding(value);
  if (!right) return false;
  const {native: aNative, limits: aLimits, ...a} = left;
  const {native: bNative, limits: bLimits, ...b} = right;
  return sameFields(a, b) && sameFields(aLimits, bLimits) &&
    (aNative === undefined ? bNative === undefined : bNative !== undefined && sameFields(aNative, bNative));
}
function samePreparation(left: MergePreparation, value: unknown): boolean {
  const right = mergePreparation(value);
  return right !== undefined && sameFields(left, right);
}
function sameReviewRetry(left: ReviewRetry, value: unknown): boolean {
  const right = reviewRetry(value);
  return right !== undefined && sameFields(left, right);
}
function sameCapture(left: Capture, right: Capture): boolean {
  return left.command === right.command && left.publication === right.publication &&
    left.run === right.run && left.candidate === right.candidate &&
    (left.funding === undefined ? right.funding === undefined : sameFunding(left.funding, right.funding)) &&
    (left.preparation === undefined ? right.preparation === undefined : samePreparation(left.preparation, right.preparation)) &&
    (left.reviewRetry === undefined ? right.reviewRetry === undefined : sameReviewRetry(left.reviewRetry, right.reviewRetry));
}
function valid(value: unknown): value is Capture {
  if (!value || typeof value !== 'object' || Array.isArray(value)) return false;
  const r = value as Record<string, unknown>;
  return (
    Object.keys(r).length === 4 + (r.funding === undefined ? 0 : 1) + (r.preparation === undefined ? 0 : 1) + (r.reviewRetry === undefined ? 0 : 1) &&
    [r.funding, r.preparation, r.reviewRetry].filter(v => v !== undefined).length <= 1 &&
    (r.reviewRetry === undefined || (reviewRetry(r.reviewRetry) !== undefined &&
      r.publication === (r.reviewRetry as ReviewRetry).publication &&
      r.run === (r.reviewRetry as ReviewRetry).run && r.candidate === (r.reviewRetry as ReviewRetry).head)) &&
    (r.preparation === undefined ||
      (mergePreparation(r.preparation) !== undefined &&
        r.publication === (r.preparation as MergePreparation).publication &&
        r.candidate === (r.preparation as MergePreparation).head)) &&
    (r.funding === undefined ||
      (retainedCandidateFunding(r.funding) !== undefined &&
        r.publication === (r.funding as RetainedCandidateFunding).publication &&
        r.candidate === (r.funding as RetainedCandidateFunding).candidate)) &&
    ['command', 'publication', 'run'].every((k) => typeof r[k] === 'string' && /^[a-f0-9]{32}$/.test(r[k] as string)) &&
    typeof r.candidate === 'string' &&
    /^(?:[a-f0-9]{40}|[a-f0-9]{64})$/.test(r.candidate)
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
        sameCapture
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
    const preparation = context.detail?.capabilities.prepare_merge;
    const retry = context.detail?.capabilities.retry_review;
    if (
      !this.pending &&
      (!publication ||
        (!context.detail?.capabilities.publish_retained?.available && !funding?.available && !preparation?.available && !retry?.available))
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
        run: retry?.available ? retry.review_retry.run : publication.run,
        candidate: publication.candidate,
        ...(retry?.available
          ? {reviewRetry: structuredClone(retry.review_retry)}
          : preparation?.available
          ? {preparation: structuredClone(preparation.merge_preparation)}
          : funding?.available
            ? {funding: structuredClone(funding.funding)}
            : {}),
      });
    if (!pending || !valid(pending) || record.retain(pending) !== 'saved') {
      this.message = 'The publication identity could not be saved. No request was sent.';
      return;
    }
    this.pending = pending;
    this.busy = true;
    this.message = pending.reviewRetry ? 'Review submission retry requested; waiting for confirmation.' : pending.preparation
      ? 'Merge preparation requested; waiting for confirmation.'
      : pending.funding
        ? 'Funding requested; waiting for confirmation.'
        : 'Publication requested; waiting for confirmation.';
    try {
      const response = await context.transport.request(`factory/runs/${pending.run}/actions`, {
        method: 'POST',
        headers: {'Content-Type': 'application/json'},
        body: JSON.stringify({
          action: pending.reviewRetry ? 'retry-unsent-review' : pending.preparation
            ? 'prepare-unsent-merge'
            : pending.funding
              ? 'fund-retained-candidate'
              : 'publish-retained-candidate',
          ...(pending.preparation ? {merge_preparation: pending.preparation} : {}),
          ...(pending.funding ? {funding: pending.funding} : {}),
          ...(pending.reviewRetry ? {review_retry: pending.reviewRetry} : {}),
          command_id: pending.command,
          publication: pending.publication,
        }),
        signal: AbortSignal.any([this.lifetime.signal, AbortSignal.timeout(90000)]),
      });
      const receipt = object(await readSodaJSON(response));
      if (!this.live(serial)) return;
      if (
        !response.ok ||
        (pending.reviewRetry ? receipt.command_id !== pending.command || !sameReviewRetry(pending.reviewRetry, receipt.review_retry) : pending.preparation
          ? receipt.command_id !== pending.command ||
            receipt.merge_id !== pending.command ||
            (pending.preparation.retry_no_send &&
              (typeof receipt.not_after !== 'number' ||
                !Number.isSafeInteger(receipt.not_after) ||
                receipt.not_after <= 0)) ||
            !samePreparation(pending.preparation, receipt.merge_preparation)
          : pending.funding
            ? receipt.command_id !== pending.command ||
              !sameFunding(pending.funding, receipt.funding)
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
      this.message = pending.reviewRetry
        ? 'Review submission retry recorded. The completed reviewer result and earlier unsent operation are preserved; no model runs again.' : pending.preparation
        ? 'Fresh merge preparation recorded under current policy. The prior unsent preparation is preserved. Current approval, CI and permission checks still apply.'
        : pending.funding
          ? pending.funding.carry_unused
            ? 'Unused review/correction budget carried forward. Earlier results, spent rounds and charges remain unchanged.'
            : 'Finite review/correction funding recorded on the selected source. Earlier charges and publication authority remain unchanged.'
          : 'Fresh publication recorded. Review and correction require separate funding.';
      this.dispatchEvent(new CustomEvent('soda-factory-work-refresh', {bubbles: true, composed: true}));
    } catch {
      if (this.live(serial))
        this.message = `${pending.reviewRetry ? 'Review retry' : pending.preparation ? 'Merge preparation' : pending.funding ? 'Funding' : 'Publication'} was not confirmed. The saved request is preserved; check the same request to recover its result.`;
    } finally {
      if (this.live(serial)) this.busy = false;
    }
  }
  protected render() {
    const publication = this.current?.detail?.publication;
    const offer = this.current?.detail?.capabilities.fund_retained;
    const retryOffer = this.current?.detail?.capabilities.retry_review;
    const preparationOffer = this.current?.detail?.capabilities.prepare_merge;
    const retry = this.pending ? this.pending.reviewRetry : retryOffer?.available ? retryOffer.review_retry : undefined;
    const preparation =
      this.pending ? this.pending.preparation : retry ? undefined : preparationOffer?.available ? preparationOffer.merge_preparation : undefined;
    const funding = this.pending ? this.pending.funding : retry || preparation ? undefined : offer?.available ? offer.funding : undefined;
    if (!this.pending && publication?.stage !== 'fenced' && !offer?.available && !preparation && !retry) return html``;
    return html`<section
      aria-label=${retry ? 'Retry unsent review' : preparation ? 'Prepare unsent merge' : funding ? 'Fund retained candidate review' : 'Publish retained candidate'}
    >
      <p>
        ${retry ? `Authorize a five-minute submission of the completed review for commit ${retry.head} under policy ${retry.policy}. The earlier operation has a definitive no-send outcome and stays preserved. This adds no inference funding and does not rerun the reviewer. Current target and permission checks still apply.` : preparation?.retry_no_send ? `Authorize only a five-minute merge retry under policy ${preparation.policy} for reviewed commit ${preparation.head}. The earlier unsent authorization and all old receipts stay preserved. This adds no inference funding; planning, Builder and review do not run again. Current review, CI and permission checks still apply.` : preparation ? `Prepare a fresh merge under policy ${preparation.policy} for reviewed commit ${preparation.head}. The earlier preparation has no recorded send and stays preserved. Current review, CI and permission checks still apply; this adds no inference budget.` : funding ? (funding.carry_unused ? `Resume only unused review/correction budget on source ${funding.connection}, at ${funding.limits.active_minutes} minutes per run. Earlier spent rounds, head limits and charges carry forward; no budget is added. The earlier attempt stays closed and its incomplete result is preserved. Planning, initial Builder and completed publication do not run again.` : `${funding.previous_root ? 'Start a fresh review continuation for the published PR. The earlier attempt stays closed. ' : ''}Fund review and correction on source ${funding.connection}: ${funding.limits.active_minutes} minutes per run, ${funding.limits.review_rounds} reviews and ${funding.limits.correction_cycles} corrections. Earlier charges and spent review rounds are preserved. Planning, initial Builder and completed publication do not run again.`) : 'Publish the completed candidate on a fresh branch. The earlier uncertain publication remains preserved. No planning or Builder work runs again.'}
      </p>
      ${funding?.requirements ? html`<p>Uses the current approved workspace setup. A setup change requires a fresh funding offer.</p>` : ''}
      <button
        class="ui button"
        type="button"
        ?disabled=${this.busy || !this.ready || (!this.pending && !this.current?.detail?.capabilities.publish_retained?.available && !offer?.available && !this.current?.detail?.capabilities.prepare_merge?.available && !retryOffer?.available)}
        @click=${() => this.publish()}
      >
        ${this.pending ? 'Check saved request' : retry ? 'Retry confirmed unsent review' : preparation?.retry_no_send ? 'Retry confirmed unsent merge' : preparation ? 'Prepare merge under current policy' : funding?.carry_unused ? 'Resume unused review budget' : funding ? 'Fund review and correction' : 'Publish retained candidate'}
      </button>
      <p role="status">${this.message}</p>
    </section>`;
  }
}
customElements.define('soda-factory-retained-publication', SodaFactoryRetainedPublication);
