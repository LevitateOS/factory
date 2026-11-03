import {flag, list, num, object, optionalNum, optionalText, record, text} from './models.js';

/** H-R permission/usage DTO v2 (revision f06-20261011). */
export interface FactoryAccessViewer {
  code_write: boolean;
  owner_or_admin: boolean;
  environment_owner: boolean;
  operator: boolean;
  member: boolean;
  session_current: boolean;
}
export interface FactoryAccessEditable {
  editable: boolean;
  reason?: string;
}
export interface FactoryAccessConnectionEditable extends FactoryAccessEditable {
  connection: string;
}
export interface FactoryAccessRequiredAction {
  code: string;
  resolve_by: string;
  setting: string;
}
export interface FactoryAccessUsage {
  connection: string;
  status?: string;
  unit?: string;
  budget_minutes?: number;
  used_minutes?: number;
}
export interface FactoryAccess {
  viewer: FactoryAccessViewer;
  editable: {
    policy: FactoryAccessEditable;
    activity_settings: FactoryAccessEditable;
    operator_grant: FactoryAccessEditable;
    capacity: FactoryAccessEditable;
    environment_grant: FactoryAccessEditable;
    sponsorships: FactoryAccessConnectionEditable[];
  };
  effective: Record<string, unknown>;
  required_actions: FactoryAccessRequiredAction[];
  preparation: {project?: string; ready?: boolean; requirements?: unknown; approval?: unknown} | null;
  usage: FactoryAccessUsage[];
  observed_unix: number;
  repository: string;
}

function editable(value: unknown): FactoryAccessEditable {
  const row = record(value);
  const reason = optionalText(row.reason);
  return {editable: flag(row.editable), ...(reason ? {reason} : {})};
}
function connectionEditable(value: unknown): FactoryAccessConnectionEditable {
  return {connection: text(record(value).connection), ...editable(value)};
}
function usageEntry(value: unknown): FactoryAccessUsage {
  const row = record(value);
  const connection = text(row.connection);
  const status = optionalText(row.status);
  if (status) return {connection, status};
  const budget = optionalNum(row.budget_minutes),
    used = optionalNum(row.used_minutes);
  return {
    connection,
    unit: text(row.unit, 'minutes'),
    ...(budget === undefined ? {} : {budget_minutes: budget}),
    ...(used === undefined ? {} : {used_minutes: used}),
  };
}
function requiredAction(value: unknown): FactoryAccessRequiredAction {
  const row = record(value);
  return {code: text(row.code), resolve_by: text(row.resolve_by), setting: text(row.setting)};
}
function preparation(value: unknown): FactoryAccess['preparation'] {
  if (value === null || value === undefined) return null;
  const row = record(value);
  const project = optionalText(row.project);
  return {
    ...(project === undefined ? {} : {project}),
    ...(typeof row.ready === 'boolean' ? {ready: row.ready} : {}),
    ...(row.requirements !== undefined ? {requirements: row.requirements} : {}),
    ...(row.approval !== undefined ? {approval: row.approval} : {}),
  };
}
export function factoryAccessResponse(value: unknown): FactoryAccess {
  const data = object(value);
  const viewer = record(data.viewer);
  const edit = record(data.editable);
  return {
    viewer: {
      code_write: flag(viewer.code_write),
      owner_or_admin: flag(viewer.owner_or_admin),
      environment_owner: flag(viewer.environment_owner),
      operator: flag(viewer.operator),
      member: flag(viewer.member),
      session_current: flag(viewer.session_current),
    },
    editable: {
      policy: editable(edit.policy),
      activity_settings: editable(edit.activity_settings),
      operator_grant: editable(edit.operator_grant),
      capacity: editable(edit.capacity),
      environment_grant: editable(edit.environment_grant),
      sponsorships: list(edit.sponsorships).map(connectionEditable),
    },
    effective: record(data.effective),
    required_actions: list(data.required_actions).map(requiredAction),
    preparation: preparation(data.preparation),
    usage: list(data.usage).map(usageEntry),
    observed_unix: num(data.observed_unix),
    repository: text(data.repository),
  };
}
