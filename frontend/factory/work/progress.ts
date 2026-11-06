import {LitElement, html} from 'lit';
import {SodaFactoryRoleSettings} from './role-settings.js';
import type {FactoryTransport} from '../api/transport.js';
import type {FactoryWorkLinks} from '../app/navigation.js';
import {check, id} from '../api/models.js';
import {decisionID, intentRevision} from '../api/request-response.js';
import type {IntentRevision} from '../api/request-response.js';
import {factoryIssueDetail, factoryRepositorySummary} from '../api/work-response.js';
import type {FactoryIssueDetail, FactoryRepositorySummary} from '../api/work-response.js';
import {getJSON, failureInput, FactoryReadError} from '../api/read.js';
import {
  describePrerequisites,
  describeRecordedChoice,
  describeRequestOverview,
  describeRevisionHistory,
  describeWhyNext,
  describeWorkOverview,
  overviewScopeFromBound,
  overviewScopeFromMappings,
  retiredOverviewScope,
  renderRecordedChoice,
  renderRequestOverview,
  renderRevisionHistory,
  renderWorkOverview,
  renderWorkPlan,
  describeDirectionRevision,
  renderDirectionRevision,
} from './progress-view.js';
import type {OverviewScope, RequestOverviewModel, SourceState, WorkOverviewModel} from './progress-view.js';
import {issuePlan, requestOverview, revisionHistory} from '../api/progress-response.js';
import type {RevisionHistory, IssuePlan, RequestOverview} from '../api/progress-response.js';
export interface WorkOverviewContext {
  readonly actor?: string;
  readonly accountLabel?: string;
  readonly transport: FactoryTransport;
  repository: string;
  readonly links?: FactoryWorkLinks;
  intentID?: string;
}

function overviewSignature(context: WorkOverviewContext): string {
  return [context.repository, context.intentID ?? ''].join('|');
}
function sameOverview(a: WorkOverviewContext | undefined, b: WorkOverviewContext | undefined) {
  if (!a || !b || a.transport !== b.transport) return false;
  return overviewSignature(a) === overviewSignature(b);
}

export class SodaFactoryWorkOverview extends LitElement {
  private readonly roleSettings = new SodaFactoryRoleSettings();
  static properties = {model: {state: true}, request: {state: true}, message: {state: true}, busy: {state: true}};
  declare private model: WorkOverviewModel | undefined;
  declare private request: RequestOverviewModel | undefined;
  declare private message: string;
  declare private busy: boolean;
  private recordedRequest: RequestOverview | undefined;
  private current: WorkOverviewContext | undefined;
  private scope: OverviewScope;
  private serial = 0;
  private generation = '';
  private lifetime = new AbortController();
  protected createRenderRoot() {
    return this;
  }
  constructor() {
    super();
    this.message = '';
    this.busy = false;
    this.scope = retiredOverviewScope();
  }
  // boundScope is the last published child-view feed: the read keys
  // in use plus the bound work items. Composers derive plan, detail
  // and revision contexts from it; soda-factory-overview-scope
  // notifies every publication, including retirement.
  get boundScope(): OverviewScope {
    return this.scope;
  }
  set context(context: WorkOverviewContext | undefined) {
    if (context)
      check(
        id(context.repository) &&
          typeof context.transport.generation === 'string' &&
          !!context.transport.generation &&
          (context.intentID === undefined || decisionID(context.intentID))
      );
    this.roleSettings.context = context;
    if (sameOverview(context, this.current) && context?.transport.generation === this.generation) {
      this.current = context;
      if (this.recordedRequest) this.request = describeRequestOverview(this.recordedRequest, context?.links);
      return;
    }
    this.lifetime.abort();
    this.lifetime = new AbortController();
    this.generation = context?.transport.generation ?? '';
    this.current = context;
    this.serial++;
    this.model = undefined;
    this.request = undefined;
    this.recordedRequest = undefined;
    this.message = '';
    this.busy = false;
    this.publishScope(retiredOverviewScope());
    if (context) void this.load();
  }
  disconnectedCallback() {
    this.context = undefined;
    super.disconnectedCallback();
  }
  private live(serial: number, context: WorkOverviewContext): boolean {
    if (serial !== this.serial || context.transport !== this.current?.transport) return false;
    if (context.transport.generation !== this.generation) {
      this.context = undefined;
      return false;
    }
    return true;
  }
  refresh() {
    return this.load();
  }
  private publishScope(scope: OverviewScope) {
    this.scope = scope;
    this.dispatchEvent(new CustomEvent('soda-factory-overview-scope', {bubbles: true, composed: true, detail: scope}));
  }
  private async applyRepositoryView(context: WorkOverviewContext, serial: number): Promise<void> {
    const overview = await readOverview(context, this.lifetime.signal);
    if (!this.live(serial, context)) return;
    this.request = undefined;
    this.recordedRequest = undefined;
    this.model = overview.model;
    this.message = '';
    this.publishScope(overview.scope);
  }
  private async load() {
    const context = this.current;
    if (!context || !this.live(this.serial, context) || this.busy) return;
    const serial = this.serial;
    this.busy = true;
    try {
      const intentID = context.intentID;
      if (intentID) {
        const request = await readRequestOverview(context, intentID, this.lifetime.signal);
        if (!this.live(serial, context)) return;
        this.recordedRequest = request;
        this.request = describeRequestOverview(request, this.current?.links);
        this.model = undefined;
        this.message = '';
        this.publishScope(overviewScopeFromMappings(this.request.mappings, context.repository, context.intentID));
      } else {
        await this.applyRepositoryView(context, serial);
      }
    } catch (error) {
      if (this.live(serial, context)) {
        if (error instanceof FactoryReadError && (error.status === 401 || error.status === 403)) {
          this.model = undefined;
          this.request = undefined;
          this.recordedRequest = undefined;
        }
        const retained = this.model || this.request ? ' Last loaded work is still shown.' : '';
        this.message = (error instanceof Error ? error.message : 'Work overview is unavailable.') + retained;
        this.publishScope(overviewScopeFromBound([], context.repository, context.intentID));
      }
    } finally {
      if (this.live(serial, context)) this.busy = false;
    }
  }
  protected render() {
    if (!this.current) return html``;
    return html`<section aria-label="Factory work overview">
      <p data-work-message aria-live="polite">${this.message}</p>
      <button type="button" class="ui button" ?disabled=${this.busy} @click=${() => this.load()}>
        Refresh overview</button
      >${
        this.request
          ? renderRequestOverview(this.request)
          : this.model
            ? renderWorkOverview(this.model)
            : this.busy
              ? html`<p>Loading work overview…</p>`
              : ''
      }
      ${this.roleSettings}
    </section>`;
  }
}
customElements.define('soda-factory-work-overview', SodaFactoryWorkOverview);

