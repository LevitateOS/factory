import {LitElement, html} from 'lit';
import {getJSON, failureInput, readFailureText} from '../api/read.js';
import {readRunRetry, retainRunRetry, settleRunRetry} from './retry-record.js';
import type {PendingRunRetry, RunRetryAddress} from './retry-record.js';
import './acceptance.js';
import './context-resolution.js';
import './completion-retry.js';
import './retained-publication.js';
import './external-pull.js';
import './journey.js';
import type {FactoryTransport} from '../api/transport.js';
import type {FactoryWorkLinks, FactoryAcceptanceLinks} from '../app/navigation.js';
import {check, id, projectId, readSodaJSON} from '../api/models.js';
import {factoryCommandId, factoryRunID, factoryRunStatusResponse, factoryRunText} from '../api/run-response.js';
import type {FactoryRunDetail} from '../api/run-response.js';
import {workJourney} from '../api/journey-response.js';
import {object} from '../api/models.js';
import {factoryAccess} from '../api/progress-response.js';
import type {WorkObservation} from './observation.js';
import {statusText} from '../shared/format.js';

import {
  factoryIssueDetail,
  pendingRuns,
  pauseResult,
  projectStopResult,
  resumeResult,
  retryDecision,
  sodaErrorDetail,
  stopReceipt,
  takeoverRecord,
} from '../api/work-response.js';
import type {FactoryIssueDetail} from '../api/work-response.js';
import {
  capabilityGate,
  capabilityReasonText,
  describeIntervention,
  describeIssueDetail,
  interventionResultText,
  renderInterventions,
  renderWorkDetail,
} from './work-view.js';
import type {InterventionKind, InterventionResult} from './work-view.js';
import type {SourceState} from './progress-view.js';
interface Posted {
  readonly status: number;
  readonly value: unknown;
}

async function writeJSON(
  transport: FactoryTransport,
  path: string,
  method: 'POST' | 'PUT',
  body: unknown,
  signal: AbortSignal
): Promise<Posted> {
  const response = await transport.request(path, {
    method,
    headers: {'Content-Type': 'application/json'},
    body: JSON.stringify(body),
    signal: AbortSignal.any([signal, AbortSignal.timeout(90000)]),
  });
  return {status: response.status, value: await readSodaJSON(response).catch(() => undefined)};
}

async function postJSON(
  transport: FactoryTransport,
  path: string,
  body: unknown,
  signal: AbortSignal
): Promise<Posted> {
  return writeJSON(transport, path, 'POST', body, signal);
}

export interface WorkDetailContext {
  /** Stable profile/provider/account reference admitted by the host; never a credential. */
  readonly commandScope?: (signal: AbortSignal) => Promise<string>;
  transport: FactoryTransport;
  repository: string;
  readonly links?: FactoryWorkLinks;
  readonly acceptanceLinks?: FactoryAcceptanceLinks;
  readonly detailLinks?: (detail: FactoryIssueDetail) => FactoryWorkLinks | undefined;
  issue: string;
  runIDs: string[];
  capabilities: Record<InterventionKind, boolean>;
  readonly watchAvailable?: boolean;
  runID?: string;
  projectID?: string;
  environmentID?: string;
}

interface RunEntry {
  readonly id: string;
  readonly detail: FactoryRunDetail | undefined;
  readonly state: SourceState;
}

type InterventionMarks = Record<InterventionKind, {result: InterventionResult; text: string}>;

function idleMarks(): InterventionMarks {
  const mark = {result: 'idle' as InterventionResult, text: 'Not requested.'};
  return {pause: mark, resume: mark, stop: mark, retry: mark, takeover: mark, project_stop: mark};
}

