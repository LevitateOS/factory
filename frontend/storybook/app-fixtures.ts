import type {FactoryAppContext, SodaFactoryApp} from '../factory/app/factory-app.js';
import type {FactoryRoute} from '../factory/app/routes.js';
import type {FactoryTransport} from '../factory/api/transport.js';
import {feedStoryPeer} from './fixtures.js';
import {libraryStoryPeer} from './library-fixtures.js';
import {progressStoryPeer} from './progress-fixtures.js';
import {detailStoryPeer} from './detail-fixtures.js';
import {watchStoryPeer, watchRenderer, watchRun} from './watch-fixtures.js';
import {issuesStoryPeer} from './issues-fixtures.js';
import {issueDetailStoryPeer} from './issue-detail-fixtures.js';
import {journeyResponse} from './journey-fixtures.js';
import {issueCreateStoryPeer} from './issue-create-fixtures.js';
import {PlacementController, placementView} from '../desktop/placement.js';
import {signInStoryPlacement} from './sign-in-fixtures.js';
import {projectStoryPort} from './projects-fixtures.js';

export type AppStoryState = 'ready' | 'loading' | 'unavailable' | 'disconnected' | 'issues' | 'issue' | 'issue-new' | 'settings' | 'accounts' | 'projects' | 'feed-projects' | 'jump';
export const appRepository = '7';

/** Composition peer only. All services/renderers are existing declared story
 * peers; it creates no account, backend, provider, worker or execution grant. */
