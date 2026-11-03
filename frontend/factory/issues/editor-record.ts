import {check, id, object} from '../api/models.js';
import {inspectPersisted, writeStored} from '../shared/drafts.js';
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
export interface EditorSaved {
  raw: string | undefined;
  writing: EditorWriting | undefined;
  blocked: boolean;
  durable: boolean;
}
const issued = (value: unknown): value is string => typeof value === 'string' && /^[a-f0-9]{32}$/.test(value);
const fields = ({scope, host, actor, repository, issue}: EditorAddress): EditorAddress => ({
  scope,
  host,
  actor,
  repository,
  issue,
});
const unsaved = new Map<string, {raw: string; baseline: string | undefined}>();
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
export function readEditorWriting(address: EditorAddress): EditorSaved {
  const key = editorRecordKey(address),
    persisted = inspectPersisted(key),
    local = unsaved.get(key),
    raw = local?.raw ?? persisted.value,
    writing = decode(raw, address);
  return {
    raw,
    writing,
    durable: !local && persisted.durable,
    blocked: (raw !== '' && !writing) || (!!local && persisted.value !== local.baseline),
  };
}
export function saveEditorWriting(
  address: EditorAddress,
  expected: string | undefined,
  writing: EditorWriting
): 'saved' | 'unavailable' | 'conflict' {
  const key = editorRecordKey(address),
    current = readEditorWriting(address);
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
export function reloadEditorWriting(address: EditorAddress): EditorSaved {
  const key = editorRecordKey(address),
    persisted = inspectPersisted(key),
    local = unsaved.get(key);
  if (!local || persisted.value === undefined) return readEditorWriting(address);
  const saved = decode(persisted.value, address),
    pending = decode(local.raw, address)?.pending;
  if (persisted.value !== '' && !saved) return readEditorWriting(address);
  if (pending) {
    if (saved?.pending && !samePendingEdit(pending, saved.pending)) return readEditorWriting(address);
    const writing: EditorWriting = {
      ...(saved?.draft ? {draft: saved.draft} : {}),
      pending: {...pending, cancel: pending.cancel || !!saved?.pending?.cancel},
    };
    unsaved.set(key, {raw: JSON.stringify({...fields(address), writing}), baseline: persisted.value});
  } else unsaved.delete(key);
  return readEditorWriting(address);
}
