import type {FactoryWatchCommands, FactoryWatchPresentation} from './watch-view.js';

export type FactoryWatchState = 'idle' | 'opening' | 'ready' | 'closed' | 'stale';

export interface DisplayInput {
  readDisposed: () => boolean;
  readState: () => FactoryWatchState;
  readMessage: () => string;
  readTechnical: () => string;
  readNotice: () => boolean;
  readScreenVisible: () => boolean;
  readStatusLine: () => string;
  readIssue: () => string | undefined;
  readAttempt: () => string | undefined;
  readRunId: () => string | undefined;
  readRole: () => string | undefined;
  setRetries: (retries: number) => void;
  setCursor: (cursor: number) => void;
  isHiddenOrInert: () => boolean;
  queryDetails: () => {open: boolean} | null;
  focusSummary: () => void;
  dispatchHide: () => void;
  watchNow: () => void;
  detach: (message: string) => void;
}

function viewDisabled(input: DisplayInput) {
  return input.readDisposed() || input.readState() === 'stale';
}

function canWatchView(input: DisplayInput, disabled: boolean) {
  return !(disabled || input.readState() === 'opening' || input.readState() === 'ready');
}

export function watchTitle(input: DisplayInput) {
  const target = input.readIssue()
    ? `issue #${input.readIssue()}`
    : input.readAttempt() || `run ${input.readRunId()?.slice(0, 8) || ''}`;
  return `${input.readRole() || 'factory'} · ${target}`;
}

export function watchPresentation(input: DisplayInput): FactoryWatchPresentation {
  const disabled = viewDisabled(input);
  return {
    watching: input.readState() === 'ready' || input.readState() === 'opening',
    disabled,
    canWatch: canWatchView(input, disabled),
    watchLabel: input.readState() === 'idle' ? 'Watch run' : 'Watch again',
    title: watchTitle(input),
    status: input.readStatusLine() || 'Not watching.',
    message: input.readMessage(),
    technical: input.readTechnical(),
    notice: input.readNotice(),
    screenVisible: input.readScreenVisible(),
  };
}

function closeMenu(input: DisplayInput) {
  const menu = input.queryDetails();
  if (menu) menu.open = false;
}

function menuKey(input: DisplayInput, event: KeyboardEvent) {
  if (event.key === 'Escape') {
    event.preventDefault();
    event.stopPropagation();
    closeMenu(input);
    input.focusSummary();
  }
}

function watchFromControls(input: DisplayInput) {
  closeMenu(input);
  input.setRetries(0);
  input.setCursor(0);
  input.watchNow();
}

function stopFromControls(input: DisplayInput) {
  closeMenu(input);
  input.detach('Stopped watching. The run continues without this view.');
}

function hideFromControls(input: DisplayInput) {
  if (input.readDisposed() || input.readState() === 'stale' || input.isHiddenOrInert()) return;
  closeMenu(input);
  input.dispatchHide();
}

export function watchCommands(input: DisplayInput): FactoryWatchCommands {
  return {
    watch: () => watchFromControls(input),
    stop: () => stopFromControls(input),
    hide: () => hideFromControls(input),
    menuKey: (event) => menuKey(input, event),
  };
}
