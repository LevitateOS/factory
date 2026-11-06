import {check, object} from './models.js';

export interface CommandOutcome {
  effect: string;
  cancellation: string;
  completion: string;
  finished: boolean;
}
export function commandOutcome(value: unknown, command: string): CommandOutcome {
  const row = object(value);
  check(row.command_id === command && typeof row.finished === 'boolean');
  check(
    Object.keys(row).every((key) =>
      ['command_id', 'observation', 'effect', 'cancellation', 'completion', 'finished'].includes(key)
    )
  );
  check(['found', 'not_observed', 'unavailable', 'unknown'].includes(String(row.observation)));
  if (row.observation !== 'found') {
    check(!row.finished && row.effect === undefined && row.cancellation === undefined && row.completion === undefined);
    return {effect: '', cancellation: '', completion: '', finished: false};
  }
  check(['pending', 'not_committed', 'committed', 'indeterminate'].includes(String(row.effect)));
  check(['none', 'pending', 'cancelled', 'too_late', 'indeterminate'].includes(String(row.cancellation)));
  check(['pending', 'complete', 'needs_intervention'].includes(String(row.completion)));
  const terminal = (row.effect === 'committed' || row.effect === 'not_committed') && row.completion === 'complete';
  check(row.finished === terminal && (row.completion !== 'complete' || terminal));
  check(row.effect !== 'committed' || row.cancellation !== 'cancelled');
  return {
    effect: row.effect as string,
    cancellation: row.cancellation as string,
    completion: row.completion as string,
    finished: row.finished,
  };
}
