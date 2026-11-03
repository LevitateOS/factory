import {check, id, list, num, object, record, text} from './models.js';
export const activityKinds = ['issue_created', 'pr_created', 'verified_merge'] as const;
export type ActivityKind = (typeof activityKinds)[number];
export type ActivitySettings = Record<ActivityKind, boolean>;
export interface ActivityItem {
  readonly id: string;
  readonly kind: ActivityKind;
  readonly issue: string;
  readonly pr?: string;
  readonly summary: string;
  readonly url: string;
  readonly created_unix: number;
}
export interface FactoryActivity {
  readonly repository: string;
  readonly repository_path: string;
  readonly revision: number;
  readonly observed_unix: number;
  readonly truncated: boolean;
  readonly settings: ActivitySettings;
  readonly items: readonly ActivityItem[];
}
export function activitySettings(value: unknown): ActivitySettings {
  const settings = object(value);
  check(activityKinds.every((kind) => typeof settings[kind] === 'boolean'));
  return {
    issue_created: settings.issue_created as boolean,
    pr_created: settings.pr_created as boolean,
    verified_merge: settings.verified_merge as boolean,
  };
}
export function parseFactoryActivity(value: unknown, repository: string): FactoryActivity {
  const row = object(value);
  check(
    id(row.repository) &&
      row.repository === repository &&
      typeof row.revision === 'number' &&
      Number.isSafeInteger(row.revision) &&
      row.revision >= 0 &&
      row.revision < Number.MAX_SAFE_INTEGER &&
      Array.isArray(row.items) &&
      row.items.length <= 60
  );
  return {
    repository,
    repository_path: text(row.repository_path),
    revision: row.revision,
    observed_unix: num(row.observed_unix),
    truncated: row.truncated === true,
    settings: activitySettings(row.settings),
    items: list(row.items).flatMap((value) => {
      const item = record(value),
        kind = activityKinds.find((kind) => kind === item.kind);
      if (!kind) return [];
      return [
        {
          id: text(item.id),
          kind,
          issue: text(item.issue),
          ...(typeof item.pr === 'string' ? {pr: item.pr} : {}),
          summary: text(item.summary),
          url: text(item.url),
          created_unix: num(item.created_unix),
        },
      ];
    }),
  };
}
