import type {FactoryTransport} from '../factory/app/browser.js';
import {check, id, object} from '../factory/api/models.js';
import type {RepositoryRef} from '../factory/api/collaboration.js';

export interface NativeReply {
  status: number;
  body: unknown;
}
interface Completion {
  reply: NativeReply | null;
  error: string | null;
}
interface Channel<T> {
  onmessage: (value: T) => void;
}
export interface Core {
  invoke<T>(command: string, args?: unknown): Promise<T>;
  Channel: new <T>() => Channel<T>;
}
export interface Binding {
  session: string;
  generation: number;
  identity: {host: string; actor: string};
  repository: RepositoryRef | null;
  account_label: string;
}
export function binding(value: unknown, generation: number): Binding {
  const row = object(value),
    identity = object(row.identity);
  check(
    id(row.session) &&
      row.generation === generation &&
      /^[a-f0-9]{32}$/.test(String(identity.host)) &&
      id(identity.actor)
  );
  check(typeof row.account_label === 'string' && !!row.account_label.trim() && row.account_label.length <= 256);
  let repository: RepositoryRef | null = null;
  if (row.repository !== null) {
    const repo = object(row.repository);
    check(
      id(repo.id) &&
        repo.host === identity.host &&
        id(repo.external_id) &&
        typeof repo.name === 'string' &&
        repo.name.length <= 512
    );
    repository = {id: repo.id, host: identity.host as string, external_id: repo.external_id, name: repo.name};
  }
  return {
    session: row.session,
    generation,
    identity: {host: identity.host as string, actor: identity.actor},
    repository,
    account_label: row.account_label,
  };
}

/** One transport per native selection, shared by every product component. Native
 * cancellation joins its task; this layer also refuses buffered stale replies. */
export class DesktopTransport implements FactoryTransport {
  readonly assetBase = '';
  readonly generation: string;
  private lifetime = new AbortController();
  constructor(
    private core: Core,
    readonly selected: Binding,
    profile: string
  ) {
    this.generation = `${profile}:${selected.generation}:${selected.session}`;
  }
  retire() {
    this.lifetime.abort();
  }
  async request(input: string, init: RequestInit = {}): Promise<Response> {
    const path = input.startsWith('/api/') ? input : '/api/' + input.replace(/^\//, '');
    check(path.startsWith('/api/factory/') || path.startsWith('/api/repositories/'));
    const method = init.method ?? 'GET';
    let request: unknown;
    if (method === 'GET') {
      check(init.body === undefined);
      request = {kind: 'read', path};
    } else {
      const bodyLimit =
        method === 'PUT' && /^\/api\/repositories\/[1-9][0-9]*\/factory\/setup$/.test(path) ? 1024 * 1024 : 65536;
      check(typeof init.body === 'string' && new TextEncoder().encode(init.body).byteLength <= bodyLimit);
      const body = object(JSON.parse(init.body));
      if (method === 'PUT') {
        if (path === '/api/factory/feed/subscription') request = {kind: 'subscription', body};
        else if (path === '/api/factory/capacity') request = {kind: 'capacity', body};
        else {
          const sponsorship = /^\/api\/repositories\/([1-9][0-9]*)\/factory\/sponsorships\/([0-9a-f]{32})$/.exec(path);
          if (sponsorship)
            request = {kind: 'sponsorship', repository: sponsorship[1], connection: sponsorship[2], body};
          else {
            const setting =
              /^\/api\/repositories\/([1-9][0-9]*)\/factory\/(placement|environment-grant|operator-grant|policy|setup)$/.exec(
                path
              );
            check(setting && id(setting[1]));
            request = {kind: 'repository_setting', repository: setting[1], setting: setting[2], body};
          }
        }
      } else {
        check(method === 'POST');
        request = {kind: 'write', path, body};
      }
    }
    const signal = AbortSignal.any([
      this.lifetime.signal,
      ...(init.signal ? [init.signal] : []),
      AbortSignal.timeout(22000),
    ]);
    signal.throwIfAborted();
    const response = await new Promise<NativeReply>((resolve, reject) => {
      let handle: string | undefined,
        settled = false;
      const cancel = () => {
        if (handle)
          void this.core
            .invoke('factory_product_cancel', {session: this.selected.session, request: handle})
            .catch(() => {});
      };
      const finish = (reply?: NativeReply, error?: unknown) => {
        if (settled) return;
        settled = true;
        signal.removeEventListener('abort', aborted);
        if (error !== undefined) reject(error);
        else resolve(reply!);
      };
      const aborted = () => {
        cancel();
        finish(undefined, signal.reason);
      };
      const channel = new this.core.Channel<Completion>();
      channel.onmessage = (value) => {
        if (signal.aborted) {
          aborted();
          return;
        }
        if (value.error || !value.reply)
          finish(undefined, Error('Factory request is unavailable. Reconnect and try again.'));
        else finish(value.reply);
      };
      signal.addEventListener('abort', aborted, {once: true});
      void this.core
        .invoke<string>('factory_product_request', {session: this.selected.session, request, channel})
        .then((id) => {
          handle = id;
          if (signal.aborted) cancel();
        })
        .catch(() => finish(undefined, Error('Factory connection is unavailable. Reconnect and try again.')));
    });
    signal.throwIfAborted();
    check(Number.isInteger(response.status) && response.status >= 200 && response.status <= 599);
    return new Response([204, 304].includes(response.status) ? null : JSON.stringify(response.body), {
      status: response.status,
      headers: {'Content-Type': 'application/json', 'Cache-Control': 'no-store'},
    });
  }
}
