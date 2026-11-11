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
import {ScopedRead, errorReason, readReason, readValue} from '../shared/read-state.js';
import type {ReadState} from '../shared/read-state.js';
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
import type {OverviewScope, SourceState, WorkOverviewModel} from './progress-view.js';
import {issuePlan, requestOverview, revisionHistory} from '../api/progress-response.js';
import type {RevisionHistory, IssuePlan, RequestOverview} from '../api/progress-response.js';
export interface WorkOverviewContext {
  readonly actor?: string;
  readonly accountLabel?: string;
  readonly scope?: string;
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

interface OverviewRead {
  readonly model?: WorkOverviewModel;
  readonly recorded?: RequestOverview;
  readonly scope: OverviewScope;
}

export class SodaFactoryWorkOverview extends LitElement {
  private readonly roleSettings = new SodaFactoryRoleSettings();
  private scope: OverviewScope;
  private read = new ScopedRead<WorkOverviewContext, OverviewRead>(
    sameOverview,
    () => this.requestUpdate(),
    () => {
      this.context = undefined;
    }
  );
  protected createRenderRoot() {
    return this;
  }
  constructor() {
    super();
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
    if (this.read.bind(context) === 'same') {
      this.requestUpdate();
      return;
    }
    this.publishScope(retiredOverviewScope());
    if (context) void this.load();
  }
  disconnectedCallback() {
    this.context = undefined;
    super.disconnectedCallback();
  }
  refresh() {
    return this.load();
  }
  private publishScope(scope: OverviewScope) {
    this.scope = scope;
    this.dispatchEvent(new CustomEvent('soda-factory-overview-scope', {bubbles: true, composed: true, detail: scope}));
  }
  private async load() {
    const settled = await this.read.run(
      async (signal) => {
        const context = this.read.current!;
        const intentID = context.intentID;
        if (!intentID) return readOverview(context, signal);
        const recorded = await readRequestOverview(context, intentID, signal);
        const request = describeRequestOverview(recorded, this.read.current?.links);
        return {recorded, scope: overviewScopeFromMappings(request.mappings, context.repository, context.intentID)};
      },
      {
        policy: 'drop',
        failure: (error, previous) => {
          const dropped = error instanceof FactoryReadError && (error.status === 401 || error.status === 403);
          const retained = !dropped && previous ? ' Last loaded work is still shown.' : '';
          return {keep: !dropped, reason: errorReason(error, 'Work overview is unavailable.') + retained};
        },
      }
    );
    const context = this.read.current;
    if (!settled || !context) return;
    if (settled.kind === 'ready') this.publishScope(settled.value.scope);
    else this.publishScope(overviewScopeFromBound([], context.repository, context.intentID));
  }
  protected render() {
    const context = this.read.current;
    if (!context) return html``;
    const value = readValue(this.read.state);
    return html`<section aria-label="Factory work overview">
      <p data-work-message aria-live="polite">${readReason(this.read.state)}</p>
      <button type="button" class="ui button" ?disabled=${this.read.busy} @click=${() => this.load()}>
        Refresh overview</button
      >${
        value?.recorded
          ? renderRequestOverview(describeRequestOverview(value.recorded, context.links))
          : value?.model
            ? renderWorkOverview(value.model)
            : this.read.busy
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
): Promise<OverviewRead> {
  const {summary, state: summaryState} = await readOverviewSummary(context, signal);
  return {
    model: describeWorkOverview({
      ...(summary === undefined ? {} : {summary}),
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

interface PlanRead {
  readonly details: FactoryIssueDetail[];
  readonly recorded: RecordedPlanEntry[];
  readonly unread: string[];
}

const emptyPlan: PlanRead = {details: [], recorded: [], unread: []};

export class SodaFactoryWorkPlan extends LitElement {
  private read = new ScopedRead<WorkPlanContext, PlanRead>(
    samePlan,
    () => this.requestUpdate(),
    () => {
      this.context = undefined;
    }
  );
  protected createRenderRoot() {
    return this;
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
    if (this.read.bind(context) === 'same') {
      this.requestUpdate();
      return;
    }
    if (context) void this.load();
  }
  disconnectedCallback() {
    this.context = undefined;
    super.disconnectedCallback();
  }
  refresh() {
    return this.load();
  }
  private async load() {
    await this.read.run(
      async (signal) => {
        const context = this.read.current!;
        const details = await readPlanIssues(context, signal);
        const recorded = await readRecordedPlans(context, signal);
        return {details: details.issues, recorded, unread: details.unread};
      },
      {policy: 'drop', failure: (error) => ({keep: true, reason: errorReason(error, 'Work plan is unavailable.')})}
    );
  }
  protected render() {
    const context = this.read.current;
    if (!context) return html``;
    const loaded = readValue(this.read.state),
      {details, recorded, unread} = loaded ?? emptyPlan;
    const known = new Set(recorded.map((entry) => entry.issue));
    return html`<section aria-label="Factory work plan">
      <p data-work-message aria-live="polite">${readReason(this.read.state)}</p>
      ${unread.length > 0 ? html`<p>${unread.length} work item${unread.length === 1 ? '' : 's'} could not be read.</p>` : ''}
      <button type="button" class="ui button" ?disabled=${this.read.busy} @click=${() => this.load()}>Refresh plan</button>
      ${recorded.map((entry) => renderRecordedChoice(describeRecordedChoice(entry.plan, context.links)))}
      ${
        !loaded && this.read.busy
          ? html`<p>Loading work plan…</p>`
          : renderWorkPlan(
              describeWhyNext(details, context.links, Math.floor(Date.now() / 1000)),
              describePrerequisites(
                details.filter((detail) => !known.has(detail.issue)),
                context.links
              )
            )
      }
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

interface DirectionRead {
  readonly revision: ReadState<IntentRevision>;
  readonly history: ReadState<RevisionHistory>;
}

// The view functions take a value and a SourceState; this is the one place a part's ReadState becomes both.
function viewPart<T>(part: ReadState<T>): [T | undefined, SourceState] {
  return part.kind === 'ready' ? [part.value, 'ready'] : [undefined, part.kind === 'missing' ? 'missing' : 'unavailable'];
}

export class SodaFactoryDirectionRevision extends LitElement {
  private read = new ScopedRead<DirectionRevisionContext, DirectionRead>(
    (a, b) => a.transport === b.transport && a.intentID === b.intentID,
    () => this.requestUpdate(),
    () => {
      this.context = undefined;
    }
  );
  protected createRenderRoot() {
    return this;
  }
  set context(context: DirectionRevisionContext | undefined) {
    if (context)
      check(
        decisionID(context.intentID) &&
          typeof context.transport.generation === 'string' &&
          !!context.transport.generation
      );
    if (this.read.bind(context) === 'same') return;
    if (context) void this.load();
  }
  disconnectedCallback() {
    this.context = undefined;
    super.disconnectedCallback();
  }
  refresh() {
    return this.load();
  }
  private async load() {
    await this.read.run(
      async (signal, previous) => {
        const context = this.read.current!;
        const revision = await readDirectionRevision(context, signal);
        const history = await readRevisionHistory(context, signal);
        // A failed refresh of a part that was loaded keeps the loaded value and marks it stale.
        if (
          (revision.kind === 'unavailable' && previous?.revision.kind === 'ready') ||
          (history.kind === 'unavailable' && previous?.history.kind === 'ready')
        )
          throw Error('Direction is unavailable.');
        return {revision, history};
      },
      {
        policy: 'drop',
        failure: (error) => ({
          keep: true,
          reason: errorReason(error, 'Direction is unavailable.') + ' Last loaded direction is still shown.',
        }),
      }
    );
  }
  protected render() {
    if (!this.read.current) return html``;
    const state = this.read.state,
      value = readValue(state);
    return html`<section aria-label="Factory direction revision">
      <p data-work-message aria-live="polite">${readReason(state)}</p>
      <button type="button" class="ui button" ?disabled=${this.read.busy} @click=${() => this.load()}>
        Refresh direction</button
      >${
        value
          ? html`${renderDirectionRevision(describeDirectionRevision(...viewPart(value.revision)))}
              ${renderRevisionHistory(describeRevisionHistory(...viewPart(value.history)))}`
          : this.read.busy
            ? html`<p>Loading direction…</p>`
            : html`${renderDirectionRevision(describeDirectionRevision(undefined, 'missing'))}
              ${renderRevisionHistory(describeRevisionHistory(undefined, 'missing'))}`
      }
    </section>`;
  }
}
customElements.define('soda-factory-direction-revision', SodaFactoryDirectionRevision);

async function readDirectionRevision(
  context: DirectionRevisionContext,
  signal: AbortSignal
): Promise<ReadState<IntentRevision>> {
  try {
    const revision = intentRevision(await getJSON(context.transport, `factory/intents/${context.intentID}`, signal));
    return {kind: 'ready', value: revision};
  } catch (error) {
    const input = failureInput(error);
    return input.status === 404 ? {kind: 'missing'} : {kind: 'unavailable'};
  }
}

async function readRevisionHistory(
  context: DirectionRevisionContext,
  signal: AbortSignal
): Promise<ReadState<RevisionHistory>> {
  try {
    const history = revisionHistory(
      await getJSON(context.transport, `factory/intents/${context.intentID}/revisions?limit=2`, signal),
      context.intentID
    );
    return {kind: 'ready', value: history};
  } catch (error) {
    const input = failureInput(error);
    return input.status === 404 ? {kind: 'missing'} : {kind: 'unavailable'};
  }
}
