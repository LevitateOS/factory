import {check, object} from './models.js';
import {factoryRunID} from './run-response.js';

export type VerificationLimits = {runs: number; active_minutes: number};
export type VerificationAllowance = {
  intent: string;
  revision: string;
  limits: VerificationLimits;
  consumption: {runs: number; active_seconds: number};
  remaining: {runs: number; active_seconds: number};
  can_increase: boolean;
  command_id?: string;
};
export type VerificationAllowanceChange = {command_id: string; expected_revision: string; limits: VerificationLimits};

export function verificationAllowance(value: unknown, intent: string): VerificationAllowance {
  const body = object(value),
    limits = object(body.limits),
    used = object(body.consumption),
    left = object(body.remaining);
  check(
    body.intent === intent && typeof body.revision === 'string' && (body.revision === '' || factoryRunID(body.revision))
  );
  check(typeof body.can_increase === 'boolean');
  check(body.command_id === undefined || factoryRunID(body.command_id));
  for (const amount of [
    limits.runs,
    limits.active_minutes,
    used.runs,
    used.active_seconds,
    left.runs,
    left.active_seconds,
  ])
    check(typeof amount === 'number' && Number.isSafeInteger(amount) && amount >= 0);
  check(Number(limits.runs) > 0 && Number(limits.active_minutes) > 0);
  return {
    intent,
    revision: body.revision,
    limits: {runs: Number(limits.runs), active_minutes: Number(limits.active_minutes)},
    consumption: {runs: Number(used.runs), active_seconds: Number(used.active_seconds)},
    remaining: {runs: Number(left.runs), active_seconds: Number(left.active_seconds)},
    can_increase: body.can_increase,
    ...(body.command_id === undefined ? {} : {command_id: factoryRunID(body.command_id) ? body.command_id : ''}),
  };
}
