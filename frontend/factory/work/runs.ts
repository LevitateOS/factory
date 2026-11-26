import {LitElement, html} from 'lit';
import {check, id, object} from '../api/models.js';
import {getJSON} from '../api/read.js';
import type {FactoryTransport} from '../api/transport.js';
import {factoryRunID} from '../api/run-response.js';
import {roleLabel} from '../feed/roles.js';
import {SodaFactoryWatch} from '../watch/watch.js';
import type {FactoryWatchRenderer, FactoryWatchSource} from '../watch/ports.js';

/** The host's read-only output attachment; absent or unavailable shows the
 * list without a terminal. */
export interface WorkWatch {
  readonly source: FactoryWatchSource;
  readonly renderer: () => Promise<FactoryWatchRenderer>;
  readonly available: boolean;
}
export interface WorkRunsContext {
  readonly transport: FactoryTransport;
  readonly repository: string;
  readonly watch?: WorkWatch;
}
interface WorkRun {
  readonly id: string;
  readonly role: string;
  readonly issue?: string;
  readonly attempt?: string;
  readonly started: number;
  readonly state: 'running' | 'succeeded' | 'failed' | 'cancelled' | 'needs-human' | 'ended';
}

const states: Record<WorkRun['state'], string> = {
  running: 'Running',
  succeeded: 'Finished',
  failed: 'Failed',
  cancelled: 'Cancelled',
  'needs-human': 'Needs you',
  ended: 'Ended',
};
/** While a run is live the list is re-read from Factory's own records; no
 * provider request is made. */
const POLL_MS = 10_000;

function admitRun(value: unknown): WorkRun | undefined {
  try {
    const row = object(value);
    check(factoryRunID(row.id) && typeof row.role === 'string' && /^[a-z][a-z0-9-]{0,63}$/.test(row.role));
    check(row.issue === undefined || id(row.issue));
    check(row.attempt === undefined || (typeof row.attempt === 'string' && /^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$/.test(row.attempt)));
    const started = Date.parse(String(row.started));
    check(Number.isFinite(started));
    const outcome = typeof row.outcome === 'string' ? row.outcome : '';
    const state: WorkRun['state'] =
      !outcome && row.reconciled !== true ? 'running'
      : outcome === 'succeeded' || outcome === 'failed' || outcome === 'cancelled' || outcome === 'needs-human' ? outcome
      : 'ended';
    return {id: row.id as string, role: row.role as string, started, state,
      ...(row.issue ? {issue: row.issue as string} : {}), ...(row.attempt ? {attempt: row.attempt as string} : {})};
  } catch {
    return undefined;
  }
}

