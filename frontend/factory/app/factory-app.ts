import {LitElement, html} from 'lit';
import {check, id, object} from '../api/models.js';
import {renderFactoryWordmark} from '../shared/factory-mark.js';
import type {FactoryTransport} from '../api/transport.js';
import {SodaFactoryFeed} from '../feed/feed.js';
import type {FactoryFeedContext} from '../feed/feed.js';
import {SodaFactoryWorkOverview, SodaFactoryWorkPlan} from '../work/progress.js';
import type {WorkOverviewContext, WorkPlanContext} from '../work/progress.js';
import {SodaFactoryWorkDetail} from '../work/detail.js';
import type {WorkDetailContext} from '../work/detail.js';
import {SodaFactoryIssues} from '../issues/issues.js';
import type {IssueListContext} from '../issues/issues.js';
import {SodaFactoryPulls} from '../pulls/list.js';
import type {PullListContext} from '../pulls/list.js';
import {SodaFactoryPull} from '../pulls/detail.js';
import type {PullDetailContext} from '../pulls/detail.js';
import {mountIssueScreen} from '../issues/screen.js';
import type {IssueDetailContext} from '../issues/detail.js';
import type {IssueCommentPorts} from '../issues/comment.js';
import {SodaFactoryIssueCreate} from '../issues/create.js';
import type {IssueCreatePorts} from '../issues/create.js';
import type {IssueEditorPorts} from '../issues/editor.js';
import type {JourneyContext} from '../work/journey.js';
import {SodaFactoryLibrary} from '../library/library.js';
import type {LibraryContext, LibrarySource} from '../library/library.js';
import {SodaFactoryWatch} from '../watch/watch.js';
import type {FactoryWatchContext, FactoryWatchRenderer} from '../watch/ports.js';
import {SodaFactorySettings} from '../settings/settings.js';
import type {FactorySettingsContext} from '../settings/settings.js';
import {SodaFactoryAccounts} from '../accounts/accounts.js';
import type {FactoryHostSection} from './host-section.js';
import type {FactoryAccountsContext} from '../accounts/accounts.js';
import {admitFactoryRoute, admitHostDestinations, factoryRouteTitle, hostResolves} from './routes.js';
import type {FactoryHostDestination, FactoryRoute} from './routes.js';
import {admitProjectRows, admitProjectsPort, projectMark} from '../projects/projects.js';
import type {FactoryProject, FactoryProjectsPort} from '../projects/projects.js';
import {SodaFactoryProjects} from '../projects/page.js';
import {SodaFactoryNewProject} from '../projects/new.js';
import './jump.js';
import type {JumpItem, SodaFactoryJump} from './jump.js';

/** Host-resolved ports, never a second workflow model. Host-created DOM enters
 * only as a `FactoryHostSection` (host-section.ts) the shared page places. */
export type FactoryScreen =
  | {readonly kind: 'feed'; readonly context: FactoryFeedContext}
  | {readonly kind: 'work'; readonly context: WorkOverviewContext}
  | {readonly kind: 'issues'; readonly context: Omit<IssueListContext, 'openIssue' | 'newIssue'>}
  | {readonly kind: 'pulls'; readonly context: Omit<PullListContext, 'openPull'>}
  | {readonly kind: 'pull'; readonly context: PullDetailContext}
  | {readonly kind: 'issue-new'; readonly context: IssueCreatePorts}
  | {
      readonly kind: 'issue';
      readonly context: IssueDetailContext;
      readonly journey?: JourneyContext;
      readonly comment?: IssueCommentPorts;
      readonly editor?: IssueEditorPorts;
    }
  | {readonly kind: 'work-detail'; readonly context: WorkDetailContext}
  | {readonly kind: 'plan'; readonly context: WorkPlanContext}
  | {readonly kind: 'library'; readonly context: LibraryContext}
  | {
      readonly kind: 'watch';
      readonly context: FactoryWatchContext;
      readonly renderer: () => Promise<FactoryWatchRenderer>;
    }
  | {
      readonly kind: 'settings';
      /** The frame supplies the actor, account label, scope and project name. */
      readonly context: Omit<FactorySettingsContext, 'actor' | 'accountLabel' | 'scope' | 'repositoryLabel'>;
    }
  | {readonly kind: 'accounts'; readonly context: FactoryAccountsContext}
  /** The frame supplies the project port; the host only binds the connection. */
  | {readonly kind: 'projects'; readonly context: {readonly transport: FactoryTransport; readonly sections?: readonly FactoryHostSection[]}}
  /** The frame supplies the project port and account label. */
  | {readonly kind: 'project-new'; readonly context: {readonly transport: FactoryTransport}};

