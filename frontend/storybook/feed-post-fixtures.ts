import type {FactoryFeedContext} from '../factory/feed/feed.js';

// Feed pages that hold one design sheet's post types. Each specimen is one
// wire entry as the server serves it (snake_case); the parser camelizes it.
// Declared local service only: no backend, provider or worker effects.

export const feedPostRepository = '7';
const stamp = Math.floor(Date.now() / 1000);

export interface FeedPostSpecimen {
  /** The design sheet label, for example "ask · question". */
  readonly design: string;
  /** Text the card shows when this specimen renders. */
  readonly probe: string;
  readonly entry: Record<string, unknown>;
}

let serial = 0;

/** One feed entry: identity, repository and feed position are filled here. */
export function specimen(design: string, probe: string, entry: Record<string, unknown>): FeedPostSpecimen {
  const number = ++serial;
  const id = number.toString(16).padStart(32, '0');
  const post =
    entry.kind === 'post'
      ? {
          post_id: id,
          repository: feedPostRepository,
          author_name: entry.author,
          actor: entry.actor,
          at: stamp,
          ...(entry.post as Record<string, unknown>),
        }
      : undefined;
  return {
    design,
    probe,
    entry: {id, repository: feedPostRepository, seq: 100000 - number, ...entry, ...(post ? {post} : {})},
  };
}

/** A work record card: its communication, or one typed payload family. */
export function record(
  author: string,
  payload: Record<string, unknown>,
  state?: string,
  extra: Record<string, unknown> = {}
): Record<string, unknown> {
  return {kind: 'record', author, actor: author.toLowerCase().replaceAll(' ', '-'), state, payload, ...extra};
}

// The wire contract always sends a summary; one equal to the title is hidden by the card.
export function communication(fields: Record<string, unknown>): Record<string, unknown> {
  return {communication: {summary: fields.title, ...fields}};
}

/** A human post. Its pipeline is present when `post.judgment` is set. */
export function humanPost(
  author: string,
  stored: Record<string, unknown>,
  extra: Record<string, unknown> = {}
): Record<string, unknown> {
  return {kind: 'post', author, actor: '1', post: stored, ...extra};
}

