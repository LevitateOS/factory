import {
  asks,
  communication,
  humanPost,
  namedRoles,
  problems,
  progress,
  record,
  specimen,
  typedQuestions,
} from './feed-post-fixtures.js';
import type {FeedPostSpecimen} from './feed-post-fixtures.js';

// The Results sheet: finished work, merges and handovers.
export const results: FeedPostSpecimen[] = [
  specimen(
    'Factory · result (Work created, F05)',
    'Three issues created',
    record(
      'Factory',
      communication({
        kind: 'system',
        component_id: 'F05',
        title: 'Three issues created for “Search the project library”',
        summary: 'Work is recorded for the request.',
        items: [
          {label: '#12 Index library items for search'},
          {label: '#13 Query box and result list'},
          {label: '#14 Filter by item type and source post'},
        ],
        resolution: '#12 starts first. The others wait for it.',
      }),
      'done'
    )
  ),
  specimen(
    'Factory · merge (Merged, F06)',
    'SodaOS merged pull request #15',
    record(
      'Factory',
      communication({
        kind: 'system',
        component_id: 'F06',
        title: 'SodaOS merged pull request #15 and closed issue #12',
        summary: 'Review and required checks passed.',
        items: [{label: 'Pull request #15 into the target branch'}],
        resolution: 'Closure confirmed for issue #12.',
      }),
      'done'
    )
  ),
  specimen(
    'Test Pilot · result (App ready, F10)',
    'The indexed library is up to try',
    record(
      'Test Pilot',
      communication({
        kind: 'system',
        component_id: 'F10',
        title: 'The indexed library is up to try',
        summary: 'This version includes #12. Search itself arrives with #13.',
      }),
      'done'
    )
  ),
  specimen(
    'Builder · artifact',
    'search-notes.txt',
    record(
      'Builder',
      communication({
        kind: 'artifact',
        title: 'Notes on the result list',
        items: [
          {
            label: 'search-notes.txt',
            detail:
              'Query box reads the library index. Result list shows title, type and source post. Older items page in after the first 20.',
          },
        ],
        artifacts: [
          {
            id: '1'.padStart(32, '1'),
            name: 'search-notes.txt',
            media_type: 'text/plain',
            href: '/artifacts/search-notes.txt',
          },
        ],
      }),
      'info'
    )
  ),
  specimen(
    'Fixer · correction',
    'Fixer corrected the removed-items finding',
    record(
      'Fixer',
      communication({
        kind: 'partial',
        title: 'Fixer corrected the removed-items finding',
        source: {
          path: 'library/search-index.ts',
          revision: '4c1e9ab2a07d0f3e',
          start_line: 41,
          code: 'export function removeItem(id) {\n  store.delete(id);\n}',
          after: 'export function removeItem(id) {\n  store.delete(id);\n  index.remove(id);\n}',
        },
        rationale: 'The index now changes in the same step as the removal.',
        resolution: 'Candidate b83f20c9d1e4 submitted for review.',
      }),
      'done'
    )
  ),
];

