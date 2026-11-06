import {LitElement} from 'lit';
import {renderFactoryWatch} from './watch-view.js';
import {WatchScreen} from './watch-screen.js';
import {factoryOutputCursor, factoryOutputFrame, factoryClosedReason} from '../api/output-response.js';
import {factoryRunStatusResponse, factoryStatusFrame} from '../api/run-response.js';
import {statusText} from '../shared/format.js';
import {check, id, object, readSodaJSON} from '../api/models.js';
import {factoryRunID} from '../api/run-response.js';
import type {
  FactoryWatchContext,
  FactoryWatchResume,
  FactoryWatchPeer,
  FactoryWatchTerminal as TerminalView,
  FactoryWatchRenderer as Renderer,
} from './ports.js';
// One immutable run binding and imperative attachment owner. Rendering never
// launches, resumes or inputs to the run; the socket only reads. Detaching or
// hiding the view never ends execution or returns its lease.
export class SodaFactoryWatch extends LitElement {
  static properties = {
    state: {
      state: true,
    },
    message: {
      state: true,
    },
    technical: {
      state: true,
    },
    statusLine: {
      state: true,
    },
    notice: {
      state: true,
    },
  };
  declare private state: 'idle' | 'opening' | 'ready' | 'closed' | 'stale';
  declare private message: string;
  declare private technical: string;
  declare private statusLine: string;
  declare private notice: boolean;
  private binding: FactoryWatchContext | undefined;
  private loadRenderer: (() => Promise<Renderer>) | undefined;
  private runtimeGeneration = '';
  private lifetime = new AbortController();
  private disposed = false;
  private generation = 0;
  private retries = 0;
  private cursor = 0;
  private socket: FactoryWatchPeer | undefined;
  private screen = new WatchScreen(this, () => this.requestUpdate());
  private timer: number | undefined;
  private request: AbortController | undefined;
  constructor() {
    super();
    this.notice = true;
    this.state = 'idle';
    this.statusLine = '';
    this.message = 'Not watching.';
    this.technical = '';
  }
  protected createRenderRoot() {
    return this;
  }
  configure(context: FactoryWatchContext, load: () => Promise<Renderer>) {
    if (this.binding || this.disposed) throw Error('Factory binding is immutable');
    check(
      typeof context.transport.generation === 'string' &&
        !!context.transport.generation &&
        factoryRunID(context.runId) &&
        /^[a-z][a-z0-9-]{0,63}$/.test(context.role) &&
        (context.issue === undefined || id(context.issue)) &&
        (context.attempt === undefined || /^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$/.test(context.attempt))
    );
    this.runtimeGeneration = context.transport.generation;
    this.binding = {
      ...context,
    };
    this.loadRenderer = load;
    document.fonts.addEventListener('loadingdone', () => this.screen.resize(this.watching), {
      signal: this.lifetime.signal,
    });
    window.visualViewport?.addEventListener('resize', () => this.screen.resize(this.watching), {
      signal: this.lifetime.signal,
    });
    // Host lifecycle only (NFX-D05): no pagehide/pageshow self-interpretation.
    // The host drives suspend/resume/retire; ordinary navigation retains
    // this renderer and its read-only stream.
  }
  disconnectedCallback() {
    super.disconnectedCallback();
    this.dispose();
  }
  private get watching() {
    return this.state === 'ready' || this.state === 'opening';
  }
  private get watchTitle() {
    const target = this.binding?.issue
      ? `issue #${this.binding.issue}`
      : this.binding?.attempt || `run ${this.binding?.runId.slice(0, 8) || ''}`;
    return `${this.binding?.role || 'factory'} · ${target}`;
  }
  protected render() {
    const disabled = this.disposed || this.state === 'stale';
    return renderFactoryWatch(
      {
        watching: this.watching,
        disabled,
        canWatch: !disabled && !this.watching,
        watchLabel: this.state === 'idle' ? 'Watch run' : 'Watch again',
        title: this.watchTitle,
        status: this.statusLine || 'Not watching.',
        message: this.message,
        technical: this.technical,
        notice: this.notice,
        screenVisible: this.screen.visible,
      },
      {
        watch: () => {
          this.closeMenu();
          this.retries = 0;
          this.cursor = 0;
          void this.watch();
        },
        stop: () => {
          this.closeMenu();
          this.stop();
        },
        hide: () => {
          if (this.disposed || this.state === 'stale' || this.closest('[hidden], [inert]')) return;
          this.closeMenu();
          this.dispatchEvent(new CustomEvent('soda-factory-command', {bubbles: true, detail: 'hide'}));
        },
        menuKey: (event) => {
          if (event.key !== 'Escape') return;
          event.preventDefault();
          event.stopPropagation();
          this.closeMenu();
          this.querySelector<HTMLElement>('summary')?.focus();
        },
      }
    );
  }
  private closeMenu() {
    const menu = this.querySelector('details');
    if (menu) menu.open = false;
  }
  private live(generation: number) {
    if (
      this.binding &&
      this.binding.transport.generation !== this.runtimeGeneration &&
      !this.disposed &&
      this.state !== 'stale'
    )
      this.invalidate();
    return !this.disposed && this.state !== 'stale' && this.generation === generation;
  }
  private closeSocket() {
    const old = this.socket;
    this.socket = undefined;
    if (old && old.readyState < 2) old.close();
  }
  private detach(message: string, stale = false) {
    ++this.generation;
    this.state = stale ? 'stale' : 'closed';
    this.notice = true;
    window.clearTimeout(this.timer);
    this.request?.abort();
    this.request = undefined;
    this.screen.stopObserving();
    this.closeSocket();
    this.message = message;
    this.technical = '';
  }
  invalidate() {
    this.detach('Page context changed. No action was replayed; watch through a fresh authorized page.', true);
    this.screen.clear();
  }
  private async json(path: string, signal: AbortSignal) {
    if (!this.binding) throw Error('Missing factory binding');
    const n = this.generation;
    const response = await this.binding.transport.request(path, {method: 'GET', signal});
    if (!this.live(n)) throw Error('Factory watch binding retired.');
    if (!response.ok) {
      if (response.status === 401 || response.status === 403) {
        this.invalidate();
        this.authorityLost();
      }
      throw Error('Soda request refused');
    }
    return object(await readSodaJSON(response));
  }
  private authorityLost() {
    if (!this.disposed)
      this.dispatchEvent(
        new CustomEvent('soda-factory-authority-lost', {
          bubbles: true,
        })
      );
  }
  async watch(resume = false) {
    if (this.disposed || !this.binding || this.state === 'stale' || this.watching) return;
    if (!resume) {
      this.screen.clear();
      this.cursor = 0;
    }
    window.clearTimeout(this.timer);
    this.state = 'opening';
    const n = ++this.generation,
      request = (this.request = new AbortController());
    this.timer = window.setTimeout(
      () => this.detach('Factory view timed out. Inspect the exact run; nothing was replayed.'),
      45000
    );
    this.notice = true;
    this.message = 'Reading the recorded run…';
    try {
      if (await this.inspectRun(n, request)) return;
      if (!this.loadRenderer) throw Error('Missing Factory renderer');
      const renderer = await this.loadRenderer();
      if (!this.live(n)) return;
      const terminal = await this.screen.open(renderer, () => this.live(n));
      if (terminal && this.live(n)) this.bindPeer(n, terminal, this.cursor);
    } catch {
      if (this.live(n))
        this.detach('Could not authorize or attach. Sign in again or inspect the exact run; nothing was replayed.');
    }
  }
  private async inspectRun(n: number, request: AbortController): Promise<boolean> {
    const binding = this.binding!;
    const repository = await binding.source.repository(request.signal);
    if (!this.live(n)) return true;
    check(id(repository));
    const detail = factoryRunStatusResponse(
      await this.json(`factory/runs/${binding.runId}`, request.signal),
      binding.runId,
      repository
    );
    if (!this.live(n)) return true;
    if (detail.state?.terminal) {
      const state = detail.state,
        statusLine = statusText(state.phase, false, true, state.exit_code ?? null),
        exit = state.exit_code !== undefined ? ` (exit ${state.exit_code})` : '';
      this.statusLine = statusLine;
      this.detach(`This run has finished${exit}. Live view is not available for finished runs.`);
      const output = (state.output ?? '').replace(/^\s*```[\w-]*\s*$/gm, '').trim(),
        cut = state.output_truncated ? '\n… output truncated …' : '';
      this.technical = `Status: ${statusLine}${output ? `\nLast output:\n${output}` : ''}${cut}`;
      return true;
    }
    this.statusLine = detail.state
      ? statusText(detail.state.phase, detail.state.live, false, null)
      : detail.outcome
        ? `recorded · ${detail.outcome}`
        : 'recorded';
    return false;
  }
  private bindPeer(n: number, terminal: TerminalView, cursor: number) {
    const binding = this.binding!,
      peer = binding.source.connect(),
      queued = {bytes: 0};
    this.socket = peer;
    peer.onopen = () => {
      if (!this.live(n)) {
        peer.close();
        return;
      }
      try {
        peer.send(JSON.stringify(binding.source.attachment(cursor, binding.transport.generation)));
        this.message = 'Attaching the recorded run…';
      } catch {
        this.detach('Attachment dispatch was not confirmed. No input was sent.');
      }
    };
    peer.onmessage = (event) => {
      if (!this.live(n) || !this.screen.owns(terminal)) return;
      try {
        if (typeof event.data !== 'string' || event.data.length > 65536) throw Error('frame');
        const frame = object(JSON.parse(event.data));
        if (frame.type === 'status') this.acceptStatus(factoryStatusFrame(frame, binding.runId));
        else if (frame.type === 'output') this.acceptOutput(factoryOutputFrame(frame), terminal, queued);
        else if (frame.type === 'closed') {
          const reason = factoryClosedReason(frame);
          this.detach(reason === 'eof' ? 'Run ended. This view keeps its rendered output.' : `View ended: ${reason}.`);
        }
      } catch {
        this.detach('Invalid or overloaded stream. No input was sent.');
      }
    };
    peer.onerror = peer.onclose = () => {
      if (!this.live(n)) return;
      this.detach('Connection lost. The run continues without this view.');
      const retries = this.retries;
      if (retries < 3) {
        this.retries = retries + 1;
        this.timer = window.setTimeout(() => void this.watch(true), 1000 * 2 ** retries);
      }
    };
  }
  private acceptStatus(frame: ReturnType<typeof factoryStatusFrame>) {
    this.statusLine = statusText(frame.phase, frame.live, frame.terminal, frame.exit_code);
    if (this.state !== 'opening') return;
    window.clearTimeout(this.timer);
    this.state = 'ready';
    this.retries = 0;
    this.notice = false;
    this.message = `Watching ${this.watchTitle}.`;
    const n = this.generation;
    void this.screen.observe(
      () => this.live(n),
      () => this.detach('Factory rendering failed. No input was sent.')
    );
  }
  private acceptOutput(frame: ReturnType<typeof factoryOutputFrame>, terminal: TerminalView, queued: {bytes: number}) {
    if (this.state !== 'ready') throw Error('frame');
    const next = factoryOutputCursor(this.cursor, frame);
    if (frame.truncated) terminal.write('\r\n… earlier output truncated …\r\n');
    if (frame.gap) terminal.write(`\r\n… output gap: skipped to byte ${frame.next} …\r\n`);
    if (queued.bytes + frame.bytes.length > 262144) throw Error('output');
    queued.bytes += frame.bytes.length;
    this.cursor = next;
    if (frame.bytes.length)
      terminal.write(frame.bytes, () => {
        queued.bytes -= frame.bytes.length;
      });
  }
  setVisible(visible: boolean) {
    this.screen.setVisible(visible, this.watching);
  }
  /** Host concealment: pause visible work, retain renderer and stream. */
  suspend() {
    this.setVisible(false);
  }
  /** Host rebind after shelter/revalidation; never reconnects the stream. */
  resume(validation?: FactoryWatchResume) {
    if (this.disposed) return;
    if (
      this.binding &&
      (this.binding.transport.generation !== this.runtimeGeneration ||
        (validation !== undefined && validation.generation !== this.runtimeGeneration))
    ) {
      this.retire();
      return;
    }
    this.setVisible(true);
  }
  /** Host retirement: release the view without ending the watched run. */
  retire() {
    this.dispose();
  }
  stop() {
    this.detach('Stopped watching. The run continues without this view.');
  }
  get started() {
    return this.state !== 'idle';
  }
  dispose() {
    if (this.disposed) return;
    this.detach('Detached.', true);
    this.screen.clear();
    this.disposed = true;
    this.lifetime.abort();
    this.remove();
  }
}
customElements.define('soda-factory-watch', SodaFactoryWatch);
export function mountFactoryWatch(
  root: HTMLElement,
  context: FactoryWatchContext,
  loadRenderer: () => Promise<Renderer>
) {
  check(root.ownerDocument === document);
  const view = new SodaFactoryWatch();
  view.configure(context, loadRenderer);
  root.append(view);
  return {
    setVisible: (visible: boolean) => view.setVisible(visible),
    get connected() {
      return view.isConnected;
    },
    watch: () => view.watch(),
    get started() {
      return view.started;
    },
    get ready() {
      return view.updateComplete;
    },
    stop: () => view.stop(),
    suspend: () => view.suspend(),
    resume: (validation?: FactoryWatchResume) => view.resume(validation),
    retire: () => view.retire(),
    invalidate: () => view.invalidate(),
    dispose: () => view.dispose(),
  };
}
