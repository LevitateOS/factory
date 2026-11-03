import type {Terminal, ITerminalAddon, ITerminalOptions, ITerminalInitOnlyOptions} from '@xterm/xterm';
import type {FitAddon} from '@xterm/addon-fit';
import type {FactoryTransport} from '../api/transport.js';

/** Output rendering deliberately exposes no keyboard/input event API. */
export type FactoryWatchTerminal = Pick<
  Terminal,
  'cols' | 'rows' | 'options' | 'parser' | 'loadAddon' | 'open' | 'dispose' | 'write'
>;
export interface FactoryWatchRenderer {
  Terminal: new (options: ITerminalOptions & ITerminalInitOnlyOptions) => FactoryWatchTerminal;
  FitAddon: new () => Pick<FitAddon, 'fit'> & ITerminalAddon;
}
export type FactoryWatchPeer = Pick<
  WebSocket,
  'readyState' | 'onopen' | 'onmessage' | 'onerror' | 'onclose' | 'send' | 'close'
>;

/** The host owns discovery, credential/custody admission and its attachment protocol. */
export interface FactoryWatchSource {
  repository(signal: AbortSignal): Promise<string>;
  connect(): FactoryWatchPeer;
  attachment(cursor: number, generation: string): Readonly<Record<string, unknown>>;
}
export interface FactoryWatchContext {
  readonly transport: FactoryTransport;
  readonly source: FactoryWatchSource;
  readonly runId: string;
  readonly role: string;
  readonly issue?: string;
  readonly attempt?: string;
}
export interface FactoryWatchResume {
  readonly generation: string;
}