// The Decisions sheet: choices, JEV judgments, knowledge and plan changes.
export const decisions: FeedPostSpecimen[] = [
  specimen(
    'record_decision · decision',
    'A working search answers the request on its own',
    record(
      'Architect',
      communication({
        kind: 'record_decision',
        title: 'Search first, filters after',
        decision: 'Search first, filters after.',
        rationale: 'A working search answers the request on its own. Filters depend on the result list.',
        items: [{label: 'Search first, filters after'}, {label: 'One combined search screen'}],
        alternatives: ['One combined search screen'],
        decisionOwnerName: 'Alex',
      }),
      'decided'
    )
  ),
  specimen(
    'JEV · judgment (Why this work is next)',
    'Issue #13 · Query box and result list',
    record(
      'JEV',
      {
        judgment: {
          kind: 'priority',
          selected: '13',
          confidence: 0.82,
          effective: 'SodaOS runs the top pick next. The rest wait their turn.',
          probabilities: {'13': 0.71, '11': 0.22, '14': 0.07},
          candidates: [
            {id: '13', issue: 13, objective: 'Query box and result list'},
            {id: '11', issue: 11, objective: 'Release tasks that need review'},
            {id: '14', issue: 14, objective: 'Filters'},
          ],
        },
      },
      'decided'
    )
  ),
  specimen(
    'JEV · delivery judgment (JEV assessment)',
    'Ready to merge',
    record(
      'JEV',
      {
        judgment: {
          kind: 'delivery',
          selected: 'ready',
          confidence: 0.64,
          effective: 'JEV is not sure enough to merge #15 on its own. Waits for you.',
          probabilities: {ready: 0.64, review: 0.36},
          candidates: [
            {id: 'ready', objective: 'Ready to merge'},
            {id: 'review', objective: 'Review again'},
          ],
        },
      },
      'needs_you'
    )
  ),
  specimen(
    'learned · knowledge',
    'Removing an item must update the search index',
    record(
      'Library Admin',
      communication({
        kind: 'learned',
        title: 'Removing an item must update the search index',
        scope: 'library search',
        decision: 'Removing an item must update the search index in the same step.',
        rationale: 'A timed index pass leaves removed items visible in between.',
        resolution: 'Any work adds, changes or removes library items.',
      }),
      'info'
    )
  ),
  specimen(
    'library_maintenance · maintenance',
    'The search index updates with each change',
    record(
      'Library Admin',
      communication({
        kind: 'library_maintenance',
        title: 'The search index updates with each change',
        before: 'The index refreshes on a timer.',
        after: 'The index updates with each change.',
        rationale: 'The correction to #12 replaced the timer.',
      }),
      'info'
    )
  ),
  specimen(
    'plan record · proposed plan changes',
    'Add: keep list position on return',
    record(
      'Strategist',
      {
        reassessment: {
          changes: [
            {reason: 'Feedback on #13 asked for it.', tasks: [{key: 'list-position', objective: 'Add: keep list position on return'}]},
            {reason: 'Paging changes the list filters read.', tasks: [{key: 'filters', objective: 'Move filters after paging'}]},
          ],
          changes_total: 2,
          retained_issues: [12],
        },
      },
      'needs_you'
    )
  ),
];

