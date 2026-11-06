import {check} from './models.js';
import {issueActionAllowed} from './capability-response.js';
import type {IssueRef} from './collaboration.js';

export interface EditOriginal {
  external: string;
  title: string;
  body: string;
  digest: string;
}
export function issueEditAllowed(value: unknown, repository: string, actor: string, parent: IssueRef): boolean {
  return issueActionAllowed(value, repository, actor, parent, 'issue.edit');
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
