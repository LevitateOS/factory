import {Terminal} from '@xterm/xterm';
import {FitAddon} from '@xterm/addon-fit';
import type {FactoryWatchContext, FactoryWatchPeer, FactoryWatchRenderer} from '../factory/watch/ports.js';

export type WatchStoryState = 'live' | 'idle' | 'loading' | 'ended' | 'denied' | 'offline' | 'invalid';
export const watchRun = 'a'.repeat(32);
export const watchRepository = '9007199254740993';
export const watchRenderer: FactoryWatchRenderer = {Terminal, FitAddon};

/** Declared output transport; the renderer is the actual installed xterm. */
export function watchStoryPeer(state: WatchStoryState, repository = watchRepository) {
  let generation = `declared-watch:${state}`;
  let holdRead = false;
  let held: {signal: AbortSignal | null | undefined; resolve: () => void} | undefined;
  const reads: string[] = [];
  const peers: DeclaredPeer[] = [];
  class DeclaredPeer implements FactoryWatchPeer {
    readyState: 0 | 1 | 2 | 3 = 0;
    onopen: ((event: Event) => void) | null = null;
    onmessage: ((event: MessageEvent) => void) | null = null;
    onerror: ((event: Event) => void) | null = null;
    onclose: ((event: CloseEvent) => void) | null = null;
    readonly sent: Record<string, unknown>[] = [];
    constructor() {
      setTimeout(() => {
        if (this.readyState === 0) {
          this.readyState = 1;
          this.onopen?.(new Event('open'));
        }
      }, 0);
    }
    send(text: string) {
      if (this.sent.length) throw Error('Only the read-only attachment is admitted.');
      const value = JSON.parse(text) as Record<string, unknown>;
      if (value.observe !== watchRun || value.capability !== generation || !Number.isSafeInteger(value.after))
        throw Error('Invalid declared attachment.');
      this.sent.push(value);
      queueMicrotask(() => {
        if (this.readyState !== 1) return;
        this.frame({type: 'status', run_id: watchRun, phase: 'running', live: true, terminal: false});
        this.output(peers.length === 1 ? 'Declared live output.\r\n' : 'Resumed output.\r\n', Number(value.after));
        if (state === 'invalid') this.frame({type: 'output', cursor: -1, next: 0, data: ''});
      });
    }
    frame(value: unknown) {
      this.onmessage?.(new MessageEvent('message', {data: JSON.stringify(value)}));
    }
    output(text: string, cursor: number) {
      const bytes = new TextEncoder().encode(text);
      this.frame({type: 'output', cursor, next: cursor + bytes.length, data: btoa(String.fromCharCode(...bytes))});
    }
    close() {
      this.readyState = 3;
    }
    lose() {
      this.readyState = 3;
      this.onclose?.(new CloseEvent('close'));
    }
  }
  const context: FactoryWatchContext = {
    runId: watchRun,
    role: 'soda-coder',
    issue: '12',
    transport: {
      get generation() {
        return generation;
      },
      assetBase: '/',
      async request(path, init) {
        init?.signal?.throwIfAborted();
        reads.push(path);
        if (path !== `factory/runs/${watchRun}` || init?.method !== 'GET') return Response.json({}, {status: 404});
        if (holdRead)
          await new Promise<void>((resolve) => {
            held = {signal: init.signal, resolve};
          });
        if (state === 'loading') {
          const signal = init.signal;
          if (!signal) throw Error('A read signal is required.');
          signal.throwIfAborted();
          return new Promise<Response>((_, reject) =>
            signal.addEventListener('abort', () => reject(signal.reason), {once: true})
          );
        }
        if (state === 'denied' || state === 'offline')
          return Response.json({}, {status: state === 'denied' ? 403 : 503});
        return Response.json({
          run: {
            id: watchRun,
            role: 'soda-coder',
            project_id: 'p0123456789abcdef01234567',
            reconciled: state === 'ended',
          },
          view: {repository: repository, issue: '12'},
          state: {
            phase: state === 'ended' ? 'finished' : 'running',
            terminal: state === 'ended',
            live: state !== 'ended',
            ...(state === 'ended' ? {exit_code: 0, output: 'Recorded final output.'} : {}),
          },
        });
      },
    },
    source: {
      repository: async (signal) => {
        signal.throwIfAborted();
        return repository;
      },
      connect() {
        const peer = new DeclaredPeer();
        peers.push(peer);
        return peer;
      },
      // Deliberately different from Fountain's native attachment: the host owns it.
      attachment: (cursor, current) => ({observe: watchRun, after: cursor, capability: current}),
    },
  };
  return {
    context,
    peers,
    reads,
    holdRead() {
      holdRead = true;
    },
    release() {
      holdRead = false;
      held?.resolve();
    },
    aborted() {
      return held?.signal?.aborted ?? false;
    },
    retireGeneration() {
      generation += ':retired';
    },
  };
}