// The Run and plan records sheet: what each role posts when a turn finishes.
export const runRecords: FeedPostSpecimen[] = [
  specimen(
    'finish_product_plan · candidate plans',
    'Search first, filters after',
    record(
      'Product Lead',
      {
        decomposition: {
          candidates: [
            {
              key: 'Search first, filters after',
              rationale: 'A working search ships before any filtering.',
              tasks: [
                {key: 'index', objective: 'Index library items'},
                {key: 'query-results', objective: 'Query and results', depends_on: ['index']},
                {key: 'filters', objective: 'Filters', depends_on: ['query-results']},
              ],
            },
            {
              key: 'One combined search screen',
              rationale: 'Query, filters and results arrive together, later.',
              tasks: [
                {key: 'index', objective: 'Index library items'},
                {key: 'search-screen', objective: 'Search screen', depends_on: ['index']},
              ],
            },
          ],
        },
      },
      'needs_you'
    )
  ),
  specimen(
    'finish_issue_plan · issue plan',
    'Read the index from #12',
    record(
      'Architect',
      {
        plan: {
          approach: 'Read the index from #12, then build the query box and the list as two tasks.',
          tasks: [
            {id: 'query-box', objective: 'Query box'},
            {id: 'result-list', objective: 'Result list', depends_on: ['query-box']},
            {id: 'paging', objective: 'Paging', depends_on: ['result-list']},
          ],
          inspected: [
            {path: 'library/search-index.ts', evidence: 'The index exposes a query function that returns item ids.'},
          ],
          findings: ['Items carry their source post, so results can show it.'],
          unresolved: ['How a result opens: in place, or beside the list.'],
        },
      },
      'done'
    )
  ),
  specimen(
    'finish_implementation · start_task · submit_task_output',
    'Candidate recorded for the next review.',
    record(
      'Builder',
      {
        implementation: {candidate: 'b83f20c9d1e4f7a2'},
        tasks: {
          tasks: [
            {id: 'query-box', state: 'complete'},
            {id: 'result-list', state: 'complete'},
            {id: 'paging', state: 'complete'},
          ],
        },
      },
      'done'
    )
  ),
  specimen(
    'finish_review · approval',
    'Inspector approved pull request #15',
    record(
      'Inspector',
      {
        review: {
          verdict: 'approved',
          summary: 'Inspector approved pull request #15',
          clear: {
            head: '4c1e9ab2a07d0f3e',
            assessments: [{key: 'Changed files read', status: '3 of 3', evidence: 'No findings'}],
          },
        },
      },
      'done'
    )
  ),
  specimen(
    'Factory · run lifecycle (Work started, F02)',
    'The worker assignment and its run are recorded',
    record(
      'Factory',
      communication({
        kind: 'system',
        component_id: 'F02',
        title: 'Builder assigned',
        summary: 'The worker assignment and its run are recorded. Startup is next.',
        items: [{label: 'Worker assignment recorded', state: '09:02'}],
      }),
      'in_progress'
    )
  ),
  specimen(
    'Factory · pause, stop and takeover (F08)',
    'An owner paused Factory work',
    record(
      'Factory',
      communication({
        kind: 'system',
        component_id: 'F08',
        title: 'Factory paused',
        summary: 'An owner paused Factory work for this repository.',
        items: [{label: 'Worker dispatch', state: 'closed'}],
        resolution: 'Open work to resume.',
      }),
      'waiting'
    )
  ),
  specimen(
    'Factory · run problem (F09)',
    'Worker run failed',
    record(
      'Factory',
      communication({
        kind: 'system',
        component_id: 'F09',
        title: 'Worker run failed',
        summary: 'start-unconfirmed',
        resolution: 'Needs attention before dependent work continues.',
      }),
      'problem'
    )
  ),
];

