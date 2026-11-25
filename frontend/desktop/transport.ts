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
/** An artifact read arrives as one raw frame: a 4-byte big-endian header
 * length, the JSON header, then exactly `length` body bytes. */
interface ArtifactReply {
  status: number;
  contentType: string;
  bytes: Uint8Array<ArrayBuffer>;
}
function artifactReply(frame: ArrayBuffer): ArtifactReply {
  check(frame.byteLength >= 4);
  const size = new DataView(frame).getUint32(0);
  check(size <= 1024 && frame.byteLength >= 4 + size);
  const header = object(JSON.parse(new TextDecoder().decode(new Uint8Array(frame, 4, size))));
  const bytes = new Uint8Array(frame, 4 + size);
  check(
    typeof header.status === 'number' &&
      typeof header.content_type === 'string' &&
      header.content_type.length <= 256 &&
      header.length === bytes.byteLength
  );
  return {status: header.status, contentType: header.content_type, bytes};
}
interface Channel<T> {
  onmessage: (value: T) => void;
}
export interface Core {
  invoke<T>(command: string, args?: unknown, options?: {headers: Record<string, string>}): Promise<T>;
  Channel: new <T>() => Channel<T>;
}
export interface Binding {
  session: string;
  generation: number;
  identity: {host: string; actor: string};
  repository: RepositoryRef | null;
  account_label: string;
  provider: 'github' | 'fountain';
  origin: string;
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
  check(row.provider === 'github' || row.provider === 'fountain');
  check(typeof row.origin === 'string' && new URL(row.origin).origin === row.origin && row.origin.startsWith('https://'));
  check(row.provider !== 'github' || row.origin === 'https://github.com');
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
    provider: row.provider,
    origin: row.origin,
  };
}

/** One transport per native selection, shared by every product component. Native
 * cancellation joins its task; this layer also refuses buffered stale replies. */
export class DesktopTransport implements FactoryTransport {
  readonly assetBase = '';
  readonly generation: string;
  readonly scope: string;
  private lifetime = new AbortController();
  private suspended = false;
  private retired = false;
  constructor(
    private core: Core,
    readonly selected: Binding,
    profile: string
  ) {
    this.generation = `${profile}:${selected.generation}:${selected.session}`;
    this.scope = `${selected.provider}:${profile}:${selected.identity.host}:${selected.identity.actor}`;
  }
  retire() {
    this.retired = true; this.suspend();
  }
  suspend() { this.suspended = true; this.lifetime.abort(); }
  resume() { check(!this.retired); this.lifetime = new AbortController(); this.suspended = false; }
  // This names retained commands; it grants no authority. Every product request
  // still authenticates the captured native selection, including after revocation.
  async commandScope(signal: AbortSignal): Promise<string> {
    check(!this.retired && !this.suspended);
    AbortSignal.any([signal, this.lifetime.signal]).throwIfAborted();
    return this.scope;
  }
  async request(input: string, init: RequestInit = {}): Promise<Response> {
    check(!this.retired && !this.suspended);
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
    const response = await new Promise<NativeReply | ArtifactReply>((resolve, reject) => {
      let handle: string | undefined,
        settled = false;
      const cancel = () => {
        if (handle)
          void this.core
            .invoke('factory_product_cancel', {session: this.selected.session, request: handle})
            .catch(() => {});
      };
      const finish = (reply?: NativeReply | ArtifactReply, error?: unknown) => {
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
      const channel = new this.core.Channel<Completion | ArrayBuffer>();
      channel.onmessage = (value) => {
        if (signal.aborted) {
          aborted();
          return;
        }
        if (value instanceof ArrayBuffer) {
          try {
            finish(artifactReply(value));
          } catch {
            finish(undefined, Error('Factory request is unavailable. Reconnect and try again.'));
          }
        } else if (value.error || !value.reply)
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
    if ('bytes' in response) {
      const empty = [204, 304].includes(response.status);
      return new Response(empty ? null : response.bytes, {
        status: response.status,
        headers: {
          'Content-Type': response.contentType,
          'Content-Length': String(empty ? 0 : response.bytes.byteLength),
          'Cache-Control': 'no-store',
        },
      });
    }
    return new Response([204, 304].includes(response.status) ? null : JSON.stringify(response.body), {
      status: response.status,
      headers: {'Content-Type': 'application/json', 'Cache-Control': 'no-store'},
    });
  }
  /** Saves an artifact this selection already read; the webview cannot follow
   * `blob:` downloads. Returns the file name chosen in the Downloads folder. */
  async saveArtifact(name: string, content: Blob): Promise<string> {
    check(!this.retired && !this.suspended && content.size <= 8 * 1024 * 1024);
    const bytes = new Uint8Array(await content.arrayBuffer());
    check(!this.retired && !this.suspended);
    return this.core.invoke<string>('factory_save_artifact', bytes, {
      headers: {'X-Factory-Session': this.selected.session, 'X-Factory-Name': encodeURIComponent(name)},
    });
  }
}
