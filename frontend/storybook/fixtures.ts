import type {FactoryFeedContext} from '../factory/feed/feed.js';
import {feedRevisionBodyDigest} from '../factory/feed/feed-response.js';

export type FeedStoryState =
  | 'activity'
  | 'empty'
  | 'loading'
  | 'unavailable'
  | 'denied'
  | 'stale'
  | 'expired'
  | 'withdrawn'
  | 'paging'
  | 'replies'
  | 'busy'
  | 'retry'
  | 'unknown'
  | 'revision-unknown'
  | 'review-unknown';
export const feedStoryPost = 'c'.repeat(32);
export const feedStoryCursor = 'declared-older-window';
/** Declared local service only. It creates no backend/provider/model/worker effects. */
export function feedStoryPeer(state: FeedStoryState) {
  const repository = '7',
    root = '/repositories/example',
    stamp = Math.floor(Date.now() / 1000);
  let windows = 0,
    firstFailure = false,
    sequence = 3,
    created = 0,
    revised = 0;
  const calls: {path: string; method: string; body: string}[] = [],
    releases: (() => void)[] = [];
  const makePost = (id: string, body: string, parent = '', seq = 0) => ({
    post_id: id,
    repository,
    author_name: 'Alex',
    actor: '1',
    at: stamp,
    body,
    revision: 1,
    intake: 'recorded',
    judgment: 'applied',
    route: 'discussion',
    draft: 'pending',
    effect: 'pending',
    can_retry: true,
    parent_id: parent,
    seq,
  });
  type Post = ReturnType<typeof makePost> & {
    product_change?: {
      intent: string;
      accepted_revision: string;
      draft_revision?: string;
      state: string;
      can_review: boolean;
    };
  };
  const records = new Map<string, Post>(),
    threads = new Map<string, Post[]>(),
    commands = new Map<string, {body: string; post: Post}>();
  const revisions = new Map<string, {input: string; receipt: Record<string, unknown>}>();
  const entries = [
    {
      kind: 'record',
      id: 'a'.repeat(32),
      repository,
      seq: 3,
      actor: 'builder',
      author: 'Builder',
      issue: 42,
      status: 'implement:running',
      summary: 'Adding search to the project library.',
      payload: {
        communication: {
          kind: 'partial',
          title: 'Search is taking shape',
          summary: 'The query and result list are connected.',
          completed: ['Read the accepted requirements', 'Connected the query'],
          remaining: ['Verify keyboard navigation'],
        },
      },
    },
    {
      kind: 'record',
      id: 'b'.repeat(32),
      repository,
      seq: 2,
      actor: 'inspector',
      author: 'Inspector',
      issue: 41,
      status: 'review:finished:succeeded',
      summary: 'The settings change is ready.',
      payload: {
        communication: {
          kind: 'system',
          component_id: 'F06',
          title: 'Settings change merged',
          summary: 'Review and required checks passed.',
        },
      },
    },
    {
      kind: 'post',
      id: 'c'.repeat(32),
      repository,
      seq: 1,
      actor: '1',
      author: 'Alex',
      post: {
        post_id: 'c'.repeat(32),
        repository,
        author_name: 'Alex',
        actor: '1',
        at: stamp,
        body: 'Could we make finding older work easier?',
      },
    },
  ];
  const rootPost: Post = makePost(feedStoryPost, 'Could we make finding older work easier?');
  if (state === 'review-unknown') {
    rootPost.route = 'new_issue';
    rootPost.can_retry = false;
    rootPost.product_change = {
      intent: 'd' + '1'.repeat(24),
      accepted_revision: 'd' + '2'.repeat(24),
      state: 'review_required',
      can_review: true,
    };
  }
  entries[2]!.post = rootPost;
  records.set(rootPost.post_id, rootPost);
  const windowsEntries: Record<string, unknown>[] = entries;
  const older = ['d', 'e', 'f'].map((hex, index) => ({
    kind: 'record',
    id: hex.repeat(32),
    repository,
    seq: 3 - index,
    author: 'Builder',
    actor: 'builder',
    payload: {
      communication: {
        kind: 'partial',
        title: 'Earlier library work ' + (index + 1),
        summary: 'An older recorded observation.',
      },
    },
  }));
  if (state === 'paging') {
    sequence = 6;
    windowsEntries.forEach((entry, index) => (entry.seq = 6 - index));
  }
  if (state === 'replies') {
    const parent = 'a'.repeat(32),
      replies = [
        makePost('d'.repeat(32), 'Keep the full search history.', parent, 1),
        makePost('e'.repeat(32), 'The query and result list share one filter.', parent, 2),
        makePost('f'.repeat(32), 'Keyboard navigation is ready for review.', parent, 3),
      ];
    threads.set(parent, replies);
    replies.forEach((post) => records.set(post.post_id, post));
  }
  const unavailable = () => Response.json({error: {code: 'example_unavailable'}}, {status: 404});
  const transport: FactoryFeedContext['transport'] = {
    generation: 'storybook-' + state,
    assetBase: '/',
    async request(path, init) {
      const method = init?.method ?? 'GET',
        body = String(init?.body ?? '');
      calls.push({path, method, body});
      init?.signal?.throwIfAborted();
      if (state === 'loading')
        return new Promise<Response>((_resolve, reject) =>
          init?.signal?.addEventListener('abort', () => reject(init.signal?.reason), {once: true})
        );
      if (state === 'unavailable') throw Error('The Factory connection is unavailable.');
      if (state === 'denied') return Response.json({error: {message: 'Access denied.'}}, {status: 403});
      if (state === 'expired') return Response.json({error: {code: 'session_expired'}}, {status: 401});
      const url = new URL(path, 'https://declared.invalid/');
      if (method === 'GET') {
        if (path === 'factory/feed/subscription')
          return Response.json({subscriber: '1', repositories: [repository], revision: 1});
        if (path.startsWith('factory/feed?')) {
          if (state === 'withdrawn' && ++windows > 1)
            return Response.json({error: {code: 'access_withdrawn'}}, {status: 403});
          const cursor = url.searchParams.get('cursor');
          if (cursor && (state !== 'paging' || cursor !== feedStoryCursor)) return unavailable();
          let page = state === 'empty' ? [] : cursor ? older : windowsEntries;
          const search = url.searchParams.get('search')?.toLowerCase(),
            role = url.searchParams.get('role'),
            view = url.searchParams.get('view');
          if (search) page = page.filter((entry) => JSON.stringify(entry).toLowerCase().includes(search));
          if (role) page = page.filter((entry) => entry.actor === role);
          if (view === 'attention' || view === 'decisions') page = [];
          if (view === 'evidence') page = page.filter((entry) => entry.id === 'b'.repeat(32));
          if (url.searchParams.get('current') === 'true') page = page.filter((entry) => entry.id === 'a'.repeat(32));
          return Response.json({
            entries: page,
            truncated: state === 'paging' && !cursor,
            next: state === 'paging' && !cursor ? feedStoryCursor : '',
          });
        }
        const reply = /^factory\/feed\/posts\/([a-f0-9]{32})\/replies\?/.exec(path);
        if (reply) {
          const before = Number(url.searchParams.get('before')) || Infinity;
          const all = [...(threads.get(reply[1]!) ?? [])]
              .filter((post) => post.seq < before)
              .sort((a, b) => b.seq - a.seq),
            page = all.slice(0, 2);
          return Response.json({items: page, next_before: all.length > page.length ? page.at(-1)!.seq : 0});
        }
        const revision = /^factory\/feed\/posts\/([a-f0-9]{32})\/retry\/([a-f0-9]{32})\?repository=7$/.exec(path);
        if (revision) {
          const found = revisions.get(revision[2]!);
          return found && found.receipt.post_id === revision[1] ? Response.json(found.receipt) : unavailable();
        }
        const source = /^factory\/feed\/posts\/([a-f0-9]{32})$/.exec(path);
        if (source) {
          const post = records.get(source[1]!);
          return post ? Response.json(post) : unavailable();
        }
        if (path === 'repositories/' + repository + '/factory') return Response.json({});
        return unavailable();
      }
      const review = /^factory\/feed\/posts\/([a-f0-9]{32})\/review$/.exec(path);
      if (method === 'POST' && review) {
        const post = records.get(review[1]!);
        const input = JSON.parse(body);
        if (!post?.product_change || input.revision !== post.revision) return unavailable();
        post.product_change.state = 'draft';
        post.product_change.draft_revision = 'd' + '3'.repeat(24);
        if (state === 'review-unknown' && !firstFailure) {
          firstFailure = true;
          throw Error('The declared change review reply was lost.');
        }
        return Response.json(post);
      }
      if (method === 'POST' && path === 'factory/feed/posts') {
        const input = JSON.parse(body) as {
          repository?: unknown;
          body?: unknown;
          idempotency_key?: unknown;
          parent_id?: unknown;
        };
        if (
          input.repository !== repository ||
          typeof input.body !== 'string' ||
          !input.body.trim() ||
          input.body.length > 4000 ||
          typeof input.idempotency_key !== 'string' ||
          !/^[A-Za-z0-9_-]{1,64}$/.test(input.idempotency_key) ||
          (input.parent_id !== undefined &&
            (typeof input.parent_id !== 'string' || !/^[a-f0-9]{32}$/.test(input.parent_id)))
        )
          return Response.json({error: {code: 'invalid_request'}}, {status: 400});
        const prior = commands.get(input.idempotency_key);
        if (prior)
          return prior.body === body
            ? Response.json(prior.post)
            : Response.json({error: {code: 'idempotency_reused'}}, {status: 409});
        if (state === 'retry' && !firstFailure) {
          firstFailure = true;
          return Response.json({error: {code: 'temporarily_unavailable'}}, {status: 503});
        }
        const post = makePost(
          crypto.randomUUID().replaceAll('-', ''),
          input.body,
          typeof input.parent_id === 'string' ? input.parent_id : ''
        );
        commands.set(input.idempotency_key, {body, post});
        records.set(post.post_id, post);
        created++;
        if (post.parent_id) {
          const replies = threads.get(post.parent_id) ?? [];
          post.seq = (replies.at(-1)?.seq ?? 0) + 1;
          replies.push(post);
          threads.set(post.parent_id, replies);
        } else
          windowsEntries.unshift({
            kind: 'post',
            id: post.post_id,
            repository,
            seq: ++sequence,
            actor: '1',
            author: 'Alex',
            post,
          });
        if (state === 'unknown' && !firstFailure) {
          firstFailure = true;
          throw Error('The declared submission reply was lost.');
        }
        if (state === 'busy')
          await new Promise<void>((resolve, reject) => {
            const signal = init?.signal;
            const abort = () => {
              signal?.removeEventListener('abort', abort);
              reject(signal?.reason);
            };
            signal?.addEventListener('abort', abort, {once: true});
            releases.push(() => {
              signal?.removeEventListener('abort', abort);
              resolve();
            });
          });
        return Response.json(post, {status: 201});
      }
      const retry = /^factory\/feed\/posts\/([a-f0-9]{32})\/retry$/.exec(path);
      if (method === 'POST' && retry) {
        const post = records.get(retry[1]!);
        if (!post) return unavailable();
        const input = JSON.parse(body) as {
          command_id?: unknown;
          repository?: unknown;
          expected_revision?: unknown;
          body?: unknown;
        };
        if (
          typeof input.command_id !== 'string' ||
          !/^[a-f0-9]{32}$/.test(input.command_id) ||
          input.repository !== repository
        )
          return Response.json({error: {code: 'invalid_request'}}, {status: 400});
        const prior = revisions.get(input.command_id);
        if (prior)
          return prior.input === body
            ? Response.json(prior.receipt)
            : Response.json({error: {code: 'changed_command'}}, {status: 409});
        if (state === 'stale' && !firstFailure) {
          firstFailure = true;
          post.revision++;
          post.body = 'Current request changed elsewhere.';
        }
        if (input.expected_revision !== post.revision)
          return Response.json({error: {code: 'stale_revision'}}, {status: 409});
        if (typeof input.body !== 'string' || !input.body.trim() || input.body.length > 4000)
          return Response.json({error: {code: 'invalid_request'}}, {status: 400});
        post.body = input.body;
        post.revision++;
        revised++;
        const receipt = {
          command_id: input.command_id,
          repository,
          author: '1',
          post_id: post.post_id,
          expected_revision: input.expected_revision,
          body_digest: await feedRevisionBodyDigest(input.body),
          revision: post.revision,
        };
        revisions.set(input.command_id, {input: body, receipt});
        if (state === 'revision-unknown' && !firstFailure) {
          firstFailure = true;
          throw Error('The declared edit reply was lost.');
        }
        return Response.json(receipt);
      }
      return unavailable();
    },
  };
  const context: FactoryFeedContext = {
    repository,
    repositoryName: 'team/project',
    transport,
    host: {
      draftScope: 'storybook:local:alex:' + state,
      async repositories(_wanted, signal) {
        signal.throwIfAborted();
        return {refs: new Map([[repository, {path: root, label: 'team/project'}]]), listedAll: true};
      },
      links() {
        return {
          repository: root,
          feed: () => root,
          request: (id) => root + '/requests/' + id,
          issue: (number) => root + '/issues/' + number,
          pull: (number) => root + '/pulls/' + number,
          factoryStatus: (issue) => root + '/work/issues' + (issue === undefined ? '' : '/' + issue + '?tab=factory'),
        };
      },
      navigate() {
        /* Declared workbench navigation stays in the workbench. */
      },
    },
  };
  return {
    context,
    calls,
    release: () => releases.splice(0).forEach((release) => release()),
    ledger: () => ({created, revised, commands: commands.size}),
  };
}
