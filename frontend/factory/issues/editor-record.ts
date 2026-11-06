import {check, id, object} from '../api/models.js';
import {WritingRecord} from '../shared/retained-record.js';
import type {RetainedWriting} from '../shared/retained-record.js';
import {scalarEditText, sameEditOriginal} from '../api/issue-edit-response.js';
import type {EditOriginal} from '../api/issue-edit-response.js';

export interface EditorAddress {
  scope: string;
  host: string;
  actor: string;
  repository: string;
  issue: string;
}
export interface EditorDraft {
  revision: string;
  title: string;
  body: string;
  original: EditOriginal;
}
export interface PendingEdit {
  command: string;
  source: EditorDraft;
  cancel: boolean;
}
export interface EditorWriting {
  draft?: EditorDraft;
  pending?: PendingEdit;
}
export type EditorSaved = RetainedWriting<EditorWriting>;
const issued = (value: unknown): value is string => typeof value === 'string' && /^[a-f0-9]{32}$/.test(value);
const fields = ({scope, host, actor, repository, issue}: EditorAddress): EditorAddress => ({
  scope,
  host,
  actor,
  repository,
  issue,
});
export function editorRecordKey(address: EditorAddress): string {
  check(issued(address.host) && id(address.actor) && id(address.repository) && id(address.issue));
  check(!!address.scope.trim() && address.scope.length <= 1024 && !/[\u0000-\u001f\u007f]/.test(address.scope));
  return `soda-issue-edit:v1:${encodeURIComponent(address.scope)}:${address.host}:${address.actor}:${address.repository}:${address.issue}`;
}
export function editFields(draft: EditorDraft): {title?: string; body?: string} {
  return {
    ...(draft.title === draft.original.title ? {} : {title: draft.title}),
    ...(draft.body === draft.original.body ? {} : {body: draft.body}),
  };
}
export function validEditDraft(draft: EditorDraft): boolean {
  const fields = editFields(draft);
  return (
    Object.keys(fields).length > 0 &&
    (fields.title === undefined ||
      (/[^\p{White_Space}]/u.test(fields.title) &&
        scalarEditText(fields.title) &&
        new TextEncoder().encode(fields.title).length <= 255)) &&
    (fields.body === undefined ||
      (scalarEditText(fields.body) && new TextEncoder().encode(fields.body).length <= 65536))
  );
}
export function sameEditorDraft(a: EditorDraft, b: EditorDraft): boolean {
  return (
    a.revision === b.revision && a.title === b.title && a.body === b.body && sameEditOriginal(a.original, b.original)
  );
}
export function samePendingEdit(a: PendingEdit, b: PendingEdit): boolean {
  return a.command === b.command && sameEditorDraft(a.source, b.source);
}
function draft(value: unknown): EditorDraft {
  const row = object(value),
    base = object(row.original);
  check(
    Object.keys(row).length === 4 &&
      issued(row.revision) &&
      typeof row.title === 'string' &&
      typeof row.body === 'string'
  );
  check(
    Object.keys(base).length === 4 &&
      typeof base.external === 'string' &&
      !!base.external.trim() &&
      base.external.length <= 512 &&
      !base.external.includes('\0')
  );
  check(
    typeof base.title === 'string' && new TextEncoder().encode(base.title).length <= 65536 && scalarEditText(base.title)
  );
  check(
    typeof base.body === 'string' &&
      scalarEditText(base.body) &&
      typeof base.digest === 'string' &&
      /^[a-f0-9]{64}$/.test(base.digest)
  );
  return {
    revision: row.revision,
    title: row.title,
    body: row.body,
    original: {external: base.external, title: base.title, body: base.body, digest: base.digest},
  };
}
function decode(raw: string | undefined, address: EditorAddress): EditorWriting | undefined {
  if (!raw) return;
  try {
    const row = object(JSON.parse(raw)),
      writing = object(row.writing);
    check(Object.keys(row).length === 6 && Object.entries(fields(address)).every(([key, value]) => row[key] === value));
    check(Object.keys(writing).every((key) => ['draft', 'pending'].includes(key)));
    const result: EditorWriting = {};
    if (writing.draft !== undefined) result.draft = draft(writing.draft);
    if (writing.pending !== undefined) {
      const pending = object(writing.pending),
        source = draft(pending.source);
      check(
        Object.keys(pending).length === 3 &&
          issued(pending.command) &&
          typeof pending.cancel === 'boolean' &&
          validEditDraft(source)
      );
      result.pending = {command: pending.command, source, cancel: pending.cancel};
    }
    return result;
  } catch {
    return;
  }
}
function record(address: EditorAddress): WritingRecord<EditorWriting> {
  return new WritingRecord(
    editorRecordKey(address),
    (raw) => decode(raw, address),
    (writing) => JSON.stringify({...fields(address), writing})
  );
}
export function readEditorWriting(address: EditorAddress): EditorSaved {
  return record(address).read();
}
export function saveEditorWriting(
  address: EditorAddress,
  expected: string | undefined,
  writing: EditorWriting
): 'saved' | 'unavailable' | 'conflict' {
  return record(address).save(expected, writing);
}
export function reloadEditorWriting(address: EditorAddress): EditorSaved {
  return record(address).reload((ours, saved) => {
    const pending = ours?.pending;
    if (!pending) return 'persisted';
    if (saved?.pending && !samePendingEdit(pending, saved.pending)) return 'conflict';
    return {
      ...(saved?.draft ? {draft: saved.draft} : {}),
      pending: {...pending, cancel: pending.cancel || !!saved?.pending?.cancel},
    };
  });
}
