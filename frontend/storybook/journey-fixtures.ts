import {factoryIssueDetail} from '../factory/api/work-response.js';
import {workJourney} from '../factory/api/journey-response.js';
import type {FactoryTransport} from '../factory/api/transport.js';
import type {JourneyContext} from '../factory/work/journey.js';
import type {FactoryWorkLinks} from '../factory/app/navigation.js';

export type JourneyStoryState = 'recorded' | 'empty' | 'loading' | 'unavailable' | 'denied';
export const journeyRepository = '9007199254740993';
export const journeyIssue = '12';

export function journeyLinks(prefix = '/desktop'): FactoryWorkLinks {
  return {
    issue: (number) => `${prefix}/repositories/${journeyRepository}/issues/${number}`,
    pull: (number) => `${prefix}/repositories/${journeyRepository}/pulls/${number}`,
  };
}

export function journeyResponse() {
  return {
    repository: journeyRepository,
    issue: journeyIssue,
    journey: {
      repository: journeyRepository,
      issue: journeyIssue,
      observed_unix: 1720000100,
      phases: [{phase: 'implement', state: 'finished'}],
      tasks: [{key: 'release-checklist', lane: 'a', issue: journeyIssue, state: 'complete'}],
      commits: [{commit: 'a'.repeat(40), base: 'b'.repeat(40), pr_number: '21', pr_id: 'opaque-pull'}],
      findings: [],
      corrections: [],
      judgments: [],
      gates: [],
      adoptions: [],
      delivered_tasks: [],
      verifications: [],
    },
  };
}

/** Story hosts supply the same typed observation as a work-detail screen. */
export function journeyStoryContext(state: JourneyStoryState): JourneyContext {
  const transport: FactoryTransport = {
    generation: `storybook-journey:${state}`,
    assetBase: '/',
    async request() {
      throw Error('Journey presentation performs no reads.');
    },
  };
  return {
    repository: journeyRepository,
    issue: journeyIssue,
    links: journeyLinks(),
    transport,
    observation: {
      transport,
      generation: transport.generation,
      repository: journeyRepository,
      issue: journeyIssue,
      refresh: 1,
      state: state === 'loading' ? 'loading' : state === 'unavailable' || state === 'denied' ? 'stale' : 'ready',
      detail: factoryIssueDetail(
        {repository: journeyRepository, issue: journeyIssue, status: {valid: true}},
        journeyRepository,
        journeyIssue
      ),
      ...(state === 'recorded'
        ? {journey: workJourney(journeyResponse().journey, journeyRepository, journeyIssue)}
        : {}),
    },
  };
}
