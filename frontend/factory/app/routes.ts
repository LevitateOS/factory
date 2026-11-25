import {check, id} from '../api/models.js';
import {decisionID} from '../api/request-response.js';
import {factoryRunID} from '../api/run-response.js';
import {libraryID} from '../api/library-response.js';
import type {PullTab} from '../pulls/detail.js';

/** Canonical product destinations. Hosts own URLs, history and account selection. */
export type FactoryRoute =
  | {readonly kind: 'feed'; readonly repository?: string; readonly post?: string}
  | {readonly kind: 'work'; readonly repository: string; readonly intent?: string}
  | {readonly kind: 'issues'; readonly repository: string}
  | {readonly kind: 'pulls'; readonly repository: string}
  | {readonly kind: 'pull'; readonly repository: string; readonly pull: string; readonly tab?: PullTab}
  | {readonly kind: 'issue-new'; readonly repository: string}
  | {readonly kind: 'issue'; readonly repository: string; readonly issue: string; readonly fragment?: string}
  | {readonly kind: 'work-detail'; readonly repository: string; readonly issue: string}
  | {readonly kind: 'plan'; readonly repository: string; readonly issues: readonly string[]}
  | {readonly kind: 'library'; readonly repository: string; readonly item?: string}
  | {
      readonly kind: 'watch';
      readonly repository: string;
      readonly run: string;
      readonly role: string;
      readonly issue?: string;
      readonly attempt?: string;
    }
  | {readonly kind: 'settings'; readonly repository: string}
  | {readonly kind: 'accounts'}
  | {readonly kind: 'projects'};

/** Destinations a host resolves only when it declares them in
 * `FactoryAppContext.destinations`; the frame offers no entry otherwise. */
export type FactoryHostDestination = 'settings' | 'accounts' | 'projects';
const hostDestinations: readonly string[] = ['settings', 'accounts', 'projects'] satisfies FactoryHostDestination[];

export function admitHostDestinations(value: unknown): void {
  check(
    value === undefined ||
      (Array.isArray(value) &&
        value.length <= hostDestinations.length &&
        new Set(value).size === value.length &&
        value.every((kind) => hostDestinations.includes(kind)))
  );
}

/** Whether a host that declared `destinations` can resolve this route. */
export function hostResolves(route: FactoryRoute, destinations: readonly FactoryHostDestination[] | undefined): boolean {
  return !hostDestinations.includes(route.kind) || !!destinations?.includes(route.kind as FactoryHostDestination);
}

export function admitFactoryRoute(route: FactoryRoute): void {
  check(route && typeof route === 'object');
  switch (route.kind) {
    case 'feed':
      check(
        (route.repository === undefined || id(route.repository)) && (route.post === undefined || libraryID(route.post))
      );
      check(route.post === undefined || route.repository !== undefined);
      return;
    case 'work':
      check(id(route.repository) && (route.intent === undefined || decisionID(route.intent)));
      return;
    case 'issues':
    case 'pulls':
    case 'issue-new':
    case 'settings':
      check(id(route.repository));
      return;
    case 'accounts':
    case 'projects':
      return;
    case 'issue':
      check(
        route.fragment === undefined ||
          (typeof route.fragment === 'string' &&
            route.fragment.length > 0 &&
            route.fragment.length <= 4096 &&
            !/[\\\u0000-\u001f\u007f]/.test(route.fragment))
      );
      check(id(route.repository) && id(route.issue));
      return;
    case 'pull':
      check(id(route.repository) && id(route.pull) &&
        (route.tab === undefined || ['conversation', 'commits', 'reviews', 'threads', 'files', 'checks'].includes(route.tab)));
      return;
    case 'work-detail':
      check(id(route.repository) && id(route.issue));
      return;
    case 'plan':
      check(id(route.repository) && Array.isArray(route.issues) && route.issues.length <= 64 && route.issues.every(id));
      return;
    case 'library':
      check(id(route.repository) && (route.item === undefined || libraryID(route.item)));
      return;
    case 'watch':
      check(id(route.repository) && factoryRunID(route.run) && /^[a-z][a-z0-9-]{0,63}$/.test(route.role));
      check(route.issue === undefined || id(route.issue));
      check(route.attempt === undefined || /^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$/.test(route.attempt));
      return;
    default:
      throw Error('Unknown Factory destination');
  }
}

export function factoryRouteTitle(route: FactoryRoute): string {
  return {
    feed: 'Feed',
    work: 'Work',
    issues: 'Issues',
    pulls: 'Pull requests',
    pull: 'Pull request',
    'issue-new': 'New issue',
    issue: 'Issue',
    'work-detail': 'Work detail',
    plan: 'Work plan',
    library: 'Knowledge',
    watch: 'Watch',
    settings: 'Settings',
    accounts: 'Accounts',
    projects: 'Projects',
  }[route.kind];
}
