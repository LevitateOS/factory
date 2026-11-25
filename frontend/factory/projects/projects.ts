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
  /** New project: creates a repository with the window's account, then
   * connects this window to it and opens its Feed. Rejects with a short
   * plain-language message when it cannot. */
  create?(project: FactoryNewProject): Promise<void>;
}

/** What the person asked for; the host derives the repository from it. */
export interface FactoryNewProject {
  readonly name: string;
  readonly description: string;
  readonly visibility: 'private' | 'public';
}

export function admitProjectsPort(port: FactoryProjectsPort | undefined) {
  if (port === undefined) return;
  check(typeof port.list === 'function' && typeof port.open === 'function');
  for (const action of [port.reconnect, port.disconnect, port.signIn, port.create]) check(action === undefined || typeof action === 'function');
}

const plain = (value: unknown, limit: number): value is string =>
  typeof value === 'string' && value.length <= limit && !/[\u0000-\u001f\u007f]/.test(value);
const count = (value: unknown, limit: number): value is number =>
  typeof value === 'number' && Number.isSafeInteger(value) && value >= 0 && value <= limit;
const connections: readonly unknown[] = ['current', 'connected', 'not_connected', 'sign_in'];

function admitProject(value: unknown): FactoryProject | undefined {
  if (!value || typeof value !== 'object') return;
  const row = value as Record<string, unknown>;
  if (!plain(row.key, 512) || !row.key || !plain(row.name, 512) || !row.name || !plain(row.accountLabel, 256) || !plain(row.provider, 64)) return;
  if (!connections.includes(row.connection) || !count(row.asOf, 1e12) || row.asOf === 0) return;
  let summary: FactoryProjectSummary | undefined;
  if (row.summary !== undefined) {
    const found = row.summary as Record<string, unknown> | null;
    if (!found || typeof found !== 'object' || !count(found.needsYou, 9999) || !['open', 'closed', 'none'].includes(found.dispatch as string)) return;
    if (!Array.isArray(found.working) || found.working.length > 8) return;
    const working: {role: string; issue?: string}[] = [];
    for (const item of found.working as Record<string, unknown>[]) {
      if (!item || typeof item !== 'object' || !plain(item.role, 64) || !item.role || (item.issue !== undefined && !plain(item.issue, 32))) return;
      working.push({role: item.role, ...(item.issue ? {issue: item.issue} : {})});
    }
    let latestPost: FactoryProjectSummary['latestPost'];
    if (found.latestPost !== undefined) {
      const post = found.latestPost as Record<string, unknown> | null;
      if (!post || typeof post !== 'object' || !plain(post.id, 128) || !plain(post.title, 4096) || !plain(post.actor, 256)) return;
      if ((post.author !== undefined && !plain(post.author, 256)) || (post.at !== undefined && (!count(post.at, 1e12) || post.at === 0))) return;
      latestPost = {id: post.id, title: post.title, actor: post.actor,
        ...(post.author ? {author: post.author as string} : {}), ...(post.at ? {at: post.at as number} : {})};
    }
    summary = {needsYou: found.needsYou, working, dispatch: found.dispatch as 'open' | 'closed' | 'none', ...(latestPost ? {latestPost} : {})};
  }
  return {key: row.key, name: row.name, accountLabel: row.accountLabel, provider: row.provider,
    connection: row.connection as FactoryProjectConnection, ...(summary ? {summary} : {}), asOf: row.asOf as number};
}

/** Host rows are data: keep the well-formed ones (bounded, known states, unique
 * keys, at most 50) and drop the rest instead of failing the list. */
export function admitProjectRows(value: unknown): FactoryProject[] {
  if (!Array.isArray(value)) return [];
  const seen = new Set<string>(),
    rows: FactoryProject[] = [];
  for (const item of value.slice(0, 200)) {
    const row = admitProject(item);
    if (!row || seen.has(row.key)) continue;
    seen.add(row.key);
    rows.push(row);
    if (rows.length === 50) break;
  }
  return rows;
}
