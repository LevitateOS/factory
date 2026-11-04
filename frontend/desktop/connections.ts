import type {SodaFactoryApp, FactoryRoute} from '../factory/app/browser.js';
import {check, id, object} from '../factory/api/models.js';
import type {Account} from './accounts.js';
import {binding} from './transport.js';
import type {Binding, Core, NativeReply} from './transport.js';
import {DesktopProduct, readRoute, routeURL} from './product.js';

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
export function mountConnections(core: Core, app: SodaFactoryApp) {
  const accounts = document.querySelector<HTMLSelectElement>('#factory-account')!;
  const repositories = document.querySelector<HTMLSelectElement>('#factory-repository')!;
  const status = document.querySelector<HTMLElement>('#connection-status')!;
  const more = document.querySelector<HTMLButtonElement>('#repository-more')!;
  const reconnect = document.querySelector<HTMLButtonElement>('#connection-reconnect')!;
  const disconnect = document.querySelector<HTMLButtonElement>('#connection-disconnect')!;
  const find = document.querySelector<HTMLFormElement>('#repository-find')!;
  const name = document.querySelector<HTMLInputElement>('#repository-name')!;
  let ready = false,
    epoch = -1,
    profile = '',
    serial = 0,
    busy = false;
  let selected: Binding | undefined, pending: string | undefined, product: DesktopProduct | undefined;
  let connection = '',
    wanted = '',
    nextPage = 0;
  let known: readonly Account[] = [],
    discovered: Repository[] = [];
  let message = 'Choose a saved account to connect.';
  let accountOptions = '',
    repositoryOptions = '',
    mounted = '';
  const live = (captured: number) => ready && serial === captured && !document.hidden;
  function render() {
    const options = JSON.stringify(
      known.map((account) => [account.connection, account.label, account.account, account.state])
    );
    if (options !== accountOptions) {
      accountOptions = options;
      accounts.replaceChildren(
        new Option('Choose an account', ''),
        ...known.map((account) => {
          const option = new Option(account.label ?? account.account, account.connection);
          option.disabled = account.state !== 'stored';
          return option;
        })
      );
    }
    accounts.value = known.some((a) => a.connection === connection) ? connection : '';
    accounts.disabled = !ready;
    const projects = JSON.stringify(discovered.map((repo) => [repo.external_id, repo.name, repo.private]));
    if (projects !== repositoryOptions) {
      repositoryOptions = projects;
      repositories.replaceChildren(
        new Option('Choose a repository', ''),
        ...discovered.map(
          (repo) => new Option(repo.name + (repo.private === true ? ' · Private' : ''), repo.external_id)
        )
      );
    }
    repositories.value = wanted;
    repositories.disabled = !selected || busy;
    more.hidden = !nextPage;
    more.disabled = busy || !selected;
    reconnect.disabled = !ready || !connection || busy;
    disconnect.disabled = !connection && !pending;
    name.disabled = !selected || busy;
    find.querySelector<HTMLButtonElement>('button')!.disabled = !selected || busy;
    status.textContent = message;
  }
  function clear() {
    const old = selected?.session ?? pending;
    product?.retire();
    product = undefined;
    selected = undefined;
    pending = undefined;
    app.context = undefined;
    mounted = '';
    if (old) void core.invoke('factory_disconnect', {session: old}).catch(() => {});
  }
  function navigate(route: FactoryRoute) {
    if (!product) return;
    try {
      const context = product.context(route);
      const hash = routeURL(context.route);
      if (mounted !== hash) {
        mounted = hash;
        app.context = context;
      }
      if (location.hash !== hash) history.pushState(null, '', hash);
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
  async function choose(account: string, remote = '') {
    const captured = ++serial;
    clear();
    connection = account;
    wanted = remote;
    busy = true;
    message = account ? 'Connecting…' : 'Choose a saved account to connect.';
    render();
    if (!account || !ready || document.hidden) {
      busy = false;
      render();
      return;
    }
    let session: string | undefined;
    try {
      session = await core.invoke<string>('factory_prepare_connection', {
        connection: account,
        repository: remote || null,
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
      const metadata = discovered.find((repo) => repo.external_id === remote);
      product = new DesktopProduct(core, value, profile, navigate, metadata?.default_branch);
      const route = readRoute();
      navigate(
        route && (!('repository' in route) || route.repository === value.repository?.id)
          ? route
          : remote
            ? {kind: 'feed', repository: value.repository!.id}
            : {kind: 'request'}
      );
      message = value.repository
        ? `Connected to ${value.repository.name}.`
        : 'Connected. Choose a repository to browse its feed and issues.';
      if (!remote) await discover(captured, 1);
    } catch {
      if (live(captured)) {
        clear();
        message = 'Could not connect. Unlock your keyring, check access or sign in again, then reconnect.';
      }
    } finally {
      if (serial === captured) {
        busy = false;
        render();
      }
    }
  }
  accounts.addEventListener('change', () => {
    discovered = [];
    nextPage = 0;
    void choose(accounts.value);
  });
  repositories.addEventListener('change', () => {
    void choose(connection, repositories.value);
  });
  reconnect.addEventListener('click', () => {
    void choose(connection, wanted);
  });
  disconnect.addEventListener('click', () => {
    void choose('');
  });
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
    if (!session || busy) return;
    let value = name.value.trim();
    try {
      if (value.startsWith('https://github.com/')) {
        const url = new URL(value);
        check(!url.search && !url.hash && !url.username && !url.password);
        value = url.pathname.replace(/^\//, '').replace(/\/$/, '');
      }
      value = value.replace(/\.git$/, '');
      check(/^[A-Za-z0-9_.-]+\/[A-Za-z0-9_.-]+$/.test(value));
    } catch {
      message = 'Enter a GitHub repository URL or owner/name.';
      render();
      return;
    }
    busy = true;
    message = 'Finding repository…';
    render();
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
      ++serial;
      clear();
      busy = false;
      message = 'Disconnected. Reconnect to continue.';
      render();
    }
  });
  return {
    refresh(available: boolean, generation: number, identity: string | null) {
      if (!available || epoch !== generation || profile !== identity) {
        ++serial;
        clear();
        busy = false;
      }
      ready = available;
      epoch = generation;
      profile = identity ?? '';
      if (!ready) message = 'Connection is unavailable until Factory is ready.';
      render();
    },
    accounts(values: readonly Account[]) {
      known = values;
      if (connection && !known.some((account) => account.connection === connection && account.state === 'stored')) {
        ++serial;
        clear();
        busy = false;
        connection = '';
        wanted = '';
        discovered = [];
        nextPage = 0;
        message = 'Your account needs sign-in or was removed. Saved drafts are kept.';
      }
      render();
    },
  };
}