export interface FactoryAppContext {
  readonly transport: FactoryTransport;
  /** Host-admitted stable profile/provider/account reference; never a credential. */
  readonly scope: string;
  readonly host: string;
  readonly actor: string;
  readonly accountLabel: string;
  /** Host-selected workspace, retained independently of the current destination. */
  readonly selectedRepository?: string;
  /** Optional plain-text name of the selected workspace for the sidebar; the frame shows no name without it. */
  readonly repositoryLabel?: string;
  /** Optional destinations this host resolves; the frame offers and admits only these. */
  readonly destinations?: readonly FactoryHostDestination[];
  /** Optional retained project list across this host's accounts. */
  readonly projects?: FactoryProjectsPort;
  readonly route: FactoryRoute;
  resolve(route: FactoryRoute, signal: AbortSignal): Promise<FactoryScreen>;
  navigate(route: FactoryRoute): void;
}

/** The header tab a page lives under, when the page is not a tab itself. */
function parentTab(route: FactoryRoute): FactoryRoute['kind'] | undefined {
  switch (route.kind) {
    case 'work-detail':
    case 'plan':
    case 'watch':
      return 'work';
    case 'issue':
    case 'issue-new':
      return 'issues';
    case 'pull':
      return 'pulls';
    default:
      return undefined;
  }
}

/** Production application composition shared by desktop and host integrations.
 * It creates no connection, backend, provider or executor. Hosts own admission. */
export class SodaFactoryApp extends LitElement {
  static properties = {message: {state: true}, busy: {state: true}, screen: {state: true}, projectRows: {state: true}, openingProject: {state: true}, confirmRemove: {state: true}, scheme: {type: String}};
  declare scheme: 'light' | 'dark' | undefined;
  declare private message: string;
  declare private busy: boolean;
  declare private screen: HTMLElement | undefined;
  /** The host's kept project list for the sidebar; undefined until read or without the port. */
  declare private projectRows: readonly FactoryProject[] | undefined;
  /** The row whose removal the person is confirming in its menu. */
  declare private confirmRemove: string;
  private projectsPort: FactoryProjectsPort | undefined;
  /** A project is opening; its buttons wait. Reactive, so they come back when it ends. */
  declare private openingProject: boolean;
  private current: FactoryAppContext | undefined;
  private generation = '';
  private lifetime = new AbortController();
  private refreshScreen: (() => unknown) | undefined;
  refresh() { return this.refreshScreen?.(); }
  private disposeScreen: (() => void) | undefined;

