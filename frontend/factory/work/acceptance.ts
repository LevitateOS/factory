import {LitElement, html} from 'lit';
import {check, id, object, readSodaJSON} from '../api/models.js';
import type {FactoryTransport} from '../api/transport.js';
import type {FactoryAcceptanceLinks} from '../app/navigation.js';
import {decisionID, newDecisionID} from '../api/request-response.js';
import {currentWorkObservation, requestWorkRefresh} from './observation.js';
import type {WorkObservation} from './observation.js';
import {factoryIssueDetail, sodaErrorDetail} from '../api/work-response.js';
import {acceptanceCommentIDs, acceptanceErrorMessage, acceptancePreview} from '../api/acceptance-response.js';
import type {AcceptancePreview} from '../api/acceptance-response.js';
import {readAcceptance, retainAcceptance, sameAcceptance, settleAcceptance} from './acceptance-record.js';
import type {AcceptanceAddress, AcceptanceDecision} from './acceptance-record.js';
import {PendingCommandController} from '../shared/pending-command.js';
import type {ClassifyRefusal} from '../shared/pending-command.js';

// These typed evidence refusals precede persistence. After any unknown outcome or recovery,
// a current refusal cannot settle the earlier attempt (the controller allows only a first send).
const refusedBeforePersistence = new Set([
  'stale_evidence',
  'objective_changed',
  'source_missing',
  'source_changed',
  'edge_changed',
  'prereq_route_unknown',
  'prereq_route_stale',
]);
const classifyRefusal: ClassifyRefusal = (status, code) =>
  status === 409 && code !== undefined && refusedBeforePersistence.has(code) ? 'discard' : 'keep';

export interface RequirementsAcceptanceContext {
  readonly observation?: WorkObservation;
  readonly transport: FactoryTransport;
  readonly repository: string;
  readonly links?: FactoryAcceptanceLinks;
  readonly issue: string;
  /** Stable host-admitted profile/provider-instance/account reference, never a credential.
   * Retire the binding on identity changes; generations are not persistence keys. */
  readonly commandScope?: (signal: AbortSignal) => Promise<string>;
}
interface PrerequisiteChoice {
  readonly occurrence: string;
  readonly depends_on: string;
  readonly endpoint_repository: string;
  readonly endpoint_issue: string;
  readonly outcome: '' | 'code' | 'result';
}

async function read(context: RequirementsAcceptanceContext, path: string, signal: AbortSignal): Promise<unknown> {
  const response = await context.transport.request(path, {
    method: 'GET',
    signal: AbortSignal.any([signal, AbortSignal.timeout(15000)]),
  });
  const value = await readSodaJSON(response);
  if (!response.ok)
    throw Error(
      acceptanceErrorMessage(value) ?? sodaErrorDetail(value).message ?? 'Current requirements are unavailable.'
    );
  return value;
}

