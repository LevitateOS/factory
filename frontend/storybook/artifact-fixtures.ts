import type {FeedArtifactContext} from '../factory/feed/feed-artifact.js';
import type {FeedArtifact} from '../factory/feed/feed-response.js';

export type ArtifactStoryState =
  | 'text'
  | 'image'
  | 'binary'
  | 'loading'
  | 'offline'
  | 'denied'
  | 'too-large'
  | 'truncated';
export const artifactRepository = '9007199254740993';

/** Declared bytes only: no backend/provider/model/worker or remote artifact. */
export function artifactStoryPeer(state: ArtifactStoryState, repository = artifactRepository) {
  const lifetime = new AbortController(),
    signals: AbortSignal[] = [],
    releases: (() => void)[] = [];
  const calls: {path: string; method: string}[] = [];
  let held = 0,
    mode: '' | 'headers' | 'body' = '',
    cancelled = 0,
    code = 0;
  let content = 'Recorded evidence. <script>untrusted()</script>';
  const image = Uint8Array.from(
    atob('iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+aX1sAAAAASUVORK5CYII='),
    (c) => c.charCodeAt(0)
  );
  const artifact: FeedArtifact = {
    id: 'a'.repeat(32),
    name: state === 'image' ? 'evidence.png' : state === 'binary' ? 'evidence.bin' : 'evidence.txt',
    mediaType:
      state === 'image' ? 'image/png' : state === 'binary' ? 'application/octet-stream' : 'text/plain; charset=utf-8',
    href: '/api/factory/artifacts/' + 'a'.repeat(32) + '?repository=' + repository,
  };
  const transport = {
    generation: 'declared-artifact-session',
    assetBase: '/',
    async request(path: string, init?: RequestInit) {
      calls.push({path, method: init?.method ?? 'GET'});
      if (init?.signal) signals.push(init.signal);
      if (
        init?.method !== 'GET' ||
        !new RegExp('^factory/artifacts/[a-f0-9]{32}\\?repository=' + repository + '$').test(path)
      )
        throw Error('Unexpected artifact request');
      const status = code || (state === 'offline' ? 503 : state === 'denied' ? 403 : 200);
      const bytes =
        state === 'image'
          ? image
          : state === 'too-large'
            ? new Uint8Array(9 * 1024 * 1024)
            : new TextEncoder().encode(state === 'truncated' ? 'e'.repeat(70 * 1024) : content);
      const responseMode = mode;
      if (responseMode === 'headers' || state === 'loading') {
        held++;
        await new Promise<void>((resolve) => releases.push(resolve));
      }
      if (status !== 200) return Response.json({error: {code: 'unavailable'}}, {status});
      let cancelStream = () => {};
      const body = new ReadableStream<Uint8Array>({
        start(controller) {
          let closed = false;
          const send = () => {
            if (!closed) {
              controller.enqueue(bytes);
              controller.close();
              closed = true;
            }
          };
          const originalCancel = () => {
            closed = true;
            cancelled++;
          };
          cancelStream = originalCancel;
          if (responseMode === 'body') {
            held++;
            releases.push(send);
          } else send();
        },
        cancel() {
          cancelStream();
        },
      });
      return new Response(body, {headers: {'Content-Type': artifact.mediaType}});
    },
  };
  const context: FeedArtifactContext = {
    transport,
    repository,
    generation: transport.generation,
    signal: lifetime.signal,
  };
  return {
    artifact,
    context,
    calls,
    hold(value: 'headers' | 'body') {
      mode = value;
    },
    release() {
      mode = '';
      releases.splice(0).forEach((release) => release());
    },
    held: () => held,
    cancelled: () => cancelled,
    aborted: () => signals.some((signal) => signal.aborted),
    retire: () => lifetime.abort(),
    retireGeneration() {
      transport.generation += ':retired';
    },
    text(value: string) {
      content = value;
    },
    fail(status: number) {
      code = status;
    },
  };
}
