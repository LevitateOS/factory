import {check} from '../api/models.js';

/** How a project row relates to this window: shown here now, reachable with
 * the account this window is connected with, reachable with another saved
 * account, or waiting for that account to sign in again. */
export type FactoryProjectConnection = 'current' | 'connected' | 'not_connected' | 'sign_in';

/** Retained Factory facts for one project as of the row's `asOf`; never access evidence. */
export interface FactoryProjectSummary {
  readonly needsYou: number;
  /** Assigned roles, with the issue each works on when it has one. */
  readonly working: readonly {readonly role: string; readonly issue?: string}[];
  readonly dispatch: 'open' | 'closed' | 'none';
  readonly latestPost?: {readonly id: string; readonly title: string; readonly author?: string; readonly actor: string; readonly at?: number};
}

export interface FactoryProject {
  /** Host-opaque and stable for the same project; the UI only passes it back. */
  readonly key: string;
  readonly name: string;
  readonly accountLabel: string;
  readonly provider: string;
  readonly connection: FactoryProjectConnection;
  readonly summary?: FactoryProjectSummary;
  /** Unix seconds when the host last read this row. */
  readonly asOf: number;
}

/** The host's retained project list. The host decides when the list is read;
 * `refresh` asks for an explicit new read. Opening is always a user action.
 * `reconnect` and `disconnect` apply to the current row, `signIn` to a
 * `sign_in` row; a host without them omits them. */
export interface FactoryProjectsPort {
  list(signal: AbortSignal, options?: {readonly refresh?: boolean}): Promise<readonly FactoryProject[]>;
  open(key: string, where: 'here' | 'window'): Promise<void>;
  reconnect?(key: string): Promise<void>;
  disconnect?(key: string): Promise<void>;
  signIn?(key: string): Promise<void>;
}

export function admitProjectsPort(port: FactoryProjectsPort | undefined) {
  if (port === undefined) return;
  check(typeof port.list === 'function' && typeof port.open === 'function');
  for (const action of [port.reconnect, port.disconnect, port.signIn]) check(action === undefined || typeof action === 'function');
}
