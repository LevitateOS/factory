import {check, id} from '../api/models.js';
import {decisionID} from '../api/request-response.js';
import {factoryRunID} from '../api/run-response.js';
import {libraryID} from '../api/library-response.js';

/** Canonical product destinations. Hosts own URLs, history and account selection. */
export type FactoryRoute =
  | {readonly kind: 'feed'; readonly repository?: string; readonly post?: string}
  | {readonly kind: 'request'; readonly intent?: string}
  | {readonly kind: 'work'; readonly repository: string; readonly intent?: string}
  | {readonly kind: 'issues'; readonly repository: string}
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
    };

export function admitFactoryRoute(route: FactoryRoute): void {
  check(route && typeof route === 'object');
  switch (route.kind) {
    case 'feed':
      check(
        (route.repository === undefined || id(route.repository)) && (route.post === undefined || libraryID(route.post))
      );
      check(route.post === undefined || route.repository !== undefined);
      return;
    case 'request':
      check(route.intent === undefined || decisionID(route.intent));
      return;
    case 'work':
      check(id(route.repository) && (route.intent === undefined || decisionID(route.intent)));
      return;
    case 'issues':
    case 'issue-new':
      check(id(route.repository));
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
    request: 'Request',
    work: 'Work',
    issues: 'Issues',
    'issue-new': 'New issue',
    issue: 'Issue',
    'work-detail': 'Work detail',
    plan: 'Work plan',
    library: 'Knowledge',
    watch: 'Watch',
  }[route.kind];
}