// Native text stays in the native issue. Only the exact reviewed selections
// from acceptance-preview enter this existing maintainer decision contract.
/** @fires soda-factory-work-refresh */
export class SodaFactoryRequirementsAcceptance extends LitElement {
  static properties = {
    allowed: {state: true},
    message: {state: true},
    busy: {state: true},
    preview: {state: true},
    sources: {state: true},
    resolutions: {state: true},
    choices: {state: true},
    reviewed: {state: true},
  };
  declare private allowed: boolean;
  declare private message: string;
  declare private busy: boolean;
  declare private preview: AcceptancePreview | undefined;
  declare private sources: string;
  declare private resolutions: string;
  declare private choices: PrerequisiteChoice[];
  declare private reviewed: boolean;
  private cmd = new PendingCommandController<AcceptanceDecision, AcceptanceAddress>(
    {read: readAcceptance, retain: retainAcceptance, settle: settleAcceptance, same: sameAcceptance},
    () => this.requestUpdate()
  );
  private get pending() {
    return this.cmd.pending;
  }
  private get ready() {
    return this.cmd.ready;
  }
  private current: RequirementsAcceptanceContext | undefined;
  private predecessor: string | undefined;
  private serial = 0;
  private generation = '';
  private lifetime = new AbortController();
  protected createRenderRoot() {
    return this;
  }
  constructor() {
    super();
    this.allowed = false;
    this.message = '';
    this.busy = false;
    this.sources = '';
    this.resolutions = '';
    this.choices = [];
    this.reviewed = false;
  }
  set context(context: RequirementsAcceptanceContext | undefined) {
    if (
      context &&
      (!id(context.repository) ||
        !id(context.issue) ||
        typeof context.transport.generation !== 'string' ||
        !context.transport.generation)
    )
      throw Error('Invalid requirements acceptance context.');
    const prior = this.current;
    if (
      context?.transport === prior?.transport &&
      context?.repository === prior?.repository &&
      context?.transport.generation === this.generation &&
      context?.issue === prior?.issue
    ) {
      this.current = context && {...context};
      if (context?.observation !== prior?.observation && context) this.observe(context);
      if (context?.links !== prior?.links) this.requestUpdate();
      return;
    }
    this.lifetime.abort();
    this.lifetime = new AbortController();
    this.generation = context?.transport.generation ?? '';
    this.current = context && {...context};
    this.serial++;
    this.allowed = false;
    this.cmd.reset();
    this.preview = undefined;
    this.predecessor = undefined;
    this.sources = '';
    this.resolutions = '';
    this.choices = [];
    this.reviewed = false;
    this.busy = false;
    this.message = '';
    if (context) this.observe(context);
  }
  disconnectedCallback() {
    super.disconnectedCallback();
    this.context = undefined;
  }
  private live(serial: number, context: RequirementsAcceptanceContext): boolean {
    if (serial !== this.serial || context.transport !== this.current?.transport) return false;
    if (context.transport.generation !== this.generation) {
      this.context = undefined;
      return false;
    }
    return true;
  }
  private async read(context: RequirementsAcceptanceContext, path: string): Promise<unknown> {
    const serial = this.serial;
    if (!this.live(serial, context)) throw Error('Requirements context retired.');
    const value = await read(context, path, this.lifetime.signal);
    if (!this.live(serial, context)) throw Error('Requirements context retired.');
    return value;
  }
  private base(context: RequirementsAcceptanceContext): string {
    return `repositories/${context.repository}/factory/issues/${context.issue}`;
  }
  private observe(context: RequirementsAcceptanceContext) {
    const observation = currentWorkObservation(context);
    this.allowed =
      observation?.state === 'ready' &&
      !!observation.access?.viewer.code_write &&
      !!observation.access.viewer.session_current;
    if (!this.allowed) {
      this.message =
        'Accepting requirements needs current repository code-write authority and a current session. Refresh the work detail.';
    } else if (!this.busy) void this.resolveIdentity(context, this.serial);
  }
  private async resolveIdentity(context: RequirementsAcceptanceContext, serial: number): Promise<boolean> {
    this.cmd.resolving();
    try {
      if (!context.commandScope) throw Error('Missing acceptance identity');
      const scope = await context.commandScope(AbortSignal.any([this.lifetime.signal, AbortSignal.timeout(15000)]));
      if (!this.live(serial, context)) return false;
      const address: AcceptanceAddress = {scope, repository: context.repository, issue: context.issue};
      if (this.cmd.address && this.cmd.address.scope !== scope) {
        this.preview = undefined;
        this.reviewed = false;
        this.choices = [];
        this.sources = this.resolutions = '';
      }
      const blocked = this.cmd.adopt(address) === 'blocked',
        stored = this.cmd;
      if (stored.pending) {
        this.sources = stored.pending.sources.map((source) => source.id).join(', ');
        this.resolutions = stored.pending.resolutions.map((source) => source.id).join(', ');
      }
      if (blocked) {
        this.message =
          'Saved acceptance is unreadable or belongs to another scope. It was preserved; no new decision was sent.';
        return false;
      }
      this.message = stored.pending
        ? 'An unconfirmed acceptance is saved. Retry the same acceptance to check its recorded result.'
        : '';
      return true;
    } catch {
      if (this.live(serial, context)) {
        this.cmd.unresolve();
        this.message = 'Acceptance identity could not be read. Refresh acceptance authority before accepting.';
      }
      return false;
    }
  }
  private changeSources(section: 'sources' | 'resolutions', event: Event) {
    this[section] = (event.target as HTMLInputElement).value;
    this.preview = undefined;
    this.reviewed = false;
    this.choices = [];
  }
  private async loadPreview() {
    const context = this.current;
    if (!context || !this.live(this.serial, context) || !this.allowed || !this.ready || this.busy || this.pending)
      return;
    const serial = this.serial;
    this.busy = true;
    this.preview = undefined;
    this.reviewed = false;
    try {
      const sources = acceptanceCommentIDs(this.sources),
        resolutions = acceptanceCommentIDs(this.resolutions);
      const selected = [...sources, ...resolutions];
      if (new Set(selected).size !== selected.length || selected.length > 50)
        throw Error('Select at most 50 distinct comment IDs across requirements and resolutions.');
      const query = new URLSearchParams(selected.map((comment) => ['comment_id', comment])).toString();
      const observation = currentWorkObservation(context);
      const value = await this.read(context, `${this.base(context)}/acceptance-preview${query ? `?${query}` : ''}`);
      if (!this.live(serial, context) || observation !== currentWorkObservation(this.current!) || !this.allowed) return;
      const preview = acceptancePreview(value, context.repository, context.issue, selected);
      this.predecessor = observation?.detail?.status.acceptance?.id;
      this.choices = preview.dependencies.map((edge) => ({
        ...edge,
        endpoint_repository: '',
        endpoint_issue: '',
        outcome: '',
      }));
      this.preview = preview;
      this.message = 'Review the native issue, selected comments and every prerequisite outcome before accepting.';
    } catch (error) {
      if (this.live(serial, context))
        this.message = error instanceof Error ? error.message : 'Requirements preview is unavailable.';
    } finally {
      if (this.live(serial, context)) this.busy = false;
    }
  }
  private changeChoice(index: number, field: 'endpoint_repository' | 'endpoint_issue' | 'outcome', event: Event) {
    const value = (event.target as HTMLInputElement | HTMLSelectElement).value;
    if (field === 'outcome' && value !== '' && value !== 'code' && value !== 'result') return;
    this.choices = this.choices.map((choice, at) =>
      at === index ? ({...choice, [field]: value} as PrerequisiteChoice) : choice
    );
    this.reviewed = false;
  }
  private async selectedPrerequisites(
    context: RequirementsAcceptanceContext
  ): Promise<AcceptanceDecision['prerequisites']> {
    return Promise.all(
      this.choices.map(async (choice) => {
        if (!id(choice.endpoint_repository) || !id(choice.endpoint_issue) || choice.outcome === '')
          throw Error('Choose the repository, issue number and required outcome for every prerequisite.');
        if (choice.outcome === 'result') return {...choice, outcome: 'result' as const};
        const detail = factoryIssueDetail(
          await this.read(
            context,
            `repositories/${choice.endpoint_repository}/factory/issues/${choice.endpoint_issue}`
          ),
          choice.endpoint_repository,
          choice.endpoint_issue
        );
        if (!detail.status.valid || detail.status.withdrawn || !detail.status.acceptance)
          throw Error(
            `Accept the current requirements of prerequisite #${choice.endpoint_issue} before choosing its code outcome.`
          );
        return {...choice, outcome: 'code' as const, prerequisite_acceptance: detail.status.acceptance.id};
      })
    );
  }
  private async accept() {
    const context = this.current,
      preview = this.preview;
    if (
      !context ||
      !this.live(this.serial, context) ||
      !this.allowed ||
      !this.ready ||
      this.busy ||
      (!this.pending && (!preview || !this.reviewed))
    )
      return;
    const serial = this.serial;
    this.busy = true;
    try {
      const shown = this.pending;
      let candidate: AcceptanceDecision | undefined;
      if (!shown && preview) {
        const prerequisites = await this.selectedPrerequisites(context);
        if (!this.live(serial, context)) return;
        const sources = new Set(acceptanceCommentIDs(this.sources));
        candidate = {
          decision_id: newDecisionID(),
          ...(this.predecessor ? {predecessor: this.predecessor} : {}),
          title_digest: preview.title_digest,
          title_occurrence: preview.title_occurrence,
          content_digest: preview.body_digest,
          content_version: preview.content_version,
          native_revision: preview.native_revision,
          sources: preview.comments.filter((source) => sources.has(source.id)),
          resolutions: preview.comments.filter((source) => !sources.has(source.id)),
          prerequisites,
        };
      }
      const admitted = await this.cmd.readmit(() => this.resolveIdentity(context, serial));
      if (admitted === 'unresolved') return;
      if (admitted === 'changed') {
        this.message = 'The saved acceptance or account changed. Review it before accepting.';
        return;
      }
      if (!this.allowed || !this.live(serial, context)) return;
      const decision = this.pending ?? candidate;
      if (!decision) return;
      const outcome = await this.cmd.submit({
        command: decision,
        live: () => this.live(serial, context),
        classifyRefusal,
        send: () =>
          context.transport.request(`${this.base(context)}/acceptances`, {
            method: 'POST',
            headers: {'Content-Type': 'application/json'},
            body: JSON.stringify(decision),
            signal: AbortSignal.any([this.lifetime.signal, AbortSignal.timeout(30000)]),
          }),
        receipt: (value) => {
          const receipt = object(value);
          check(
            receipt.decision_id === decision.decision_id &&
              decisionID(receipt.head) &&
              typeof receipt.depth === 'number' &&
              Number.isSafeInteger(receipt.depth) &&
              receipt.depth > 0
          );
        },
      });
      if (outcome.kind === 'retired') return;
      const retry = () => (this.pending ? ' Retry the same acceptance to check its recorded result.' : '');
      if (outcome.kind === 'not-saved') {
        this.message =
          outcome.result === 'unavailable'
            ? 'Acceptance could not be saved. No decision was sent. Retry the same acceptance when storage is available.'
            : 'The saved acceptance changed. Refresh acceptance authority before retrying.';
        return;
      }
      if (outcome.kind === 'record-changed') {
        this.message = 'The saved acceptance changed. Refresh acceptance authority to read its current state.';
        return;
      }
      if (outcome.kind === 'refused') {
        if (outcome.discarded) {
          this.preview = undefined;
          this.reviewed = false;
        }
        this.message = `${
          acceptanceErrorMessage(outcome.body) ??
          sodaErrorDetail(outcome.body).message ??
          'Acceptance was not confirmed. Refresh the preview before accepting current requirements.'
        }${retry()}`;
        return;
      }
      if (outcome.kind === 'unknown') {
        this.message = `${outcome.error instanceof Error ? outcome.error.message : 'Acceptance was not confirmed.'}${retry()}`;
        return;
      }
      this.preview = undefined;
      this.reviewed = false;
      this.message =
        'Requirements accepted. Factory will assess the accepted work against current grants and prerequisites.';
      this.dispatchEvent(new CustomEvent('soda-factory-requirements-accepted', {bubbles: true, composed: true}));
    } catch (error) {
      if (this.live(serial, context))
        this.message = `${error instanceof Error ? error.message : 'Acceptance was not confirmed.'}${this.pending ? ' Retry the same acceptance to check its recorded result.' : ''}`;
    } finally {
      if (this.live(serial, context)) this.busy = false;
    }
  }
  private renderPreview(context: RequirementsAcceptanceContext) {
    const preview = this.preview;
    if (!preview) return '';
    return html`<div data-requirements-preview>
      <p>
        Issue #${context.issue}, body revision ${preview.content_version}; native revision ${preview.native_revision}.
      </p>
      <p>
        ${this.predecessor ? 'This replaces the prior accepted requirements.' : 'This adopts the current issue requirements.'}
      </p>
      <ul>
        ${preview.comments.map((source) => {
          const link = id(source.id) ? context.links?.comment(context.issue, source.id) : undefined;
          return html`<li>
            ${link ? html`<a href=${link} target="_blank" rel="noreferrer">Review comment ${source.id}</a>` : html`Comment ${source.id}`}
            (revision ${source.content_version})
          </li>`;
        })}
      </ul>
      ${this.choices.length === 0 ? html`<p>No direct prerequisites.</p>` : html`<p>For every native prerequisite, confirm its address and the outcome this work needs.</p>`}
      ${this.choices.map(
        (choice, index) => html`<fieldset data-prerequisite=${choice.occurrence}>
          <legend>Native prerequisite ${choice.depends_on}</legend>
          <label
            >Prerequisite repository ID
            <input
              inputmode="numeric"
              .value=${choice.endpoint_repository}
              @input=${(event: Event) => this.changeChoice(index, 'endpoint_repository', event)}
          /></label>
          <label
            >Prerequisite issue number
            <input
              inputmode="numeric"
              .value=${choice.endpoint_issue}
              @input=${(event: Event) => this.changeChoice(index, 'endpoint_issue', event)}
          /></label>
          <label
            >Required outcome
            <select
              aria-label="Required outcome"
              .value=${choice.outcome}
              @change=${(event: Event) => this.changeChoice(index, 'outcome', event)}
            >
              <option value="">Choose an outcome</option>
              <option value="code">Code delivered under its current accepted requirements</option>
              <option value="result">An accepted result or resolution</option>
            </select></label
          >
        </fieldset>`
      )}
      <label
        ><input
          type="checkbox"
          .checked=${this.reviewed}
          @change=${(event: Event) => {
            this.reviewed = (event.target as HTMLInputElement).checked;
          }}
        />I reviewed the native issue, selected comments and prerequisite outcomes.</label
      >
      <button
        type="button"
        class="ui primary button"
        ?disabled=${!this.allowed || !this.ready || this.busy || !this.reviewed}
        @click=${() => this.accept()}
      >
        ${this.predecessor ? 'Accept revised requirements' : 'Accept requirements'}
      </button>
    </div>`;
  }
  protected render() {
    const context = this.current;
    if (!context) return html``;
    const issueLink = context.links?.issue(context.issue);
    return html`<section aria-label="Issue requirements acceptance">
      <h3>Issue requirements</h3>
      <p>
        ${issueLink ? html`<a href=${issueLink} target="_blank" rel="noreferrer">Review issue #${context.issue}</a>` : html`Issue #${context.issue}`}.
        Acceptance uses its exact title and body, plus the comments you select.
      </p>
      <p role="status">${this.message}</p>
      <button type="button" class="ui button" ?disabled=${this.busy} @click=${() => requestWorkRefresh(this)}>
        Refresh acceptance authority
      </button>
      <fieldset ?disabled=${!this.allowed || !this.ready || this.busy || this.pending !== undefined}>
        <legend>Optional discussion to include</legend>
        <label
          >Requirement comment IDs
          <input .value=${this.sources} @input=${(event: Event) => this.changeSources('sources', event)}
        /></label>
        <label
          >Resolution comment IDs
          <input .value=${this.resolutions} @input=${(event: Event) => this.changeSources('resolutions', event)}
        /></label>
        <p>
          Use native comment IDs separated by commas. Leave blank to accept only the issue and prerequisite outcomes.
        </p>
        <button type="button" class="ui button" @click=${() => this.loadPreview()}>Review current requirements</button>
      </fieldset>
      ${
        this.pending
          ? html`<p>
                Saved acceptance: body revision ${this.pending.content_version}; native revision
                ${this.pending.native_revision}. Includes ${this.pending.sources.length} requirement comments,
                ${this.pending.resolutions.length} resolutions and ${this.pending.prerequisites.length} prerequisites.
              </p>
              <button
                type="button"
                class="ui button"
                ?disabled=${this.busy || !this.allowed || !this.ready}
                @click=${() => this.accept()}
              >
                Retry same acceptance
              </button>`
          : this.renderPreview(context)
      }
    </section>`;
  }
}
customElements.define('soda-factory-requirements-acceptance', SodaFactoryRequirementsAcceptance);
