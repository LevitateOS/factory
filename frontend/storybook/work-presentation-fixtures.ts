import {html} from 'lit';
import type {FactoryWorkLinks} from '../factory/app/navigation.js';
import type {IntentRevision} from '../factory/api/request-response.js';
import {factoryIssueDetail} from '../factory/api/work-response.js';
import {factoryAccess, issuePlan, requestOverview, revisionHistory} from '../factory/api/progress-response.js';
import {
  describeIssueDetail,
  describeIntervention,
  renderWorkDetail,
  renderInterventions,
} from '../factory/work/work-view.js';
import {
  describeWorkOverview,
  renderWorkOverview,
  describeRequestOverview,
  renderRequestOverview,
  describeWhyNext,
  describePrerequisites,
  renderWorkPlan,
  describeRecordedChoice,
  renderRecordedChoice,
  describeDirectionRevision,
  renderDirectionRevision,
  describeAccessStanding,
  renderAccessStanding,
  describeRevisionHistory,
  renderRevisionHistory,
} from '../factory/work/progress-view.js';

export type PresentationState = 'recorded' | 'blocked' | 'delivered' | 'missing' | 'unavailable';
export type PresentationView =
  | 'detail'
  | 'request'
  | 'plan'
  | 'overview'
  | 'direction'
  | 'access'
  | 'history'
  | 'interventions';
export const presentationRepository = '9007199254740993';
const intent = 'd' + 'a'.repeat(24);
const observed = 1720000300;

export function presentationLinks(prefix = '/desktop'): FactoryWorkLinks {
  return {
    issue: (number) => `${prefix}/repositories/${presentationRepository}/issues/${number}`,
    pull: (number) => `${prefix}/repositories/${presentationRepository}/pulls/${number}`,
  };
}

/** Declared read facts only. No transport, credentials, provider or worker. */
export function presentationDetail(state: PresentationState) {
  return factoryIssueDetail(
    {
      repository: presentationRepository,
      issue: '12',
      observed_unix: observed,
      status: {valid: true},
      closure: {state: state === 'delivered' ? 'confirmed' : 'none'},
      readiness: {
        readiness: state === 'blocked' ? 'blocked' : 'queued',
        assessed_unix: observed - 60,
        blockers:
          state === 'blocked'
            ? [
                {
                  code: 'prereq_code_pending',
                  resolution: 'Wait for the prerequisite code.',
                  endpoint_repository: presentationRepository,
                  endpoint_issue: '11',
                },
              ]
            : [],
      },
      checks: {verdict: 'pass', pr_number: '21', head_oid: 'a'.repeat(40), base_oid: 'b'.repeat(40), results: []},
      merge: {stage: state === 'delivered' ? 'merged' : 'open', pr_number: '21'},
      // A host must consciously adopt wire URL hints; shared views use its port.
      links: {issue_url: '/recorded-forge/issues/12', pull_url: '/recorded-forge/pulls/21'},
    },
    presentationRepository,
    '12'
  );
}

export function presentationRequest() {
  return requestOverview(
    {
      intent,
      revision: 'd' + 'b'.repeat(24),
      state: 'accepted',
      observed_unix: observed,
      direction: {objective: 'Make release readiness visible.', tasks_total: 1, unresolved_assumptions: 0},
      context: {repository: presentationRepository},
      generation: 'committed',
      setup: {state: 'ready'},
      work_items: {by_state: {waiting: 1}, total: 1, outcomes_total: 1, outcomes_evidenced: 0},
      attention: [{code: 'work_attention', issue: '12'}],
      mappings: [{task_key: 'release-checklist', issue_id: 'opaque-issue', issue_index: '12', effective: true}],
    },
    intent
  );
}

/** The same model/render functions as Fountain; no second template. */
export function renderPresentation(
  view: PresentationView,
  state: PresentationState,
  links: FactoryWorkLinks | undefined
) {
  const source = state === 'missing' || state === 'unavailable' ? state : 'ready';
  const detail = presentationDetail(state);
  const revision: IntentRevision = {
    id: 'd' + 'b'.repeat(24),
    intent,
    repository: presentationRepository,
    objective: 'Make release readiness visible.',
    state: 'accepted',
    max_issues: 4,
    ideas: [],
    tasks: [],
  };
  if (view === 'detail') return renderWorkDetail(describeIssueDetail(detail, links));
  if (view === 'request') return renderRequestOverview(describeRequestOverview(presentationRequest(), links));
  if (view === 'plan') {
    const plan = issuePlan(
      {
        comparisons: [
          {
            candidates: [{issue: '12', repository: presentationRepository, age_seconds: 120, readiness: 'queued'}],
            selected: '12',
            selected_visible: true,
            candidates_total: 1,
            calls: [],
          },
        ],
      },
      presentationRepository,
      '12'
    );
    return html`${renderWorkPlan(describeWhyNext([detail], links, observed), describePrerequisites([detail], links))}${renderRecordedChoice(describeRecordedChoice(plan, links))}`;
  }
  if (view === 'overview')
    return renderWorkOverview(
      describeWorkOverview({
        ...(source === 'ready' ? {revision} : {}),
        links,
        intentState: source,
        generationState: 'missing',
        summaryState: source,
      })
    );
  if (view === 'direction')
    return renderDirectionRevision(describeDirectionRevision(source === 'ready' ? revision : undefined, source));
  if (view === 'access')
    return renderAccessStanding(
      describeAccessStanding(
        source === 'ready'
          ? factoryAccess(
              {
                repository: presentationRepository,
                viewer: {code_write: true, session_current: true},
                effective: {effective: true, dispatch_open: false, missing: []},
                required_actions: [],
              },
              presentationRepository
            )
          : undefined,
        source
      )
    );
  if (view === 'history')
    return renderRevisionHistory(
      describeRevisionHistory(
        source === 'ready'
          ? revisionHistory(
              {
                revisions: [
                  {
                    revision: revision.id,
                    repository: presentationRepository,
                    state: 'accepted',
                    created_unix: observed,
                  },
                ],
                truncated: false,
              },
              intent
            )
          : undefined,
        source
      )
    );
  return renderInterventions(
    [
      describeIntervention({
        kind: 'pause',
        target: presentationRepository,
        enabled: false,
        result: state === 'blocked' ? 'uncertain' : 'idle',
        resultText:
          state === 'blocked' ? 'No operation was confirmed.' : 'No action adapter is connected in this story.',
      }),
    ],
    {
      act: () => {
        throw Error('No story action adapter is connected.');
      },
    }
  );
}
