import {LitElement, html} from 'lit';
import {check, id, object} from '../api/models.js';
import type {FactoryTransport} from '../api/transport.js';
import {SodaFactoryFeed} from '../feed/feed.js';
import type {FactoryFeedContext} from '../feed/feed.js';
import {SodaIntent} from '../request/request.js';
import type {IntentAPI} from '../request/request.js';
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
import {admitFactoryRoute, factoryRouteTitle} from './routes.js';
import type {FactoryRoute} from './routes.js';

/** Host-resolved ports, never host-created DOM or a second workflow model. */
export type FactoryScreen =
  | {readonly kind: 'feed'; readonly context: FactoryFeedContext}
  | {readonly kind: 'request'; readonly api: IntentAPI; readonly blocked: boolean}
  | {readonly kind: 'work'; readonly context: WorkOverviewContext}
  | {readonly kind: 'issues'; readonly context: Omit<IssueListContext, 'openIssue'>}
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
    };

export interface FactoryAppContext {
  readonly transport: FactoryTransport;
  /** Host-admitted stable profile/provider/account reference; never a credential. */
  readonly scope: string;
  readonly host: string;
  readonly actor: string;
  readonly accountLabel: string;
  /** Host-selected workspace, retained independently of the current destination. */
  readonly selectedRepository?: string;
  readonly route: FactoryRoute;
  resolve(route: FactoryRoute, signal: AbortSignal): Promise<FactoryScreen>;
  navigate(route: FactoryRoute): void;
}

/** Production application composition shared by desktop and host integrations.
 * It creates no connection, backend, provider or executor. Hosts own admission. */
export class SodaFactoryApp extends LitElement {
  static properties = {message: {state: true}, busy: {state: true}, screen: {state: true}, scheme: {type: String}};
  declare scheme: 'light' | 'dark' | undefined;
  declare private message: string;
  declare private busy: boolean;
  declare private screen: HTMLElement | undefined;
  private current: FactoryAppContext | undefined;
  private generation = '';
  private lifetime = new AbortController();
  private disposeScreen: (() => void) | undefined;

  constructor() {
    super();
    this.message = 'Choose an account to open Factory.';
    this.busy = false;
    this.screen = undefined;
    this.scheme = undefined;
    this.addEventListener('library-source-open', this.openLibrarySource);
  }
  protected createRenderRoot() {
    return this;
  }
  set context(context: FactoryAppContext | undefined) {
    if (context === this.current && context?.transport.generation === this.generation) return;
    this.retire();
    if (!context) return;
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
      check(context.selectedRepository === undefined || id(context.selectedRepository));
      if (context.selectedRepository !== undefined && 'repository' in context.route && context.route.repository !== undefined)
        check(context.selectedRepository === context.route.repository);
      if (context.route.kind === 'feed' && !context.route.repository && context.selectedRepository)
        context = {...context, route: {...context.route, repository: context.selectedRepository}};
      this.current = context;
      this.generation = context.transport.generation;
      void this.load(context);
    } catch {
      this.message = 'The Factory account or destination is unavailable.';
    }
  }
  disconnectedCallback() {
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
  private clearScreen() {
    const dispose = this.disposeScreen;
    this.disposeScreen = undefined;
    this.screen = undefined;
    dispose?.();
  }
  private own<T extends HTMLElement>(view: T, retire: () => void): T {
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
        if (this.live(context, signal)) this.message = 'Could not open this view. Refresh to try again.';
      }
    } finally {
      if (this.live(context, signal)) this.busy = false;
    }
  }
  private mount(context: FactoryAppContext, ports: FactoryScreen, signal: AbortSignal): HTMLElement {
    const route = context.route;
    if (ports.kind !== 'request') check(ports.context.transport === context.transport);
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
        return this.own(screen.element, screen.dispose);
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
      case 'request': {
        check(route.kind === 'request' && typeof ports.api === 'function' && typeof ports.blocked === 'boolean');
        const view = new SodaIntent();
        this.own(view, () => {
          view.active = false;
          view.api = undefined;
        });
        view.actor = context.actor;
        view.draftScope = context.scope;
        view.request = route.intent ?? '';
        view.blocked = ports.blocked;
        view.api = async (path, body, childSignal) => {
          check(this.live(context, signal));
          const joined = childSignal ? AbortSignal.any([signal, childSignal]) : signal;
          joined.throwIfAborted();
          const value = await ports.api(path, body, joined);
          check(this.live(context, signal));
          joined.throwIfAborted();
          return value;
        };
        view.active = true;
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
  protected render() {
    const context = this.current;
    const repository = context?.selectedRepository ?? (context && 'repository' in context.route ? context.route.repository : undefined);
    const links: {label: string; route: FactoryRoute}[] = [
      {label: 'Feed', route: {kind: 'feed', ...(repository ? {repository} : {})}},
      {label: 'New request', route: {kind: 'request'}},
      ...(repository
        ? [
            {label: 'Work', route: {kind: 'work' as const, repository}},
            {label: 'Issues', route: {kind: 'issues' as const, repository}},
            {label: 'Pull requests', route: {kind: 'pulls' as const, repository}},
            {label: 'New issue', route: {kind: 'issue-new' as const, repository}},
            {label: 'Knowledge', route: {kind: 'library' as const, repository}},
          ]
        : []),
    ];
    return html`<div class="soda-factory-root soda-factory-app" data-factory-scheme=${this.scheme ?? ''}>
      <header>
        <h1>Factory</h1>
        ${context ? html`<p>Acting as ${context.accountLabel}</p>` : ''}
      </header>
      <nav aria-label="Factory">
        ${links.map(
          (link) => html`<button
            type="button"
            aria-current=${context?.route.kind === link.route.kind || (link.route.kind === 'issues' && context?.route.kind === 'issue') || (link.route.kind === 'pulls' && context?.route.kind === 'pull') || (link.route.kind === 'work' && (context?.route.kind === 'work-detail' || context?.route.kind === 'plan')) ? 'page' : 'false'}
            ?disabled=${!context}
            @click=${() => this.navigate(link.route)}
          >
            ${link.label}
          </button>`
        )}
      </nav>
      <main
        aria-label=${context ? factoryRouteTitle(context.route) : 'Factory'}
        aria-busy=${this.busy ? 'true' : 'false'}
      >
        <p role="status">${this.message}</p>
        ${this.screen ?? ''}
        ${context && !this.screen && !this.busy ? html`<button type="button" @click=${() => void this.load(context)}>Refresh view</button>` : ''}
      </main>
    </div>`;
  }
}
customElements.define('soda-factory-app', SodaFactoryApp);
