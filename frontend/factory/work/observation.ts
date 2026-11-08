import type {FactoryTransport} from '../api/transport.js';
import type {FactoryIssueDetail} from '../api/work-response.js';
import type {WorkJourney} from '../api/journey-response.js';
import type {FactoryAccess} from '../api/progress-response.js';

/** One screen refresh, not an atomic snapshot across provider reads. */
export interface WorkObservation {
  readonly transport: FactoryTransport;
  readonly generation: string;
  readonly repository: string;
  readonly issue: string;
  readonly refresh: number;
  readonly state: 'loading' | 'ready' | 'stale';
  readonly detail?: FactoryIssueDetail | undefined;
  readonly journey?: WorkJourney | undefined;
  readonly access?: FactoryAccess;
}

export function currentWorkObservation(context: {
  transport: FactoryTransport;
  repository: string;
  issue: string;
  observation?: WorkObservation;
}): WorkObservation | undefined {
  const observation = context.observation;
  return observation?.transport === context.transport &&
    observation.generation === context.transport.generation &&
    observation.repository === context.repository &&
    observation.issue === context.issue
    ? observation
    : undefined;
}

export function requestWorkRefresh(element: HTMLElement) {
  element.dispatchEvent(new CustomEvent('soda-factory-work-refresh', {bubbles: true, composed: true}));
}
