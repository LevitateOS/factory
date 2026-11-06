import {SodaRequestError} from '../factory/app/browser.js';
import type {FactoryAppContext, FactoryRoute, FactoryScreen} from '../factory/app/browser.js';
import type {FactoryFeedHost, FactoryRepositoryLinks} from '../factory/app/navigation.js';
import {admitFactoryRoute} from '../factory/app/routes.js';
import {check, id, object, readSodaJSON} from '../factory/api/models.js';
import type {Binding, Core} from './transport.js';
import {DesktopTransport} from './transport.js';

export function routeURL(route: FactoryRoute): string {
  const query = new URLSearchParams({view: route.kind});
  for (const [key, value] of Object.entries(route))
    if (key !== 'kind' && value !== undefined) query.set(key, Array.isArray(value) ? value.join(',') : String(value));
  return '#' + query;
}
export function readRoute(hash = location.hash): FactoryRoute | undefined {
  try {
    const query = new URLSearchParams(hash.slice(1));
    const route = Object.fromEntries([...query].filter(([key]) => key !== 'view')) as Record<string, unknown>;
    route.kind = query.get('view');
    if (typeof route.issues === 'string') route.issues = route.issues.split(',');
    admitFactoryRoute(route as unknown as FactoryRoute);
    return route as unknown as FactoryRoute;
  } catch {
    return;
  }
}

/** Only host composition lives here. Components, drafts and workflow decisions
 * retain their shared production owners. Native selection never grants execution. */
