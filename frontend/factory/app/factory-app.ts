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
import {automationFailure, automationRoles, changeAutomation, readAutomation, setUpProject} from '../work/role-settings.js';
import type {AutomationChange, AutomationRole, AutomationState, RoleContext} from '../work/role-settings.js';
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

/** How often the open page re-reads itself while the window is visible. */
const keepCurrentEvery = 60_000;

/** Sidebar width in CSS pixels. Dragging the edge below `collapse` hides the sidebar. */
const sidebarWidth = {min: 200, max: 420, initial: 260, collapse: 160};
const sidebarKey = 'soda-factory.sidebar';
/** Below this width the sidebar opens over the page instead of beside it. */
const narrowWindow = '(max-width: 799px)';
const clampSidebar = (width: number) => Math.round(Math.min(sidebarWidth.max, Math.max(sidebarWidth.min, width)));
/** The person's sidebar width and collapse choice on this device; defaults when storage is unavailable. */
function readSidebar(): {width: number; collapsed: boolean} {
  try {
    const saved = JSON.parse(localStorage.getItem(sidebarKey) ?? 'null') as {width?: unknown; collapsed?: unknown} | null;
    const width = Number(saved?.width);
    return {width: Number.isFinite(width) ? clampSidebar(width) : sidebarWidth.initial, collapsed: saved?.collapsed === true};
  } catch {
    return {width: sidebarWidth.initial, collapsed: false};
  }
}
const sidebarIcon = html`<svg viewBox="0 0 16 16" aria-hidden="true"><rect x="1.5" y="2.5" width="13" height="11" rx="1.5" /><path d="M6 2.5v11" /></svg>`;

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
  static properties = {message: {state: true}, busy: {state: true}, screen: {state: true}, projectRows: {state: true}, openingProject: {state: true}, confirmRemove: {state: true}, menuAt: {state: true}, menuAutomation: {state: true}, menuBusy: {state: true}, menuNote: {state: true}, submenu: {state: true}, collapsed: {state: true}, narrow: {state: true}, overlay: {state: true}, scheme: {type: String}};
  declare scheme: 'light' | 'dark' | undefined;
  declare private message: string;
  declare private busy: boolean;
  declare private screen: HTMLElement | undefined;
  /** The host's kept project list for the sidebar; undefined until read or without the port. */
  declare private projectRows: readonly FactoryProject[] | undefined;
  /** The row whose removal the person is confirming in its menu. */
  declare private confirmRemove: string;
  /** The project whose right-click menu is open, and where it opened. */
  declare private menuAt: {key: string; x: number; y: number} | undefined;
  /** The row the menu came from; focus returns to it when the menu closes. */
  private menuFrom: HTMLElement | undefined;
  /** The open project's automation in its menu, while it is read or saved. */
  declare private menuAutomation: AutomationState | 'loading' | 'failed' | 'elsewhere' | undefined;
  /** A menu choice is being saved; the items wait. */
  declare private menuBusy: boolean;
  /** Opening this project here should end on its Settings tab. */
  private settingsAfterOpen = '';
  /** Why the last menu choice did not happen. */
  declare private menuNote: string;
  /** The side menu open inside the project menu. */
  declare private submenu: AutomationRole | 'merge' | undefined;
  /** Ends the menu's reads and saves when it closes. */
  private menuReads = new AbortController();
  /** The person hid the sidebar in a wide window. */
  declare private collapsed: boolean;
  /** The window is narrow, so the sidebar opens over the page. */
  declare private narrow: boolean;
  /** In a narrow window, the sidebar is open over the page. */
  declare private overlay: boolean;
  private sidebarWidth = sidebarWidth.initial;
  private narrowQuery = window.matchMedia(narrowWindow);
  private narrowChange = (event: MediaQueryListEvent) => {
    this.narrow = event.matches;
    this.overlay = false;
  };
  private projectsPort: FactoryProjectsPort | undefined;
  /** A project is opening; its buttons wait. Reactive, so they come back when it ends. */
  declare private openingProject: boolean;
  private current: FactoryAppContext | undefined;
  private generation = '';
  private lifetime = new AbortController();
  private refreshScreen: (() => unknown) | undefined;
  private lastRead = 0;
  private keepCurrentTimer: ReturnType<typeof setInterval> | undefined;
  /** Re-reads the open page in place. Hosts call it when the window comes back. */
  refresh() {
    this.lastRead = Date.now();
    return this.refreshScreen?.();
  }
  // Pages have no Refresh buttons: the open page re-reads once a minute while
  // the window is visible, and right after the person's own actions.
  private keepCurrent = () => {
    if (!document.hidden && !this.busy && Date.now() - this.lastRead >= keepCurrentEvery) void this.refresh();
  };
  private disposeScreen: (() => void) | undefined;

  constructor() {
    super();
    this.confirmRemove = this.menuNote = '';
    this.menuAt = this.menuAutomation = this.submenu = undefined;
    const saved = readSidebar();
    this.sidebarWidth = saved.width;
    this.collapsed = saved.collapsed;
    this.narrow = this.narrowQuery.matches;
    this.overlay = false;
    this.style.setProperty('--soda-factory-sidebar-width', this.sidebarWidth + 'px');
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
      // "All settings…" on another project: once it opens here, go to its Settings tab.
      const settings = this.settingsAfterOpen && this.settingsAfterOpen === context.selectedRepository && context.route.kind !== 'settings';
      if (settings) this.settingsAfterOpen = '';
      if (settings) queueMicrotask(() => this.navigate({kind: 'settings', repository: context!.selectedRepository!}));
      void this.load(context);
      void this.readProjects(context);
    } catch {
      this.message = 'The Factory account or destination is unavailable.';
    }
  }
  connectedCallback() {
    super.connectedCallback();
    document.addEventListener('keydown', this.shortcut);
    this.narrowQuery.addEventListener('change', this.narrowChange);
    this.keepCurrentTimer = setInterval(this.keepCurrent, keepCurrentEvery / 4);
  }
  // Cmd/Ctrl+\ shows or hides the sidebar; Escape closes it over a narrow page.
  // Cmd/Ctrl+K toggles the switcher from anywhere in the page except other text fields.
  private shortcut = (event: KeyboardEvent) => {
    const command = (event.ctrlKey || event.metaKey) && !event.altKey && !event.shiftKey;
    if (command && event.code === 'Backslash') {
      event.preventDefault();
      this.toggleSidebar();
      return;
    }
    if (event.key === 'Escape' && this.narrow && this.overlay && !event.defaultPrevented) {
      this.toggleSidebar();
      return;
    }
    if (event.key.toLowerCase() !== 'k' || !command) return;
    const jump = this.querySelector<SodaFactoryJump>('soda-factory-jump');
    const target = event.composedPath()[0];
    const typing = target instanceof HTMLElement && (target.isContentEditable || /^(INPUT|TEXTAREA|SELECT)$/.test(target.tagName));
    if (!jump || !this.current || (typing && !jump.owns(target))) return;
    event.preventDefault();
    jump.toggle();
  };
  disconnectedCallback() {
    document.removeEventListener('keydown', this.shortcut);
    this.narrowQuery.removeEventListener('change', this.narrowChange);
    clearInterval(this.keepCurrentTimer);
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
    this.overlay = false;
    try {
      if (row.connection === 'sign_in') await port.signIn?.(row.key);
      else await port.open(row.key, 'here');
    } catch {
      this.settingsAfterOpen = '';
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
  /** Projects should focus its repository picker once it opens. */
  private addRepository = false;
  /** Open a GitHub repository: the Projects page, with its add-a-project picker focused. */
  private openRepository() {
    if (this.screen instanceof SodaFactoryProjects) return void this.screen.focusAdd();
    this.addRepository = true;
    this.navigate({kind: 'projects'});
  }
  /** What a project's menu offers; nothing means right-click does nothing.
   * Every project that can open shows the same settings items. */
  private projectActions(port: FactoryProjectsPort, row: FactoryProject) {
    const here = row.connection === 'current';
    return {
      open: !here && row.connection !== 'sign_in',
      window: row.connection !== 'sign_in',
      settings: row.connection !== 'sign_in',
      reconnect: here && !!port.reconnect,
      disconnect: here && !!port.disconnect,
      remove: !!port.remove && !here,
    };
  }
  private hasProjectMenu(port: FactoryProjectsPort, row: FactoryProject) {
    return Object.values(this.projectActions(port, row)).some(Boolean);
  }
  /** Right-click, the context-menu key or Shift+F10 on a project row opens its menu. */
  private projectMenuKey(event: KeyboardEvent, port: FactoryProjectsPort, row: FactoryProject) {
    if (event.key !== 'ContextMenu' && !(event.key === 'F10' && event.shiftKey)) return;
    const box = (event.currentTarget as HTMLElement).getBoundingClientRect();
    this.openProjectMenu(event, port, row, box.left + 16, box.bottom);
  }
  private async openProjectMenu(event: Event, port: FactoryProjectsPort, row: FactoryProject, x: number, y: number) {
    if (!this.hasProjectMenu(port, row)) return;
    event.preventDefault();
    this.menuFrom = event.currentTarget as HTMLElement;
    this.confirmRemove = this.menuNote = '';
    this.submenu = undefined;
    this.menuBusy = false;
    this.menuAt = {key: row.key, x, y};
    this.menuReads.abort();
    this.menuReads = new AbortController();
    const target = this.projectActions(port, row).settings ? this.automationTarget(row) : undefined;
    this.menuAutomation = target ? 'loading' : this.projectActions(port, row).settings ? 'elsewhere' : undefined;
    await this.updateComplete;
    const menu = this.querySelector<HTMLElement>('#soda-factory-project-menu');
    if (!menu) return;
    if (!menu.matches(':popover-open')) menu.showPopover();
    this.placeMenu(menu);
    if (target) await this.readMenuAutomation(target, this.menuReads.signal);
  }
  /** A project's settings as this window's connection reaches them: the project
   * open here, or another one on the same account. Another account's project
   * has to open first. */
  private automationTarget(row: FactoryProject) {
    const context = this.current;
    if (!context) return undefined;
    const repository = row.connection === 'current' ? context.selectedRepository : row.connection === 'connected' ? row.key : undefined;
    return repository ? {transport: context.transport, repository, actor: context.actor} : undefined;
  }
  private async readMenuAutomation(target: RoleContext, signal: AbortSignal) {
    try {
      const state = await readAutomation(target, signal);
      if (!signal.aborted) this.menuAutomation = state;
    } catch {
      if (!signal.aborted) this.menuAutomation = 'failed';
    }
    await this.updateComplete;
    // A note may have changed the height: keep the menu inside the window without moving focus.
    const menu = this.querySelector<HTMLElement>('#soda-factory-project-menu');
    if (!signal.aborted && menu && this.menuAt) this.placePopover(menu, this.menuAt.x, this.menuAt.y, false);
  }
  /** The project the open menu belongs to. */
  private get menuRow() {
    return this.menuAt && this.projectRows?.find((candidate) => candidate.key === this.menuAt!.key);
  }
  /** Saves one automation choice from the menu, or gives the project its first
   * settings, then closes the menu and re-reads what shows it. */
  private async changeFromMenu(menu: HTMLElement, change: AutomationChange | 'setup') {
    const context = this.current,
      row = this.menuRow,
      target = row && this.automationTarget(row),
      signal = this.menuReads.signal,
      before = this.menuAutomation;
    if (!context || !target || this.menuBusy || typeof before !== 'object') return;
    const block = change === 'setup' ? undefined
      : 'on' in change && change.on ? before.blocked : 'pickUpPulls' in change && change.pickUpPulls ? before.blockedPulls : undefined;
    // Something must be chosen first: say what, and leave the menu open beside it.
    if (block) return void (this.menuNote = block.text);
    this.menuBusy = true;
    this.menuNote = '';
    try {
      if (change === 'setup') this.menuAutomation = await setUpProject(target, signal);
      else await changeAutomation(target, signal, change);
      if (signal.aborted) return;
      // Setting up keeps the menu open: its choices are there now.
      if (change !== 'setup') menu.hidePopover();
      if (this.live(context, this.lifetime.signal)) {
        void this.readProjects(context);
        void this.refresh();
      }
    } catch (error) {
      if (signal.aborted) return;
      this.menuNote = automationFailure(error);
      await this.readMenuAutomation(target, signal);
    } finally {
      if (!signal.aborted) this.menuBusy = false;
    }
  }
  /** All settings: the project's Settings tab, opening the project here first when it is another one. */
  private allSettings(context: FactoryAppContext, row: FactoryProject) {
    if (row.connection === 'current' && context.selectedRepository) return this.navigate({kind: 'settings', repository: context.selectedRepository});
    this.settingsAfterOpen = row.key;
    void this.openProject(context, row);
  }
  private placeMenu(menu: HTMLElement) {
    if (this.menuAt) this.placePopover(menu, this.menuAt.x, this.menuAt.y);
  }
  /** Opens a menu at (x, y) inside the window, flipping up near the bottom, and focuses its first item. */
  private placePopover(menu: HTMLElement, x: number, y: number, focus = true) {
    const box = menu.getBoundingClientRect(),
      gap = 8;
    menu.style.left = Math.max(gap, Math.min(x, innerWidth - box.width - gap)) + 'px';
    menu.style.top = (y + box.height + gap > innerHeight ? Math.max(gap, y - box.height) : y) + 'px';
    if (focus) this.menuItems(menu)[0]?.focus();
  }
  /** A menu's own items, not those of a side menu inside it. */
  private menuItems(menu: HTMLElement) {
    return [...menu.querySelectorAll<HTMLElement>('[role^="menuitem"]')].filter((item) => item.closest('[role="menu"]') === menu && !item.matches(':disabled'));
  }
  /** Opens a side menu beside its item, on the left when the right has no room. */
  private async openSubmenu(item: HTMLElement, which: AutomationRole | 'merge') {
    this.submenu = which;
    await this.updateComplete;
    const sub = this.querySelector<HTMLElement>('.soda-factory-submenu');
    if (!sub) return;
    if (!sub.matches(':popover-open')) sub.showPopover();
    const box = item.getBoundingClientRect(),
      width = sub.getBoundingClientRect().width;
    this.placePopover(sub, box.right + width + 8 > innerWidth ? box.left - width : box.right, box.top - 6);
  }
  private menuKey = (event: KeyboardEvent) => {
    const item = (event.target as HTMLElement).closest<HTMLElement>('[role^="menuitem"]');
    const menu = item?.closest<HTMLElement>('[role="menu"]');
    if (!item || !menu) return;
    if (event.key === 'ArrowRight' && item.dataset.submenu) {
      event.preventDefault();
      return void this.openSubmenu(item, item.dataset.submenu as AutomationRole | 'merge');
    }
    if (event.key === 'ArrowLeft' && menu.classList.contains('soda-factory-submenu')) {
      event.preventDefault();
      return menu.hidePopover();
    }
    const items = this.menuItems(menu);
    const at = items.indexOf(item);
    const next = {ArrowDown: at + 1, ArrowUp: at - 1, Home: 0, End: items.length - 1}[event.key];
    if (next === undefined || !items.length) return;
    event.preventDefault();
    items[(next + items.length) % items.length]?.focus();
  };
  /** The side menu for a role's login or the merge method, inside the project menu. */
  private submenuFor(menu: () => HTMLElement, state: AutomationState) {
    const which = this.submenu;
    if (!which) return '';
    const choices = which === 'merge'
      ? state.mergeChoices.map((choice) => ({value: choice.value, label: choice.label, change: {merge: choice.value} as AutomationChange}))
      : state.loginChoices[which].map((login) => ({value: login.connection, label: login.label, change: {role: which, login: login.connection} as AutomationChange}));
    const selected = which === 'merge' ? state.merge : state.logins[which];
    const label = which === 'merge' ? 'Merge as' : automationRoles[which] + ' login';
    return html`<div
      class="soda-factory-context-menu soda-factory-submenu"
      popover="auto"
      role="menu"
      aria-label=${label}
      @toggle=${(event: ToggleEvent) => {
        if (event.newState !== 'closed') return;
        const sub = event.currentTarget as HTMLElement;
        const back = this.querySelector<HTMLElement>(`[data-submenu="${which}"]`);
        if (this.submenu === which) this.submenu = undefined;
        if (back && (document.activeElement === document.body || sub.contains(document.activeElement))) back.focus();
      }}
    >
      ${choices.length
        ? choices.map((choice) => html`<button type="button" role="menuitemradio" aria-checked=${choice.value === selected ? 'true' : 'false'}
            @click=${() => void this.changeFromMenu(menu(), choice.change)}><span class="soda-factory-menu-check" aria-hidden="true"></span>${choice.label}</button>`)
        : html`<p>${which === 'merge' ? 'No merge method is available.' : 'Sign in to Muse on this computer to choose a login.'}</p>`}
    </div>`;
  }
  /** A project's settings items. Every project shows the same ones, at once:
   * they fill in when its settings are read, and say why when they can't be used. */
  private automationItems(context: FactoryAppContext, row: FactoryProject, state: SodaFactoryApp['menuAutomation'], more: boolean) {
    if (state === undefined) return '';
    const ready = typeof state === 'object' ? state : undefined;
    const usable = !!ready && ready.editable && !ready.needsSetup && !this.menuBusy;
    const menu = () => this.querySelector<HTMLElement>('#soda-factory-project-menu')!;
    const note = this.menuNote
      || (state === 'elsewhere' ? 'Open this project to change its settings.'
        : state === 'failed' ? 'Its settings can’t be read right now.'
          : ready?.needsSetup ? ready.setupBlocked
            : ready?.blocked && !ready.on ? ready.blocked.text : '');
    const check = (label: string, on: boolean, change: AutomationChange) =>
      html`<button type="button" role="menuitemcheckbox" aria-checked=${on ? 'true' : 'false'} ?disabled=${!usable}
        @click=${() => void this.changeFromMenu(menu(), change)}><span class="soda-factory-menu-check" aria-hidden="true"></span>${label}</button>`;
    const value = (text: string) => (state === 'loading' ? '…' : ready && !ready.needsSetup ? text || 'None' : '—');
    const choice = (which: AutomationRole | 'merge', label: string, text: string) =>
      html`<button type="button" role="menuitem" aria-haspopup="menu" aria-expanded=${this.submenu === which ? 'true' : 'false'} data-submenu=${which}
        ?disabled=${!usable} @click=${(event: Event) => void this.openSubmenu(event.currentTarget as HTMLElement, which)}>
        <span class="soda-factory-menu-check" aria-hidden="true"></span>${label}<span class="soda-factory-menu-value">${value(text)} ›</span></button>`;
    const loginLabel = (role: AutomationRole) => ready?.loginChoices[role].find((login) => login.connection === ready.logins[role])?.label ?? ready?.logins[role] ?? '';
    const mergeLabel = ready?.mergeChoices.find((choice) => choice.value === ready.merge)?.label ?? ready?.merge ?? '';
    return html`${ready?.needsSetup
        ? html`<button type="button" role="menuitem" ?disabled=${!!ready.setupBlocked || this.menuBusy}
            @click=${() => void this.changeFromMenu(menu(), 'setup')}><span class="soda-factory-menu-check" aria-hidden="true"></span>Set up this project</button>`
        : check('Automation', !!ready?.on, {on: !ready?.on})}
      ${note ? html`<p role=${this.menuNote ? 'alert' : 'note'} data-tone="warning">${note}</p>` : ''}
      ${check('Pick up pull requests', !!ready?.pickUpPulls, {pickUpPulls: !ready?.pickUpPulls})}
      <div role="separator"></div>
      ${choice('soda-coder', 'Coding login', loginLabel('soda-coder'))}
      ${choice('soda-reviewer', 'Review login', loginLabel('soda-reviewer'))}
      ${choice('merge', 'Merge as', mergeLabel)}
      ${ready ? this.submenuFor(menu, ready) : ''}
      <div role="separator"></div>
      ${hostResolves({kind: 'settings', repository: row.key}, context.destinations)
        ? html`<button type="button" role="menuitem" @click=${(event: Event) => {
            (event.currentTarget as HTMLElement).closest<HTMLElement>('[popover]')!.hidePopover();
            this.allSettings(context, row);
          }}><span class="soda-factory-menu-check" aria-hidden="true"></span>All settings…</button>`
        : ''}
      ${more ? html`<div role="separator"></div>` : ''}`;
  }
  private projectMenu(context: FactoryAppContext) {
    const port = context.projects;
    const row = this.menuAt && this.projectRows?.find((candidate) => candidate.key === this.menuAt!.key);
    if (!port || !row) return '';
    const actions = this.projectActions(port, row);
    const confirming = this.confirmRemove === row.key;
    const close = (event: Event) => (event.currentTarget as HTMLElement).closest<HTMLElement>('.soda-factory-context-menu:not(.soda-factory-submenu)')!.hidePopover();
    const confirm = async (event: Event) => {
      const menu = (event.currentTarget as HTMLElement).closest<HTMLElement>('[popover]')!;
      this.confirmRemove = row.key;
      await this.updateComplete;
      this.placeMenu(menu);
    };
    const item = (label: string, run: () => Promise<void>, failure: string, tone = '') =>
      html`<button type="button" role="menuitem" data-tone=${tone} @click=${(event: Event) => {
        close(event);
        void run().catch(() => (this.message = failure)).then(() => { if (this.live(context, this.lifetime.signal)) void this.readProjects(context); });
      }}><span class="soda-factory-menu-check" aria-hidden="true"></span>${label}</button>`;
    return html`<div
      id="soda-factory-project-menu"
      class="soda-factory-context-menu"
      popover="auto"
      role="menu"
      aria-label=${row.name}
      @keydown=${this.menuKey}
      @toggle=${(event: ToggleEvent) => {
        const menu = event.currentTarget as HTMLElement;
        if (event.newState !== 'closed' || menu.matches(':popover-open')) return;
        const focus = document.activeElement;
        this.menuReads.abort();
        this.menuAt = this.menuAutomation = this.submenu = undefined;
        this.confirmRemove = this.menuNote = '';
        if (!focus || focus === document.body || menu.contains(focus)) this.menuFrom?.focus();
      }}
    >
      ${confirming
        ? html`<p>Remove <strong>${row.name}</strong> from your projects? It stays on GitHub, and opening it again brings it back.</p>
            <button type="button" role="menuitem" data-tone="danger" @click=${(event: Event) => { close(event); void this.removeProject(context, row); }}>Remove</button>
            <button type="button" role="menuitem" @click=${close}>Cancel</button>`
        : html`${actions.open ? html`<button type="button" role="menuitem" @click=${(event: Event) => { close(event); void this.openProject(context, row); }}><span class="soda-factory-menu-check" aria-hidden="true"></span>Open feed</button>` : ''}
            ${actions.window ? item('Open in new window', () => port.open(row.key, 'window'), 'Could not open a new window.') : ''}
            ${actions.open || actions.window ? html`<div role="separator"></div>` : ''}
            ${actions.settings ? this.automationItems(context, row, this.menuAutomation, actions.reconnect || actions.disconnect || actions.remove) : ''}
            ${actions.reconnect ? item('Reconnect', () => port.reconnect!(row.key), 'Could not reconnect this project.') : ''}
            ${actions.disconnect ? item('Disconnect', () => port.disconnect!(row.key), 'Could not disconnect this project.') : ''}
            ${actions.remove ? html`<button type="button" role="menuitem" data-tone="danger" @click=${confirm}><span class="soda-factory-menu-check" aria-hidden="true"></span>Remove from list…</button>` : ''}`}
    </div>`;
  }
  /** Shows or hides the sidebar: beside the page in a wide window, over it in a narrow one. */
  private async toggleSidebar() {
    if (this.narrow) this.overlay = !this.overlay;
    else {
      this.collapsed = !this.collapsed;
      this.saveSidebar();
    }
    await this.updateComplete;
    this.querySelector<HTMLElement>(this.sidebarShown ? '.soda-factory-sidebar .soda-factory-sidebar-toggle' : '.soda-factory-content > header .soda-factory-sidebar-toggle')?.focus();
  }
  private get sidebarShown() {
    return this.narrow ? this.overlay : !this.collapsed;
  }
  private saveSidebar() {
    try {
      localStorage.setItem(sidebarKey, JSON.stringify({width: this.sidebarWidth, collapsed: this.collapsed}));
    } catch {
      // The choice lasts for this window only.
    }
  }
  private setSidebarWidth(width: number, edge: HTMLElement) {
    this.sidebarWidth = clampSidebar(width);
    this.style.setProperty('--soda-factory-sidebar-width', this.sidebarWidth + 'px');
    edge.setAttribute('aria-valuenow', String(this.sidebarWidth));
  }
  /** Dragging the sidebar's edge resizes it; dragging it nearly shut hides it. */
  private startResize = (event: PointerEvent) => {
    if (event.button !== 0) return;
    const edge = event.currentTarget as HTMLElement,
      start = event.clientX,
      from = this.sidebarWidth;
    event.preventDefault();
    edge.setPointerCapture(event.pointerId);
    edge.dataset.dragging = '';
    const move = (moved: PointerEvent) => {
      const width = from + moved.clientX - start;
      if (width >= sidebarWidth.collapse) return this.setSidebarWidth(width, edge);
      end();
      this.setSidebarWidth(from, edge);
      void this.toggleSidebar();
    };
    const end = () => {
      edge.removeEventListener('pointermove', move);
      edge.removeEventListener('pointerup', end);
      edge.removeEventListener('pointercancel', end);
      delete edge.dataset.dragging;
      this.saveSidebar();
    };
    edge.addEventListener('pointermove', move);
    edge.addEventListener('pointerup', end);
    edge.addEventListener('pointercancel', end);
  };
  private resizeKey = (event: KeyboardEvent) => {
    const width = {ArrowLeft: this.sidebarWidth - 16, ArrowRight: this.sidebarWidth + 16, Home: sidebarWidth.min, End: sidebarWidth.max}[event.key];
    if (width === undefined) return;
    event.preventDefault();
    this.setSidebarWidth(width, event.currentTarget as HTMLElement);
    this.saveSidebar();
  };
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
      if (this.addRepository && this.screen instanceof SodaFactoryProjects) void this.screen.focusAdd();
      this.addRepository = false;
      this.lastRead = Date.now();
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
    this.overlay = false;
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
      ...(port && context.destinations?.includes('projects') ? [{group: 'Actions', label: 'Open a GitHub repository', meta: 'action', run: () => this.openRepository()}] : []),
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
            ...(context?.destinations?.includes('settings') ? [{label: 'Settings', route: {kind: 'settings' as const, repository}}] : []),
          ]
        : []),
    ];
    // A page inside a tab keeps that tab highlighted and offers one way back.
    const parent = context ? parentTab(context.route) : undefined;
    const back = repository && parent ? links.find((link) => link.route.kind === parent) : undefined;
    const project = context?.repositoryLabel?.trim();
    const port = context?.projects;
    const listed = port ? this.projectRows ?? [] : [];
    // The sidebar foot holds what belongs to the app, not to one project: sign-ins and appearance.
    const foot: {label: string; route: FactoryRoute}[] = context?.destinations?.includes('accounts') ? [{label: 'App settings', route: {kind: 'accounts'}}] : [];
    const shown = this.sidebarShown;
    const toggle = html`<button
      type="button"
      class="soda-factory-sidebar-toggle"
      aria-label=${shown ? 'Hide sidebar' : 'Show sidebar'}
      title=${(shown ? 'Hide sidebar' : 'Show sidebar') + (/Mac|iPhone|iPad/.test(navigator.platform) ? ' (⌘\\)' : ' (Ctrl \\)')}
      aria-expanded=${shown ? 'true' : 'false'}
      aria-controls="soda-factory-sidebar"
      aria-keyshortcuts="Meta+Backslash Control+Backslash"
      @click=${() => void this.toggleSidebar()}
    >
      ${sidebarIcon}
    </button>`;
    return html`<div class="soda-factory-root soda-factory-app" data-factory-scheme=${this.scheme ?? ''} data-sidebar=${shown ? 'open' : 'closed'}>
      ${this.narrow && shown ? html`<div class="soda-factory-scrim" @click=${() => void this.toggleSidebar()}></div>` : ''}
      <aside class="soda-factory-sidebar" id="soda-factory-sidebar" ?hidden=${!shown}>
        <div class="soda-factory-sidebar-top">${renderFactoryWordmark()}${toggle}</div>
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
                <button
                  type="button"
                  class="soda-factory-projects-new"
                  aria-label="Add a project"
                  title="Add a project"
                  aria-haspopup="menu"
                  popovertarget="soda-factory-add-menu"
                  aria-current=${context.route.kind === 'project-new' ? 'page' : 'false'}
                >
                  <svg viewBox="0 0 16 16" aria-hidden="true"><path d="M8 3v10M3 8h10" /></svg>
                </button>
                <div
                  id="soda-factory-add-menu"
                  class="soda-factory-context-menu"
                  popover="auto"
                  role="menu"
                  aria-label="Add a project"
                  @keydown=${this.menuKey}
                  @toggle=${(event: ToggleEvent) => {
                    if (event.newState !== 'open') return;
                    const plus = this.querySelector<HTMLElement>('.soda-factory-projects-new')!.getBoundingClientRect();
                    this.placePopover(event.currentTarget as HTMLElement, plus.left, plus.bottom + 4);
                  }}
                >
                  <button type="button" role="menuitem" @click=${(event: Event) => { (event.currentTarget as HTMLElement).closest<HTMLElement>('[popover]')!.hidePopover(); this.openRepository(); }}>
                    Open a GitHub repository
                  </button>
                  ${port.create && context.destinations.includes('project-new')
                    ? html`<button type="button" role="menuitem" @click=${(event: Event) => { (event.currentTarget as HTMLElement).closest<HTMLElement>('[popover]')!.hidePopover(); this.navigate({kind: 'project-new'}); }}>
                        Create a new repository
                      </button>`
                    : ''}
                </div>
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
                      return html`<button
                          type="button"
                          class="soda-factory-project-row"
                          aria-current=${here ? 'true' : 'false'}
                          aria-haspopup=${this.hasProjectMenu(port, row) ? 'menu' : 'false'}
                          @contextmenu=${(event: MouseEvent) => void this.openProjectMenu(event, port, row, event.clientX, event.clientY)}
                          @keydown=${(event: KeyboardEvent) => this.projectMenuKey(event, port, row)}
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
                        </button>`;
                    })}
                  </nav>`
                : ''}
              ${this.projectMenu(context)}
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
        ${this.narrow
          ? ''
          : html`<div
              class="soda-factory-sidebar-edge"
              role="separator"
              tabindex="0"
              aria-orientation="vertical"
              aria-label="Resize sidebar"
              aria-controls="soda-factory-sidebar"
              aria-valuemin=${sidebarWidth.min}
              aria-valuemax=${sidebarWidth.max}
              aria-valuenow=${this.sidebarWidth}
              title="Drag to resize. Double-click to reset."
              @pointerdown=${this.startResize}
              @keydown=${this.resizeKey}
              @dblclick=${(event: Event) => {
                this.setSidebarWidth(sidebarWidth.initial, event.currentTarget as HTMLElement);
                this.saveSidebar();
              }}
            ></div>`}
      </aside>
      <div class="soda-factory-content">
        <!-- The open project's name and its pages run across the top; the status sits at the end. -->
        <header ?data-empty=${shown && !repository && !this.message.trim()}>
          ${shown ? '' : toggle}
          ${repository ? html`<div class="soda-factory-project">
            ${project ? html`<p class="soda-factory-project-name" title=${project}>${project}</p>` : ''}
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
