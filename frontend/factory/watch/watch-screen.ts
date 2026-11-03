import type {FactoryWatchRenderer as Renderer, FactoryWatchTerminal as TerminalView} from './ports.js';
import type {FitAddon} from '@xterm/addon-fit';
import type {ITerminalAddon} from '@xterm/xterm';
import type {FactoryWatchState} from './watch-display.js';

export type FactoryFit = Pick<FitAddon, 'fit'> & ITerminalAddon;

export interface FactoryScreenInput {
  readTerminal: () => TerminalView | undefined;
  setTerminal: (terminal: TerminalView | undefined) => void;
  readFit: () => FactoryFit | undefined;
  setFit: (fit: FactoryFit | undefined) => void;
  readState: () => FactoryWatchState;
  isViewVisible: () => boolean;
  isHidden: () => boolean;
  isConnected: () => boolean;
  queryScreen: () => HTMLElement | null;
  setScreenVisible: (visible: boolean) => void;
  hostStyles: () => CSSStyleDeclaration;
  updateComplete: () => Promise<boolean>;
  isLive: (generation: number) => boolean;
  readGeneration: () => number;
  setObserver: (observer: ResizeObserver) => void;
  detach: (message: string, stale?: boolean) => void;
}

export function clearScreen(input: FactoryScreenInput) {
  input.readTerminal()?.dispose();
  input.setTerminal(undefined);
  input.setFit(undefined);
  input.queryScreen()?.replaceChildren();
  input.setScreenVisible(false);
}

function fitReady(input: FactoryScreenInput) {
  return !!input.readTerminal() && !!input.readFit() && input.isViewVisible() && !input.isHidden();
}

function screenPresent(screen: HTMLElement | null): screen is HTMLElement {
  return !!screen?.isConnected && !!screen.clientWidth && !!screen.clientHeight;
}

function canFit(input: FactoryScreenInput, screen: HTMLElement | null): screen is HTMLElement {
  return fitReady(input) && screenPresent(screen) && (input.readState() === 'ready' || input.readState() === 'opening');
}

// Presentation only: fitting never sends a resize frame. The read-only
// stream rejects every client frame past the handshake.
export function resize(input: FactoryScreenInput) {
  const screen = input.queryScreen();
  if (!canFit(input, screen)) return;
  input.readFit()!.fit();
}

function requiredToken(styles: CSSStyleDeclaration, name: string) {
  const value = styles.getPropertyValue(name).trim();
  if (!value) throw Error(`Missing terminal token ${name}`);
  return value;
}

function terminalTheme(styles: CSSStyleDeclaration) {
  return {
    background: requiredToken(styles, '--soda-terminal-screen'),
    foreground: requiredToken(styles, '--soda-terminal-text'),
    cursor: requiredToken(styles, '--soda-terminal-text'),
  };
}

export async function awaitScreen(input: FactoryScreenInput, n: number) {
  input.setScreenVisible(true);
  await input.updateComplete();
  if (!input.isLive(n) || !input.isViewVisible() || !input.isConnected() || input.isHidden()) return;
  const screen = input.queryScreen();
  if (!screen) throw Error('Missing factory screen');
  await document.fonts.ready;
  if (!input.isLive(n) || !input.isConnected() || input.isHidden()) return;
  return screen;
}

export function openScreen(
  input: FactoryScreenInput,
  screen: HTMLElement,
  Terminal: Renderer['Terminal'],
  FitAddon: Renderer['FitAddon']
) {
  const styles = input.hostStyles();
  const fontSize = Number.parseFloat(requiredToken(styles, '--soda-font-mono-size')),
    lineHeight = Number(requiredToken(styles, '--soda-font-mono-line'));
  if (!Number.isFinite(fontSize) || fontSize <= 0 || !Number.isFinite(lineHeight) || lineHeight < 1)
    throw Error('Invalid factory typography');
  const terminal = new Terminal({
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
    theme: terminalTheme(styles),
  });
  input.setTerminal(terminal);
  const fit = new FitAddon();
  input.setFit(fit);
  terminal.loadAddon(fit);
  for (const code of [0, 1, 2, 8, 52]) terminal.parser.registerOscHandler(code, () => true);
  terminal.open(screen);
  if (screen.clientWidth && screen.clientHeight) fit.fit();
  return terminal;
}

export async function screenReady(input: FactoryScreenInput) {
  const n = input.readGeneration();
  try {
    await input.updateComplete();
    if (!input.isLive(n) || !input.readTerminal()) return;
    const screen = input.queryScreen();
    if (!screen?.isConnected) return;
    const observer = new ResizeObserver(() => resize(input));
    input.setObserver(observer);
    observer.observe(screen);
    resize(input);
  } catch {
    if (input.isLive(n)) input.detach('Factory rendering failed. No input was sent.');
  }
}
