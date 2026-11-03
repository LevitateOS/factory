import {check, id, list, num, object, record, text} from './models.js';

export interface AcceptanceSource {
  readonly id: string;
  readonly digest: string;
  readonly content_version: number;
}
export interface AcceptanceDependency {
  readonly occurrence: string;
  readonly depends_on: string;
}
export interface AcceptancePreview {
  readonly repository: string;
  readonly issue: string;
  readonly title_digest: string;
  readonly title_occurrence: string;
  readonly body_digest: string;
  readonly content_version: number;
  readonly native_revision: number;
  readonly comments: readonly AcceptanceSource[];
  readonly dependencies: readonly AcceptanceDependency[];
}

// acceptanceCommentIDs parses the maintainer's typed comment IDs. This is
// user input, not a Soda reply, so the ID shape and bound stay checked.
export function acceptanceCommentIDs(input: string): string[] {
  if (!input.trim()) return [];
  const ids = input.trim().split(/[\s,]+/);
  check(ids.length <= 50 && ids.every(id) && new Set(ids).size === ids.length);
  return ids;
}

// acceptancePreview reads the digests and versions the acceptance decision
// records; the server checks them when the acceptance is posted.
export function acceptancePreview(
  value: unknown,
  repository: string,
  issue: string,
  _selected: readonly string[]
): AcceptancePreview {
  const raw = object(value);
  return {
    repository,
    issue,
    title_digest: text(raw.title_digest),
    title_occurrence: text(raw.title_occurrence),
    body_digest: text(raw.body_digest),
    content_version: num(raw.content_version),
    native_revision: num(raw.native_revision),
    comments: list(raw.comments).map((value) => {
      const source = record(value);
      return {id: text(source.id), digest: text(source.digest), content_version: num(source.content_version)};
    }),
    dependencies: list(raw.dependencies).map((value) => {
      const dependency = record(value);
      return {occurrence: text(dependency.occurrence), depends_on: text(dependency.depends_on)};
    }),
  };
}

export function acceptanceErrorMessage(value: unknown): string | undefined {
  const raw = record(value);
  return typeof raw.error === 'string' && typeof raw.message === 'string' ? raw.message : undefined;
}
