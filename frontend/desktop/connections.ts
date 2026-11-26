import type {SodaFactoryApp, SodaFactoryIssue, FactoryRoute} from '../factory/app/browser.js';
import {setUpNewProject} from '../factory/app/browser.js';
import {check, id, object} from '../factory/api/models.js';
import type {Account} from './accounts.js';
import {binding} from './transport.js';
import type {Binding, Core, NativeReply} from './transport.js';
import {DesktopProduct, readRoute, routeURL} from './product.js';
import type {SignInPlacement} from './sign-in.js';
import {DesktopProjects} from './projects.js';

interface Repository {
  external_id: string;
  name: string;
  private: boolean | null;
  default_branch: string;
}
function repository(value: unknown): Repository {
  const row = object(value);
  check(id(row.external_id) && typeof row.name === 'string' && /^[A-Za-z0-9_.-]+\/[A-Za-z0-9_.-]+$/.test(row.name));
  check(row.private === null || typeof row.private === 'boolean');
  check(
    typeof row.default_branch === 'string' &&
      row.default_branch.length <= 4096 &&
      !/[\u0000-\u001f\u007f]/.test(row.default_branch)
  );
  return {external_id: row.external_id, name: row.name, private: row.private, default_branch: row.default_branch};
}
/** `panel` holds the account and repository picker; the sign-in placement may
 * hold it outside the document, so its controls are found through it. */
