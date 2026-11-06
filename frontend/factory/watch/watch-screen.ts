import type {LitElement} from 'lit';
import type {FactoryWatchRenderer, FactoryWatchTerminal} from './ports.js';
import type {FitAddon} from '@xterm/addon-fit';
import type {ITerminalAddon} from '@xterm/xterm';

/** Owns the renderer, fitting and DOM observation. It never sends a frame. */
export class WatchScreen {
  private terminal: FactoryWatchTerminal | undefined;
  private fit: (Pick<FitAddon, 'fit'> & ITerminalAddon) | undefined;
  private observer: ResizeObserver | undefined;
  private viewVisible = true;
  visible = false;
  constructor(
    private host: LitElement,
    private changed: () => void
  ) {}
  private node() {
    return this.host.querySelector<HTMLElement>('.soda-terminal-screen');
  }
  owns(terminal: FactoryWatchTerminal): boolean {
    return this.terminal === terminal;
  }
  stopObserving() {
    this.observer?.disconnect();
    this.observer = undefined;
  }
  clear() {
    this.terminal?.dispose();
    this.terminal = undefined;
    this.fit = undefined;
    this.node()?.replaceChildren();
    this.visible = false;
    this.changed();
  }
  setVisible(visible: boolean, active: boolean) {
    this.viewVisible = visible;
    if (visible) this.resize(active);
  }
  resize(active: boolean) {
    const screen = this.node();
    if (
      active &&
      this.terminal &&
      this.fit &&
      this.viewVisible &&
      !this.host.closest('[hidden]') &&
      screen?.isConnected &&
      screen.clientWidth &&
      screen.clientHeight
    )
      this.fit.fit();
  }
  async open(renderer: FactoryWatchRenderer, live: () => boolean): Promise<FactoryWatchTerminal | undefined> {
    this.visible = true;
    this.changed();
    await this.host.updateComplete;
    if (!live() || !this.viewVisible || !this.host.isConnected || this.host.closest('[hidden]')) return;
    const screen = this.node();
    if (!screen) throw Error('Missing factory screen');
    await document.fonts.ready;
    if (!live() || !this.host.isConnected || this.host.closest('[hidden]')) return;
    if (this.terminal) return this.terminal;
    const styles = getComputedStyle(this.host),
      fontSize = Number.parseFloat(requiredToken(styles, '--soda-font-mono-size')),
      lineHeight = Number(requiredToken(styles, '--soda-font-mono-line'));
    if (!Number.isFinite(fontSize) || fontSize <= 0 || !Number.isFinite(lineHeight) || lineHeight < 1)
      throw Error('Invalid factory typography');
    const terminal = new renderer.Terminal({
      allowProposedApi: true,
      disableStdin: true,
      scrollback: 1000,
      windowOptions: {},
      convertEol: false,
      cols: 80,
      rows: 24,
      fontFamily: requiredToken(styles, '--soda-font-mono-family'),
      fontSize,
      lineHeight,
      theme: {
        background: requiredToken(styles, '--soda-terminal-screen'),
        foreground: requiredToken(styles, '--soda-terminal-text'),
        cursor: requiredToken(styles, '--soda-terminal-text'),
      },
    });
    this.terminal = terminal;
    const fit = new renderer.FitAddon();
    this.fit = fit;
    terminal.loadAddon(fit);
    for (const code of [0, 1, 2, 8, 52]) terminal.parser.registerOscHandler(code, () => true);
    terminal.open(screen);
    if (screen.clientWidth && screen.clientHeight) fit.fit();
    return terminal;
  }
  async observe(live: () => boolean, failed: () => void) {
    try {
      await this.host.updateComplete;
      if (!live() || !this.terminal) return;
      const screen = this.node();
      if (!screen?.isConnected) return;
      this.observer = new ResizeObserver(() => {
        if (live()) this.resize(true);
      });
      this.observer.observe(screen);
      this.resize(true);
    } catch {
      if (live()) failed();
    }
  }
}
function requiredToken(styles: CSSStyleDeclaration, name: string) {
  const value = styles.getPropertyValue(name).trim();
  if (!value) throw Error(`Missing terminal token ${name}`);
  return value;
}
