import {check, id, object} from '../factory/api/models.js';
import {factoryRunID} from '../factory/api/run-response.js';
import type {FactoryWatchPeer, FactoryWatchRenderer, FactoryWatchSource} from '../factory/watch/ports.js';
import type {Binding, Core} from './transport.js';

/** The xterm renderer loads only when a run is watched. */
export async function watchRenderer(): Promise<FactoryWatchRenderer> {
  const [{Terminal}, {FitAddon}] = await Promise.all([import('@xterm/xterm'), import('@xterm/addon-fit')]);
  return {Terminal, FitAddon};
}

/** Live output over the native host's read-only output response. Frames match
 * the shared WebSocket protocol; Rust delivers them over one Tauri channel and
 * ends with `{type: 'end', status}`. A `renew` close means the response's
 * lifetime ended: this peer reattaches at its cursor without the view noticing,
 * and each reattachment costs one fresh account binding. Closing the peer
 * cancels the native response; the run itself is never touched. */
export function desktopWatchSource(core: Core, selected: Binding, live: () => boolean): FactoryWatchSource {
  return {
    async repository(signal) {
      signal.throwIfAborted();
      check(live() && selected.repository);
      return selected.repository.id;
    },
    attachment(cursor, generation) {
      return {cursor, session_generation: generation};
    },
    connect() {
      check(live());
      return new DesktopWatchPeer(core, selected.session, live);
    },
  };
}

class DesktopWatchPeer implements FactoryWatchPeer {
  readyState: FactoryWatchPeer['readyState'] = 0;
  onopen: FactoryWatchPeer['onopen'] = null;
  onmessage: FactoryWatchPeer['onmessage'] = null;
  onerror: FactoryWatchPeer['onerror'] = null;
  onclose: FactoryWatchPeer['onclose'] = null;
  private run = '';
  private cursor = 0;
  private handle: string | undefined;
  private attachments = 0;
  constructor(
    private core: Core,
    private session: string,
    private live: () => boolean
  ) {
    queueMicrotask(() => {
      if (this.readyState !== 0) return;
      this.readyState = 1;
      this.onopen?.call(this as unknown as WebSocket, new Event('open'));
    });
  }
  send(data: string | ArrayBufferLike | Blob | ArrayBufferView) {
    check(this.readyState === 1 && !this.run && typeof data === 'string' && data.length <= 4096);
    const handshake = object(JSON.parse(data));
    check(factoryRunID(handshake.run_id));
    check(typeof handshake.cursor === 'number' && Number.isSafeInteger(handshake.cursor) && handshake.cursor >= 0);
    this.run = handshake.run_id;
    this.cursor = handshake.cursor;
    this.attach();
  }
  close() {
    if (this.readyState >= 2) return;
    this.readyState = 3;
    const handle = this.handle;
    this.handle = undefined;
    if (handle) void this.core.invoke('factory_product_cancel', {session: this.session, request: handle}).catch(() => {});
  }
  private attach() {
    // Renewals cover the longest run (64 x 4.5 minutes); a response that
    // renews early ends instead, so it cannot spin into repeated bindings.
    if (++this.attachments > 64) return this.ended();
    const attachment = this.attachments,
      started = Date.now(),
      channel = new this.core.Channel<unknown>();
    let closed = false;
    channel.onmessage = (value) => {
      if (this.readyState !== 1 || attachment !== this.attachments || !this.live()) return;
      try {
        const frame = object(value);
        if (frame.type === 'end') {
          if (closed) return;
          if (frame.status === 404) this.deliver({type: 'closed', reason: 'run_unknown'});
          else if (frame.status === 403) this.deliver({type: 'closed', reason: 'authority_lost'});
          else this.ended();
          return;
        }
        if (frame.type === 'closed' && frame.reason === 'renew') {
          closed = true;
          if (Date.now() - started < 60000) this.ended();
          else this.attach();
          return;
        }
        if (frame.type === 'closed') closed = true;
        if (frame.type === 'output' && typeof frame.next === 'number' && Number.isSafeInteger(frame.next) && frame.next >= this.cursor)
          this.cursor = frame.next;
        this.deliver(frame);
      } catch {
        this.ended();
      }
    };
    void this.core
      .invoke<string>('factory_product_output', {session: this.session, run: this.run, cursor: this.cursor, channel})
      .then((handle) => {
        check(id(handle));
        if (this.readyState === 1 && attachment === this.attachments) this.handle = handle;
        else void this.core.invoke('factory_product_cancel', {session: this.session, request: handle}).catch(() => {});
      })
      .catch(() => {
        if (attachment === this.attachments) this.ended();
      });
  }
  private deliver(frame: unknown) {
    const data = JSON.stringify(frame);
    if (data.length > 65536) return this.ended();
    this.onmessage?.call(this as unknown as WebSocket, new MessageEvent('message', {data}));
  }
  /** Transport loss without a closing frame: the view decides whether to retry. */
  private ended() {
    if (this.readyState >= 2) return;
    this.close();
    this.onclose?.call(this as unknown as WebSocket, new CloseEvent('close'));
  }
}