/** Serves one sheet as the whole feed page. Replies and other endpoints answer empty or unavailable. */
export function feedPostStoryPeer(specimens: readonly FeedPostSpecimen[]) {
  const entries = structuredClone(specimens.map((item) => item.entry));
  const transport: FactoryFeedContext['transport'] = {
    generation: 'storybook-feed-post-types',
    assetBase: '/',
    async request(path, init) {
      const method = init?.method ?? 'GET';
      // A typed answer: the declared peer records it on its question, with
      // a stand-in for the server's plain-text rendering.
      if (method === 'POST' && path === 'factory/feed/posts' && typeof init?.body === 'string') {
        const sent = JSON.parse(init.body) as {parent_id?: string; answer?: Record<string, unknown>};
        const asked = entries.find((entry) => entry.id === sent.parent_id);
        const c = (asked?.payload as {communication?: Record<string, unknown>} | undefined)?.communication;
        if (!asked || !c?.answer_form || !sent.answer || c.answered)
          return Response.json({error: {code: 'question_answered'}}, {status: c?.answered ? 409 : 400});
        Object.assign(c, {
          question_state: 'answered',
          answered: {post: 'a5'.padStart(32, '0'), text: 'Answer recorded by the workbench peer.', answer: sent.answer},
        });
        asked.state = 'decided';
        return Response.json({post_id: 'a5'.padStart(32, '0'), revision: 0});
      }
      if (method === 'GET' && path === 'factory/feed/subscription')
        return Response.json({subscriber: '1', repositories: [feedPostRepository], revision: 1});
      if (method === 'GET' && path.startsWith('factory/feed?')) return Response.json({entries, next: '', truncated: false});
      if (method === 'GET' && /^factory\/feed\/posts\/[a-f0-9]{32}\/replies\?/.test(path))
        return Response.json({items: [], next_before: 0});
      if (method === 'GET' && path === 'repositories/' + feedPostRepository + '/factory') return Response.json({});
      return Response.json({error: {code: 'example_unavailable'}}, {status: 404});
    },
  };
  const context: FactoryFeedContext = {
    repository: feedPostRepository,
    repositoryName: 'team/project',
    transport,
    host: {
      draftScope: 'storybook:local:alex:feed-post-types',
      async repositories(_wanted, signal) {
        signal.throwIfAborted();
        return {refs: new Map([[feedPostRepository, {path: '/repositories/example', label: 'team/project'}]]), listedAll: true};
      },
      links() {
        const root = '/repositories/example';
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
  return {context, entries};
}

// The Asks sheet: questions, answers, proposals and drafts that need a decision.
export const asks: FeedPostSpecimen[] = [
  specimen(
    'ask · question',
    'Which fields should search match?',
    record(
      'Architect',
      communication({
        kind: 'ask',
        scope: '#12',
        title: 'Which fields should search match?',
        summary: 'Title only is fastest. Matching the source post finds more, and takes a larger index.',
        options: ['Title only', 'Title and type', 'Title, type and source post'],
        resolution: 'The index the Builder creates in #12.',
      })
    )
  ),
  specimen(
    'ask · critical or warning',
    'Removed items still appear in results',
    record(
      'Inspector',
      communication({
        kind: 'ask',
        severity: 'critical',
        scope: '#12',
        title: 'Removed items still appear in results',
        summary:
          'The pull request passes its checks, but a removed item stays in results until the next index pass.',
        question: 'Merge anyway?',
        question_state: 'open',
        options: ['Hold the merge until it is fixed', 'Merge now and fix it after'],
      }),
      'needs_you'
    )
  ),
  specimen(
    'answer · question and answer',
    'Search matches title, type and source post',
    record(
      'Architect',
      communication({
        kind: 'answer',
        scope: '#12',
        title: 'Search matches title, type and source post',
        question: 'Which fields should search match?',
        resolution: 'Title, type and source post.',
      }),
      'decided'
    )
  ),
  specimen(
    'suggest · proposal',
    'Do filters before paging',
    record(
      'Strategist',
      communication({
        kind: 'suggest',
        title: 'Do filters before paging',
        before: 'Paging first, then filters.',
        after: 'Filters first. Paging then applies to the filtered list.',
        rationale: 'Paging written first would be rewritten once filters change the list length.',
        alternatives: ['Build both in one work item.'],
      }),
      'needs_you'
    )
  ),
  specimen(
    'draft_repair · comparison',
    'Repair draft for the removed-items check',
    record(
      'Strategist',
      communication({
        kind: 'repair_draft',
        scope: '#12',
        title: 'Repair draft for the removed-items check',
        before: 'The index refreshes on a timer.',
        after: 'Removing an item updates the index in the same step.',
        rationale: 'It closes the gap the Inspector found without touching search itself.',
      }),
      'needs_you'
    )
  ),
  specimen(
    'reply · discussion',
    'The result list should keep its place when I go back to it.',
    record(
      'Builder',
      communication({
        kind: 'reply',
        title: 'Result list keeps its place',
        question: 'The result list should keep its place when I go back to it.',
        resolution: 'Done. Going back now returns to the same row.',
      }),
      undefined,
      {parent_id: 'a1ce'.padStart(32, '0'), parent_author: 'Alex'}
    )
  ),
  specimen(
    'assumption challenge · open',
    'Everyone on the project may search every library item.',
    record(
      'Factory',
      communication({
        kind: 'system',
        component_id: 'F13',
        scope: 'Build request',
        title: 'One consequential assumption still needs your decision',
        summary: 'Everyone on the project may search every library item.',
        assumption: {
          intent: 'd' + '1'.repeat(24),
          idea: 'd' + '2'.repeat(24),
          owner: '1',
          assumption: 'Everyone on the project may search every library item.',
          consequence: 'Search needs per-person visibility, which changes the index in #12.',
          recommendation: "Confirm whether 'Everyone on the project may search every library item.' holds, or explain the correction.",
          state: 'open',
        },
      }),
      'needs_you'
    )
  ),
  specimen(
    'assumption challenge · answered',
    'Removed items leave search at once.',
    record(
      'Factory',
      communication({
        kind: 'system',
        component_id: 'F13',
        scope: 'Build request',
        title: 'Assumption answered',
        summary: 'A removed item may stay in results until the next index pass.',
        assumption: {
          intent: 'd' + '1'.repeat(24),
          idea: 'd' + '3'.repeat(24),
          owner: '1',
          assumption: 'A removed item may stay in results until the next index pass.',
          consequence: 'If removal must be immediate, removing an item also updates the index.',
          recommendation:
            "Confirm whether 'A removed item may stay in results until the next index pass.' holds, or explain the correction.",
          state: 'answered',
          decision: 'edit',
          correction: 'Removed items leave search at once.',
        },
      }),
      'decided'
    )
  ),
];

// Typed questions (5.1): each answer kind open and answered. The answered
// copies carry the reply the server would serve; its text stands in for the
// server's plain-text rendering.
function typedAsk(
  design: string,
  author: string,
  title: string,
  form: Record<string, unknown>,
  answered?: {text: string; answer: Record<string, unknown>},
  extra: Record<string, unknown> = {}
): FeedPostSpecimen {
  return specimen(
    design,
    title,
    record(
      author,
      communication({
        kind: 'ask',
        scope: '#12',
        title,
        question: title,
        answer_form: form,
        question_state: answered ? 'answered' : 'open',
        ...(answered ? {answered: {post: 'b0b'.padStart(32, '0'), ...answered}} : {}),
        ...extra,
      }),
      answered ? 'decided' : 'needs_you'
    )
  );
}
const layouts = {
  kind: 'pick_one',
  options: [
    {id: 'in-place', label: 'Open in place', detail: 'The row expands inside the list. Nothing else moves.'},
    {id: 'beside', label: 'Open beside the list', detail: 'The list stays on the left; the result opens on the right.'},
  ],
};
const merge = {kind: 'yes_no', yes_means: 'search ships now and the fix follows in #16.', no_means: '#15 waits for the fix.'};
const feel = {kind: 'score', low: 0, high: 4, low_label: 'Hard', high_label: 'Effortless'};
const changes = {
  kind: 'pick_several',
  options: [
    {id: 'split', label: 'Split #12 into index and query'},
    {id: 'defer', label: 'Defer export to the next request'},
    {id: 'drop', label: 'Drop fuzzy matching'},
  ],
  max_picks: 2,
};
const pageSize = {kind: 'value', label: 'Results before “load more”', unit: 'rows', number: true};
export const typedQuestions: FeedPostSpecimen[] = [
  typedAsk('ask · pick one · open', 'Architect', 'How should a result open?', layouts),
  typedAsk('ask · pick one · answered', 'Architect', 'How should a result open next to filters?', layouts, {
    text: 'Open beside the list',
    answer: {kind: 'pick_one', choice: 'beside'},
  }),
  typedAsk('ask · yes or no · open', 'Inspector', 'Merge #15 before the removed-items fix?', merge, undefined, {
    severity: 'warning',
    summary: 'The pull request passes its checks; removed items stay in results until the next index pass.',
  }),
  typedAsk('ask · yes or no · answered', 'Inspector', 'Merge #14 before the paging fix?', merge, {
    text: 'No',
    answer: {kind: 'yes_no', yes: false},
  }),
  typedAsk('ask · score · open', 'Test Pilot', 'How does finding an item feel?', feel),
  typedAsk('ask · score · answered', 'Test Pilot', 'How does filtering feel?', feel, {
    text: '3 on a scale of 0 to 4 (0 = Hard, 4 = Effortless)',
    answer: {kind: 'score', score: 3},
  }),
  typedAsk('ask · pick several · open', 'Strategist', 'Which plan changes do you accept?', changes),
  typedAsk('ask · pick several · answered', 'Strategist', 'Which plan changes do you accept for #13?', changes, {
    text: 'Split #12 into index and query; Drop fuzzy matching',
    answer: {kind: 'pick_several', choices: ['split', 'drop']},
  }),
  typedAsk('ask · value · open', 'Builder', 'How many results before “load more”?', pageSize),
  typedAsk('ask · value · answered', 'Builder', 'How many library items before “load more”?', pageSize, {
    text: 'Results before “load more”: 30 rows',
    answer: {kind: 'value', value: '30'},
  }),
];

// The Progress sheet: work that is moving, checks, unlocks and handovers.
export const progress: FeedPostSpecimen[] = [
  specimen(
    'partial · progress',
    'Search is taking shape',
    record(
      'Builder',
      communication({
        kind: 'partial',
        scope: '#13',
        title: 'Search is taking shape',
        summary: 'The query and result list are connected.',
        completed: ['Query box reads the index', 'Result list shows title, type and source'],
        remaining: ['Paging after the first 20 items'],
        resolution: 'Paging, then hand over for review.',
      }),
      'in_progress'
    )
  ),
  specimen(
    'Factory · checks (Preparation, F01)',
    'Dependency download',
    record(
      'Factory',
      communication({
        kind: 'system',
        component_id: 'F01',
        title: 'Preparation',
        summary: 'Workspace and dependencies are ready.',
        items: [
          {label: 'Workspace setup', state: 'passed'},
          {label: 'Dependency download', state: 'passed'},
          {label: 'Required tools', state: 'pending'},
        ],
      })
    )
  ),
  specimen(
    'Factory · unlock (Ready, F07)',
    'Index library items for search',
    record(
      'Factory',
      communication({
        kind: 'system',
        component_id: 'F07',
        title: '#13 Query box and result list',
        summary: 'Says which finished work unblocked which next work.',
        items: [{label: '#12 Index library items for search', state: 'merged'}],
      }),
      'in_progress'
    )
  ),
  specimen(
    'finish_incomplete · incomplete',
    'Builder stopped before paging was finished',
    record(
      'Builder',
      communication({
        kind: 'finish_incomplete',
        title: 'Builder stopped before paging was finished',
        summary: 'A run that ended early.',
        completed: ['Query box', 'Result list'],
        remaining: ['Paging after 20 items'],
        resolution: 'The page size is set; the “load more” control is not wired yet.',
      }),
      'problem'
    )
  ),
  specimen(
    'system · brief',
    'People on the project can search the library',
    record(
      'Product Lead',
      communication({
        kind: 'system',
        title: 'Brief: search the project library',
        summary: 'People on the project can search the library and find older work by title, type or the post it came from.',
        scope: 'project library',
        rationale: 'Search first, filters after.',
        items: [
          {label: 'Index library items for search'},
          {label: 'Query box and result list'},
          {label: 'Filter by item type and source post'},
        ],
      })
    )
  ),
  specimen(
    'journey',
    'Open a result, then go back',
    record(
      'Test Pilot',
      communication({
        kind: 'journey',
        title: 'Result list keeps its place when going back',
        summary: 'The steps a person would take, in order, with what happened at each.',
        items: [
          {label: 'Open the library', state: 'passed'},
          {label: 'Type “search” in the query box', state: 'passed'},
          {label: 'Open a result, then go back', state: 'failed'},
        ],
        before: 'The list returns to the same row.',
        after: 'The list jumps back to the top.',
      })
    )
  ),
  specimen(
    'finish_product_plan · work map',
    'Index library items',
    record('Product Lead', {
      plan: {
        approach: 'Search first, filters after.',
        tasks: [
          {id: 'index', objective: 'Index library items', lane: 'library'},
          {id: 'query-results', objective: 'Query box and result list', depends_on: ['index']},
          {id: 'filters', objective: 'Filters', depends_on: ['query-results']},
        ],
      },
    })
  ),
  specimen(
    'commits · saved changes',
    'Connect the query box to the index',
    record(
      'Builder',
      communication({
        kind: 'partial',
        title: 'Builder saved two commits',
        summary: 'Recorded commits are kept for review.',
        commits: [
          {revision: '4c1e9ab2a07d0f3e', summary: 'Connect the query box to the index'},
          {revision: 'a07d51f3c9e2b104', summary: 'Show title, type and source in results'},
        ],
      }),
      'in_progress'
    )
  ),
];

// The Problems sheet: stops, findings, failed checks and diagnoses.
export const problems: FeedPostSpecimen[] = [
  specimen(
    'blocked · blocker',
    'Filters cannot start until the result list is merged',
    record(
      'Builder',
      communication({
        kind: 'blocked',
        title: 'Filters cannot start until the result list is merged',
        summary: 'Starts by itself when what it waits for is ready.',
        items: [{label: '#13 is still in review', detail: 'Filters read the result list that #13 adds.'}],
        resolution: '#13 is merged.',
      }),
      'waiting'
    )
  ),
  specimen(
    'Factory · blocker (Waiting on allowance, F12)',
    'Verification limits are used up for #13',
    record(
      'Factory',
      communication({
        kind: 'system',
        component_id: 'F12',
        title: 'Verification limits are used up for #13',
        summary: 'Both corrections allowed for this attempt were used. No further review can start.',
        resolution: 'An owner increases the verification limits.',
      }),
      'needs_you'
    )
  ),
  specimen(
    'flag · finding',
    'Removed items stay in search results',
    record(
      'Inspector',
      communication({
        kind: 'flag',
        title: 'Removed items stay in search results',
        summary: 'Removing an item leaves it in results until the next index pass.',
        source: {
          path: 'library/search-index.ts',
          revision: '4c1e9ab2a07d0f3e',
          start_line: 41,
          code: 'export function removeItem(id) {\n  store.delete(id);\n}',
          highlight_lines: [42],
        },
        before: 'Removing an item removes it from results.',
        after: 'It stays until the next index pass.',
        rationale: 'People open items that no longer exist.',
        resolution: 'Update the index in the same step as the removal.',
      }),
      'problem'
    )
  ),
  specimen(
    'review record · findings',
    'Removal does not update the index.',
    record(
      'Inspector',
      {
        review: {
          verdict: 'changes_requested',
          summary: 'Changes are needed before this pull request can merge.',
          new_findings: [
            {
              key: 'removed-items',
              severity: 'major',
              defect: 'Removal does not update the index.',
              location: {path: 'library/search-index.ts', line: 42},
              required_outcome: 'A removed item is absent from the next search.',
              evidence: 'Search for a removed title still returns it.',
            },
          ],
          clear: {
            head: '4c1e9ab2a07d0f3e',
            assessments: [{key: 'Changed files read', status: '3 of 3', evidence: '1 finding'}],
          },
        },
      },
      'in_progress'
    )
  ),
  specimen(
    'stuck · record_diagnosis · diagnosis',
    'Fixer is stuck on the removed-items check',
    record(
      'Fixer',
      communication({
        kind: 'stuck',
        title: 'Fixer is stuck on the removed-items check',
        attempts: ['Updating the index inside removeItem', 'Running the index pass after every removal'],
        items: [
          {label: 'The check reads a cached result', state: 'unverified'},
          {label: 'Two index copies exist', state: 'not confirmed'},
        ],
        rationale: 'The fix works, but the check may be reading an older result.',
        resolution: 'Where the check gets its result list.',
      }),
      'problem'
    )
  ),
  specimen(
    'diagnosis · Strategist',
    'Strategist diagnosis: the check reads a cached result',
    record(
      'Strategist',
      communication({
        kind: 'diagnosis',
        title: 'Strategist diagnosis: the check reads a cached result',
        attempts: ['Reading the check output'],
        items: [{label: 'The check reads a cached result', state: 'unverified'}],
        rationale: 'The result list is cached between runs.',
        resolution: 'Clear the cache before the check runs.',
      }),
      'problem'
    )
  ),
  specimen(
    'check record · application checks',
    'List keeps its place after going back',
    record(
      'Test Pilot',
      {
        verification: {
          outcome: 'failed',
          checks: [
            {id: 'Query returns matching items', outcome: 'passed'},
            {id: 'List keeps its place after going back', outcome: 'failed'},
          ],
          checks_total: 2,
          failures: [{check: 'List keeps its place after going back', detail: 'The list returned to the top.'}],
        },
      },
      'problem'
    )
  ),
];