function detailSignature(context: WorkDetailContext): string {
  const kinds = Object.keys(context.capabilities).sort() as InterventionKind[];
  return [
    context.repository,
    context.issue,
    context.runID ?? '',
    context.runIDs.join(','),
    context.projectID ?? '',
    context.environmentID ?? '',
    context.watchAvailable === false ? '0' : '1',
    ...kinds.map((kind) => (context.capabilities[kind] ? '1' : '0')),
  ].join('|');
}
function sameDetail(a: WorkDetailContext | undefined, b: WorkDetailContext | undefined) {
  if (!a || !b || a.transport !== b.transport) return false;
  return detailSignature(a) === detailSignature(b);
}

function runRowText(entry: RunEntry): string {
  if (!entry.detail) return `run ${entry.id.slice(0, 8)}: unavailable.`;
  const row = factoryRunText({
    id: entry.detail.id,
    role: entry.detail.role,
    reconciled: entry.detail.reconciled,
    ...(entry.detail.outcome === undefined ? {} : {outcome: entry.detail.outcome}),
    ...(entry.detail.view?.issue === undefined ? {} : {issue: entry.detail.view.issue}),
    ...(entry.detail.view?.attempt === undefined ? {} : {attempt: entry.detail.view.attempt}),
  });
  const state = entry.detail.state
    ? statusText(
        entry.detail.state.phase,
        entry.detail.state.live,
        entry.detail.state.terminal,
        entry.detail.state.exit_code ?? null
      )
    : 'recorded';
  return `${row} · ${state}`;
}