/** Settings names these execution roles Coding and Review; the list matches. */
const runRoles: Record<string, string> = {'soda-coder': 'Coding', 'soda-reviewer': 'Review'};
const time = (ms: number) => new Date(ms).toLocaleString([], {month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit'});

/** Every agent run on this project, newest first, beside the selected run's
 * full CLI output. Watching only reads; it never starts, stops or types into
 * a run. */
export class SodaFactoryWorkRuns extends LitElement {
  static properties = {runs: {state: true}, selected: {state: true}, message: {state: true}};
  declare private runs: readonly WorkRun[] | undefined;
  declare private selected: string;
  declare private message: string;
  private current: WorkRunsContext | undefined;
  private lifetime = new AbortController();
  private timer: number | undefined;
  private watch: SodaFactoryWatch | undefined;
  constructor() {
    super();
    this.runs = undefined;
    this.selected = '';
    this.message = '';
  }
  protected createRenderRoot() {
    return this;
  }
  set context(context: WorkRunsContext | undefined) {
    if (context === this.current) return;
    this.lifetime.abort();
    this.lifetime = new AbortController();
    window.clearTimeout(this.timer);
    this.current = context;
    this.runs = undefined;
    this.selected = this.message = '';
    this.detach();
    if (context) {
      check(id(context.repository) && typeof context.transport.generation === 'string' && !!context.transport.generation);
      void this.read();
    }
  }
  disconnectedCallback() {
    this.context = undefined;
    super.disconnectedCallback();
  }
  /** The Work page calls this to keep the list current. */
  refresh() {
    return this.read();
  }
  private async read() {
    const context = this.current,
      signal = this.lifetime.signal;
    if (!context) return;
    window.clearTimeout(this.timer);
    try {
      const body = object(await getJSON(context.transport, `repositories/${context.repository}/factory/runs`, signal));
      check(Array.isArray(body.runs) && body.runs.length <= 200);
      if (signal.aborted || this.current !== context) return;
      this.runs = body.runs.flatMap((row) => admitRun(row) ?? []);
      this.message = '';
      if (!this.runs.some((run) => run.id === this.selected)) {
        const pick = this.runs.find((run) => run.state === 'running') ?? this.runs[0];
        if (pick) this.choose(pick);
      }
    } catch {
      if (signal.aborted || this.current !== context) return;
      this.message = this.runs ? 'Could not update the run list. Showing the last one read.' : 'Runs are unavailable right now.';
      this.runs ??= [];
    }
    if (this.runs?.some((run) => run.state === 'running'))
      this.timer = window.setTimeout(() => void this.read(), POLL_MS);
  }
  private detach() {
    this.watch?.dispose();
    this.watch?.remove();
    this.watch = undefined;
  }
  private choose(run: WorkRun) {
    const context = this.current;
    if (!context || run.id === this.selected) return;
    this.selected = run.id;
    this.detach();
    const watch = context.watch;
    if (!watch) return;
    const view = new SodaFactoryWatch();
    view.configure(
      {transport: context.transport, source: watch.source, runId: run.id, role: run.role, watchAvailable: watch.available,
        ...(run.issue ? {issue: run.issue} : {}), ...(run.attempt ? {attempt: run.attempt} : {})},
      watch.renderer
    );
    this.watch = view;
    void this.updateComplete.then(() => {
      // A fresh view reads from the first byte.
      if (this.watch === view && watch.available) void view.watch();
    });
  }
  private row(run: WorkRun) {
    const label = runRoles[run.role] ?? roleLabel(run.role) ?? run.role;
    return html`<button type="button" class="soda-work-run" aria-current=${run.id === this.selected ? 'true' : 'false'}
      data-state=${run.state} @click=${() => this.choose(run)}>
      <span class="soda-work-run-name">${label}${run.issue ? html` <span>on #${run.issue}</span>` : ''}</span>
      <span class="soda-work-run-meta"><span data-state=${run.state}>${states[run.state]}</span> · ${time(run.started)}</span>
    </button>`;
  }
  protected render() {
    const context = this.current;
    if (!context) return html``;
    const runs = this.runs;
    return html`<section class="soda-work-runs" aria-label="Agent runs">
      <div class="soda-work-runs-list">
        <div class="soda-work-runs-head">
          <h2>Runs</h2>
        </div>
        ${this.message ? html`<p role="alert">${this.message}</p>` : ''}
        ${runs === undefined
          ? html`<p role="status">Reading runs…</p>`
          : runs.length
            ? html`<nav aria-label="Runs">${runs.map((run) => this.row(run))}</nav>`
            : html`<p class="soda-work-runs-empty">No agent has run on this project yet.</p>`}
      </div>
      <div class="soda-work-runs-output">
        ${this.watch
          ? this.watch
          : html`<p class="soda-work-runs-empty">${runs?.length
              ? context.watch ? 'Choose a run to see its output.' : 'Live output is not available in this app.'
              : 'When an agent starts work, everything its CLI writes appears here.'}</p>`}
      </div>
    </section>`;
  }
}
customElements.define('soda-factory-work-runs', SodaFactoryWorkRuns);
