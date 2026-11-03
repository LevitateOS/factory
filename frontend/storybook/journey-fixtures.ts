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

/** A declared read peer only; no discovery, login, provider or worker calls. */
export function journeyStoryContext(state: JourneyStoryState): JourneyContext {
  return {
    repository: journeyRepository,
    issue: journeyIssue,
    links: journeyLinks(),
    transport: {
      generation: `storybook-journey:${state}`,
      assetBase: '/',
      async request(path, init) {
        init?.signal?.throwIfAborted();
        if (path !== `repositories/${journeyRepository}/factory/issues/${journeyIssue}` || init?.method !== 'GET')
          return Response.json({error: {code: 'not_found'}}, {status: 404});
        if (state === 'loading') {
          const signal = init.signal;
          if (!signal) throw Error('A read lifetime is required.');
          return new Promise((_, reject) => {
            signal.addEventListener('abort', () => reject(signal.reason), {once: true});
          });
        }
        if (state === 'unavailable' || state === 'denied')
          return Response.json({error: {code: state}}, {status: state === 'denied' ? 403 : 503});
        return Response.json(
          state === 'empty' ? {repository: journeyRepository, issue: journeyIssue} : journeyResponse()
        );
      },
    },
  };
}
