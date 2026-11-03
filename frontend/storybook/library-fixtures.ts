import type {LibraryContext} from '../factory/library/library.js';

export const libraryRepository = '9007199254740993';
export const libraryItemID = 'a'.repeat(32);
export type LibraryStoryState =
  | 'recorded'
  | 'empty'
  | 'loading'
  | 'offline'
  | 'denied'
  | 'paged'
  | 'detail'
  | 'removed';
type Mismatch = '' | 'repository' | 'item' | 'history' | 'cursor' | 'revision';

function item(repository: string, id: string, title: string, kind = 'knowledge') {
  return {
    repository,
    id,
    revision: 3,
    kind,
    title,
    body: 'Keep work and its recorded evidence together. <script>untrusted()</script>',
    scope: 'Repository work',
    owner_role: 'soda_reviewer',
    state: 'current',
    updated_at: 0,
    source_post: 'd'.repeat(32),
    evidence_refs: ['sha256:' + '1'.repeat(64)],
    decision: kind === 'decision' ? 'Use a bounded queue.' : '',
    rationale: kind === 'decision' ? 'Keep the remaining capacity visible.' : '',
    alternatives: kind === 'decision' ? ['Unbounded admission'] : [],
    removed: false,
    replacements: ['b'.repeat(32)],
    derived_from: ['c'.repeat(32)],
    relations: [{kind: 'supports', target: 'b'.repeat(32), evidence: ['Recorded observation']}],
  };
}

/** Declared read peer only. No backend, provider, model, account or worker effects. */
export function libraryStoryPeer(state: LibraryStoryState, repository = libraryRepository) {
  const calls: {path: string; method: string}[] = [],
    signals: AbortSignal[] = [],
    gates: (() => void)[] = [];
  let held = false,
    code = 0,
    mismatch: Mismatch = '';
  const all = [
    item(
      repository,
      libraryItemID,
      repository === libraryRepository ? 'Choose a bounded worker queue' : 'Another repository choice',
      'decision'
    ),
    item(repository, 'b'.repeat(32), 'Retained observations'),
    item(repository, 'c'.repeat(32), 'Earlier context'),
  ];
  if (state === 'removed') all[0] = {...all[0]!, removed: true, state: 'retracted'};
  const transport = {
    generation: 'declared-library-session',
    assetBase: '/',
    async request(path: string, init?: RequestInit) {
      calls.push({path, method: init?.method ?? 'GET'});
      if (init?.signal) signals.push(init.signal);
      const url = new URL(path, 'https://declared.invalid/'),
        id = url.searchParams.get('id');
      if (
        init?.method !== 'GET' ||
        url.searchParams.get('repository') !== repository ||
        !['/factory/library', '/factory/library/item'].includes(url.pathname)
      )
        throw Error('Unexpected declared library request');
      const status = code || (state === 'offline' ? 503 : state === 'denied' ? 403 : 200);
      let data: unknown;
      if (id) {
        const found = all.find((item) => item.id === id);
        data = found
          ? {
              item: found,
              history: [
                {item: found, reason: 'Recorded decision', actor: '11'},
                {
                  item: {...found, revision: 2, title: 'Earlier reviewed choice', source_post: 'e'.repeat(32)},
                  reason: 'Initial observation',
                  actor: '12',
                },
              ],
            }
          : undefined;
      } else {
        const query = url.searchParams.get('query')?.toLowerCase() ?? '',
          kind = url.searchParams.get('kind'),
          cursor = url.searchParams.get('cursor') ?? '';
        let items =
          state === 'empty'
            ? []
            : all.filter(
                (item) =>
                  item.id > cursor &&
                  (!kind || item.kind === kind) &&
                  (!item.removed || url.searchParams.get('history') === 'true') &&
                  (item.title + item.body).toLowerCase().includes(query)
              );
        const paged = state === 'paged' && !cursor && items.length > 1;
        if (paged) items = items.slice(0, 1);
        data = {items, next_cursor: paged ? items[0]!.id : '', complete: !paged};
      }
      const snapshot = structuredClone(data);
      if (snapshot && typeof snapshot === 'object') {
        const record = snapshot as {
          item?: ReturnType<typeof item>;
          items?: ReturnType<typeof item>[];
          history?: {item: ReturnType<typeof item>}[];
          next_cursor?: string;
          complete?: boolean;
        };
        if (mismatch === 'repository') {
          if (record.item) record.item.repository = '7';
          record.items?.forEach((item) => (item.repository = '7'));
        }
        if (mismatch === 'item' && record.item) record.item.id = 'f'.repeat(32);
        if (mismatch === 'history' && record.history?.[1]) record.history[1].item.repository = '7';
        if (mismatch === 'revision' && record.history?.[1]) record.history[1].item.revision = 4;
        if (mismatch === 'cursor') {
          record.complete = false;
          record.next_cursor = '';
        }
      }
      if (held || state === 'loading') await new Promise<void>((resolve) => gates.push(resolve));
      if (status !== 200 || !snapshot)
        return Response.json({error: {code: 'unavailable'}}, {status: status !== 200 ? status : 404});
      return Response.json(snapshot);
    },
  };
  const context: LibraryContext = {transport, repository};
  return {
    context,
    calls,
    hold() {
      held = true;
    },
    release() {
      held = false;
      gates.splice(0).forEach((resolve) => resolve());
    },
    aborted: () => signals.some((signal) => signal.aborted),
    fail(value: number) {
      code = value;
    },
    mismatch(value: Mismatch) {
      mismatch = value;
    },
    retireGeneration() {
      transport.generation += ':retired';
    },
  };
}
