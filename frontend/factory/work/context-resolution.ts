import {LitElement, html} from 'lit';
import type {FactoryTransport} from '../api/transport.js';
import {object, check, id, readSodaJSON} from '../api/models.js';
import {factoryRunID, factoryRunStatusResponse} from '../api/run-response.js';
import type {FactoryRunDetail} from '../api/run-response.js';
import {readContextChoices, retainContextChoice, sameContextChoice, settleContextChoice} from './context-record.js';
import type {ContextChoiceAddress, PendingContextChoice} from './context-record.js';

/** A refreshed run takes precedence over an issue-based lookup. */
export interface ResolutionContext {
  readonly transport: FactoryTransport;
  readonly run?: FactoryRunDetail;
  readonly repository?: string;
  readonly issue?: string;
  /** Stable profile/provider/account reference supplied by the host, never a credential. */
  readonly commandScope?: (signal: AbortSignal) => Promise<string>;
}

function runSelectionKey(run: FactoryRunDetail): string {
  const selection = run.context_selection;
  return [
    run.id,
    run.view?.repository ?? '',
    run.view?.issue ?? '',
    selection?.action ?? '',
    selection?.availability ?? '',
    selection?.can_use_current === true ? '1' : '0',
    selection?.fingerprint ?? '',
    selection?.scope_digest ?? '',
    selection?.required_digest ?? '',
  ].join('|');
}

export class SodaFactoryContextResolution extends LitElement {
  static properties = {
    message: {state: true},
    busy: {state: true},
    loading: {state: true},
    selectedRun: {state: true},
    ready: {state: true},
    pending: {state: true},
    identityMessage: {state: true},
    cleanupMessage: {state: true},
  };
  declare private ready: boolean;
  declare private pending: readonly PendingContextChoice[];
  private address: ContextChoiceAddress | undefined;
  declare private identityMessage: string;
  declare private cleanupMessage: string;
  declare private message: string;
  declare private busy: boolean;
  declare private loading: boolean;
  declare private selectedRun: FactoryRunDetail | undefined;
  private current: ResolutionContext | undefined;
  private scope = '';
  private generation = '';
  private binding = 0;
  private readToken = 0;
  private selectionVersion = 0;
  private readRequest: AbortController | undefined;
  private actionRequest: AbortController | undefined;
  private lastRunKey = '';
  private admittedScope = '';
  private identityRequest: AbortController | undefined;

