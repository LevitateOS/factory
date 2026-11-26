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
  /** While closed, the recorded cause, such as policy_disabled or control_paused. */
  readonly pausedBy?: string;
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
  /** Why the provider refused the last attempt to open it here; absent once it opens. */
  readonly refused?: string;
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
  /** Takes a project out of this list. Nothing at the provider changes, and
   * opening the repository again lists it again. Never the current row. */
  remove?(key: string): Promise<void>;
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
  /** Agents may work on it on this computer, as Settings' Execution controller allows. */
  readonly useThisComputer: boolean;
}

export function admitProjectsPort(port: FactoryProjectsPort | undefined) {
  if (port === undefined) return;
  check(typeof port.list === 'function' && typeof port.open === 'function');
  for (const action of [port.reconnect, port.disconnect, port.signIn, port.create, port.remove]) check(action === undefined || typeof action === 'function');
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
    if (found.pausedBy !== undefined && (!plain(found.pausedBy, 256) || !found.pausedBy)) return;
    summary = {needsYou: found.needsYou, working, dispatch: found.dispatch as 'open' | 'closed' | 'none',
      ...(found.pausedBy ? {pausedBy: found.pausedBy as string} : {}),...(latestPost ? {latestPost} : {})};
  }
  if (row.refused !== undefined && (!plain(row.refused, 512) || !row.refused)) return;
  return {key: row.key, name: row.name, accountLabel: row.accountLabel, provider: row.provider,
    connection: row.connection as FactoryProjectConnection, ...(summary ? {summary} : {}), asOf: row.asOf as number,
    ...(row.refused ? {refused: row.refused as string} : {})};
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

const pauses: Record<string, readonly [string, string]> = {
  control_paused: ['Paused', 'Someone paused this project. Resume it on its Work page.'],
  project_stop: ['Stopped', 'This project was stopped. Start it again on its Work page.'],
  policy_disabled: ['Automation off', 'Automation is turned off in this project’s settings.'],
  policy_paused: ['Automation paused', 'Automation is paused in this project’s settings.'],
  operator_grant_withdrawn: ['Access withdrawn', 'Factory’s permission to work on this repository was withdrawn.'],
  sponsorship_withdrawn: ['No funding', 'The funding for runs on this project was withdrawn.'],
  environment_grant_withdrawn: ['No environment', 'The environment agents run in was withdrawn.'],
};

/** Why Factory is not starting work on a project, as a short label and a
 * sentence; undefined while its work is open or was never set up. */
export function projectPause(summary: FactoryProjectSummary | undefined): {label: string; detail: string} | undefined {
  if (summary?.dispatch !== 'closed') return;
  const [label, detail] = pauses[summary.pausedBy ?? ''] ?? ['Paused', 'Factory is not starting new work on this project.'];
  return {label, detail};
}

/** The one thing a project row most needs said, for compact lists: signing in,
 * a refused open, posts waiting on the person, or paused work. */
export function projectMark(row: FactoryProject): {label: string; kind: 'sign-in' | 'refused' | 'needs' | 'paused'; detail: string} | undefined {
  if (row.connection === 'sign_in') return {label: 'Sign in', kind: 'sign-in', detail: 'Sign in to this account again to open it.'};
  if (row.refused) return {label: 'No access', kind: 'refused', detail: row.refused};
  const needs = row.summary?.needsYou ?? 0;
  if (needs) return {label: String(needs), kind: 'needs', detail: needs + (needs === 1 ? ' post needs you' : ' posts need you')};
  const pause = projectPause(row.summary);
  return pause ? {...pause, kind: 'paused'} : undefined;
}