export class SodaFactoryWorkDetail extends LitElement {
  static properties = {
    detail: {state: true},
    runs: {state: true},
    marks: {state: true},
    message: {state: true},
    busy: {state: true},
    acting: {state: true},
    retryReady: {state: true},
    observation: {state: true},
  };
  declare private detail: FactoryIssueDetail | undefined;
  declare private runs: RunEntry[];
  declare private marks: InterventionMarks;
  declare private message: string;
  declare private busy: boolean;
  declare private acting: InterventionKind | undefined;
  declare private retryReady: boolean;
  declare private observation: WorkObservation | undefined;
  private readSerial = 0;
  private readRequest: AbortController | undefined;
  private current: WorkDetailContext | undefined;
  private armedStop = false;
  private serial = 0;
  private generation = '';
  private lifetime = new AbortController();
  private pendingRetry: PendingRunRetry | undefined;
  private retryAddress: RunRetryAddress | undefined;
  protected createRenderRoot() {
    return this;
  }
  constructor() {
    super();
    this.runs = [];
    this.marks = idleMarks();
    this.message = '';
    this.busy = false;
    this.retryReady = false;
  }
  set context(context: WorkDetailContext | undefined) {
    if (context)
      check(
        id(context.repository) &&
          id(context.issue) &&
          typeof context.transport.generation === 'string' &&
          !!context.transport.generation &&
          context.runIDs.length <= 64 &&
          context.runIDs.every(factoryRunID) &&
          (context.runID === undefined || factoryRunID(context.runID)) &&
          (context.projectID === undefined || projectId(context.projectID)) &&
          (context.environmentID === undefined || projectId(context.environmentID))
      );
    if (sameDetail(context, this.current) && context?.transport.generation === this.generation) {
      this.current = context && {...context, runIDs: [...context.runIDs], capabilities: {...context.capabilities}};
      this.requestUpdate();
      return;
    }
    this.readRequest?.abort();
    this.readSerial++;
    this.observation = undefined;
    this.lifetime.abort();
    this.lifetime = new AbortController();
    this.generation = context?.transport.generation ?? '';
    this.pendingRetry = undefined;
    this.retryAddress = undefined;
    this.retryReady = false;
    this.current = context && {...context, runIDs: [...context.runIDs], capabilities: {...context.capabilities}};
    this.serial++;
    this.detail = undefined;
    this.runs = [];
    this.marks = idleMarks();
    this.message = '';
    this.busy = false;
    this.acting = undefined;
    this.armedStop = false;
    if (context) void this.load();
  }
  disconnectedCallback() {
    this.context = undefined;
    super.disconnectedCallback();
  }
  private live(serial: number, context: WorkDetailContext): boolean {
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
  private setMark(kind: InterventionKind, result: InterventionResult, text: string) {
    this.marks = {...this.marks, [kind]: {result, text}};
  }
  private async load() {
    const context = this.current;
    if (!context || !this.live(this.serial, context)) return;
    const serial = this.serial,
      refresh = ++this.readSerial;
    this.readRequest?.abort();
    const request = new AbortController();
    this.readRequest = request;
    const signal = AbortSignal.any([request.signal, this.lifetime.signal]);
    const current = () => this.live(serial, context) && refresh === this.readSerial;
    const observation = {
      transport: context.transport,
      generation: this.generation,
      repository: context.repository,
      issue: context.issue,
      refresh,
    };
    this.busy = true;
    this.retryReady = false;
    this.observation = {...observation, state: 'loading', detail: this.detail, journey: this.observation?.journey};
    try {
      const [detail, accessResult, runs] = await Promise.all([
        readDetailIssue(context, signal),
        getJSON(context.transport, `repositories/${context.repository}/factory/access`, signal)
          .then((value) => {
            check(object(value).repository === context.repository);
            return factoryAccess(value, context.repository);
          })
          .catch(() => undefined),
        readDetailRuns(context, signal),
      ]);
      if (!current()) return;
      const observedRun = detail.detail?.attempt?.run;
      if (factoryRunID(observedRun) && !runs.some(entry => entry.id === observedRun)) {
        const observed = await readDetailRuns(context, signal, [observedRun]);
        if (!current()) return;
        runs.unshift(...observed);
      }
      const ready = !!detail.detail && !!accessResult;
      this.detail = detail.detail ?? this.detail;
      this.runs = runs;
      this.observation = {
        ...observation,
        state: ready ? 'ready' : 'stale',
        detail: this.detail,
        journey: detail.detail ? detail.journey : this.observation?.journey,
        ...(ready ? {access: accessResult} : {}),
      };
      this.message = ready
        ? ''
        : `${detail.message || 'Current repository access is unavailable.'} Retained work history is stale; controls are unavailable.`;
      if (ready && context.capabilities.retry && this.backendGate('retry').available)
        await this.resolveRetryIdentity(context, current, signal);
    } catch (error) {
      if (current()) {
        this.observation = {...observation, state: 'stale', detail: this.detail, journey: this.observation?.journey};
        this.message = 'Work detail is unavailable. Retained work history is stale; controls are unavailable.';
      }
    } finally {
      if (current()) this.busy = false;
    }
  }
  private act = (kind: InterventionKind) => {
    void this.runIntervention(kind);
  };
  private async resolveRetryIdentity(
    context: WorkDetailContext,
    current: () => boolean,
    signal: AbortSignal
  ): Promise<boolean> {
    try {
      if (!context.commandScope) throw Error('Missing retry identity');
      const scope = await context.commandScope(AbortSignal.any([signal, AbortSignal.timeout(15000)]));
      if (!current()) return false;
      const address: RunRetryAddress = {scope, repository: context.repository, issue: context.issue};
      const captured = readRunRetry(address);
      if (captured.blocked) {
        this.retryReady = false;
        this.pendingRetry = undefined;
        this.setMark(
          'retry',
          'failed',
          'Saved retry is unreadable or belongs to another scope. It was preserved; no new request was sent.'
        );
        return false;
      }
      const pending = captured.pending;
      if (pending && pending.run !== context.runID && !context.runIDs.includes(pending.run)) {
        const run = factoryRunStatusResponse(
          await getJSON(context.transport, `factory/runs/${pending.run}`, signal),
          pending.run,
          context.repository
        );
        if (!current()) return false;
        check(run.view?.issue === context.issue);
      }
      this.retryAddress = address;
      this.pendingRetry = pending;
      this.retryReady = true;
      if (pending)
        this.setMark(
          'retry',
          'uncertain',
          'An unconfirmed retry is saved. Retry the same request to check its recorded result.'
        );
      return true;
    } catch {
      if (current()) {
        this.retryReady = false;
        this.pendingRetry = undefined;
        this.setMark(
          'retry',
          'failed',
          'Retry identity or its saved run could not be read. Refresh detail before retrying. No new request was sent.'
        );
      }
      return false;
    }
  }
  private armStop(kind: InterventionKind): boolean {
    if (kind !== 'project_stop' || this.armedStop) return false;
    this.armedStop = true;
    this.setMark(kind, 'idle', 'Click again to confirm stopping the project for everyone.');
    return true;
  }
  private backendGate(kind: InterventionKind): {available: boolean; reasonText: string} {
    if (!this.detail || this.observation?.state !== 'ready')
      return {available: false, reasonText: 'Current work authority is unavailable.'};
    return capabilityGate(this.detail.capabilities, kind);
  }
  private async runIntervention(kind: InterventionKind) {
    const context = this.current;
    if (!context || !this.live(this.serial, context) || this.acting) return;
    if (!context.capabilities[kind] || this.armStop(kind)) return;
    const gate = this.backendGate(kind);
    if (!gate.available) {
      this.setMark(kind, 'failed', `Unavailable: ${gate.reasonText}`);
      return;
    }
    const serial = this.serial;
    this.acting = kind;
    try {
      if (
        kind === 'retry' &&
        !(await this.resolveRetryIdentity(context, () => this.live(serial, context), this.lifetime.signal))
      )
        return;
      if (!this.live(serial, context) || !this.backendGate(kind).available) return;
      const runID = kind === 'retry' ? this.retryRunID() : this.selectedRunID();
      if ((kind === 'stop' || kind === 'retry' || kind === 'takeover') && !runID) {
        this.setMark(kind, 'failed', 'No recorded run is available for this action on this work.');
        return;
      }
      const commandID = kind === 'retry' ? (this.pendingRetry?.command ?? factoryCommandId()) : '';
      if (kind === 'retry' && runID && this.retryAddress) {
        const pending = {command: commandID, run: runID};
        this.pendingRetry = pending;
        const retained = retainRunRetry(this.retryAddress, pending);
        if (retained !== 'saved') {
          this.setMark(
            kind,
            'failed',
            retained === 'conflict'
              ? 'A different or unreadable retry is already saved. It was preserved; no new request was sent.'
              : 'Retry identity could not be saved. No request was sent. Retry after storage is available.'
          );
          return;
        }
      }
      this.setMark(kind, 'pending', 'Requested; awaiting confirmation.');
      await this.executeIntervention(context, kind, commandID, serial, runID, this.retryAddress);
    } finally {
      if (this.live(serial, context)) this.acting = undefined;
    }
  }
  private async executeIntervention(
    context: WorkDetailContext,
    kind: InterventionKind,
    commandID: string,
    serial: number,
    runID: string | undefined,
    retryAddress: RunRetryAddress | undefined
  ) {
    try {
      const posted = await postIntervention(context, kind, commandID, runID, this.lifetime.signal);
      if (!this.live(serial, context)) return;
      if (kind === 'retry' && retryAddress) {
        const captured = readRunRetry(retryAddress);
        if (
          this.retryAddress?.scope !== retryAddress.scope ||
          captured.blocked ||
          captured.pending?.command !== commandID ||
          captured.pending.run !== runID
        ) {
          this.setMark(
            'retry',
            'uncertain',
            'Saved retry changed while waiting. Its current record was preserved; refresh detail.'
          );
          return;
        }
      }
      this.applyPosted(kind, context, posted, commandID, runID, retryAddress);
      void this.load();
    } catch {
      if (!this.live(serial, context)) return;
      this.setMark(
        kind,
        'uncertain',
        kind === 'retry'
          ? 'No reply was confirmed. Retry the same request or refresh.'
          : 'No reply was confirmed. Refresh before acting again.'
      );
    }
  }
  private applyPosted(
    kind: InterventionKind,
    context: WorkDetailContext,
    posted: Posted,
    commandID: string,
    runID: string | undefined,
    retryAddress: RunRetryAddress | undefined
  ) {
    try {
      const outcome = parsePostedOutcome(kind, context, posted, commandID, runID);
      // An access refusal on a replay cannot prove whether an earlier request
      // applied. Only the recorded retry receipt settles this captured identity.
      if (kind === 'retry' && outcome.kind === 'retry' && retryAddress && runID) {
        settleRunRetry(retryAddress, {command: commandID, run: runID});
        if (
          this.retryAddress?.scope === retryAddress.scope &&
          this.pendingRetry?.command === commandID &&
          this.pendingRetry.run === runID
        )
          this.pendingRetry = undefined;
      }
      const rendered = interventionResultText(outcome);
      this.setMark(kind, rendered.result, rendered.text);
    } catch {
      this.setMark(kind, 'uncertain', 'The reply could not be understood; refresh before acting again.');
    }
  }
  private requestWatch(runID: string) {
    const context = this.current;
    if (
      !context ||
      context.watchAvailable === false ||
      !this.live(this.serial, context) ||
      this.observation?.state !== 'ready' ||
      !this.detail?.capabilities.watch.available ||
      !this.runs.some((entry) => entry.id === runID)
    )
      return;
    this.dispatchEvent(
      new CustomEvent('soda-factory-watch-request', {
        bubbles: true,
        composed: true,
        detail: {run: runID, repository: this.current?.repository, environment: this.current?.environmentID},
      })
    );
  }
  /** Newest-first runIDs make the first settled row the latest recorded
   * run for this work: the exact retry target, never a guess. Live work
   * keeps the live selection; stop and takeover never fall back. */
  private retryRunID(): string | undefined {
    const context = this.current;
    if (this.pendingRetry) return this.pendingRetry.run;
    if (context?.runID) return context.runID;
    return this.runs.find((entry) => entry.detail?.reconciled)?.id;
  }
  private selectedRunID(): string | undefined {
    if (this.current?.runID) return this.current.runID;
    const observed = this.detail?.attempt?.run;
    return this.observation?.state === 'ready' && factoryRunID(observed) ? observed : undefined;
  }
  private interventionMarks(kind: InterventionKind) {
    const context = this.current;
    const gate = this.backendGate(kind);
    const mark = this.marks[kind] ?? {result: 'idle' as InterventionResult, text: 'Not requested.'};
    const runID = kind === 'retry' ? this.retryRunID() : this.selectedRunID();
    return describeIntervention({
      kind,
      enabled:
        (context?.capabilities[kind] ?? false) &&
        gate.available &&
        this.acting === undefined &&
        (kind !== 'retry' || this.retryReady) &&
        (!['retry', 'stop', 'takeover'].includes(kind) || runID !== undefined),
      result: mark.result,
      resultText: !gate.available && mark.result === 'idle' ? `Unavailable: ${gate.reasonText}` : mark.text,
      target: interventionTarget(context, kind, runID),
    });
  }
  private watchNote(): string {
    if (this.current?.watchAvailable === false) return 'Live output is unavailable in this app.';
    const watch = this.detail?.capabilities.watch;
    if (!watch || watch.available) return '';
    return `Watch unavailable: ${capabilityReasonText(watch.reason)}`;
  }
  protected render() {
    const context = this.current;
    if (!context) return html``;
    const watchable = context.watchAvailable !== false && this.observation?.state === 'ready' && (this.detail?.capabilities.watch.available ?? false);
    return html`<section aria-label="Factory work detail" @soda-factory-work-refresh=${() => this.load()}>
      <p data-work-message aria-live="polite">${this.message}</p>
      <button type="button" class="ui button" ?disabled=${this.busy} @click=${() => this.load()}>Refresh detail</button>
      ${this.detail ? renderWorkDetail(describeIssueDetail(this.detail, context.detailLinks ? context.detailLinks(this.detail) : context.links)) : this.busy ? html`<p>Loading work detail…</p>` : ''}
      <soda-factory-retained-publication
        .context=${{transport: context.transport, repository: context.repository, issue: context.issue, detail: this.detail, commandScope: context.commandScope}}
      ></soda-factory-retained-publication>
      <soda-factory-external-pull
        .context=${{transport: context.transport, repository: context.repository, issue: context.issue, available: this.detail?.capabilities.adopt_external?.available === true, commandScope: context.commandScope}}
      ></soda-factory-external-pull>
      <soda-factory-work-journey
        .context=${{observation: this.observation, transport: context.transport, repository: context.repository, links: context.links, issue: context.issue}}
      ></soda-factory-work-journey>
      <soda-factory-requirements-acceptance
        .context=${{observation: this.observation, transport: context.transport, repository: context.repository, links: context.acceptanceLinks, issue: context.issue, commandScope: context.commandScope}}
        @soda-factory-requirements-accepted=${() => this.load()}
      ></soda-factory-requirements-acceptance>
      <soda-factory-completion-retry
        .context=${{observation: this.observation, transport: context.transport, repository: context.repository, issue: context.issue, commandScope: context.commandScope}}
        @soda-completion-retry-queued=${() => this.load()}
      ></soda-factory-completion-retry>
      ${this.watchNote() ? html`<p>${this.watchNote()}</p>` : ''}
      <ul>
        ${this.runs.map(
          (entry) => html`<li>
            ${runRowText(entry)}
            ${entry.detail ? html`<soda-factory-context-resolution .context=${{transport: context.transport, repository: context.repository, issue: context.issue, commandScope: context.commandScope, run: entry.detail}} @soda-context-resolved=${() => this.load()}></soda-factory-context-resolution>` : ''}
            <button type="button" class="ui button" ?disabled=${!watchable} @click=${() => this.requestWatch(entry.id)}>
              Watch output
            </button>
          </li>`
        )}
      </ul>
      ${renderInterventions(
        (Object.keys(this.marks) as InterventionKind[]).map((kind) => this.interventionMarks(kind)),
        {act: this.act}
      )}
    </section>`;
  }
}
customElements.define('soda-factory-work-detail', SodaFactoryWorkDetail);

function interventionTarget(
  context: WorkDetailContext | undefined,
  kind: InterventionKind,
  runID: string | undefined = context?.runID
): string {
  if (!context) return 'this work';
  if (kind === 'pause' || kind === 'resume') return `repository ${context.repository}`;
  if (kind === 'project_stop') return context.projectID ? `project ${context.projectID}` : 'this project';
  return runID ? `run ${runID.slice(0, 8)}` : 'no run selected';
}

async function readDetailIssue(
  context: WorkDetailContext,
  signal: AbortSignal
): Promise<{detail?: FactoryIssueDetail; journey?: import('../api/journey-response.js').WorkJourney; message: string}> {
  try {
    const value = await getJSON(
      context.transport,
      `repositories/${context.repository}/factory/issues/${context.issue}`,
      signal
    );
    const raw = object(value);
    check(raw.repository === context.repository && raw.issue === context.issue);
    const journey = raw.journey === undefined ? undefined : workJourney(raw.journey, context.repository, context.issue);
    const detail = factoryIssueDetail(value, context.repository, context.issue);
    return {detail, ...(journey ? {journey} : {}), message: ''};
  } catch (error) {
    const input = failureInput(error);
    const failure = readFailureText(input.status, input.detail, 'Work item not found.', 'Work detail is unavailable.');
    return {message: failure.text};
  }
}

async function readDetailRuns(context: WorkDetailContext, signal: AbortSignal, selected = [...context.runIDs, ...(context.runID ? [context.runID] : [])]): Promise<RunEntry[]> {
  const runIDs = [...new Set(selected)];
  const settled = await Promise.allSettled(
    runIDs.map((runID) => getJSON(context.transport, `factory/runs/${runID}`, signal))
  );
  return settled.map((result, index) => {
    const runID = runIDs[index] ?? '';
    if (result.status !== 'fulfilled' || runID === '')
      return {id: runID, detail: undefined, state: 'unavailable' as SourceState};
    try {
      return {
        id: runID,
        detail: factoryRunStatusResponse(result.value, runID, context.repository),
        state: 'ready' as SourceState,
      };
    } catch {
      return {id: runID, detail: undefined, state: 'unavailable' as SourceState};
    }
  });
}

async function postIntervention(
  context: WorkDetailContext,
  kind: InterventionKind,
  commandID: string,
  runID: string | undefined,
  signal: AbortSignal
): Promise<Posted> {
  if (kind === 'pause' || kind === 'resume')
    return postJSON(
      context.transport,
      `repositories/${context.repository}/factory/actions`,
      {
        action: kind,
      },
      signal
    );
  if (kind === 'project_stop' && context.environmentID)
    return postJSON(
      context.transport,
      `environments/${context.environmentID}/lifecycle`,
      {
        action: 'stop',
        confirm_stop: true,
      },
      signal
    );
  if ((kind === 'stop' || kind === 'takeover') && runID)
    return postJSON(context.transport, `factory/runs/${runID}/actions`, {action: kind}, signal);
  if (kind === 'retry' && runID)
    return postJSON(context.transport, `factory/runs/${runID}/actions`, {command_id: commandID, action: kind}, signal);
  throw Error('Intervention target is not available in this context.');
}

function parseRepositoryReceipt(kind: 'pause' | 'resume', context: WorkDetailContext, posted: Posted) {
  if (kind === 'pause') return {kind: 'pause' as const, receipt: pauseResult(posted.value, context.repository)};
  return {kind: 'resume' as const, receipt: resumeResult(posted.value)};
}
function parseRunReceipt(
  kind: 'stop' | 'retry' | 'takeover',
  context: WorkDetailContext,
  posted: Posted,
  commandID: string,
  runID: string | undefined
) {
  const target = runID ?? '';
  if (kind === 'stop') return {kind: 'stop' as const, receipt: stopReceipt(posted.value, target)};
  if (kind === 'retry') return {kind: 'retry' as const, receipt: retryDecision(posted.value, commandID, target)};
  return {kind: 'takeover' as const, receipt: takeoverRecord(posted.value, target)};
}
function parsePostedReceipt(
  kind: InterventionKind,
  context: WorkDetailContext,
  posted: Posted,
  commandID: string,
  runID: string | undefined
) {
  if (kind === 'pause' || kind === 'resume') return parseRepositoryReceipt(kind, context, posted);
  if (kind === 'project_stop')
    return {kind: 'project_stop' as const, receipt: projectStopResult(posted.value, context.projectID ?? '')};
  return parseRunReceipt(kind, context, posted, commandID, runID);
}

function parsePostedOutcome(
  kind: InterventionKind,
  context: WorkDetailContext,
  posted: Posted,
  commandID: string,
  runID: string | undefined
) {
  if (posted.status === 200) return parsePostedReceipt(kind, context, posted, commandID, runID);
  if (posted.status === 409) {
    try {
      return {kind: 'pending_runs' as const, pending: pendingRuns(posted.value)};
    } catch {
      return {kind: 'refused' as const, status: posted.status, message: 'Request refused while recording runs.'};
    }
  }
  const detail = sodaErrorDetail(posted.value);
  return {
    kind: 'refused' as const,
    status: posted.status,
    message: detail.message ?? `Request refused (${posted.status}).`,
  };
}