async function readOverviewSummary(
  context: WorkOverviewContext,
  signal: AbortSignal
): Promise<{summary?: FactoryRepositorySummary; state: SourceState}> {
  try {
    const summary = factoryRepositorySummary(
      await getJSON(context.transport, `repositories/${context.repository}/factory`, signal),
      context.repository
    );
    return {summary, state: 'ready'};
  } catch (error) {
    const input = failureInput(error);
    return {state: input.status === 404 ? 'missing' : 'unavailable'};
  }
}

// readRequestOverview reads the authoritative H-R request overview.
// Endpoint and parser failures propagate to the honest unavailable
// boundary; a failed intent read never falls back to composed reads.
async function readRequestOverview(
  context: WorkOverviewContext,
  intent: string,
  signal: AbortSignal
): Promise<RequestOverview> {
  const view = requestOverview(await getJSON(context.transport, `factory/intents/${intent}/overview`, signal), intent);
  check(view.context.repository === context.repository);
  return view;
}

// readOverview reads the repository-only view: queue and setup
// summary without intent scope. It serves contexts without an
// intentID only; intent contexts read the H-R overview above.
async function readOverview(
  context: WorkOverviewContext,
  signal: AbortSignal
): Promise<{model: WorkOverviewModel; scope: OverviewScope}> {
  const {summary, state: summaryState} = await readOverviewSummary(context, signal);
  return {
    model: describeWorkOverview({
      ...(summary === undefined ? {} : {summary}),
      issues: [],
      unreadIssues: [],
      links: context.links,
      intentState: 'missing' as SourceState,
      generationState: 'missing' as SourceState,
      summaryState,
    }),
    scope: overviewScopeFromBound([], context.repository, context.intentID),
  };
}

export interface WorkPlanContext {
  readonly transport: FactoryTransport;
  repository: string;
  readonly links?: FactoryWorkLinks;
  issues: string[];
}

function planSignature(context: WorkPlanContext): string {
  return [context.repository, context.issues.join(',')].join('|');
}
function samePlan(a: WorkPlanContext | undefined, b: WorkPlanContext | undefined) {
  if (!a || !b || a.transport !== b.transport) return false;
  return planSignature(a) === planSignature(b);
}

interface RecordedPlanEntry {
  readonly issue: string;
  readonly plan: IssuePlan;
}