export class DesktopProduct {
  readonly transport: DesktopTransport;
  readonly scope: string;
  private lifetime = new AbortController();
  constructor(
    private core: Core,
    readonly selected: Binding,
    profile: string,
    private navigate: (route: FactoryRoute) => void,
    private defaultBranch?: string
  ) {
    this.scope = `github:${profile}:${selected.identity.host}:${selected.identity.actor}`;
    this.transport = new DesktopTransport(core, selected, profile);
  }
  retire() {
    this.lifetime.abort();
    this.transport.retire();
  }
  context(route: FactoryRoute): FactoryAppContext {
    admitFactoryRoute(route);
    if (route.kind === 'feed' && !route.repository && this.selected.repository)
      route = {...route, repository: this.selected.repository.id};
    if ('repository' in route && route.repository !== undefined)
      check(route.repository === this.selected.repository?.id);
    return {
      transport: this.transport,
      scope: this.scope,
      ...this.selected.identity,
      accountLabel: this.selected.account_label,
      route,
      resolve: (route, signal) => this.resolve(route, AbortSignal.any([this.lifetime.signal, signal])),
      navigate: (route) => {
        this.lifetime.signal.throwIfAborted();
        this.navigate(route);
      },
    };
  }
  private links(repository: string): FactoryRepositoryLinks {
    check(repository === this.selected.repository?.id);
    return {
      repository,
      feed: (post) => routeURL({kind: 'feed', repository, ...(post ? {post} : {})}),
      request: (intent) => routeURL({kind: 'request', intent}),
      issue: (issue) => (id(String(issue)) ? routeURL({kind: 'issue', repository, issue: String(issue)}) : undefined),
      pull: (pull) => id(String(pull)) ? routeURL({kind: 'pull', repository, pull: String(pull)}) : undefined,
      factoryStatus: (issue) =>
        issue
          ? routeURL({kind: 'work-detail', repository, issue: String(issue)})
          : routeURL({kind: 'work', repository}),
      knowledge: (item) => routeURL({kind: 'library', repository, ...(item ? {item} : {})}),
    };
  }
  private openLink = async (url: string, signal: AbortSignal) => {
    const joined = AbortSignal.any([signal, this.lifetime.signal]);
    joined.throwIfAborted();
    if (url.startsWith('#')) {
      // Route parsing uses a URL rather than temporarily mutating history.
      const route = readRoute(url);
      check(route);
      this.navigate(route);
    } else {
      const target = new URL(url),
        repository = this.selected.repository;
      const path = target.origin === 'https://github.com' ? target.pathname.split('/').map(decodeURIComponent) : [];
      if (
        repository &&
        target.origin === 'https://github.com' &&
        !target.username &&
        !target.password &&
        !target.search &&
        path.length === 5 &&
        path.slice(1, 3).join('/').toLowerCase() === repository.name.toLowerCase() &&
        (path[3] === 'issues' || path[3] === 'pull') &&
        id(path[4])
      ) {
        const fragment = target.hash ? decodeURIComponent(target.hash.slice(1)) : undefined;
        const route: FactoryRoute = path[3] === 'pull'
          ? {kind: 'pull', repository: repository.id, pull: path[4]}
          : {kind: 'issue', repository: repository.id, issue: path[4], ...(fragment ? {fragment} : {})};
        admitFactoryRoute(route);
        this.navigate(route);
      } else await this.core.invoke('factory_open_link', {session: this.selected.session, url});
    }
    joined.throwIfAborted();
  };
  private resolveLink(href: string, number: string, kind: 'issues' | 'pull' = 'issues'): string | undefined {
    const repository = this.selected.repository;
    if (!repository || !href || /[\\\u0000-\u001f\u007f]/.test(href)) return;
    try {
      const base = `https://github.com/${repository.name}/`;
      if (href.startsWith('#') || href.startsWith('?')) return new URL(href, base + kind + '/' + number).href;
      let url: URL;
      if (/^[a-z][a-z0-9+.-]*:/i.test(href) || href.startsWith('/')) url = new URL(href, base);
      else {
        if (!this.defaultBranch) return;
        url = new URL(href, base + 'blob/' + encodeURIComponent(this.defaultBranch) + '/');
      }
      if (!['https:', 'http:', 'mailto:'].includes(url.protocol) || url.username || url.password) return;
      return url.href;
    } catch {
      return;
    }
  }
  private async commandScope(signal: AbortSignal): Promise<string> {
    const response = await this.transport.request('/api/factory/identity', {signal});
    check(response.ok);
    const identity = object(await readSodaJSON(response));
    check(identity.host === this.selected.identity.host && identity.actor === this.selected.identity.actor);
    return this.scope;
  }
  private async resolve(route: FactoryRoute, signal: AbortSignal): Promise<FactoryScreen> {
    signal.throwIfAborted();
    admitFactoryRoute(route);
    const {transport, scope} = this,
      {host, actor} = this.selected.identity;
    const repository = this.selected.repository;
    if ('repository' in route && route.repository !== undefined) check(repository?.id === route.repository);
    const common = {transport, scope, host, actor};
    const commandScope = (signal: AbortSignal) => this.commandScope(signal);
    switch (route.kind) {
      case 'request':
        return {
          kind: 'request',
          blocked: false,
          api: async (path, body, signal) => {
            const response = await transport.request(path, {
              method: body === undefined ? 'GET' : 'POST',
              ...(body === undefined ? {} : {body: JSON.stringify(body)}),
              ...(signal ? {signal} : {}),
            });
            const value = await readSodaJSON(response);
            if (!response.ok) {
              const error = object(object(value).error);
              throw new SodaRequestError(response.status, typeof error.code === 'string' ? error.code : undefined);
            }
            return value;
          },
        };
      case 'feed': {
        const feedHost: FactoryFeedHost = {
          draftScope: scope,
          repositories: async (_wanted, signal) => {
            signal.throwIfAborted();
            return {
              refs: new Map(
                repository
                  ? [
                      [
                        repository.id,
                        {label: repository.name, path: routeURL({kind: 'feed', repository: repository.id})},
                      ],
                    ]
                  : []
              ),
              listedAll: true,
            };
          },
          links: (id) => (id === repository?.id ? this.links(id) : undefined),
          navigate: (path) => {
            void this.openLink(path, this.lifetime.signal);
          },
        };
        return {
          kind: 'feed',
          context: {
            transport,
            host: feedHost,
            account: actor,
            repository: route.repository ?? '',
            ...(repository ? {repositoryName: repository.name} : {}),
            global: route.repository === undefined,
          },
        };
      }
      case 'issues':
        return {kind: 'issues', context: {...common, repository: route.repository}};
      case 'pulls':
        return {kind: 'pulls', context: {...common, repository: route.repository}};
      case 'pull':
        return {kind: 'pull', context: {...common, repository: route.repository, pull: route.pull,
          resolveLink: (href) => this.resolveLink(href, route.pull, 'pull'), openLink: this.openLink}};
      case 'issue-new':
        return {kind: 'issue-new', context: {...common, repository: route.repository, commandScope}};
      case 'issue': {
        const address = {...common, repository: route.repository, issue: route.issue, commandScope};
        return {
          kind: 'issue',
          context: {...address, resolveLink: (href) => this.resolveLink(href, route.issue), openLink: this.openLink},
          journey: {transport, repository: route.repository, issue: route.issue, links: this.links(route.repository)},
          comment: address,
          editor: address,
        };
      }
      case 'work':
        return {
          kind: 'work',
          context: {
            transport,
            repository: route.repository,
            ...(route.intent ? {intentID: route.intent} : {}),
            links: this.links(route.repository),
          },
        };
      case 'work-detail':
        return {
          kind: 'work-detail',
          context: {
            transport,
            repository: route.repository,
            issue: route.issue,
            runIDs: [],
            commandScope,
            links: this.links(route.repository),
            capabilities: {
              pause: false,
              resume: false,
              stop: false,
              retry: false,
              takeover: false,
              project_stop: false,
            },
          },
        };
      case 'plan':
        return {
          kind: 'plan',
          context: {
            transport,
            repository: route.repository,
            issues: [...route.issues],
            links: this.links(route.repository),
          },
        };
      case 'library':
        return {kind: 'library', context: {transport, repository: route.repository}};
      case 'watch':
        throw Error('Native execution is unavailable');
    }
  }
}
