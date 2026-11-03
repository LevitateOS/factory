import {LitElement} from 'lit';
import {renderFactoryWatch} from './watch-view.js';
import {watchCommands, watchPresentation, watchTitle} from './watch-display.js';
import type {DisplayInput} from './watch-display.js';
import {watch as runWatch} from './watch-request.js';
import type {RequestInput} from './watch-request.js';
import {awaitScreen, clearScreen, openScreen, resize, screenReady} from './watch-screen.js';
import type {FactoryScreenInput} from './watch-screen.js';
import {check, id, object, readSodaJSON} from '../api/models.js';
import {factoryRunID} from '../api/run-response.js';
import type {
  FactoryWatchContext,
  FactoryWatchResume,
  FactoryWatchPeer,
  FactoryWatchTerminal as TerminalView,
  FactoryWatchRenderer as Renderer,
} from './ports.js';
import type {FitAddon} from '@xterm/addon-fit';
import type {ITerminalAddon} from '@xterm/xterm';
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
    screenVisible: {
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
  declare private screenVisible: boolean;
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
  private terminal: TerminalView | undefined;
  private fit: (Pick<FitAddon, 'fit'> & ITerminalAddon) | undefined;
  private observer: ResizeObserver | undefined;
  private timer: number | undefined;
  private request: AbortController | undefined;
  private viewVisible = true;
  constructor() {
    super();
    this.notice = true;
    this.state = 'idle';
    this.screenVisible = false;
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
    document.fonts.addEventListener('loadingdone', () => resize(this.screenInput()), {
      signal: this.lifetime.signal,
    });
    window.visualViewport?.addEventListener('resize', () => resize(this.screenInput()), {
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
  protected render() {
    return renderFactoryWatch(watchPresentation(this.displayInput()), watchCommands(this.displayInput()));
  }
  private displayInput(): DisplayInput {
    return {
      readDisposed: () => this.disposed,
      readState: () => this.state,
      readMessage: () => this.message,
      readTechnical: () => this.technical,
      readNotice: () => this.notice,
      readScreenVisible: () => this.screenVisible,
      readStatusLine: () => this.statusLine,
      readIssue: () => this.binding?.issue,
      readAttempt: () => this.binding?.attempt,
      readRunId: () => this.binding?.runId,
      readRole: () => this.binding?.role,
      setRetries: (retries) => {
        this.retries = retries;
      },
      setCursor: (cursor) => {
        this.cursor = cursor;
      },
      isHiddenOrInert: () => !!this.closest('[hidden], [inert]'),
      queryDetails: () => this.querySelector('details'),
      focusSummary: () => {
        this.querySelector<HTMLElement>('summary')?.focus();
      },
      dispatchHide: () => {
        this.dispatchEvent(
          new CustomEvent('soda-factory-command', {
            bubbles: true,
            detail: 'hide',
          })
        );
      },
      watchNow: () => {
        void runWatch(this.requestInput());
      },
      detach: (message) => this.detach(message),
    };
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
    this.observer?.disconnect();
    this.observer = undefined;
    this.closeSocket();
    this.message = message;
    this.technical = '';
  }
  invalidate() {
    this.detach('Page context changed. No action was replayed; watch through a fresh authorized page.', true);
    clearScreen(this.screenInput());
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
    return runWatch(this.requestInput(), resume);
  }
  private screenInput(): FactoryScreenInput {
    return {
      readTerminal: () => this.terminal,
      setTerminal: (terminal) => {
        this.terminal = terminal;
      },
      readFit: () => this.fit,
      setFit: (fit) => {
        this.fit = fit;
      },
      readState: () => this.state,
      isViewVisible: () => this.viewVisible,
      isHidden: () => !!this.closest('[hidden]'),
      isConnected: () => this.isConnected,
      queryScreen: () => this.querySelector<HTMLElement>('.soda-terminal-screen'),
      setScreenVisible: (visible) => {
        this.screenVisible = visible;
      },
      hostStyles: () => getComputedStyle(this),
      updateComplete: () => this.updateComplete,
      isLive: (generation) => this.live(generation),
      readGeneration: () => this.generation,
      setObserver: (observer) => {
        this.observer = observer;
      },
      detach: (message, stale) => this.detach(message, stale),
    };
  }
  private requestInput(): RequestInput {
    return {
      readBinding: () => this.binding,
      readDisposed: () => this.disposed,
      readState: () => this.state,
      setState: (state) => {
        this.state = state;
      },
      beginGeneration: () => ++this.generation,
      isLive: (generation) => this.live(generation),
      detach: (message, stale) => this.detach(message, stale),
      setMessage: (message) => {
        this.message = message;
      },
      setTechnical: (technical) => {
        this.technical = technical;
      },
      setNotice: (notice) => {
        this.notice = notice;
      },
      setStatusLine: (line) => {
        this.statusLine = line;
      },
      readRetries: () => this.retries,
      setRetries: (retries) => {
        this.retries = retries;
      },
      readCursor: () => this.cursor,
      setCursor: (cursor) => {
        this.cursor = cursor;
      },
      clearTimer: () => {
        window.clearTimeout(this.timer);
      },
      setTimer: (timer) => {
        this.timer = timer;
      },
      takeRequest: () => (this.request = new AbortController()),
      json: (path, signal) => this.json(path, signal),
      readTitle: () => watchTitle(this.displayInput()),
      loadRenderer: () => {
        if (!this.loadRenderer) throw Error('Missing Factory renderer');
        return this.loadRenderer();
      },
      awaitScreen: (n) => awaitScreen(this.screenInput(), n),
      openScreen: (screen, Terminal, FitAddon) => openScreen(this.screenInput(), screen, Terminal, FitAddon),
      readTerminal: () => this.terminal,
      setSocket: (socket) => {
        this.socket = socket;
      },
      screenReady: () => {
        void screenReady(this.screenInput());
      },
      clearScreen: () => clearScreen(this.screenInput()),
    };
  }
  setVisible(visible: boolean) {
    this.viewVisible = visible;
    if (visible) resize(this.screenInput()); // Presentation only: never connect.
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
    clearScreen(this.screenInput());
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