export function appStoryPeer(state: AppStoryState = 'ready', account = 'Alex') {
  let route: FactoryRoute =
    state === 'issues'
      ? {kind: 'issues', repository: appRepository}
      : state === 'issue'
        ? {kind: 'issue', repository: appRepository, issue: '12'}
        : state === 'issue-new'
          ? {kind: 'issue-new', repository: appRepository}
          : state === 'settings'
            ? {kind: 'settings', repository: appRepository}
            : state === 'accounts'
              ? {kind: 'accounts'}
              : state === 'projects'
                ? {kind: 'projects'}
                : {kind: 'feed', repository: appRepository};
  let view: SodaFactoryApp | undefined;
  const withProjects = state === 'projects' || state === 'feed-projects' || state === 'jump';
  const projects = projectStoryPort(() => view);
  let unavailable = state === 'unavailable',
    held = state === 'loading',
    generation = 'declared-app:' + account;
  let bindingMismatch = '';
  let retireOnRead = false,
    preConnectSignal: AbortSignal | null | undefined;
  const gates: {signal: AbortSignal; release: () => void}[] = [];
  const resolved: FactoryRoute[] = [];
  const calls: {path: string; method: string; signal: AbortSignal | null | undefined}[] = [];
  const scope = 'storybook:factory-app:' + account;
  const issueList = issuesStoryPeer('ready', appRepository),
    issueDetails = new Map<string, ReturnType<typeof issueDetailStoryPeer>>();
  const issuePeer = (number = route.kind === 'issue' ? route.issue : '12') => {
    let peer = issueDetails.get(number);
    if (!peer) {
      peer = issueDetailStoryPeer('ready', appRepository, number);
      issueDetails.set(number, peer);
    }
    return peer;
  };
  let heldFacts = false,
    factsMode = 'recorded';
  const factsGates: (() => void)[] = [];
  const feed = feedStoryPeer('activity'),
    library = libraryStoryPeer('recorded', appRepository);
  const progress = progressStoryPeer('ready', appRepository),
    detail = detailStoryPeer('read-only', appRepository);
  const watch = watchStoryPeer('live', appRepository);
  // Role settings with a saved policy, plus the desktop execution controller's
  // reads, for the Settings route.
  const settingsRoles = progressStoryPeer('ready', appRepository, true);
  const settingsRequest: FactoryTransport['request'] = async (path, init) => {
    if (path === 'repositories/' + appRepository + '/factory/placement')
      return Response.json({
        runtime: 'e'.repeat(32),
        can_select: true,
        placement: {repository: appRepository, runtime: 'e'.repeat(32), generation: '1', state: 'active', revision: 'f'.repeat(32)},
      });
    if (path === 'repositories/' + appRepository + '/factory/environment-grant') return Response.json({environment: null});
    // The controller reads without an explicit method; the role peer requires one.
    const response = await settingsRoles.overview.transport.request(path, {...init, method: init?.method ?? 'GET'});
    if (path !== 'repositories/' + appRepository + '/factory') return response;
    return Response.json({
      ...(await response.json()),
      capacity: {revision: 1, max_concurrent_runs: 2},
      operator_grant: {repository: appRepository, revision: 1, active: true},
    });
  };
  const transport: FactoryTransport = {
    get generation() {
      return generation;
    },
    assetBase: '/',
    async request(path, init) {
      calls.push({path, method: init?.method ?? 'GET', signal: init?.signal});
      if (retireOnRead) {
        retireOnRead = false;
        preConnectSignal = init?.signal;
        queueMicrotask(() => {
          if (view) view.context = undefined;
        });
      }
      if (path === 'factory/feed/posts/' + 'd'.repeat(32))
        return Promise.resolve(
          Response.json({
            post_id: 'd'.repeat(32),
            repository: appRepository,
            body: 'Source for recorded Knowledge',
            author: '1',
            author_name: account,
            at: 1720000000,
          })
        );
      if (path.startsWith('factory/collaboration/')) {
        if (route.kind === 'issue-new') return issueCreateStoryPeer().transport.request(path, init);
        if (route.kind === 'issue') return issuePeer().context.transport.request(path, init);
        return issueList.context.transport.request(path, init);
      }
      if (route.kind === 'issue' && path === 'repositories/' + appRepository + '/factory/issues/' + route.issue) {
        const number = route.issue,
          value = journeyResponse();
        value.repository = appRepository;
        value.issue = number;
        value.journey.repository = appRepository;
        value.journey.issue = number;
        value.journey.tasks.forEach((task) => (task.issue = number));
        if (heldFacts) await new Promise<void>((resolve) => factsGates.push(resolve));
        if (factsMode === 'unavailable') return Response.json({error: {code: 'unavailable'}}, {status: 503});
        return Response.json(factsMode === 'empty' ? {repository: appRepository, issue: number} : value);
      }
      if (path.startsWith('factory/feed')) return feed.context.transport.request(path, init);
      if (path.startsWith('factory/library')) return library.context.transport.request(path, init);
      if (route.kind === 'settings') return settingsRequest(path, init);
      if (route.kind === 'watch') return watch.context.transport.request(path, init);
      if (route.kind === 'work-detail') return detail.context.transport.request(path, init);
      return progress.overview.transport.request(path, init);
    },
  };
  const context = (): FactoryAppContext => ({
    transport,
    actor: '1',
    host: 'a'.repeat(32),
    scope,
    accountLabel: account,
    selectedRepository: appRepository,
    repositoryLabel: 'acme/factory',
    destinations: withProjects ? ['settings', 'accounts', 'projects'] : ['settings', 'accounts'],
    ...(withProjects ? {projects: projects.port} : {}),
    route,
    async resolve(selected, signal) {
      resolved.push(selected);
      if (held) await new Promise<void>((release) => gates.push({signal, release}));
      if (unavailable) throw Error('Declared app admission unavailable');
      if (bindingMismatch === 'kind')
        return selected.kind === 'library' ? {kind: 'feed', context: feed.context} : {kind: 'library', context: library.context};
      // Do not disguise delayed, cancelled host answers as authoritative.
      const supplied = bindingMismatch === 'transport' ? {...transport} : transport;
      switch (selected.kind) {
        case 'feed':
          return {
            kind: 'feed',
            context: {
              ...feed.context,
              transport: supplied,
              global: selected.repository === undefined,
              repository: selected.repository ?? '',
              account: bindingMismatch === 'actor' ? '2' : '1',
              host: {...feed.context.host, draftScope: bindingMismatch === 'scope' ? 'foreign' : scope},
            },
          };
        case 'work': {
          const context = {...progress.overview, transport: supplied};
          delete context.intentID;
          if (selected.intent) context.intentID = selected.intent;
          return {kind: 'work', context};
        }
        case 'issues':
          return {
            kind: 'issues',
            context: {
              ...issueList.context,
              transport: supplied,
              repository: bindingMismatch === 'repository' ? '8' : appRepository,
              host: bindingMismatch === 'host' ? 'b'.repeat(32) : 'a'.repeat(32),
              actor: bindingMismatch === 'actor' ? '2' : '1',
              scope: bindingMismatch === 'scope' ? 'foreign' : scope,
            },
          };
        case 'pulls':
          return {kind: 'pulls', context: {
            ...issueList.context, transport: supplied, repository: appRepository,
            host: 'a'.repeat(32), actor: '1', scope,
          }};
        case 'pull':
          return {kind: 'pull', context: {
            ...issuePeer(selected.pull).context, transport: supplied, repository: appRepository,
            pull: selected.pull, host: 'a'.repeat(32), actor: '1', scope,
          }};
        case 'issue-new': {
          const peer = issueCreateStoryPeer();
          return {
            kind: 'issue-new',
            context: {
              ...peer,
              transport: supplied,
              repository: bindingMismatch === 'repository' ? '8' : appRepository,
              host: bindingMismatch === 'host' ? 'b'.repeat(32) : 'a'.repeat(32),
              actor: bindingMismatch === 'actor' ? '2' : '1',
              scope: bindingMismatch === 'scope' ? 'foreign' : scope,
              commandScope: async () => scope,
            },
          };
        }
        case 'issue':
          return {
            kind: 'issue',
            context: {
              ...issuePeer(selected.issue).context,
              transport: supplied,
              repository: bindingMismatch === 'repository' ? '8' : appRepository,
              issue: bindingMismatch === 'issue' ? '13' : selected.issue,
              host: bindingMismatch === 'host' ? 'b'.repeat(32) : 'a'.repeat(32),
              actor: bindingMismatch === 'actor' ? '2' : '1',
              scope: bindingMismatch === 'scope' ? 'foreign' : scope,
            },
            journey: {
              transport: bindingMismatch === 'journey-transport' ? {...transport} : supplied,
              repository: bindingMismatch === 'journey-repository' ? '8' : appRepository,
              issue: bindingMismatch === 'journey-issue' ? '13' : selected.issue,
            },
          };
        case 'work-detail':
          return {
            kind: 'work-detail',
            context: {
              ...detail.context,
              transport: supplied,
              repository: bindingMismatch === 'repository' ? '8' : appRepository,
              issue: bindingMismatch === 'issue' ? '13' : '12',
              commandScope: async (childSignal) => {
                childSignal.throwIfAborted();
                return scope;
              },
            },
          };
        case 'plan':
          return {kind: 'plan', context: {...progress.plan, transport: supplied, issues: [...selected.issues]}};
        case 'library':
          return {
            kind: 'library',
            context: {
              ...library.context,
              transport: supplied,
              repository: bindingMismatch === 'repository' ? '8' : appRepository,
            },
          };
        case 'watch':
          return {kind: 'watch', context: {...watch.context, transport: supplied}, renderer: async () => watchRenderer};
        case 'settings': {
          const placement = new PlacementController(supplied, appRepository);
          return {
            kind: 'settings',
            context: {
              transport: supplied,
              repository: appRepository,
              sections: [{id: 'controller', title: 'Execution controller', mount: () => placementView(placement)}],
              repairFundingAvailable: false,
              activityAvailable: false,
            },
          };
        }
        case 'projects':
          return {kind: 'projects', context: {transport: supplied}};
        case 'accounts': {
          // The desktop sign-in views with saved accounts, framed on the page.
          const placement = signInStoryPlacement(document.createElement('section'), 'saved');
          placement.frame(true);
          return {kind: 'accounts', context: {transport: supplied, sections: placement.sections}};
        }
      }
    },
    navigate(next) {
      route = next;
      if (view) view.context = context();
    },
  });
  return {
    attach(element: SodaFactoryApp) {
      view = element;
      view.context = state === 'disconnected' ? undefined : context();
    },
    select(next: FactoryRoute) {
      route = next;
      if (view) view.context = context();
    },
    calls,
    projectCalls: projects.calls,
    resolved,
    hold() {
      held = true;
    },
    release() {
      held = false;
      gates.splice(0).forEach((gate) => gate.release());
    },
    held: () => gates.length,
    aborted: () => gates.some((gate) => gate.signal.aborted),
    unavailable(value: boolean) {
      unavailable = value;
    },
    mismatch(value: string) {
      bindingMismatch = value;
    },
    retire() {
      if (view) view.context = undefined;
    },
    retireGeneration() {
      generation += ':retired';
    },
    retireBeforeConnect() {
      retireOnRead = true;
    },
    preConnectAborted: () => preConnectSignal?.aborted === true,
    failIssue(status: number) {
      issuePeer().fail(status, '/comments');
    },
    holdIssue() {
      issuePeer().hold('/comments');
    },
    releaseIssue() {
      issueDetails.forEach((peer) => peer.release());
    },
    issueHeld: () => [...issueDetails.values()].reduce((count, peer) => count + peer.held(), 0),
    holdFacts() {
      heldFacts = true;
    },
    releaseFacts() {
      heldFacts = false;
      factsGates.splice(0).forEach((resolve) => resolve());
    },
    factsHeld: () => factsGates.length,
    factsMode(value: string) {
      factsMode = value;
    },
    links: () => [...issueDetails.values()].flatMap((peer) => peer.opened),
    holdLink() {
      issuePeer().holdLink();
    },
    releaseLink() {
      issueDetails.forEach((peer) => peer.releaseLink());
    },
    linkAborted: () => [...issueDetails.values()].some((peer) => peer.linkAborted()),
    watchRun,
  };
}
