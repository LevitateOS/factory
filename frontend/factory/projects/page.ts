import {LitElement, html} from 'lit';
import {check} from '../api/models.js';
import type {FactoryTransport} from '../api/transport.js';
import {feedRolePortraits, roleLabel} from '../feed/roles.js';
import {admitProjectRows} from './projects.js';
import type {FactoryProject, FactoryProjectsPort} from './projects.js';
import {admitHostSections, mountHostSections, renderHostSection} from '../app/host-section.js';
import type {FactoryHostSection, MountedHostSection} from '../app/host-section.js';

export interface FactoryProjectsContext {
  /** Binds the page to the frame's current connection; supplies the portrait base. */
  readonly transport: FactoryTransport;
  readonly port: FactoryProjectsPort;
  /** Tells the frame which rows were read so its sidebar agrees with the page. */
  readonly onRows?: (rows: readonly FactoryProject[]) => void;
  /** Shows the frame's Feed for the project this window now has open. */
  readonly openFeed: () => void;
  /** Host cards beside the list, such as the desktop's account and repository picker. */
  readonly sections?: readonly FactoryHostSection[];
}

type Action = 'here' | 'window' | 'reconnect' | 'disconnect' | 'sign-in';

/** Time a row was read, in the viewer's locale. */
export const projectAsOf = (seconds: number) =>
  new Date(seconds * 1000).toLocaleString([], {month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit'});

/** The retained projects across the host's accounts, one row each. It reads the
 * kept list when it opens and asks for a new read only from Refresh. */
export class SodaFactoryProjects extends LitElement {
  static properties = {rows: {state: true}, state: {state: true}, message: {state: true}, pending: {state: true}};
  declare private rows: readonly FactoryProject[];
  declare private state: 'loading' | 'ready' | 'failed';
  declare private message: string;
  declare private pending: string;
  private current: FactoryProjectsContext | undefined;
  private mounted: MountedHostSection[] = [];
  private lifetime = new AbortController();
  constructor() {
    super();
    this.rows = [];
    this.state = 'loading';
    this.message = '';
    this.pending = '';
  }
  protected createRenderRoot() {
    return this;
  }
  set context(context: FactoryProjectsContext | undefined) {
    if (context === this.current) return;
    this.lifetime.abort();
    this.lifetime = new AbortController();
    this.current = context;
    this.mounted = [];
    this.rows = [];
    this.message = this.pending = '';
    this.state = 'loading';
    if (!context) return;
    check(typeof context.transport.generation === 'string' && !!context.transport.generation && typeof context.openFeed === 'function');
    admitHostSections(context.sections);
    this.mounted = mountHostSections(context.sections, this.lifetime.signal);
    void this.read(context, false);
  }
  disconnectedCallback() {
    this.context = undefined;
    super.disconnectedCallback();
  }
  private async read(context: FactoryProjectsContext, refresh: boolean) {
    const signal = this.lifetime.signal;
    try {
      const rows = admitProjectRows(await (refresh ? context.port.list(signal, {refresh: true}) : context.port.list(signal)));
      if (signal.aborted || this.current !== context) return;
      this.rows = rows;
      this.state = 'ready';
      this.message = '';
      context.onRows?.(rows);
    } catch {
      if (signal.aborted || this.current !== context) return;
      // Keep rows already shown; a failed read says so without hiding them.
      if (!this.rows.length) this.state = 'failed';
      else this.message = 'Could not update the list. Showing the last one read.';
    }
  }
  private refresh = async () => {
    const context = this.current;
    if (!context || this.pending) return;
    this.pending = 'refresh';
    this.message = '';
    await this.read(context, true);
    if (this.current === context) this.pending = '';
  };
  private async act(row: FactoryProject, action: Action) {
    const context = this.current,
      signal = this.lifetime.signal,
      port = context?.port;
    if (!context || !port || this.pending) return;
    this.pending = row.key + ':' + action;
    this.message = '';
    try {
      if (action === 'here') await port.open(row.key, 'here');
      else if (action === 'window') await port.open(row.key, 'window');
      else if (action === 'reconnect') await port.reconnect!(row.key);
      else if (action === 'disconnect') await port.disconnect!(row.key);
      else await port.signIn!(row.key);
    } catch {
      if (signal.aborted || this.current !== context) return;
      this.message = 'That did not work. Check the project and try again.';
    }
    if (signal.aborted || this.current !== context) return;
    this.pending = '';
    if (action === 'sign-in') return;
    // The host changed something: re-read the kept list, never a new provider read.
    await this.read(context, false);
    if (action === 'here' && this.current === context && !signal.aborted && !this.message) context.openFeed();
  }
  private summary() {
    const needs = this.rows.reduce((total, row) => total + (row.summary?.needsYou ?? 0), 0);
    const working = this.rows.reduce((total, row) => total + (row.summary?.working.length ?? 0), 0);
    const plural = (n: number, one: string, many: string) => n + ' ' + (n === 1 ? one : many);
    return `${plural(this.rows.length, 'project', 'projects')} · ${needs} ${needs === 1 ? 'post needs' : 'posts need'} you · ${plural(working, 'agent', 'agents')} working`;
  }
  private tags(row: FactoryProject) {
    if (row.connection === 'sign_in') return [html`<span class="soda-factory-tag" data-tone="sign-in">Sign in</span>`];
    if (row.connection === 'not_connected') return [html`<span class="soda-factory-tag" data-tone="idle">Not connected</span>`];
    const tags = [];
    if (row.summary?.needsYou) tags.push(html`<span class="soda-factory-tag" data-tone="needs">Needs you · ${row.summary.needsYou}</span>`);
    if (row.summary?.dispatch === 'closed') tags.push(html`<span class="soda-factory-tag" data-tone="paused">Paused</span>`);
    return tags;
  }
  private working(row: FactoryProject) {
    const working = row.summary?.working ?? [];
    if (!working.length) return '';
    const base = this.current?.transport.assetBase ?? '/';
    return html`<div class="soda-factory-project-working">
      <span class="soda-factory-portraits" aria-hidden="true">
        ${working.map((work) => {
          const portrait = feedRolePortraits[roleLabel(work.role) ?? ''];
          return portrait ? html`<img src=${base + portrait} alt="" width="32" height="32" loading="lazy" />` : '';
        })}
      </span>
      <p>${working.map((work) => roleLabel(work.role) + (work.issue ? ' on #' + work.issue : '')).join(', ')}</p>
    </div>`;
  }
  private card(row: FactoryProject) {
    const port = this.current!.port;
    const busy = this.pending !== '';
    const here = row.connection === 'current';
    const post = row.summary?.latestPost;
    const button = (action: Action, label: string, tone = '') =>
      html`<button type="button" data-tone=${tone} ?disabled=${busy} aria-busy=${this.pending === row.key + ':' + action ? 'true' : 'false'} @click=${() => void this.act(row, action)}>${label}</button>`;
    return html`<article class="soda-factory-project-card" data-connection=${row.connection} aria-label=${row.name}>
      <div class="soda-factory-project-card-body">
        <div class="soda-factory-project-card-title">
          <h3>${row.name}</h3>
          ${this.tags(row)}
          ${here ? html`<span class="soda-factory-tag" data-tone="here">Open in this window</span>` : ''}
        </div>
        <p class="soda-factory-project-meta">${row.provider} · ${row.accountLabel} · as of ${projectAsOf(row.asOf)}${row.summary ? ' · dispatch ' + row.summary.dispatch : ''}</p>
        ${this.working(row)}
        ${post
          ? html`<p class="soda-factory-project-latest">Latest post: “${post.title}” · ${post.author ?? roleLabel(post.actor)}${post.at ? ' · ' + projectAsOf(post.at) : ''}</p>`
          : row.summary ? html`<p class="soda-factory-project-latest">No posts yet.</p>` : html`<p class="soda-factory-project-latest">No summary read for this project yet.</p>`}
      </div>
      <div class="soda-factory-project-actions">
        ${row.connection === 'sign_in'
          ? port.signIn ? button('sign-in', 'Sign in again', 'primary') : ''
          : html`${button('here', 'Open feed', 'primary')}${button('window', 'New window')}`}
        ${here && (port.reconnect || port.disconnect)
          ? html`<div class="soda-factory-project-pair">
              ${port.reconnect ? button('reconnect', 'Reconnect') : ''}${port.disconnect ? button('disconnect', 'Disconnect') : ''}
            </div>`
          : ''}
      </div>
    </article>`;
  }
  protected render() {
    if (!this.current) return html``;
    return html`<section class="soda-factory-projects-page" aria-labelledby="projects-title">
      <header>
        <div>
          <h2 id="projects-title">Projects</h2>
          ${this.state === 'ready' ? html`<p>${this.summary()}</p>` : ''}
        </div>
        <button type="button" ?disabled=${this.pending !== '' || this.state === 'loading'} aria-busy=${this.pending === 'refresh' ? 'true' : 'false'} @click=${this.refresh}>
          ${this.pending === 'refresh' ? 'Refreshing…' : 'Refresh'}
        </button>
      </header>
      ${this.message ? html`<p class="soda-factory-projects-message" role="alert">${this.message}</p>` : ''}
      ${this.state === 'loading'
        ? html`<p class="soda-factory-projects-note" role="status">Reading your projects…</p>`
        : this.state === 'failed'
          ? html`<p class="soda-factory-projects-note" role="alert">Projects are unavailable. Refresh to try again.</p>`
          : this.rows.length
            ? html`<div class="soda-factory-project-list">${this.rows.map((row) => this.card(row))}</div>`
            : html`<p class="soda-factory-projects-note">No projects yet. Add one with your account and a repository.</p>`}
      ${this.mounted.length
        ? html`<aside class="soda-factory-projects-aside">${this.mounted.map((section) => renderHostSection('projects', section))}</aside>`
        : ''}
    </section>`;
  }
}
customElements.define('soda-factory-projects', SodaFactoryProjects);
