import {check, object} from './models.js';
import type {IssueRef} from './collaboration.js';

export interface EditOriginal {
  external: string;
  title: string;
  body: string;
  digest: string;
}
export function issueEditAllowed(value: unknown, repository: string, actor: string, parent: IssueRef): boolean {
  const row = object(value),
    issue = object(row.issue),
    observation = object(row.observation);
  check(
    row.repository === repository && row.actor === actor && row.pull === null && row.head === null && row.base === null
  );
  check(
    issue.kind === 'issue' &&
      issue.repository === parent.repository &&
      issue.number === parent.number &&
      issue.external_id === parent.external_id
  );
  check(observation.state === 'available' && typeof observation.revision === 'string' && !!observation.revision.trim());
  check(typeof observation.observed_at === 'string' && Number.isFinite(Date.parse(observation.observed_at)));
  const action = object(object(row.actions)['issue.edit']);
  check(['allowed', 'denied', 'unsupported', 'unavailable', 'unknown'].includes(String(action.state)));
  return action.state === 'allowed';
}
export function scalarEditText(value: string): boolean {
  return (
    !value.includes('\0') &&
    [...value].every((char) => {
      const code = char.codePointAt(0)!;
      return code < 0xd800 || code > 0xdfff;
    })
  );
}
export async function editBodyDigest(body: string): Promise<string> {
  check(scalarEditText(body));
  const digest = await crypto.subtle.digest('SHA-256', new TextEncoder().encode(body));
  return [...new Uint8Array(digest)].map((byte) => byte.toString(16).padStart(2, '0')).join('');
}
export function sameEditOriginal(a: EditOriginal, b: EditOriginal): boolean {
  return a.external === b.external && a.title === b.title && a.body === b.body && a.digest === b.digest;
}
