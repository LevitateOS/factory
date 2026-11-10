export interface ExternalPullAdoption {
	policy: number;
  repository: string;
  issue: string;
  acceptance: string;
  plan_assignment: string;
  pr_number: string;
  pr_id: string;
  target: {
    author_id: string;
    issue_id: string;
    head_repository: string;
    head_ref: string;
    base_ref: string;
    head_oid: string;
    base_oid: string;
  };
  connection: string;
  limits: {active_minutes: number; review_rounds: number; correction_cycles: number; max_children: number};
  fork_consent: boolean;
}

const object = (value: unknown): value is Record<string, unknown> => !!value && typeof value === 'object' && !Array.isArray(value);
const positive = (value: unknown): value is string => typeof value === 'string' && /^[1-9][0-9]{0,18}$/.test(value);
const command = (value: unknown) => typeof value === 'string' && /^[a-f0-9]{32}$/.test(value);
const commit = (value: unknown) => typeof value === 'string' && /^(?:[a-f0-9]{40}|[a-f0-9]{64})$/.test(value);
const ref = (value: unknown) => typeof value === 'string' && value.startsWith('refs/heads/') && value.length > 11 && value.length <= 256 && !/[\u0000-\u0020\u007f]/.test(value);
const integer = (value: unknown, low: number, high = Number.MAX_SAFE_INTEGER): value is number => typeof value === 'number' && Number.isSafeInteger(value) && value >= low && value <= high;

export function externalPullAdoption(value: unknown): ExternalPullAdoption | undefined {
  if (!object(value) || Object.keys(value).length !== 11 || !object(value.target) || Object.keys(value.target).length !== 7 || !object(value.limits) || Object.keys(value.limits).length !== 4) return undefined;
  const target = value.target, limits = value.limits;
  if (![value.repository, value.issue, value.pr_number, value.pr_id, target.author_id, target.issue_id, target.head_repository].every(positive) ||
      !integer(value.policy,1) || typeof value.acceptance !== 'string' || !/^d[a-f0-9]{24}$/.test(value.acceptance) || value.plan_assignment !== '' && !command(value.plan_assignment) ||
      ![target.head_oid, target.base_oid].every(commit) || target.head_oid === target.base_oid || ![target.head_ref, target.base_ref].every(ref) ||
      typeof value.connection !== 'string' || value.connection === '' || new TextEncoder().encode(value.connection).length > 128 || /[\u0000-\u001f\u007f]/.test(value.connection) ||
      !integer(limits.active_minutes, 1) || !integer(limits.review_rounds, 1, 16) || !integer(limits.correction_cycles, 0) || !integer(limits.max_children, 1, 4) ||
      typeof value.fork_consent !== 'boolean' || value.fork_consent !== (target.head_repository !== value.repository)) return undefined;
  return value as unknown as ExternalPullAdoption;
}

export function sameExternalPull(a: ExternalPullAdoption, b: ExternalPullAdoption): boolean {
  return a.policy === b.policy && a.repository === b.repository && a.issue === b.issue && a.acceptance === b.acceptance && a.plan_assignment === b.plan_assignment && a.pr_number === b.pr_number && a.pr_id === b.pr_id && a.connection === b.connection && a.fork_consent === b.fork_consent &&
    Object.keys(a.target).every(key => a.target[key as keyof typeof a.target] === b.target[key as keyof typeof b.target]) &&
    Object.keys(a.limits).every(key => a.limits[key as keyof typeof a.limits] === b.limits[key as keyof typeof b.limits]);
}
