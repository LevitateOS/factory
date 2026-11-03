import {check, id, object} from '../api/models.js';
import type {IssueRef} from '../api/collaboration.js';
import {createdIssueRef} from '../api/issue-create-response.js';
import {factoryCommandId} from '../api/run-response.js';
import {inspectPersisted, writeStored} from '../shared/drafts.js';

export interface IssueCreateAddress {
  scope: string;
  host: string;
  actor: string;
  repository: string;
}
export interface IssueDraft {
  revision: string;
  title: string;
  body: string;
}
export interface PendingIssue {
  command: string;
  title: string;
  body: string;
  draft: string;
  cancel: boolean;
}
export interface IssueCreation {
  draft: IssueDraft;
  pending?: PendingIssue;
  created?: {command: string; issue: IssueRef};
}
export interface IssueCreationSaved {
  raw: string | undefined;
  writing: IssueCreation | undefined;
  blocked: boolean;
  durable: boolean;
}
const issued = (value: unknown): value is string => typeof value === 'string' && /^[a-f0-9]{32}$/.test(value);
const unsaved = new Map<string, {raw: string; baseline: string | undefined}>();
const fields = ({scope, host, actor, repository}: IssueCreateAddress): IssueCreateAddress => ({
  scope,
  host,
  actor,
  repository,
});
export function issueCreateRecordKey(address: IssueCreateAddress): string {
  check(issued(address.host) && id(address.actor) && id(address.repository));
  check(!!address.scope.trim() && address.scope.length <= 1024 && !/[\u0000-\u001f\u007f]/.test(address.scope));
  return `soda-issue-create:v1:${encodeURIComponent(address.scope)}:${address.host}:${address.actor}:${address.repository}`;
}
export function validIssueWriting(title: string, body: string): boolean {
  const scalar = (text: string) =>
    !text.includes('\0') &&
    [...text].every((char) => {
      const code = char.codePointAt(0)!;
      return code < 0xd800 || code > 0xdfff;
    });
  // Go TrimSpace uses Unicode White_Space (including NEL, excluding BOM).
  return (
    title !== '' &&
    !/^\p{White_Space}|\p{White_Space}$/u.test(title) &&
    scalar(title) &&
    scalar(body) &&
    new TextEncoder().encode(title).length <= 255 &&
    new TextEncoder().encode(body).length <= 65536
  );
}
export function samePendingIssue(a: PendingIssue, b: PendingIssue): boolean {
  return a.command === b.command && a.title === b.title && a.body === b.body && a.draft === b.draft;
}
export function emptyIssueCreation(): IssueCreation {
  return {draft: {revision: factoryCommandId(), title: '', body: ''}};
}
function decode(raw: string | undefined, address: IssueCreateAddress): IssueCreation | undefined {
  if (!raw) return;
  try {
    const row = object(JSON.parse(raw)),
      writing = object(row.writing),
      draft = object(writing.draft);
    check(Object.keys(row).length === 5 && Object.entries(fields(address)).every(([key, value]) => row[key] === value));
    check(
      Object.keys(writing).every((key) => ['draft', 'pending', 'created'].includes(key)) &&
        !(writing.pending && writing.created)
    );
    check(
      Object.keys(draft).length === 3 &&
        issued(draft.revision) &&
        typeof draft.title === 'string' &&
        typeof draft.body === 'string'
    );
    const result: IssueCreation = {draft: {revision: draft.revision, title: draft.title, body: draft.body}};
    if (writing.pending !== undefined) {
      const pending = object(writing.pending);
      check(
        Object.keys(pending).length === 5 &&
          issued(pending.command) &&
          issued(pending.draft) &&
          typeof pending.cancel === 'boolean'
      );
      check(
        typeof pending.title === 'string' &&
          typeof pending.body === 'string' &&
          validIssueWriting(pending.title, pending.body)
      );
      result.pending = {
        command: pending.command,
        title: pending.title,
        body: pending.body,
        draft: pending.draft,
        cancel: pending.cancel,
      };
    }
    if (writing.created !== undefined) {
      const created = object(writing.created);
      check(Object.keys(created).length === 2 && issued(created.command));
      result.created = {command: created.command, issue: createdIssueRef(created.issue, address.repository)};
    }
    return result;
  } catch {
    return;
  }
}
export function readIssueCreation(address: IssueCreateAddress): IssueCreationSaved {
  const persisted = inspectPersisted(issueCreateRecordKey(address)),
    local = unsaved.get(issueCreateRecordKey(address));
  const raw = local?.raw ?? persisted.value,
    writing = decode(raw, address);
  return {
    raw,
    writing,
    durable: !local && persisted.durable,
    blocked: (raw !== '' && !writing) || (!!local && persisted.value !== local.baseline),
  };
}
export function saveIssueCreation(
  address: IssueCreateAddress,
  expected: string | undefined,
  writing: IssueCreation
): 'saved' | 'unavailable' | 'conflict' {
  const key = issueCreateRecordKey(address),
    current = readIssueCreation(address);
  if (current.blocked || current.raw !== expected) return 'conflict';
  const raw = JSON.stringify({...fields(address), writing});
  check(decode(raw, address) !== undefined);
  const persisted = inspectPersisted(key),
    baseline = unsaved.get(key)?.baseline ?? expected;
  if (persisted.value !== baseline) return 'conflict';
  unsaved.set(key, {raw, baseline});
  if (persisted.value === undefined || !writeStored(key, raw)) return 'unavailable';
  unsaved.delete(key);
  return 'saved';
}
export function reloadIssueCreation(address: IssueCreateAddress): IssueCreationSaved {
  const key = issueCreateRecordKey(address),
    persisted = inspectPersisted(key),
    local = unsaved.get(key);
  if (!local || persisted.value === undefined) return readIssueCreation(address);
  const saved = decode(persisted.value, address),
    ours = decode(local.raw, address);
  if (persisted.value !== '' && !saved) return readIssueCreation(address);
  if (ours?.pending) {
    if (saved?.created || (saved?.pending && !samePendingIssue(ours.pending, saved.pending)))
      return readIssueCreation(address);
    const writing: IssueCreation = {
      draft: saved?.draft ?? emptyIssueCreation().draft,
      pending: {...ours.pending, cancel: ours.pending.cancel || !!saved?.pending?.cancel},
    };
    unsaved.set(key, {raw: JSON.stringify({...fields(address), writing}), baseline: persisted.value});
  } else if (ours?.created) {
    // An unsaved successful result cannot disappear behind newer writing.
    if (
      (saved?.pending && saved.pending.command !== ours.created.command) ||
      (saved?.created && saved.created.command !== ours.created.command)
    )
      return readIssueCreation(address);
    const writing: IssueCreation = {draft: saved?.draft ?? ours.draft, created: ours.created};
    unsaved.set(key, {raw: JSON.stringify({...fields(address), writing}), baseline: persisted.value});
  } else unsaved.delete(key);
  return readIssueCreation(address);
}