export function mountConnections(core: Core, app: SodaFactoryApp, signIn: SignInPlacement, panel: HTMLElement) {
  const accounts = panel.querySelector<HTMLSelectElement>('#factory-account')!;
  const suggestions = panel.querySelector<HTMLDataListElement>('#repository-options')!;
  const status = panel.querySelector<HTMLElement>('#connection-status')!;
  const more = panel.querySelector<HTMLButtonElement>('#repository-more')!;
  const reconnect = panel.querySelector<HTMLButtonElement>('#connection-reconnect')!;
  const disconnect = panel.querySelector<HTMLButtonElement>('#connection-disconnect')!;
  const find = panel.querySelector<HTMLFormElement>('#repository-find')!;
  const name = panel.querySelector<HTMLInputElement>('#repository-name')!;
  const actions = panel.querySelector<HTMLElement>('.desktop-connections-actions')!;
  let ready = false,
    epoch = -1,
    profile = '',
    serial = 0,
    busy = false;
  let selected: Binding | undefined, pending: string | undefined, product: DesktopProduct | undefined;
  let connection = '',
    wanted = '',
    nextPage = 0;
  let fountainRepository = '';
  const fountain = () => known.find(account => account.connection === connection)?.provider === 'fountain';
  // Account/repository are user choices; selected is only the current binding.
  let resumeRequested = false;
  let accountsCurrent = false;
  // Launch opens the last project, or the only saved account, once per window;
  // a window opened for a project opens that instead.
  let autoOpened = false,
    targetRead = false;
  // A remembered project is about to open; set once the first account status arrives.
  let launching = false;
  let known: readonly Account[] = [],
    discovered: Repository[] = [];
  let message = 'Choose a saved account to connect.';
  let accountOptions = '',
    repositoryOptions = '',
    mounted = '';
  let fragmentObserver: MutationObserver | undefined;
  const live = (captured: number) => ready && serial === captured && !document.hidden;
  // Sign-in views live on the Accounts page only while the frame has a context
  // and accepts input; otherwise the desktop shows them outside the frame.
  // While a project opens or resumes, setup stays hidden behind the frame.
  const framed = () =>
    signIn.frame(mounted !== '' && !app.inert, (!!connection && (busy || resumeRequested)) || launching);
  function inert(value: boolean) {
    app.inert = value;
    framed();
  }
  const reconnectHere = () => choose(connection, wanted, fountainRepository),
    disconnectHere = () => {
      remember(undefined);
      return choose('');
    };
  // Every project action reuses this window's own connection path.
  const projects = new DesktopProjects({
    accounts: () => (accountsCurrent ? known : []),
    window: () => ({connection, wanted, repository: selected?.repository ?? undefined, connected: !!selected && !app.inert}),
    choose: (account, remote) => {
      if (account !== connection) {
        discovered = [];
        nextPage = 0;
      }
      return choose(account, remote);
    },
    openWindow: (account, remote) => core.invoke('open_window', {target: {connection: account, repository: remote}}),
    reconnect: reconnectHere,
    disconnect: disconnectHere,
    // New project: the window's GitHub account creates the repository, then
    // this window connects to it like any chosen repository.
    create: async ({name, description, visibility, useThisComputer}) => {
      const session = selected?.session;
      if (!session || busy || fountain()) throw Error('Connect a GitHub account in this window first.');
      let reply: NativeReply;
      try {
        reply = await core.invoke<NativeReply>('factory_create_repository', {session, name, description, private: visibility === 'private'});
      } catch {
        throw Error('The project could not be created. Try again.');
      }
      if (session !== selected?.session) throw Error('This window changed accounts. Check Projects before trying again.');
      if (reply.status === 409) throw Error(`GitHub refused the name “${name}”. You may already have a project with it; choose another name.`);
      if (reply.status === 202) throw Error('GitHub did not confirm the new project. Check Projects before trying again.');
      if (reply.status !== 200) throw Error('GitHub did not create the project. Try again.');
      const created = repository(object(reply.body).repository);
      discovered = [...discovered.filter((repo) => repo.external_id !== created.external_id), created];
      await choose(connection, created.external_id);
      // When the person let agents work here, record that like the Execution
      // controller buttons do, then give the project its first role settings so
      // its first post is read. Otherwise the post says what Settings needs.
      const now = product, bound = selected?.repository;
      if (useThisComputer && now?.placement && bound?.external_id === created.external_id && await now.placement.useThisComputer())
        await setUpNewProject({transport: now.transport, repository: bound.id, actor: now.selected.identity.actor}, AbortSignal.timeout(20000)).catch(() => {});
    },
    signIn: () => {
      if (product && mounted) navigate({kind: 'accounts'});
      else document.querySelector<HTMLElement>('#github-sign-in')?.focus();
    },
  });
  // The last project this window opened. Each profile has its own webview
  // storage, so this is the profile's last project.
  interface Last {
    connection: string;
    repository: string;
    enrolled: string;
  }
  const lastKey = 'factory.last-project';
  function remembered(): Last | undefined {
    try {
      const value = object(JSON.parse(localStorage.getItem(lastKey) ?? 'null'));
      check(typeof value.connection === 'string' && id(value.repository) && typeof value.enrolled === 'string');
      return {connection: value.connection, repository: value.repository, enrolled: value.enrolled};
    } catch {
      return undefined;
    }
  }
  function remember(value: Last | undefined) {
    try {
      if (value) localStorage.setItem(lastKey, JSON.stringify(value));
      else localStorage.removeItem(lastKey);
    } catch {}
  }
  launching = remembered() !== undefined;
  // A window opened for a project starts with that account and repository
  // through the same path as resuming a saved choice.
  void core.invoke<{connection: string; repository: string} | null>('factory_window_target').then((target) => {
    if (!target || connection || selected || pending) return;
    autoOpened = true;
    connection = target.connection;
    wanted = target.repository;
    resumeRequested = true;
  }).catch(() => {}).finally(() => {
    targetRead = true;
    if (accountsCurrent) api.accounts(known);
  });
  function render() {
    framed();
    const options = JSON.stringify(
      known.map((account) => [account.connection, account.label, account.account, account.state, account.provider, account.origin])
    );
    if (options !== accountOptions) {
      accountOptions = options;
      accounts.replaceChildren(
        new Option('Choose an account', ''),
        ...known.map((account) => {
          const label = account.label ?? account.account;
          const option = new Option(account.provider === 'fountain' ? `${label} — ${account.origin}` : label, account.connection);
          option.disabled = account.state !== 'stored';
          return option;
        })
      );
    }
    accounts.value = known.some((a) => a.connection === connection) ? connection : '';
    accounts.disabled = !ready || !accountsCurrent;
    const projects = JSON.stringify(discovered.map((repo) => [repo.external_id, repo.name, repo.private]));
    if (projects !== repositoryOptions) {
      repositoryOptions = projects;
      suggestions.replaceChildren(...discovered.map((repo) => new Option(repo.private === true ? 'Private' : '', repo.name)));
    }
    more.hidden = !nextPage;
    more.disabled = busy || !selected;
    reconnect.disabled =
      !ready ||
      !accountsCurrent ||
      !connection ||
      busy ||
      !known.some((account) => account.connection === connection && account.state === 'stored');
    disconnect.disabled = !connection && !pending;
    name.disabled = (!selected && !fountain()) || busy;
    // Checks and developer scopes read the open repository from here.
    name.dataset.repository = selected?.repository?.external_id ?? '';
    find.querySelector<HTMLButtonElement>('button')!.disabled = name.disabled;
    status.textContent = message;
    // With a project open, its own card on Projects shows the connection and
    // its Reconnect and Disconnect; this card only adds projects.
    const open = !!selected?.repository && message === `Connected to ${selected.repository.name}.`;
    status.hidden = open;
    actions.hidden = !!selected?.repository;
  }
  function clear() {
    fragmentObserver?.disconnect();
    fragmentObserver = undefined;
    const old = selected?.session ?? pending;
    product?.retire();
    product = undefined;
    selected = undefined;
    pending = undefined;
    app.context = undefined;
    mounted = '';
    framed();
    if (old) void core.invoke('factory_disconnect', {session: old}).catch(() => {});
  }
  function navigate(route: FactoryRoute) {
    if (!product) return;
    try {
      const context = product.context(route);
      const hash = routeURL(context.route);
      const view =
        context.route.kind === 'issue'
          ? routeURL({kind: 'issue', repository: context.route.repository, issue: context.route.issue})
          : context.route.kind === 'pull' ? routeURL({kind: 'pull', repository: context.route.repository, pull: context.route.pull}) : hash;
      fragmentObserver?.disconnect();
      fragmentObserver = undefined;
      if (mounted !== view) {
        mounted = view;
        app.context = context;
        framed();
      }
      if (context.route.kind === 'pull') app.querySelector<import('../factory/pulls/detail.js').SodaFactoryPull>('soda-factory-pull')?.selectTab(context.route.tab ?? 'conversation');
      if (location.hash !== hash) history.pushState(null, '', hash);
      if (context.route.kind === 'issue' && context.route.fragment) {
        const target = context.route,
          current = product;
        const scroll = () => {
          if (product !== current || mounted !== view) return;
          const detail = app.querySelector<SodaFactoryIssue>('soda-factory-issue');
          if (detail?.context?.repository !== target.repository || detail.context.issue !== target.issue) return;
          const node = [...detail.querySelectorAll<HTMLElement>('[id]')].find((node) => node.id === target.fragment);
          if (!node) return;
          node.scrollIntoView();
          fragmentObserver?.disconnect();
          fragmentObserver = undefined;
        };
        fragmentObserver = new MutationObserver(scroll);
        fragmentObserver.observe(app, {childList: true, subtree: true});
        scroll();
      }
    } catch {
      message = 'Choose the repository for this destination.';
      render();
    }
  }
  async function discover(captured: number, page: number) {
    const session = selected?.session;
    check(session);
    const reply = await core.invoke<NativeReply>('factory_repositories', {session, page});
    if (!live(captured) || session !== selected?.session) return;
    check(reply.status === 200);
    const body = object(reply.body);
    check(Array.isArray(body.items) && body.items.length <= 20 && Number.isSafeInteger(body.next_page));
    const next = Number(body.next_page);
    check(next === 0 || (next > page && next <= 1000000));
    const values = body.items.map(repository);
    const unique = new Map((page === 1 ? [] : discovered).map((r) => [r.external_id, r]));
    for (const repo of values) unique.set(repo.external_id, repo);
    // Bound the display catalogue; callers can open another repository by name.
    check(unique.size <= 1000);
    discovered = [...unique.values()];
    nextPage = next;
  }
  async function choose(account: string, remote = '', enrolled = '') {
    resumeRequested = false;
    inert(false);
    const captured = ++serial;
    const previousRepository = selected?.repository?.external_id;
    // Where this window was, so a repository that will not open can return there.
    const previous = selected && !fountain() ? {connection, repository: previousRepository ?? ''} : undefined;
    clear();
    connection = account;
    wanted = remote;
    fountainRepository = enrolled;
    busy = true;
    message = account ? 'Connecting…' : 'Choose a saved account to connect.';
    render();
    if (!account || !ready || !accountsCurrent || document.hidden) {
      resumeRequested = !!account;
      busy = false;
      render();
      return;
    }
    if (fountain() && !remote) {
      busy = false;
      message = 'Enter a repository you have enrolled on this Fountain host.';
      render();
      return;
    }
    let session: string | undefined;
    let refused = '';
    try {
      session = await core.invoke<string>('factory_prepare_connection', {
        connection: account,
        repository: remote || null,
        fountainRepository: enrolled || null,
      });
      if (!live(captured)) {
        void core.invoke('factory_disconnect', {session}).catch(() => {});
        return;
      }
      pending = session;
      const value = binding(await core.invoke('factory_connect', {session}), epoch);
      check(value.session === session && (value.repository?.external_id ?? '') === remote);
      if (!live(captured)) {
        void core.invoke('factory_disconnect', {session}).catch(() => {});
        return;
      }
      pending = undefined;
      selected = value;
      if (value.repository) name.value = value.repository.name;
      const metadata = discovered.find((repo) => repo.external_id === remote);
      product = new DesktopProduct(core, value, profile, navigate, signIn.sections, projects, metadata?.default_branch);
      projects.connected(product.transport);
      const route = readRoute();
      navigate(
        route &&
          ('repository' in route ? route.repository === value.repository?.id : !remote || remote === previousRepository)
          ? route
          : remote
            ? {kind: 'feed', repository: value.repository!.id}
            : {kind: 'projects'}
      );
      message = value.repository
        ? `Connected to ${value.repository.name}.`
        : 'Connected. Choose a repository to browse its feed and issues.';
      if (remote) {
        remember({connection: account, repository: remote, enrolled});
        projects.refusal(account, remote, '');
      }
      if (!remote) await discover(captured, 1);
    } catch (error) {
      if (live(captured)) {
        clear();
        message =
          typeof error === 'string'
            ? `Could not connect. ${error}`
            : 'Could not connect. Try reconnecting.';
        // A repository that will not open leaves the account itself usable.
        if (remote && !fountain()) {
          refused = typeof error === 'string' ? `Could not open that repository. ${error}` : message;
          projects.refusal(account, remote, typeof error === 'string' ? error : 'The repository could not be opened.');
        }
      }
    } finally {
      if (serial === captured) {
        busy = false;
        render();
      }
    }
    if (!refused || serial !== captured) return;
    const back = previous && previous.repository !== remote ? previous : {connection: account, repository: ''};
    await choose(back.connection, back.repository);
    if (connection === back.connection && selected && (selected.repository?.external_id ?? '') === back.repository && !busy) {
      message = refused;
      render();
    }
  }
  accounts.addEventListener('change', () => {
    discovered = [];
    nextPage = 0;
    void choose(accounts.value);
  });
  // With a project open the list is read only when the person reaches for it.
  name.addEventListener('focus', () => {
    if (discovered.length || busy || !selected) return;
    const captured = serial;
    void discover(captured, 1)
      .catch(() => {
        if (live(captured)) message = 'Your repositories could not be listed. Enter owner/name instead.';
      })
      .finally(() => {
        if (serial === captured) render();
      });
  });
  // Picking a suggestion opens it; typing only filters the suggestions.
  name.addEventListener('input', (event) => {
    const picked = !(event instanceof InputEvent) || event.inputType === 'insertReplacementText';
    if (picked && discovered.some((repo) => repo.name === name.value)) find.requestSubmit();
  });
  reconnect.addEventListener('click', () => void reconnectHere());
  disconnect.addEventListener('click', () => void disconnectHere());
  more.addEventListener('click', () => {
    const captured = serial;
    busy = true;
    render();
    void discover(captured, nextPage)
      .catch(() => {
        if (live(captured)) message = 'More repositories could not be loaded. Try again.';
      })
      .finally(() => {
        if (serial === captured) {
          busy = false;
          render();
        }
      });
  });
  find.addEventListener('submit', (event) => {
    event.preventDefault();
    const captured = serial,
      session = selected?.session;
    if ((!session && !fountain()) || busy) return;
    let value = name.value.trim();
    try {
      const origin = known.find(account => account.connection === connection)?.origin;
      if (origin && value.startsWith(origin + '/')) {
        const url = new URL(value);
        check(!url.search && !url.hash && !url.username && !url.password);
        value = url.pathname.replace(/^\//, '').replace(/\/$/, '');
      }
      value = value.replace(/\.git$/, '');
      check(/^[A-Za-z0-9_.-]+\/[A-Za-z0-9_.-]+$/.test(value));
    } catch {
      message = 'Enter a repository URL on the selected host or owner/name.';
      render();
      return;
    }
    // A repository already in the list needs no lookup.
    const listed = !fountain() && discovered.find((repo) => repo.name.toLowerCase() === value.toLowerCase());
    if (listed) {
      void choose(connection, listed.external_id);
      return;
    }
    busy = true;
    message = 'Finding repository…';
    render();
    if (fountain()) {
      void core.invoke<{active:boolean;runtime_bound:boolean;repository:{id:string;owner:string;name:string;default_branch:string}}>('fountain_enrollment', {
        connection, repository:value, action:'status',
      }).then(enrollment => {
        if (!live(captured)) return;
        check(enrollment.active && enrollment.runtime_bound && id(enrollment.repository.id));
        const repo = enrollment.repository;
        discovered = [{external_id:repo.id,name:repo.owner+'/'+repo.name,private:null,default_branch:repo.default_branch}];
        return choose(connection, repo.id, value);
      }).catch(() => {
        if (live(captured)) message = 'Current enrollment is unavailable. Enroll this repository before opening it.';
      }).finally(() => {
        if (serial === captured) { busy = false; render(); }
      });
      return;
    }
    void core
      .invoke<NativeReply>('factory_find_repository', {session, name: value})
      .then((reply) => {
        if (!live(captured)) return;
        check(reply.status === 200);
        const found = repository(object(reply.body).repository);
        discovered = [...discovered.filter((repo) => repo.external_id !== found.external_id), found];
        return choose(connection, found.external_id);
      })
      .catch(() => {
        if (live(captured)) message = 'Repository could not be opened. Check its name and your access.';
      })
      .finally(() => {
        if (serial === captured) {
          busy = false;
          render();
        }
      });
  });
  // Work detail asks its host to watch one listed run; the route admits it.
  app.addEventListener('soda-factory-watch-request', (event) => {
    if (!(event instanceof CustomEvent) || typeof event.detail !== 'object' || !event.detail) return;
    const {run, role, issue, repository} = event.detail as Record<string, unknown>;
    navigate({kind: 'watch', repository, run, role, ...(issue ? {issue} : {})} as FactoryRoute);
  });
  window.addEventListener('popstate', () => {
    const route = readRoute();
    if (route) navigate(route);
  });
  window.addEventListener('hashchange', () => {
    const route = readRoute();
    if (route) navigate(route);
  });
  document.addEventListener('visibilitychange', () => {
    if (document.hidden) {
      resumeRequested = !!connection;
      ++serial;
      product?.suspend();
      inert(true);
      busy = false;
      message = connection ? 'Connection suspended while Factory is hidden.' : 'Choose a saved account to connect.';
      render();
    }
  });
  const api = {
    refresh(available: boolean, generation: number, identity: string | null) {
      const changedProfile = identity !== null && profile !== '' && profile !== identity;
      if (changedProfile) {
        projects.forget();
        connection = '';
        wanted = '';
        discovered = [];
        nextPage = 0;
        resumeRequested = false;
      }
      if (!available || epoch !== generation || changedProfile) {
        if (!changedProfile && connection) resumeRequested = true;
        ++serial;
        clear();
        busy = false;
      }
      ready = available;
      epoch = generation;
      profile = identity ?? profile;
      if (!ready) message = 'Connection is unavailable until Factory is ready.';
      render();
    },
    accounts(values: readonly Account[] | undefined) {
      accountsCurrent = values !== undefined;
      if (values === undefined) {
        ++serial;
        if (product) { product.suspend(); inert(true); } else clear();
        busy = false;
        resumeRequested = !!connection;
        message =
          'Saved account status is unavailable. Waiting for current status before reconnecting. Saved drafts are kept.';
        render();
        return;
      }
      known = values;
      if (ready && !connection)
        message = known.length ? 'Choose a saved account to connect.' : 'Sign in to connect a repository.';
      const account = known.find((account) => account.connection === connection);
      if (connection && account?.state !== 'stored') {
        ++serial;
        clear();
        busy = false;
        resumeRequested = !!account && account.state !== 'withdrawn';
        if (!resumeRequested) {
          connection = '';
          wanted = '';
          discovered = [];
          nextPage = 0;
        }
        message =
          account?.state === 'locked'
            ? 'Your saved account is locked. Unlock the desktop keyring before reconnecting. Saved drafts are kept.'
            : account?.state === 'expired'
              ? 'Your saved sign-in has expired. Renew it or sign in again to reconnect. Saved drafts are kept.'
              : account?.state === 'refresh_pending'
                ? 'Renewing your sign-in did not finish. Check Accounts before reconnecting. Saved drafts are kept.'
                : 'This account is no longer available in Factory. Choose a saved account. Saved drafts are kept.';
      }
      if (targetRead) launching = false;
      const stored = known.filter((account) => account.state === 'stored');
      if (!autoOpened && targetRead && ready && !document.hidden && !connection && !busy && stored.length) {
        autoOpened = true;
        const last = remembered(),
          github = stored.filter((account) => account.provider === 'github');
        if (last && stored.some((account) => account.connection === last.connection)) {
          void choose(last.connection, last.repository, last.enrolled);
          return;
        }
        if (github.length === 1) {
          void choose(github[0]!.connection);
          return;
        }
      }
      if (resumeRequested && ready && !document.hidden && !busy && account?.state === 'stored') {
        if (product && selected?.generation === epoch) {
          const current = product, captured = ++serial;
          busy = true; resumeRequested = false;
          void current.resume().then(() => {
            if (!live(captured) || product !== current) return;
            inert(false); message = 'Connected.'; void app.refresh();
          }).catch(() => {
            if (live(captured) && product === current) { resumeRequested = true; clear(); message = 'Connection changed. Reconnect to continue. Saved drafts are kept.'; }
          }).finally(() => { if (serial === captured) {busy = false; render();} });
        } else void choose(connection, wanted, fountainRepository);
        return;
      }
      render();
    },
  };
  return api;
}
