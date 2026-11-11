import {check, object} from './models.js';

export type CommandEffect = 'pending' | 'committed' | 'not_committed' | 'indeterminate';
/** What a command lookup/submit/cancel answer means; every accepted combination maps to one variant. */
export type CommandOutcome =
  /** The journal did not observe the command (not_observed, unavailable, unknown). */
  | {kind: 'not_found'}
  /** Found, no commit confirmed yet (effect pending, indeterminate, or not_committed while completion is pending). */
  | {kind: 'pending'}
  | {kind: 'needs_intervention'; effect: CommandEffect}
  /** Effect committed, completion still pending. */
  | {kind: 'committed_completing'}
  | {kind: 'finished'; effect: 'committed' | 'not_committed'; cancelled: boolean};
export const outcomeCommitted = (outcome: CommandOutcome) =>
  outcome.kind === 'committed_completing' ||
  ((outcome.kind === 'finished' || outcome.kind === 'needs_intervention') && outcome.effect === 'committed');
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
    return {kind: 'not_found'};
  }
  check(['pending', 'not_committed', 'committed', 'indeterminate'].includes(String(row.effect)));
  check(['none', 'pending', 'cancelled', 'too_late', 'indeterminate'].includes(String(row.cancellation)));
  check(['pending', 'complete', 'needs_intervention'].includes(String(row.completion)));
  const terminal = (row.effect === 'committed' || row.effect === 'not_committed') && row.completion === 'complete';
  check(row.finished === terminal && (row.completion !== 'complete' || terminal));
  check(row.effect !== 'committed' || row.cancellation !== 'cancelled');
  const effect = row.effect as CommandEffect;
  if (row.completion === 'needs_intervention') return {kind: 'needs_intervention', effect};
  if (row.finished)
    return {
      kind: 'finished',
      effect: effect as 'committed' | 'not_committed',
      cancelled: row.cancellation === 'cancelled',
    };
  return effect === 'committed' ? {kind: 'committed_completing'} : {kind: 'pending'};
}
