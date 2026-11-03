// Coder-time allowance holds. The factory status read carries the latest
// dispatch pass's waits; an allowance_exhausted wait marked coder_time means
// the sponsorship's coder minutes are used up. The Feed and Work views say so
// in plain words and name the setting that fixes it.

export interface AllowanceHold {
  readonly repository: string;
  readonly issue?: string;
}

const settingsPath = 'Settings → SodaOS → Factory → Provider allowance and sponsorship';

// allowanceHolds reads the allowance holds from one factory status body.
// Anything malformed is skipped: the notice is advisory, never a gate.
export function allowanceHolds(status: unknown, repository: string): AllowanceHold[] {
  if (!status || typeof status !== 'object') return [];
  const observation = (status as Record<string, unknown>).dispatch_observation;
  if (!observation || typeof observation !== 'object') return [];
  const waits = (observation as Record<string, unknown>).waits;
  if (!Array.isArray(waits)) return [];
  const holds: AllowanceHold[] = [];
  for (const wait of waits.slice(0, 64)) {
    if (!wait || typeof wait !== 'object') continue;
    const raw = wait as Record<string, unknown>;
    if (raw.coder_time !== true || raw.reason !== 'allowance_exhausted') continue;
    const issue = typeof raw.issue === 'string' && /^\d+$/.test(raw.issue) ? raw.issue : undefined;
    holds.push({repository, ...(issue ? {issue} : {})});
  }
  return holds;
}

// allowanceHoldText says why work waits and where to fix it.
export function allowanceHoldText(holds: readonly AllowanceHold[]): string {
  if (holds.length === 0) return '';
  const issues = [...new Set(holds.flatMap((hold) => (hold.issue ? [`#${hold.issue}`] : [])))];
  const what = issues.length > 0 ? `Issue ${issues.join(', ')} is waiting` : 'Work is waiting';
  return `${what}: the coder-time allowance is used up. Raise Allowance minutes in ${settingsPath} to continue.`;
}
