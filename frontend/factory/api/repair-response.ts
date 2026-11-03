import {check, flag, list, num, object, record, text} from './models.js';
import {factoryRunID} from './run-response.js';

export type RepairLimits = {investigator_runs: number; proposal_judgments: number; active_minutes: number};
export type RepairGrant = RepairLimits & {id: string; active: boolean; effect: string};
type Charge = {id: string; kind: string; episode_id: string; cycle: number; active_minutes: number; failed: boolean};
type Cycle = {id: string; number: number; state: string; reason: string; run_id: string};
type Episode = {episode_id: string; reason: string; cycles: Cycle[]};
export type RepairView = {revision: number; grant?: RepairGrant; charges: Charge[]; episodes: Episode[]};
export type RepairChange = RepairLimits & {
  command_id: string;
  expected_revision: number;
  active: boolean;
  effect: string;
};

function limits(value: Record<string, unknown>): RepairLimits {
  for (const [amount, max] of [
    [value.investigator_runs, 4],
    [value.proposal_judgments, 4],
    [value.active_minutes, 120],
  ] as const)
    check(typeof amount === 'number' && Number.isSafeInteger(amount) && amount >= 0 && amount <= max);
  return {
    investigator_runs: num(value.investigator_runs),
    proposal_judgments: num(value.proposal_judgments),
    active_minutes: num(value.active_minutes),
  };
}

export function repairView(value: unknown, repository: string): RepairView {
  const data = object(value);
  let grant: RepairGrant | undefined;
  if (data.grant !== null && data.grant !== undefined) {
    const row = record(data.grant);
    check(
      row.repository === repository &&
        factoryRunID(row.id) &&
        typeof row.active === 'boolean' &&
        (row.effect === undefined || typeof row.effect === 'string')
    );
    grant = {...limits(row), id: text(row.id), active: flag(row.active), effect: text(row.effect)};
  }
  const charges = list(data.charges).map((item) => {
    const row = record(item);
    return {
      id: text(row.id),
      kind: text(row.kind),
      episode_id: text(row.episode_id),
      cycle: num(row.cycle),
      active_minutes: num(row.active_minutes),
      failed: flag(row.failed),
    };
  });
  const episodes = list(data.episodes).map((item) => {
    const row = record(item);
    return {
      episode_id: text(row.episode_id),
      reason: text(row.reason),
      cycles: list(row.cycles).map((entry) => {
        const cycle = record(entry);
        return {
          id: text(cycle.id),
          number: num(cycle.number),
          state: text(cycle.state),
          reason: text(cycle.reason),
          run_id: text(cycle.run_id),
        };
      }),
    };
  });
  check(typeof data.revision === 'number' && Number.isSafeInteger(data.revision) && data.revision >= 0);
  return {revision: num(data.revision), ...(grant ? {grant} : {}), charges, episodes};
}
