import type {FactoryProject, FactoryProjectConnection, FactoryProjectSummary, FactoryProjectsPort, FactoryNewProject} from '../factory/app/browser.js';
import {check, id, object, readSodaJSON} from '../factory/api/models.js';
import type {RepositoryRef} from '../factory/api/collaboration.js';
import type {Account} from './accounts.js';
import type {DesktopTransport} from './transport.js';

/** One repository a profile connection opened, as Go last listed it. */
interface Opened {
  repository: string;
  external: string;
  name: string;
  connection: string;
  summary?: FactoryProjectSummary;
  asOf: number;
}
/** This window's connection choice and, once connected, its binding. */
export interface ProjectsWindow {
  connection: string;
  wanted: string;
  repository: RepositoryRef | undefined;
  connected: boolean;
}
export interface ProjectsHost {
  accounts(): readonly Account[];
  window(): ProjectsWindow;
  choose(connection: string, repository: string): Promise<void>;
  openWindow(connection: string, repository: string): Promise<void>;
  reconnect(): Promise<void>;
  disconnect(): Promise<void>;
  signIn(): void;
  /** Creates the repository with this window's account and connects to it. */
  create(project: FactoryNewProject): Promise<void>;
}

const text = (value: unknown, limit: number): value is string =>
  typeof value === 'string' && value.length <= limit && !/[\u0000-\u001f\u007f]/.test(value);
const states: readonly FactoryProjectConnection[] = ['current', 'connected', 'not_connected', 'sign_in'];

function opened(value: unknown, asOf: number): Opened {
  const row = object(value);
  check(id(row.repository) && typeof row.external_id === 'string' && id(row.external_id));
  check(text(row.name, 512) && !!row.name && typeof row.connection === 'string' && /^[a-f0-9]{32}$/.test(row.connection));
  check(row.dispatch === 'open' || row.dispatch === 'closed' || row.dispatch === 'none');
  check(row.paused_by === undefined || (text(row.paused_by, 256) && /^[a-z_]+$/.test(row.paused_by)));
  check(Number.isSafeInteger(row.needs_you) && Number(row.needs_you) >= 0 && Number(row.needs_you) <= 99);
  check(Array.isArray(row.working) && row.working.length <= 8);
  const working = row.working.map((value) => {
    const work = object(value);
    check(text(work.role, 64) && !!work.role && Number.isSafeInteger(work.issue) && Number(work.issue) >= 0);
    return {role: work.role, ...(work.issue ? {issue: String(work.issue)} : {})};
  });
  let latestPost: FactoryProjectSummary['latestPost'];
  if (row.latest_post !== null) {
    const post = object(row.latest_post);
    check(text(post.id, 128) && text(post.title, 4096) && text(post.actor, 256));
    check(post.author === undefined || text(post.author, 256));
    check(post.at === undefined || (Number.isSafeInteger(post.at) && Number(post.at) > 0));
    latestPost = {id: post.id, title: post.title, actor: post.actor,
      ...(post.author ? {author: post.author as string} : {}), ...(post.at ? {at: post.at as number} : {})};
  }
  return {
    repository: row.repository, external: row.external_id, name: row.name, connection: row.connection, asOf,
    summary: {needsYou: row.needs_you as number, working, dispatch: row.dispatch, ...(row.paused_by ? {pausedBy: row.paused_by as string} : {}), ...(latestPost ? {latestPost} : {})},
  };
}

/** The window's project list. Go lists the repositories this profile's
 * connections opened; this host merges them with saved-account state and the
 * window's binding, one row per repository. Each read is one product request
 * and so one provider observation: the host reads on the first connection, at
 * most once when the page first asks without a retained list, and on an
 * explicit refresh; otherwise it answers from the retained list. */
export class DesktopProjects implements FactoryProjectsPort {
  private retained: Opened[] | undefined;
  private reading: Promise<void> | undefined;
  private transport: DesktopTransport | undefined;
  private asked = false;
  private generation = 0;
  /** Refusals from opening a repository here, by connection/external id, kept
   * across connections so the list can say which projects will not open. */
  private readonly refusals = new Map<string, string>();
  constructor(private readonly host: ProjectsHost) {}

  /** The provider refused (or, with no reason, accepted) opening this repository. */
  refusal(connection: string, external: string, reason: string) {
    if (reason) this.refusals.set(connection + '/' + external, reason);
    else this.refusals.delete(connection + '/' + external);
  }

