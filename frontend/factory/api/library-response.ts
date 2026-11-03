import {check, id, object, text} from './models.js';

export interface LibraryItem {
  readonly id: string;
  readonly repository: string;
  readonly revision: number;
  readonly kind: string;
  readonly title: string;
  readonly body: string;
  readonly scope: string;
  readonly decision: string;
  readonly rationale: string;
  readonly alternatives: string[];
  readonly evidence: string[];
  readonly role: string;
  readonly state: string;
  readonly updated: number;
  readonly source: string;
  readonly removed: boolean;
  readonly replacements: string[];
  readonly derived: string[];
  readonly relations: {kind: string; target: string; evidence: string[]}[];
}
export interface LibraryHistory {
  readonly item: LibraryItem;
  readonly reason: string;
  readonly actor: string;
}
export const libraryID = (value: unknown): value is string => typeof value === 'string' && /^[a-f0-9]{32}$/.test(value);
const strings = (value: unknown): string[] =>
  Array.isArray(value) ? value.filter((v): v is string => typeof v === 'string').slice(0, 24) : [];

export function libraryItem(value: unknown, repository: string): LibraryItem {
  const item = object(value);
  check(id(repository) && item.repository === repository && libraryID(item.id));
  check(Number.isSafeInteger(item.revision) && Number(item.revision) > 0 && typeof item.title === 'string');
  return {
    id: item.id,
    repository,
    revision: Number(item.revision),
    kind: text(item.kind),
    title: item.title,
    body: text(item.body),
    scope: text(item.scope),
    decision: text(item.decision),
    rationale: text(item.rationale),
    alternatives: strings(item.alternatives),
    evidence: strings(item.evidence_refs),
    role: text(item.owner_role).replaceAll('_', ' '),
    state: text(item.state),
    updated:
      typeof item.updated_at === 'number' && Number.isFinite(item.updated_at) && item.updated_at > 0
        ? item.updated_at
        : 0,
    source: text(item.source_post),
    removed: item.removed === true,
    replacements: strings(item.replacements),
    derived: strings(item.derived_from),
    relations: Array.isArray(item.relations)
      ? item.relations.slice(0, 24).map((value) => {
          const relation = object(value);
          return {kind: text(relation.kind), target: text(relation.target), evidence: strings(relation.evidence)};
        })
      : [],
  };
}

export function libraryPage(value: unknown, repository: string, cursor = '', kind = '', history = false) {
  const page = object(value);
  check(Array.isArray(page.items) && page.items.length <= 50 && typeof page.complete === 'boolean');
  const items = page.items.map((item) => libraryItem(item, repository)),
    next = text(page.next_cursor);
  let previous = cursor;
  for (const item of items) {
    check(item.id > previous && (!kind || item.kind === kind) && (history || !item.removed));
    previous = item.id;
  }
  check(page.complete ? !next : items.length > 0 && next === previous);
  return {items, next, complete: page.complete};
}

export function libraryRead(value: unknown, repository: string, expectedID: string) {
  const data = object(value),
    item = libraryItem(data.item, repository);
  check(item.id === expectedID && Array.isArray(data.history) && data.history.length <= 20);
  let previous = Infinity;
  const history: LibraryHistory[] = data.history.map((value) => {
    const revision = object(value),
      recorded = libraryItem(revision.item, repository);
    check(recorded.id === item.id && recorded.revision <= item.revision && recorded.revision < previous);
    previous = recorded.revision;
    return {item: recorded, reason: text(revision.reason), actor: text(revision.actor)};
  });
  return {item, history};
}
