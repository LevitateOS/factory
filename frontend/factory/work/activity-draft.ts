import {check, id, object} from '../api/models.js';
import {activitySettings} from '../api/activity-response.js';
import type {ActivitySettings} from '../api/activity-response.js';
import {inspectStored, writeStored} from '../shared/drafts.js';
export interface ActivityAddress {
  readonly scope: string;
  readonly repository: string;
}
export interface ActivityDraft {
  readonly expected_revision: number;
  readonly settings: ActivitySettings;
  readonly submitted: boolean;
}
const key = (address: ActivityAddress) => {
  check(
    typeof address.scope === 'string' &&
      address.scope.trim().length > 0 &&
      address.scope.length <= 1024 &&
      !/[\u0000-\u001f\u007f]/.test(address.scope) &&
      id(address.repository)
  );
  return `soda-work-activity:v1:${encodeURIComponent(address.scope)}:${address.repository}`;
};
export function sameActivitySettings(a: ActivitySettings, b: ActivitySettings): boolean {
  return a.issue_created === b.issue_created && a.pr_created === b.pr_created && a.verified_merge === b.verified_merge;
}
export function readActivityDraft(address: ActivityAddress): {
  draft: ActivityDraft | undefined;
  blocked: boolean;
  durable: boolean;
} {
  const stored = inspectStored(key(address));
  if (stored.value === '') return {draft: undefined, blocked: false, durable: stored.durable};
  if (stored.value !== undefined && stored.value.length <= 4096) {
    try {
      const row = object(JSON.parse(stored.value)),
        draft = object(row.draft);
      check(
        Object.keys(row).length === 3 &&
          row.scope === address.scope &&
          row.repository === address.repository &&
          Object.keys(draft).length === 3 &&
          Object.keys(object(draft.settings)).length === 3 &&
          typeof draft.expected_revision === 'number' &&
          Number.isSafeInteger(draft.expected_revision) &&
          draft.expected_revision >= 0 &&
          draft.expected_revision < Number.MAX_SAFE_INTEGER &&
          typeof draft.submitted === 'boolean'
      );
      return {
        draft: {
          expected_revision: draft.expected_revision,
          submitted: draft.submitted,
          settings: activitySettings(draft.settings),
        },
        blocked: false,
        durable: stored.durable,
      };
    } catch {
      /* Preserve unknown bytes. */
    }
  }
  return {draft: undefined, blocked: true, durable: stored.durable};
}
export function sameActivityDraft(a: ActivityDraft, b: ActivityDraft): boolean {
  return (
    a.expected_revision === b.expected_revision &&
    a.submitted === b.submitted &&
    sameActivitySettings(a.settings, b.settings)
  );
}
export function saveActivityDraft(address: ActivityAddress, draft: ActivityDraft, prior?: ActivityDraft): boolean {
  const stored = readActivityDraft(address);
  check(!stored.blocked && (stored.draft ? prior && sameActivityDraft(stored.draft, prior) : !prior));
  return writeStored(key(address), JSON.stringify({...address, draft}));
}
export function settleActivityDraft(address: ActivityAddress, draft: ActivityDraft): boolean {
  const stored = readActivityDraft(address);
  if (stored.blocked || !stored.draft || !sameActivityDraft(stored.draft, draft)) return false;
  writeStored(key(address), '');
  return true;
}