export class SodaFactoryWorkPlan extends LitElement {
  static properties = {
    details: {state: true},
    recorded: {state: true},
    unread: {state: true},
    message: {state: true},
    busy: {state: true},
  };
  declare private details: FactoryIssueDetail[];
  declare private recorded: RecordedPlanEntry[];
  declare private unread: string[];
  declare private message: string;
  declare private busy: boolean;
  private current: WorkPlanContext | undefined;
  private serial = 0;
  private generation = '';
  private lifetime = new AbortController();
  protected createRenderRoot() {
    return this;
  }
  constructor() {
    super();
    this.details = [];
    this.recorded = [];
    this.unread = [];
    this.message = '';
    this.busy = false;
  }
  set context(context: WorkPlanContext | undefined) {
    if (context)
      check(
        id(context.repository) &&
          typeof context.transport.generation === 'string' &&
          !!context.transport.generation &&
          context.issues.length <= 64 &&
          context.issues.every(id)
      );
    if (samePlan(context, this.current) && context?.transport.generation === this.generation) {
      this.current = context;
      this.requestUpdate();
      return;
    }
    this.lifetime.abort();
    this.lifetime = new AbortController();
    this.generation = context?.transport.generation ?? '';
    this.current = context;
    this.serial++;
    this.details = [];
    this.recorded = [];
    this.unread = [];
    this.message = '';
    this.busy = false;
    if (context) void this.load();
  }
  disconnectedCallback() {
    this.context = undefined;
    super.disconnectedCallback();
  }
  private live(serial: number, context: WorkPlanContext): boolean {
    if (serial !== this.serial || context.transport !== this.current?.transport) return false;
    if (context.transport.generation !== this.generation) {
      this.context = undefined;
      return false;
    }
    return true;
  }
  refresh() {
    return this.load();
  }
  private async load() {
    const context = this.current;
    if (!context || !this.live(this.serial, context) || this.busy) return;
    const serial = this.serial;
    this.busy = true;
    try {
      const details = await readPlanIssues(context, this.lifetime.signal);
      if (!this.live(serial, context)) return;
      const recorded = await readRecordedPlans(context, this.lifetime.signal);
      if (this.live(serial, context)) {
        this.details = details.issues;
        this.recorded = recorded;
        this.unread = details.unread;
        this.message = '';
      }
    } catch (error) {
      if (this.live(serial, context))
        this.message = error instanceof Error ? error.message : 'Work plan is unavailable.';
    } finally {
      if (this.live(serial, context)) this.busy = false;
    }
  }
  private fallbackDetails(): FactoryIssueDetail[] {
    const recorded = new Set(this.recorded.map((entry) => entry.issue));
    return this.details.filter((detail) => !recorded.has(detail.issue));
  }
  protected render() {
    const context = this.current;
    if (!context) return html``;
    return html`<section aria-label="Factory work plan">
      <p data-work-message aria-live="polite">${this.message}</p>
      ${this.unread.length > 0 ? html`<p>${this.unread.length} work item${this.unread.length === 1 ? '' : 's'} could not be read.</p>` : ''}
      <button type="button" class="ui button" ?disabled=${this.busy} @click=${() => this.load()}>Refresh plan</button>
      ${this.recorded.map((entry) => renderRecordedChoice(describeRecordedChoice(entry.plan, context.links)))}
      ${renderWorkPlan(
        describeWhyNext(this.details, context.links, Math.floor(Date.now() / 1000)),
        describePrerequisites(this.fallbackDetails(), context.links)
      )}
    </section>`;
  }
}
customElements.define('soda-factory-work-plan', SodaFactoryWorkPlan);

// readRecordedPlans reads each issue's H-R plan independently. A
// missing route or unreadable plan leaves that issue to the
// readiness-based fallback; it never blocks the other issues.
async function readRecordedPlans(context: WorkPlanContext, signal: AbortSignal): Promise<RecordedPlanEntry[]> {
  const settled = await Promise.allSettled(
    context.issues.map((issue) =>
      getJSON(context.transport, `repositories/${context.repository}/factory/issues/${issue}/plan?limit=5`, signal)
    )
  );
  const entries: RecordedPlanEntry[] = [];
  settled.forEach((result, index) => {
    const issue = context.issues[index] ?? '';
    if (result.status !== 'fulfilled' || issue === '') return;
    try {
      const plan = issuePlan(result.value, context.repository, issue);
      entries.push({
        issue,
        plan,
      });
    } catch {
      return;
    }
  });
  return entries;
}

async function readPlanIssues(
  context: WorkPlanContext,
  signal: AbortSignal
): Promise<{issues: FactoryIssueDetail[]; unread: string[]}> {
  const issues: FactoryIssueDetail[] = [];
  const unread: string[] = [];
  const settled = await Promise.allSettled(
    context.issues.map((issue) =>
      getJSON(context.transport, `repositories/${context.repository}/factory/issues/${issue}`, signal)
    )
  );
  settled.forEach((result, index) => {
    const issue = context.issues[index] ?? '';
    if (result.status === 'fulfilled' && issue !== '') {
      try {
        issues.push(factoryIssueDetail(result.value, context.repository, issue));
      } catch {
        unread.push(issue);
      }
    } else if (issue !== '') {
      unread.push(issue);
    }
  });
  return {issues, unread};
}