// The Closing the loop sheet: what happened to a suggestion, an answer or a post.
export const closingTheLoop: FeedPostSpecimen[] = [
  specimen(
    'resolve_post · applied',
    'Do filters before paging',
    record(
      'Strategist',
      communication({
        kind: 'suggest',
        title: 'Do filters before paging',
        before: 'Paging first, then filters.',
        after: 'Filters first. Paging then applies to the filtered list.',
        disposition: 'applied',
        disposition_reason: 'Filters are built first in #14; paging follows on the filtered list.',
      }),
      'decided'
    )
  ),
  specimen(
    'resolve_post · refused',
    'Rebuild the whole index on every removal',
    record(
      'Inspector',
      communication({
        kind: 'suggest',
        title: 'Rebuild the whole index on every removal',
        before: 'Entries leave the index one at a time.',
        after: 'Rebuild the whole index on every removal.',
        disposition: 'refused',
        disposition_reason:
          'Removing one entry is enough and keeps removal fast. A full rebuild would slow every delete.',
      }),
      'decided'
    )
  ),
  specimen(
    'ask · reply received',
    'Which fields should search match?',
    record(
      'Architect',
      communication({
        kind: 'ask',
        severity: 'warning',
        scope: '#12',
        title: 'Which fields should search match?',
        question: 'Which fields should search match?',
        question_state: 'replied',
        summary: 'Alex replied: title, type and source post.',
      }),
      'needs_you'
    )
  ),
  specimen(
    'ask · ordinary, reply received',
    'Should search ignore case?',
    record(
      'Architect',
      communication({
        kind: 'ask',
        scope: '#12',
        title: 'Should search ignore case?',
        question: 'Should search ignore case?',
        question_state: 'replied',
        summary: 'Alex replied: yes, ignore case.',
      }),
      'decided'
    )
  ),
  specimen(
    'ask · reply received, then incorporated',
    'Which fields should search match?',
    record(
      'Architect',
      communication({
        kind: 'ask',
        scope: '#12',
        title: 'Which fields should search match?',
        question: 'Which fields should search match?',
        question_state: 'incorporated',
        summary: 'Alex replied: title, type and source post.',
      }),
      'decided'
    )
  ),
  specimen(
    'ask · urgent, incorporated',
    'Should removed entries keep their history?',
    record(
      'Architect',
      communication({
        kind: 'ask',
        severity: 'warning',
        scope: '#12',
        title: 'Should removed entries keep their history?',
        question: 'Should removed entries keep their history?',
        question_state: 'incorporated',
        summary: 'Alex replied: keep it for 30 days.',
      }),
      'decided'
    )
  ),
  specimen(
    'suggest · incorporated',
    'Show the filter count beside the heading',
    record(
      'Strategist',
      communication({
        kind: 'suggest',
        title: 'Show the filter count beside the heading',
        before: 'The count appears below the list.',
        after: 'The count appears beside the heading.',
        disposition: 'incorporated',
        disposition_reason: 'The count now sits beside the heading in #14.',
      }),
      'decided'
    )
  ),
  specimen(
    'create_issue · refine_issue (Issue Maker)',
    'Writing issues for “finding older work”',
    record(
      'Issue Maker',
      communication({
        kind: 'partial',
        title: 'Writing issues for “finding older work”',
        summary: 'One post per request, updated as each issue is created or refined.',
        items: [
          {label: '#12 Index library items for search', state: 'created'},
          {label: '#13 Query box and result list', state: 'refined'},
          {
            label: '#14 Filter by item type and source post',
            state: 'not refined',
            detail: '#14 was edited by a person after it was created, so the Issue Maker left it as it is.',
          },
        ],
      }),
      'in_progress'
    )
  ),
  specimen(
    'JEV reply · clarify',
    'Make search better.',
    humanPost('Alex', {
      body: 'Make search better.',
      judgment: 'applied',
      intake: 'recorded',
      route: 'clarify',
      draft: 'none',
      effect: 'none',
      revision: 1,
      can_retry: true,
    })
  ),
  specimen(
    'JEV reply · failed',
    'Search thing from before, same as last time.',
    humanPost('Alex', {
      body: 'Search thing from before, same as last time.',
      judgment: 'failed',
      intake: 'recorded',
      route: '',
      draft: 'none',
      effect: 'none',
      revision: 1,
      can_retry: true,
    })
  ),
  specimen(
    'JEV reply · cannot run (Not connected)',
    'Could we make finding older work easier?',
    humanPost('Alex', {
      body: 'Could we make finding older work easier?',
      judgment: 'applied',
      intake: 'recorded',
      route: 'new_issue',
      draft: 'pending',
      stalled: 'product_lead',
      effect: 'none',
      revision: 1,
      can_retry: false,
    })
  ),
  specimen(
    'JEV reply · proposed change',
    'Search should also match the text inside an item',
    humanPost('Alex', {
      body: 'Search should also match the text inside an item, not just its title.',
      judgment: 'applied',
      intake: 'recorded',
      route: 'new_issue',
      draft: 'applied',
      effect: 'none',
      revision: 1,
      can_retry: false,
      product_change: {
        intent: 'd' + '1'.repeat(24),
        accepted_revision: 'd' + '2'.repeat(24),
        state: 'review_required',
        can_review: true,
      },
    })
  ),
];

export const feedPostSheets = {
  asks,
  typedQuestions,
  namedRoles,
  progress,
  problems,
  results,
  decisions,
  runRecords,
  closingTheLoop,
} as const;
export type FeedPostSheet = keyof typeof feedPostSheets;