  constructor() {
    super();
    this.message = '';
    this.identityMessage = '';
    this.cleanupMessage = '';
    this.ready = false;
    this.pending = [];
    this.busy = this.loading = false;
  }
  protected createRenderRoot() {
    return this;
  }
  get context(): ResolutionContext | undefined {
    return this.current;
  }
  set context(context: ResolutionContext | undefined) {
    if (context) {
      check(typeof context.transport.generation === 'string' && !!context.transport.generation);
      if (context.run) check(factoryRunID(context.run.id));
      else check(id(context.repository) && id(context.issue));
    }
    const scope = context
      ? context.run
        ? `run:${context.run.id}/${context.repository ?? ''}/${context.issue ?? ''}`
        : `issue:${context.repository}/${context.issue}`
      : '';
    const generation = context?.transport.generation ?? '';
    const changed =
      scope !== this.scope ||
      context?.transport !== this.current?.transport ||
      generation !== this.generation ||
      !!context?.commandScope !== !!this.current?.commandScope;
    this.current = context;
    if (changed) {
      this.binding++;
      this.scope = scope;
      this.generation = generation;
      this.cancelRead();
      this.clearSelection();
      this.identityRequest?.abort();
      this.identityRequest = undefined;
      this.admittedScope = '';
      this.cleanupMessage = '';
      this.address = undefined;
      this.pending = [];
      this.identityMessage = '';
      this.ready = false;
      this.requestUpdate();
    }
    // Direct refreshed data supersedes lookups without retiring an unchanged
    // action selection on every parent render (FJ-447).
    if (context?.run) {
      this.cancelRead();
      this.installRun(context.run);
    } else if (changed && context) void this.readRun(context);
    if (changed && context) void this.admitIdentity(context);
  }
  disconnectedCallback() {
    this.context = undefined;
    super.disconnectedCallback();
  }
  private cancelRead() {
    this.readToken++;
    this.readRequest?.abort();
    this.readRequest = undefined;
    this.loading = false;
  }
  private clearSelection() {
    this.selectionVersion++;
    this.actionRequest?.abort();
    this.actionRequest = undefined;
    this.selectedRun = undefined;
    this.lastRunKey = '';
    this.message = '';
    this.busy = false;
  }
  private installRun(run: FactoryRunDetail) {
    const key = runSelectionKey(run);
    if (key !== this.lastRunKey) {
      this.clearSelection();
      this.lastRunKey = key;
    }
    this.selectedRun = run;
    this.syncSaved();
  }
  private bound(context: ResolutionContext, binding: number, generation: string): boolean {
    if (binding !== this.binding || context.transport !== this.current?.transport) return false;
    if (context.transport.generation !== generation) {
      this.context = undefined;
      return false;
    }
    return true;
  }
  private async commandScope(context: ResolutionContext, signal: AbortSignal): Promise<string> {
    check(id(context.repository) && (context.issue === undefined || id(context.issue)));
    check(!!context.commandScope);
    const scope = await context.commandScope!(signal);
    signal.throwIfAborted();
    check(
      typeof scope === 'string' && scope.length > 0 && scope.length <= 1024 && !/[\u0000-\u0020\u007f]/.test(scope)
    );
    return scope;
  }
  private async admitIdentity(context: ResolutionContext) {
    const binding = this.binding,
      generation = this.generation;
    if (!this.bound(context, binding, generation)) return;
    this.identityRequest?.abort();
    const request = new AbortController();
    this.identityRequest = request;
    this.ready = false;
    const live = () => this.identityRequest === request && this.bound(context, binding, generation);
    try {
      const scope = await this.commandScope(context, AbortSignal.any([request.signal, AbortSignal.timeout(15000)]));
      if (!live()) return;
      if (this.address?.scope !== scope) this.cleanupMessage = '';
      this.admittedScope = scope;
      this.identityMessage = '';
      this.address = {scope, repository: context.repository!, issue: context.issue ?? ''};
      this.ready = this.syncSaved();
    } catch {
      if (live())
        this.identityMessage = 'Could not verify the current account. Refresh current context before choosing.';
    } finally {
      if (this.identityRequest === request) this.identityRequest = undefined;
    }
  }
  private syncSaved(): boolean {
    if (!this.address) return false;
    const stored = readContextChoices(this.address);
    this.pending = this.current?.run
      ? stored.choices.filter((choice) => choice.run === this.current!.run!.id)
      : stored.choices;
    if (stored.blocked) {
      this.ready = false;
      this.identityMessage =
        'Saved context choices are unreadable or belong to another scope. They were preserved; no request was sent.';
      return false;
    }
    return true;
  }
  private async readRun(context: ResolutionContext) {
    const binding = this.binding,
      generation = this.generation;
    if (!this.bound(context, binding, generation)) return;
    this.cancelRead();
    const token = this.readToken;
    const request = new AbortController();
    this.readRequest = request;
    const signal = AbortSignal.any([request.signal, AbortSignal.timeout(15000)]);
    const live = () => token === this.readToken && this.bound(context, binding, generation);
    this.loading = true;
    if (!this.selectedRun) this.message = 'Loading current context…';
    try {
      let runID = context.run?.id;
      if (!runID) {
        const response = await context.transport.request(
          `repositories/${context.repository}/factory/issues/${context.issue}`,
          {method: 'GET', signal}
        );
        if (!live()) return;
        check(response.ok);
        const issue = object(await readSodaJSON(response));
        if (!live()) return;
        const lineage = object(object(issue.journey).lineage);
        check(factoryRunID(lineage.latest_run));
        runID = lineage.latest_run;
      }
      const runResponse = await context.transport.request(`factory/runs/${runID}`, {
        method: 'GET',
        signal,
      });
      if (!live()) return;
      check(runResponse.ok);
      const run = this.decodeRun(await readSodaJSON(runResponse), runID, context);
      if (!live()) return;
      // Changed same-issue selections retire only the superseded action UI;
      // the server operation is neither retried nor declared cancelled (FJ-450).
      this.installRun(run);
    } catch {
      if (live()) {
        this.clearSelection();
        this.message = 'Could not load current context. Refresh the work detail.';
      }
    } finally {
      if (live()) this.loading = false;
      if (this.readRequest === request) this.readRequest = undefined;
    }
  }
  private decodeRun(value: unknown, runID: string, context: ResolutionContext): FactoryRunDetail {
    const row = object(value);
    // The general display decoder uses the requested ID; admission must verify the wire ID too.
    check(object(row.run).id === runID);
    const run = factoryRunStatusResponse(row, runID, context.repository!);
    check(
      id(context.repository) &&
        run.view?.repository === context.repository &&
        (context.issue === undefined || run.view?.issue === context.issue)
    );
    return run;
  }
  private verify(
    value: unknown,
    run: string,
    selection: Pick<PendingContextChoice, 'fingerprint' | 'scope_digest' | 'required_digest'>
  ) {
    const row = object(value);
    check(
      row.run_id === run &&
        row.bundle === 'current' &&
        row.scope_digest === selection.scope_digest &&
        row.fingerprint === selection.fingerprint &&
        row.required_digest === selection.required_digest
    );
  }
  refresh() {
    const context = this.current;
    if (!context) return;
    // Explicit refresh admits the current account as well as an unchanged issue (FJ-446).
    void this.readRun(context);
    if (!this.busy) void this.admitIdentity(context);
  }
  private async resolve(saved?: PendingContextChoice) {
    const context = this.current,
      run = this.selectedRun;
    const binding = this.binding,
      generation = this.generation;
    if (!context || !this.bound(context, binding, generation) || this.busy || !this.ready || !this.address) return;
    const selection = run?.context_selection;
    if (!saved && (!run || !selection?.can_use_current || selection.action !== 'hold')) return;
    if (saved && !this.pending.some((choice) => sameContextChoice(choice, saved))) return;
    const choice: PendingContextChoice = saved
      ? {...saved}
      : {
          run: run!.id,
          fingerprint: selection!.fingerprint,
          scope_digest: selection!.scope_digest,
          required_digest: selection!.required_digest ?? '',
        };
    const replay = this.pending.some((row) => sameContextChoice(row, choice));
    const address = this.address,
      version = this.selectionVersion;
    const selectionKey = run && runSelectionKey(run);
    const request = new AbortController();
    this.actionRequest = request;
    const live = () =>
      this.bound(context, binding, generation) &&
      version === this.selectionVersion &&
      (!!saved || (!!this.selectedRun && runSelectionKey(this.selectedRun) === selectionKey));
    this.busy = true;
    this.message = 'Recording your current-context choice…';
    const signal = AbortSignal.any([request.signal, AbortSignal.timeout(30000)]);
    let dispatched = false;
    try {
      check(
        factoryRunID(choice.run) &&
          [choice.fingerprint, choice.scope_digest, choice.required_digest].every((value) =>
            /^[0-9a-f]{64}$/.test(value)
          )
      );
      const freshResponse = await context.transport.request(`factory/runs/${choice.run}`, {method: 'GET', signal});
      if (!live()) return;
      check(freshResponse.ok);
      const fresh = this.decodeRun(await readSodaJSON(freshResponse), choice.run, context);
      if (!live()) return;
      // A new choice needs current eligibility. Recovery submits only its exact
      // retained tuple: a changed current projection cannot prove whether an
      // earlier request applied. The writer owns admission and principal-bound replay.
      if (!replay)
        check(
          !fresh.reconciled &&
            !fresh.outcome &&
            fresh.context_selection?.can_use_current === true &&
            fresh.context_selection.action === 'hold' &&
            fresh.context_selection.availability === 'complete' &&
            fresh.context_selection.fingerprint === choice.fingerprint &&
            fresh.context_selection.scope_digest === choice.scope_digest &&
            fresh.context_selection.required_digest === choice.required_digest
        );
      const currentScope = await this.commandScope(this.current!, signal);
      if (!live()) return;
      if (currentScope !== address.scope) {
        this.ready = false;
        this.message = '';
        this.identityMessage = 'The current account changed. Refresh current context before choosing.';
        return;
      }
      const current = readContextChoices(address);
      if (current.blocked || (replay && !current.choices.some((row) => sameContextChoice(row, choice)))) {
        this.syncSaved();
        this.ready = false;
        this.message = 'The saved context choice changed. Refresh current context before retrying.';
        return;
      }
      const retained = retainContextChoice(address, choice);
      this.syncSaved();
      if (retained !== 'saved') {
        this.message =
          retained === 'unavailable'
            ? 'The context choice could not be saved. No request was sent. Retry when storage is available.'
            : retained === 'full'
              ? 'Saved context choices need to be checked before another choice can be saved. No request was sent.'
              : 'The saved context choices changed. Refresh current context before retrying.';
        return;
      }
      signal.throwIfAborted();
      this.cleanupMessage = '';
      dispatched = true;
      const response = await context.transport.request(`factory/runs/${choice.run}/actions`, {
        method: 'POST',
        headers: {'Content-Type': 'application/json'},
        signal,
        body: JSON.stringify({
          action: 'use_current_context',
          context: {
            fingerprint: choice.fingerprint,
            scope_digest: choice.scope_digest,
            required_digest: choice.required_digest,
          },
        }),
      });
      if (!live()) return;
      const body = await readSodaJSON(response);
      if (!live()) return;
      if (!response.ok) {
        this.message =
          'This context choice is no longer available. Its exact request remains saved. Refresh the work detail.';
        return;
      }
      this.verify(body, choice.run, choice);
      const settled = settleContextChoice(address, choice);
      if (settled === 'conflict') {
        this.syncSaved();
        this.ready = false;
        this.message = 'The saved context choices changed. No saved choice was cleared. Refresh current context.';
        return;
      }
      this.syncSaved();
      this.cleanupMessage =
        settled === 'unavailable'
          ? 'Saved-choice cleanup could not be persisted; reloading may offer the same retry.'
          : '';
      this.message =
        'Current required context selected. Factory will recheck this run before starting.' +
        (settled === 'unavailable'
          ? ' Saved-choice cleanup could not be persisted; reloading may offer the same retry.'
          : '');
      this.dispatchEvent(new CustomEvent('soda-context-resolved', {bubbles: true, composed: true}));
    } catch {
      if (live()) {
        if (!dispatched) {
          this.ready = false;
          this.message = '';
          this.identityMessage = 'This context choice could not be verified. Refresh current context before choosing.';
        } else this.message = 'No result was confirmed. Retry this same choice or refresh the work detail.';
      }
    } finally {
      if (live()) this.busy = false;
      if (this.actionRequest === request) this.actionRequest = undefined;
    }
  }
  protected render() {
    if (!this.current) return html``;
    const selection = this.selectedRun?.context_selection;
    const held = selection?.action === 'hold';
    const message =
      this.message ||
      this.identityMessage ||
      this.cleanupMessage ||
      (this.pending.length
        ? 'An unconfirmed context choice is saved. Retry the same choice to check its recorded result.'
        : '');
    if (!held && !this.pending.length && !message) return html``;
    return html`<section aria-label=${held ? 'Context choice needs attention' : 'Context choice'}>
      ${
        held
          ? html`<p>Factory could not confidently choose which context to foreground.</p>
              <p>
                ${selection.availability === 'complete' ? 'The required current context is complete. You can use it for this admitted run.' : 'Required current context is unavailable. Resolve its missing inputs before continuing.'}
              </p>
              <button
                type="button"
                class="ui button"
                ?disabled=${this.busy || !this.ready || !selection.can_use_current}
                @click=${() => this.resolve()}
              >
                Use current required context
              </button>`
          : ''
      }
      ${
        this.pending.length
          ? html`<div aria-label="Saved context choices">
              <p>Each retry submits the original saved selection.</p>
              <ul>
                ${this.pending.map(
                  (choice, index) => html`<li>
                    <span>Saved choice ${index + 1} for run ${choice.run.slice(0, 8)}.</span>
                    <button
                      type="button"
                      class="ui button"
                      ?disabled=${this.busy || !this.ready || this.loading}
                      @click=${() => this.resolve(choice)}
                    >
                      Retry saved context choice ${index + 1}
                    </button>
                  </li>`
                )}
              </ul>
            </div>`
          : ''
      }
      <p role="status">${message}</p>
      ${
        !this.busy && !this.loading && (!this.ready || this.pending.length || !selection)
          ? html`<button type="button" class="ui button" @click=${() => this.refresh()}>
              Refresh current context
            </button>`
          : ''
      }
    </section>`;
  }
}
customElements.define('soda-factory-context-resolution', SodaFactoryContextResolution);