  constructor() {
    super();
    this.confirmRemove = '';
    this.message = 'Choose an account to open Factory.';
    this.busy = false;
    this.screen = undefined;
    this.projectRows = undefined;
    this.openingProject = false;
    this.scheme = undefined;
    this.addEventListener('library-source-open', this.openLibrarySource);
  }
  protected createRenderRoot() {
    return this;
  }
  set context(context: FactoryAppContext | undefined) {
    if (context === this.current && context?.transport.generation === this.generation) return;
    this.retire();
    if (!context) {
      this.projectRows = this.projectsPort = undefined;
      return;
    }
    try {
      check(
        /^[a-f0-9]{32}$/.test(context.host) &&
          id(context.actor) &&
          typeof context.scope === 'string' &&
          context.scope.trim().length > 0 &&
          context.scope.length <= 1024 &&
          !/[\u0000-\u001f\u007f]/.test(context.scope)
      );
      check(
        typeof context.accountLabel === 'string' && !!context.accountLabel.trim() && context.accountLabel.length <= 256
      );
      check(
        typeof context.transport.generation === 'string' &&
          !!context.transport.generation &&
          typeof context.resolve === 'function' &&
          typeof context.navigate === 'function'
      );
      admitFactoryRoute(context.route);
      admitHostDestinations(context.destinations);
      admitProjectsPort(context.projects);
      check(hostResolves(context.route, context.destinations));
      check(context.selectedRepository === undefined || id(context.selectedRepository));
      check(
        context.repositoryLabel === undefined ||
          (typeof context.repositoryLabel === 'string' &&
            context.repositoryLabel.length <= 256 &&
            !/[\u0000-\u001f\u007f]/.test(context.repositoryLabel))
      );
      if (context.selectedRepository !== undefined && 'repository' in context.route && context.route.repository !== undefined)
        check(context.selectedRepository === context.route.repository);
      if (context.route.kind === 'feed' && !context.route.repository && context.selectedRepository)
        context = {...context, route: {...context.route, repository: context.selectedRepository}};
      this.current = context;
      this.generation = context.transport.generation;
      void this.load(context);
      void this.readProjects(context);
    } catch {
      this.message = 'The Factory account or destination is unavailable.';
    }
  }
  connectedCallback() {
    super.connectedCallback();
    document.addEventListener('keydown', this.jumpKey);
  }
  // Ctrl/Cmd+K toggles the switcher from anywhere in the page except other text fields.
  private jumpKey = (event: KeyboardEvent) => {
    if (event.key.toLowerCase() !== 'k' || !(event.ctrlKey || event.metaKey) || event.altKey || event.shiftKey) return;
    const jump = this.querySelector<SodaFactoryJump>('soda-factory-jump');
    const target = event.composedPath()[0];
    const typing = target instanceof HTMLElement && (target.isContentEditable || /^(INPUT|TEXTAREA|SELECT)$/.test(target.tagName));
    if (!jump || !this.current || (typing && !jump.owns(target))) return;
    event.preventDefault();
    jump.toggle();
  };
  disconnectedCallback() {
    document.removeEventListener('keydown', this.jumpKey);
    this.retire();
    super.disconnectedCallback();
  }
  private retire() {
    this.lifetime.abort();
    this.lifetime = new AbortController();
    this.current = undefined;
    this.generation = '';
    this.message = 'Choose an account to open Factory.';
    this.busy = false;
    this.clearScreen();
  }
  // Plain list() reads the host's kept list; only the Projects page asks for a refresh.
  private async readProjects(context: FactoryAppContext) {
    const port = context.projects,
      signal = this.lifetime.signal;
    if (port !== this.projectsPort) this.projectRows = undefined;
    this.projectsPort = port;
    if (!port) return;
    try {
      const rows = admitProjectRows(await port.list(signal));
      if (this.live(context, signal)) this.projectRows = rows;
    } catch {
      // The sidebar keeps its last list; the Projects page reports failures.
    }
  }
  private async openProject(context: FactoryAppContext, row: FactoryProject) {
    const port = context.projects,
      signal = this.lifetime.signal;
    if (!port || this.openingProject) return;
    this.openingProject = true;
    try {
      if (row.connection === 'sign_in') await port.signIn?.(row.key);
      else await port.open(row.key, 'here');
    } catch {
      if (this.live(context, signal)) this.message = 'This project could not be opened.';
    } finally {
      this.openingProject = false;
    }
    if (this.live(context, signal)) await this.readProjects(context);
  }
  private async removeProject(context: FactoryAppContext, row: FactoryProject) {
    const port = context.projects,
      signal = this.lifetime.signal;
    if (!port?.remove || this.openingProject) return;
    this.confirmRemove = '';
    try {
      await port.remove(row.key);
    } catch {
      if (this.live(context, signal)) this.message = 'This project could not be removed.';
    }
    if (this.live(context, signal)) await this.readProjects(context);
    if (this.live(context, signal) && this.screen instanceof SodaFactoryProjects) this.screen.reload();
  }
  /** Right-click on a row opens its menu, as the ⋯ button does. */
  private openProjectMenu(event: MouseEvent) {
    const menu = (event.currentTarget as HTMLElement).querySelector('details');
    if (!menu) return;
    event.preventDefault();
    menu.open = true;
    menu.querySelector<HTMLElement>('summary')?.focus();
  }
  private projectMenu(context: FactoryAppContext, row: FactoryProject) {
    const port = context.projects!;
    const here = row.connection === 'current';
    const canWindow = row.connection !== 'sign_in';
    const canRemove = !!port.remove && !here;
    if (!canWindow && !canRemove) return '';
    const close = (event: Event) => ((event.currentTarget as HTMLElement).closest('details')!.open = false);
    const confirming = this.confirmRemove === row.key;
    return html`<details class="soda-factory-project-menu" @toggle=${(event: Event) => {
      if (!(event.currentTarget as HTMLDetailsElement).open && confirming) this.confirmRemove = '';
    }}>
      <summary aria-label=${'Actions for ' + row.name} title="Project actions">
        <svg viewBox="0 0 16 16" aria-hidden="true"><circle cx="3" cy="8" r="1.3" /><circle cx="8" cy="8" r="1.3" /><circle cx="13" cy="8" r="1.3" /></svg>
      </summary>
      <div role="menu" aria-label=${row.name}>
        ${confirming
          ? html`<p>Remove <strong>${row.name}</strong> from your projects? It stays on GitHub, and opening it again brings it back.</p>
              <button type="button" role="menuitem" data-tone="danger" @click=${(event: Event) => { close(event); void this.removeProject(context, row); }}>Remove</button>
              <button type="button" role="menuitem" @click=${() => (this.confirmRemove = '')}>Cancel</button>`
          : html`${canWindow
                ? html`<button type="button" role="menuitem" @click=${(event: Event) => { close(event); void port.open(row.key, 'window').catch(() => (this.message = 'Could not open a new window.')); }}>Open in new window</button>`
                : ''}
              ${canRemove ? html`<button type="button" role="menuitem" @click=${() => (this.confirmRemove = row.key)}>Remove from list…</button>` : ''}`}
      </div>
    </details>`;
  }
  private clearScreen() {
    const dispose = this.disposeScreen;
    this.disposeScreen = undefined;
    this.refreshScreen = undefined;
    this.screen = undefined;
    dispose?.();
  }
  private own<T extends HTMLElement>(view: T, retire: () => void, refresh?: () => unknown): T {
    const readable = view as HTMLElement & {refresh?: () => unknown};
    this.refreshScreen = refresh ?? (typeof readable.refresh === 'function' ? () => readable.refresh!() : undefined);
    // A resolved view can be retired before Lit has connected it. Explicitly
    // clear its binding rather than depending solely on disconnectedCallback.
    this.disposeScreen = () => {
      retire();
      view.remove();
    };
    return view;
  }
  private live(context: FactoryAppContext, signal: AbortSignal): boolean {
    if (this.current !== context || signal !== this.lifetime.signal || signal.aborted) return false;
    if (context.transport.generation !== this.generation) {
      this.retire();
      return false;
    }
    return true;
  }
  private async load(context: FactoryAppContext) {
    const signal = this.lifetime.signal;
    if (!this.live(context, signal) || this.busy) return;
    if (context.route.kind === 'feed' && !context.route.repository) {
      this.message = 'Choose a repository to view its activity.';
      return;
    }
    this.busy = true;
    this.message = 'Opening ' + factoryRouteTitle(context.route) + '…';
    try {
      const ports = await context.resolve(context.route, signal);
      if (!this.live(context, signal)) return;
      check(ports.kind === context.route.kind);
      this.screen = this.mount(context, ports, signal);
      this.message = '';
    } catch {
      if (this.live(context, signal)) {
        this.clearScreen();
        if (this.live(context, signal)) this.message = 'This page could not open.';
      }
    } finally {
      if (this.live(context, signal)) this.busy = false;
    }
  }
  private mount(context: FactoryAppContext, ports: FactoryScreen, signal: AbortSignal): HTMLElement {
    const route = context.route;
    check(ports.context.transport === context.transport);
    switch (ports.kind) {
      case 'issue-new': {
        check(route.kind === 'issue-new' && ports.context.repository === route.repository);
        check(
          ports.context.host === context.host &&
            ports.context.actor === context.actor &&
            ports.context.scope === context.scope
        );
        const view = new SodaFactoryIssueCreate();
        this.own(view, () => {
          view.context = undefined;
        });
        view.context = {
          ...ports.context,
          openIssue: (issue, childSignal) => {
            check(
              this.live(context, signal) &&
                !childSignal.aborted &&
                issue.kind === 'issue' &&
                issue.repository === route.repository &&
                id(issue.number)
            );
            this.navigate({kind: 'issue', repository: route.repository, issue: issue.number});
          },
        };
        return view;
      }
      case 'issues': {
        check(route.kind === 'issues' && ports.context.repository === route.repository);
        check(
          ports.context.host === context.host &&
            ports.context.actor === context.actor &&
            ports.context.scope === context.scope
        );
        const view = new SodaFactoryIssues();
        this.own(view, () => {
          view.context = undefined;
        });
        view.context = {
          ...ports.context,
          newIssue: () => {
            if (this.live(context, signal)) this.navigate({kind: 'issue-new', repository: route.repository});
          },
          openIssue: (issue, childSignal) => {
            check(
              this.live(context, signal) &&
                !childSignal.aborted &&
                issue.kind === 'issue' &&
                issue.repository === route.repository &&
                id(issue.number)
            );
            this.navigate({kind: 'issue', repository: route.repository, issue: issue.number});
          },
        };
        return view;
      }
      case 'pulls': {
        check(route.kind === 'pulls' && ports.context.repository === route.repository);
        check(ports.context.host === context.host && ports.context.actor === context.actor && ports.context.scope === context.scope);
        const view = new SodaFactoryPulls();
        this.own(view, () => { view.context = undefined; });
        view.context = {
          ...ports.context,
          openPull: (pull, childSignal) => {
            check(this.live(context, signal) && !childSignal.aborted && pull.kind === 'pull' && pull.repository === route.repository && id(pull.number));
            this.navigate({kind: 'pull', repository: route.repository, pull: pull.number});
          },
        };
        return view;
      }
      case 'pull': {
        check(route.kind === 'pull' && ports.context.repository === route.repository && ports.context.pull === route.pull);
        check((ports.context.initialTab ?? 'conversation') === (route.tab ?? 'conversation'));
        check(ports.context.host === context.host && ports.context.actor === context.actor && ports.context.scope === context.scope);
        const view = new SodaFactoryPull();
        this.own(view, () => { view.context = undefined; });
        view.context = ports.context;
        return view;
      }
      case 'issue': {
        check(
          route.kind === 'issue' && ports.context.repository === route.repository && ports.context.issue === route.issue
        );
        check(
          ports.context.host === context.host &&
            ports.context.actor === context.actor &&
            ports.context.scope === context.scope
        );
        const screen = mountIssueScreen(
          ports.context,
          ports.journey,
          signal,
          () => this.live(context, signal),
          undefined,
          ports.comment,
          ports.editor,
          ports.editor
        );
        return this.own(screen.element, screen.dispose, () => screen.refresh());
      }
      case 'feed': {
        check(
          route.kind === 'feed' &&
            ports.context.host.draftScope === context.scope &&
            ports.context.account === context.actor
        );
        check(
          route.repository === undefined
            ? ports.context.global === true
            : ports.context.repository === route.repository && !ports.context.global
        );
        const view = new SodaFactoryFeed();
        this.own(view, () => {
          view.context = undefined;
        });
        view.context = ports.context;
        if (route.post) view.post = route.post;
        return view;
      }
      case 'work': {
        check(
          route.kind === 'work' &&
            ports.context.repository === route.repository &&
            ports.context.intentID === route.intent
        );
        const view = new SodaFactoryWorkOverview();
        this.own(view, () => {
          view.context = undefined;
        });
        view.context = {...ports.context, actor: context.actor, accountLabel: context.accountLabel, scope: context.scope};
        return view;
      }
      case 'work-detail': {
        check(
          route.kind === 'work-detail' &&
            ports.context.repository === route.repository &&
            ports.context.issue === route.issue
        );
        const view = new SodaFactoryWorkDetail();
        this.own(view, () => {
          view.context = undefined;
        });
        const scope = ports.context.commandScope;
        view.context = {
          ...ports.context,
          ...(scope
            ? {
                commandScope: async (childSignal: AbortSignal) => {
                  check(this.live(context, signal));
                  const value = await scope(AbortSignal.any([signal, childSignal]));
                  check(this.live(context, signal) && value === context.scope);
                  return value;
                },
              }
            : {}),
        };
        return view;
      }
      case 'plan': {
        check(
          route.kind === 'plan' &&
            ports.context.repository === route.repository &&
            ports.context.issues.join(',') === route.issues.join(',')
        );
        const view = new SodaFactoryWorkPlan();
        this.own(view, () => {
          view.context = undefined;
        });
        view.context = ports.context;
        return view;
      }
      case 'library': {
        check(route.kind === 'library' && ports.context.repository === route.repository);
        const view = new SodaFactoryLibrary();
        this.own(view, () => {
          view.context = undefined;
        });
        view.itemID = route.item;
        view.context = ports.context;
        return view;
      }
      case 'settings': {
        check(route.kind === 'settings' && ports.context.repository === route.repository);
        const view = new SodaFactorySettings();
        this.own(view, () => {
          view.context = undefined;
        });
        view.context = {
          ...ports.context,
          actor: context.actor,
          accountLabel: context.accountLabel,
          scope: context.scope,
          ...(context.repositoryLabel === undefined ? {} : {repositoryLabel: context.repositoryLabel}),
        };
        return view;
      }
      case 'accounts': {
        check(route.kind === 'accounts');
        const view = new SodaFactoryAccounts();
        this.own(view, () => {
          view.context = undefined;
        });
        view.context = ports.context;
        return view;
      }
      case 'projects': {
        check(route.kind === 'projects' && context.projects !== undefined);
        const view = new SodaFactoryProjects();
        this.own(view, () => {
          view.context = undefined;
        });
        view.context = {
          transport: ports.context.transport,
          port: context.projects,
          ...(ports.context.sections ? {sections: ports.context.sections} : {}),
          onRows: (rows) => {
            if (this.live(context, signal)) this.projectRows = rows;
          },
          openFeed: () => {
            if (this.live(context, signal)) this.navigate({kind: 'feed', ...(context.selectedRepository ? {repository: context.selectedRepository} : {})});
          },
        };
        return view;
      }
      case 'project-new': {
        const create = context.projects?.create;
        check(route.kind === 'project-new' && create !== undefined);
        const view = new SodaFactoryNewProject();
        this.own(view, () => {
          view.context = undefined;
        });
        view.context = {accountLabel: context.accountLabel, port: {...context.projects!, create}};
        return view;
      }
      case 'watch': {
        check(
          route.kind === 'watch' &&
            ports.context.runId === route.run &&
            ports.context.role === route.role &&
            ports.context.issue === route.issue &&
            ports.context.attempt === route.attempt
        );
        const source = ports.context.source;
        const view = new SodaFactoryWatch();
        this.own(view, () => view.dispose());
        view.configure(
          {
            ...ports.context,
            source: {
              repository: async (childSignal) => {
                check(this.live(context, signal));
                const repository = await source.repository(AbortSignal.any([signal, childSignal]));
                check(this.live(context, signal) && repository === route.repository);
                return repository;
              },
              connect: () => {
                check(this.live(context, signal));
                return source.connect();
              },
              attachment: (cursor, generation) => {
                check(this.live(context, signal));
                return source.attachment(cursor, generation);
              },
            },
          },
          ports.renderer
        );
        return view;
      }
    }
  }
  private navigate(route: FactoryRoute) {
    const context = this.current,
      signal = this.lifetime.signal;
    if (!context || !this.live(context, signal)) return;
    try {
      admitFactoryRoute(route);
      check(hostResolves(route, context.destinations));
      context.navigate(route);
    } catch {
      if (this.live(context, signal)) this.message = 'This destination is unavailable.';
    }
  }
  private openLibrarySource = (event: Event) => {
    const context = this.current;
    if (!context || context.route.kind !== 'library' || event.target !== this.screen || !(event instanceof CustomEvent))
      return;
    try {
      const source = object(event.detail) as unknown as LibrarySource;
      check(source.repository === context.route.repository);
      this.navigate({kind: 'feed', repository: source.repository, post: source.id});
    } catch {
      this.message = 'The source post is unavailable.';
    }
  };
  private jumpItems(
    context: FactoryAppContext | undefined,
    project: string | undefined,
    links: readonly {label: string; route: FactoryRoute}[],
    foot: readonly {label: string; route: FactoryRoute}[]
  ): JumpItem[] {
    if (!context) return [];
    const port = context.projects;
    const projectItems: JumpItem[] = port
      ? (this.projectRows ?? []).map((row) => {
          const mark = projectMark(row);
          const meta =
            row.connection === 'current'
              ? 'Current'
              : mark
                ? mark.kind === 'needs' ? mark.detail : mark.label
                : row.connection === 'not_connected'
                  ? 'Other account'
                  : row.accountLabel;
          return {
            group: 'Projects',
            label: row.name,
            meta,
            disabled: row.connection === 'sign_in' && !port.signIn,
            run: () => {
              if (row.connection !== 'current') void this.openProject(context, row);
            },
          };
        })
      : [];
    const pages = links.map((link) => ({group: project ? 'In ' + project : 'Pages', label: link.label, meta: 'page', run: () => this.navigate(link.route)}));
    const actions: JumpItem[] = [
      ...(port && context.destinations?.includes('projects') ? [{group: 'Actions', label: 'All projects', meta: 'action', run: () => this.navigate({kind: 'projects'})}] : []),
      ...(port?.create && context.destinations?.includes('project-new') ? [{group: 'Actions', label: 'New project', meta: 'action', run: () => this.navigate({kind: 'project-new'})}] : []),
      ...links.flatMap(({route}) => (route.kind === 'issues' ? [{group: 'Actions', label: 'New issue', meta: 'action', run: () => this.navigate({kind: 'issue-new', repository: route.repository})}] : [])),
      ...foot.map((link) => ({group: 'Actions', label: 'Open ' + link.label, meta: 'action', run: () => this.navigate(link.route)})),
    ];
    return [...projectItems, ...pages, ...actions];
  }
  protected render() {
    const context = this.current;
    const repository = context?.selectedRepository ?? (context && 'repository' in context.route ? context.route.repository : undefined);
    const links: {label: string; route: FactoryRoute}[] = [
      {label: 'Feed', route: {kind: 'feed', ...(repository ? {repository} : {})}},
      ...(repository
        ? [
            {label: 'Work', route: {kind: 'work' as const, repository}},
            {label: 'Issues', route: {kind: 'issues' as const, repository}},
            {label: 'Pull requests', route: {kind: 'pulls' as const, repository}},
            {label: 'Knowledge', route: {kind: 'library' as const, repository}},
          ]
        : []),
    ];
    // A page inside a tab keeps that tab highlighted and offers one way back.
    const parent = context ? parentTab(context.route) : undefined;
    const back = repository && parent ? links.find((link) => link.route.kind === parent) : undefined;
    const project = context?.repositoryLabel?.trim();
    const port = context?.projects;
    const listed = port ? this.projectRows ?? [] : [];
    // Host-declared destinations at the foot: project settings, then account sign-ins.
    const foot: {label: string; route: FactoryRoute}[] = [
      ...(repository && context?.destinations?.includes('settings')
        ? [{label: 'Settings', route: {kind: 'settings' as const, repository}}]
        : []),
      ...(context?.destinations?.includes('accounts') ? [{label: 'Accounts', route: {kind: 'accounts' as const}}] : []),
    ];
    return html`<div class="soda-factory-root soda-factory-app" data-factory-scheme=${this.scheme ?? ''}>
      <aside class="soda-factory-sidebar">
        ${renderFactoryWordmark()}
        <button
          type="button"
          class="soda-factory-jump-open"
          aria-haspopup="dialog"
          aria-keyshortcuts="Control+K Meta+K"
          ?disabled=${!context}
          @click=${(event: Event) => this.querySelector<SodaFactoryJump>('soda-factory-jump')?.show(event.currentTarget as HTMLElement)}
        >
          <span>Jump to…</span><kbd>${/Mac|iPhone|iPad/.test(navigator.platform) ? '⌘K' : 'Ctrl K'}</kbd>
        </button>
        ${port && context?.destinations?.includes('projects')
          ? html`<section class="soda-factory-projects" aria-label="Projects">
              <div class="soda-factory-projects-head">
                <button
                  type="button"
                  class="soda-factory-projects-all"
                  aria-label="All projects"
                  aria-current=${context.route.kind === 'projects' ? 'page' : 'false'}
                  @click=${() => this.navigate({kind: 'projects'})}
                >
                  Projects
                </button>
                ${port.create && context.destinations.includes('project-new')
                  ? html`<button
                      type="button"
                      class="soda-factory-projects-new"
                      aria-label="New project"
                      title="New project"
                      aria-current=${context.route.kind === 'project-new' ? 'page' : 'false'}
                      @click=${() => this.navigate({kind: 'project-new'})}
                    >
                      <svg viewBox="0 0 16 16" aria-hidden="true"><path d="M8 3v10M3 8h10" /></svg>
                    </button>`
                  : ''}
              </div>
              ${listed.length
                ? html`<nav aria-label="Your projects">
                    ${listed.map((row) => {
                      const mark = projectMark(row);
                      const here = row.connection === 'current';
                      const slash = row.name.indexOf('/');
                      const owner = slash > 0 ? row.name.slice(0, slash) : '';
                      const name = slash > 0 ? row.name.slice(slash + 1) : row.name;
                      const status = mark && mark.kind !== 'needs' ? mark : undefined;
                      return html`<div class="soda-factory-project-item" @contextmenu=${(event: MouseEvent) => this.openProjectMenu(event)}>
                        <button
                          type="button"
                          class="soda-factory-project-row"
                          aria-current=${here ? 'true' : 'false'}
                          ?disabled=${(row.connection === 'sign_in' && !port.signIn) || this.openingProject}
                          title=${row.name + (mark ? ' · ' + mark.detail : '')}
                          @click=${() => (here ? this.navigate({kind: 'feed', ...(repository ? {repository} : {})}) : void this.openProject(context, row))}
                        >
                          <span class="soda-factory-project-row-name">${name}</span>
                          <span class="soda-factory-project-row-meta"
                            >${owner}${owner && status ? ' · ' : ''}${status
                              ? html`<span data-mark=${status.kind}>${status.label}</span>`
                              : ''}</span
                          >
                          ${mark?.kind === 'needs' ? html`<span class="soda-factory-project-row-count" aria-label=${mark.detail}>${mark.label}</span>` : ''}
                        </button>
                        ${this.projectMenu(context, row)}
                      </div>`;
                    })}
                  </nav>`
                : ''}
            </section>`
          : ''}
        <div class="soda-factory-sidebar-foot">
          ${foot.length
            ? html`<nav aria-label="Settings and accounts">
                ${foot.map(
                  (link) => html`<button
                    type="button"
                    aria-current=${context?.route.kind === link.route.kind ? 'page' : 'false'}
                    @click=${() => this.navigate(link.route)}
                  >
                    ${link.label}
                  </button>`
                )}
              </nav>`
            : ''}
          ${context ? html`<p class="soda-factory-acting">Acting as ${context.accountLabel}</p>` : ''}
        </div>
      </aside>
      <div class="soda-factory-content">
        <!-- The open project's name and its pages run across the top; the status sits at the end. -->
        <header ?data-empty=${!repository && !this.message.trim()}>
          ${repository ? html`<div class="soda-factory-project">
            ${project ? html`<p class="soda-factory-project-name">${project}</p>` : ''}
            <nav aria-label="Factory">
              ${links.map(
                (link) => html`<button
                  type="button"
                  aria-current=${context?.route.kind === link.route.kind || parent === link.route.kind ? 'page' : 'false'}
                  ?disabled=${!context}
                  @click=${() => this.navigate(link.route)}
                >
                  ${link.label}
                </button>`
              )}
            </nav>
          </div>` : ''}
          <p role="status" ?hidden=${!this.message.trim()}>${this.message}</p>
        </header>
        <main
          aria-label=${context ? factoryRouteTitle(context.route) : 'Factory'}
          aria-busy=${this.busy ? 'true' : 'false'}
        >
          ${back ? html`<button type="button" class="soda-factory-back" @click=${() => this.navigate(back.route)}>← ${back.label}</button>` : ''}
          ${this.screen ?? ''}
          ${context && !this.screen && !this.busy ? html`<button type="button" @click=${() => void this.load(context)}>Try again</button>` : ''}
        </main>
      </div>
      <soda-factory-jump .items=${this.jumpItems(context, project, links, foot)}></soda-factory-jump>
    </div>`;
  }
}
customElements.define('soda-factory-app', SodaFactoryApp);