  /** A new connection; reads only when nothing is retained. */
  connected(transport: DesktopTransport) {
    this.transport = transport;
    if (!this.retained) void this.read().catch(() => {});
  }
  /** Another profile: the retained list no longer applies. */
  forget() {
    this.generation++;
    this.retained = this.reading = this.transport = undefined;
    this.asked = false;
  }
  private read() {
    if (this.reading) return this.reading;
    const transport = this.transport,
      generation = this.generation;
    if (!transport) return Promise.reject(Error('Connect an account to list projects.'));
    const reading = (async () => {
      const response = await transport.request('/api/factory/projects');
      check(response.ok);
      const body = object(await readSodaJSON(response));
      check(Array.isArray(body.projects) && body.projects.length <= 50 && Number.isSafeInteger(body.as_of) && Number(body.as_of) > 0);
      const rows = body.projects.map((row) => opened(row, body.as_of as number));
      if (generation === this.generation) this.retained = rows;
    })().finally(() => {
      if (this.reading === reading) this.reading = undefined;
    });
    this.reading = reading;
    return reading;
  }
  async list(signal: AbortSignal, options?: {readonly refresh?: boolean}) {
    signal.throwIfAborted();
    if (options?.refresh || this.reading || (!this.retained && !this.asked)) {
      if (!options?.refresh) this.asked = true;
      const reading = this.read();
      await new Promise<void>((resolve, reject) => {
        signal.addEventListener('abort', () => reject(signal.reason), {once: true});
        reading.then(resolve, reject);
      }).catch((error) => {
        if (signal.aborted || options?.refresh || !this.retained) throw error;
      });
    }
    signal.throwIfAborted();
    if (!this.retained) throw Error('Projects are unavailable. Refresh to try again.');
    return this.merge().map(({row, account, connection}): FactoryProject => ({
      key: row.repository, name: row.name, accountLabel: account.label ?? account.account, provider: account.provider,
      connection, ...(row.summary ? {summary: row.summary} : {}), asOf: row.asOf,
      ...(this.refusals.has(row.connection + '/' + row.external) ? {refused: this.refusals.get(row.connection + '/' + row.external)!} : {}),
    }));
  }
  /** One row per repository. A repository opened by several saved accounts is
   * represented by this window's binding, else by the account this window is
   * connected with, else by the first other usable account in Go's order, else
   * by one that needs to sign in again. Removed, unknown and Fountain accounts
   * contribute nothing. */
  private merge() {
    const accounts = new Map(this.host.accounts().map((account) => [account.connection, account]));
    const window = this.host.window();
    const live = window.connected ? window.connection : '';
    const rows = [...(this.retained ?? [])];
    // The binding may name a repository Go has not listed yet.
    const bound = window.connected ? window.repository : undefined;
    if (bound && !rows.some((row) => row.repository === bound.id && row.connection === live))
      rows.unshift({repository: bound.id, external: bound.external_id, name: bound.name, connection: live, asOf: Math.floor(Date.now() / 1000)});
    const chosen = new Map<string, {row: Opened; account: Account; rank: number}>();
    for (const row of rows) {
      const account = accounts.get(row.connection);
      if (!account || account.provider !== 'github' || account.state === 'withdrawn') continue;
      const rank =
        row.connection === live && row.external === bound?.external_id ? 0
        : account.state !== 'stored' ? 3
        : row.connection === live ? 1 : 2;
      const best = chosen.get(row.repository);
      if (!best || rank < best.rank) chosen.set(row.repository, {row, account, rank});
    }
    return [...chosen.values()].map(({row, account, rank}) => ({row, account, connection: states[rank]!}));
  }
  private pick(key: string) {
    const found = this.merge().find(({row}) => row.repository === key);
    if (!found) throw Error('This project is no longer listed.');
    return found;
  }
  async open(key: string, where: 'here' | 'window') {
    const {row, connection} = this.pick(key);
    if (connection === 'sign_in') throw Error('Sign in to this account again before opening the project.');
    if (where === 'window') return this.host.openWindow(row.connection, row.external);
    if (connection !== 'current') return this.host.choose(row.connection, row.external);
  }
  /** Whether this window chose the project. Another account may represent its
   * row, so the window's own account and repository decide. */
  private chosenHere(key: string) {
    this.pick(key);
    const window = this.host.window();
    const here =
      !!window.connection &&
      (window.repository?.id === key ||
        (this.retained ?? []).some((row) => row.repository === key && row.connection === window.connection && row.external === window.wanted));
    const stored = this.host.accounts().some((account) => account.connection === window.connection && account.state === 'stored');
    return {here, stored};
  }
  async reconnect(key: string) {
    const {here, stored} = this.chosenHere(key);
    if (!here || !stored) throw Error('Only this window’s project can reconnect.');
    return this.host.reconnect();
  }
  async disconnect(key: string) {
    if (!this.chosenHere(key).here) throw Error('Only this window’s project can disconnect.');
    return this.host.disconnect();
  }
  create(project: FactoryNewProject) {
    return this.host.create(project);
  }
  async signIn(key: string) {
    if (this.pick(key).connection !== 'sign_in') throw Error('This account is already signed in.');
    this.host.signIn();
  }
}