export interface DirectionRevisionContext {
  readonly transport: FactoryTransport;
  intentID: string;
}

export class SodaFactoryDirectionRevision extends LitElement {
  static properties = {
    revision: {state: true},
    state: {state: true},
    history: {state: true},
    historyState: {state: true},
    message: {state: true},
    busy: {state: true},
  };
  declare private revision: IntentRevision | undefined;
  declare private state: SourceState;
  declare private history: RevisionHistory | undefined;
  declare private historyState: SourceState;
  declare private message: string;
  declare private busy: boolean;
  private current: DirectionRevisionContext | undefined;
  private serial = 0;
  private generation = '';
  private lifetime = new AbortController();
  protected createRenderRoot() {
    return this;
  }
  constructor() {
    super();
    this.state = 'missing';
    this.historyState = 'missing';
    this.message = '';
    this.busy = false;
  }
  set context(context: DirectionRevisionContext | undefined) {
    if (context)
      check(
        decisionID(context.intentID) &&
          typeof context.transport.generation === 'string' &&
          !!context.transport.generation
      );
    if (
      context?.transport === this.current?.transport &&
      context?.intentID === this.current?.intentID &&
      context?.transport.generation === this.generation
    )
      return;
    this.lifetime.abort();
    this.lifetime = new AbortController();
    this.generation = context?.transport.generation ?? '';
    this.current = context;
    this.serial++;
    this.revision = undefined;
    this.state = 'missing';
    this.history = undefined;
    this.historyState = 'missing';
    this.message = '';
    this.busy = false;
    if (context) void this.load();
  }
  disconnectedCallback() {
    this.context = undefined;
    super.disconnectedCallback();
  }
  private live(serial: number, context: DirectionRevisionContext): boolean {
    if (serial !== this.serial || context.transport !== this.current?.transport) return false;
    if (context.transport.generation !== this.generation) {
      this.context = undefined;
      return false;
    }
    return true;
  }
  refresh() {
    return this.load();
  }
  private async load() {
    const context = this.current;
    if (!context || !this.live(this.serial, context) || this.busy) return;
    const serial = this.serial;
    this.busy = true;
    try {
      await this.loadReads(context, serial);
    } catch (error) {
      if (this.live(serial, context))
        this.message = error instanceof Error ? error.message : 'Direction is unavailable.';
    } finally {
      if (this.live(serial, context)) this.busy = false;
    }
  }
  private async loadReads(context: DirectionRevisionContext, serial: number) {
    const revision = await readDirectionRevision(context, this.lifetime.signal);
    if (!this.live(serial, context)) return;
    const history = await readRevisionHistory(context, this.lifetime.signal);
    if (this.live(serial, context)) {
      this.revision = revision.revision;
      this.state = revision.state;
      this.history = history.history;
      this.historyState = history.state;
      this.message = '';
    }
  }
  protected render() {
    if (!this.current) return html``;
    return html`<section aria-label="Factory direction revision">
      <p data-work-message aria-live="polite">${this.message}</p>
      <button type="button" class="ui button" ?disabled=${this.busy} @click=${() => this.load()}>
        Refresh direction</button
      >${renderDirectionRevision(describeDirectionRevision(this.revision, this.state))}
      ${renderRevisionHistory(describeRevisionHistory(this.history, this.historyState))}
    </section>`;
  }
}
customElements.define('soda-factory-direction-revision', SodaFactoryDirectionRevision);

async function readDirectionRevision(
  context: DirectionRevisionContext,
  signal: AbortSignal
): Promise<{revision?: IntentRevision; state: SourceState}> {
  try {
    const revision = intentRevision(await getJSON(context.transport, `factory/intents/${context.intentID}`, signal));
    return {revision, state: 'ready'};
  } catch (error) {
    const input = failureInput(error);
    return {state: input.status === 404 ? 'missing' : 'unavailable'};
  }
}

async function readRevisionHistory(
  context: DirectionRevisionContext,
  signal: AbortSignal
): Promise<{history?: RevisionHistory; state: SourceState}> {
  try {
    const history = revisionHistory(
      await getJSON(context.transport, `factory/intents/${context.intentID}/revisions?limit=2`, signal),
      context.intentID
    );
    return {history, state: 'ready'};
  } catch (error) {
    const input = failureInput(error);
    return {state: input.status === 404 ? 'missing' : 'unavailable'};
  }
}
