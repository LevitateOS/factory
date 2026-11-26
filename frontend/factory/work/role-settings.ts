import {LitElement, html} from 'lit';
import {check, id, object, readSodaJSON} from '../api/models.js';
import type {FactoryTransport} from '../api/transport.js';
import './workspace-setup.js';

export interface RoleContext {
  readonly transport: FactoryTransport;
  readonly repository: string;
  readonly actor?: string;
  readonly accountLabel?: string;
  readonly scope?: string;
}
const roleNames = {'soda-coder': 'Coding', 'soda-reviewer': 'Review'} as const;
const toolStatusLabels = {
  not_set_up: 'Muse is not set up yet. Sign in to Muse to download and verify it.',
  checking: 'Checking Muse for the current release.',
  downloading: 'Downloading the current Muse release.',
  ready: 'Muse is verified.',
  unavailable: 'Muse verification or update failed.',
} as const;
type Role = keyof typeof roleNames;
type Selection = Record<string, unknown> & {account: string; harness: string; model: string};
type Selections = Record<Role, Selection>;
interface Account {
  connection: string;
  active: boolean;
  roles: string[];
  revision: number;
  owner: string | undefined;
  native: boolean;
}
interface SpendingSource {
  connection: string;
  label: string;
  available: boolean;
  authorizable: boolean;
  tool: {
    state: keyof typeof toolStatusLabels;
    latest: string | undefined;
    selected: string | undefined;
    checked: number | undefined;
    error: string | undefined;
  } | undefined;
}
interface Settings {
  policy: Record<string, unknown> | null;
  roles?: Selections;
  accounts: Account[];
  sources: SpendingSource[];
  editable: boolean;
  bindings: Record<string, unknown>[];
  /** What the server says still stops new work, such as dispatch_closed. */
  missing: string[];
  options: {targetBranch: string; mergeMethods: string[]; nativeSetup: boolean; controllerGrantRequired: boolean} | undefined;
}
interface SetupDraft {
  targetBranch: string;
  checks: string;
  method: string;
  useAccount: boolean;
  controllerGrant: string;
}
interface LimitDraft {
  active_minutes: string;
  review_rounds: string;
  correction_cycles: string;
}
const mergeLabels: Record<string, string> = {
  merge: 'Merge commit',
  squash: 'Squash',
  rebase: 'Rebase',
  'fast-forward-only': 'Fast-forward only',
  'rebase-merge': 'Rebase and merge',
};
class RoleSaveError extends Error {
  constructor(
    message: string,
    readonly status: number
  ) {
    super(message);
  }
}

function policyActor(value: unknown) {
  const {kind: _kind, ...binding} = object(value);
  return binding;
}

function samePolicyActor(left: unknown, right: unknown) {
	if (left == null || right == null) return false;
	const a = object(left), b = object(right);
	return ['actor_id', 'token_id', 'native_profile', 'native_connection', 'controller_grant_id'].every((key) => a[key] === b[key]);
}

function settings(statusValue: unknown, accessValue: unknown, repository: string): Settings {
  const status = object(statusValue),
    access = object(accessValue);
  check(status.repository === repository && access.repository === repository && Array.isArray(status.sponsorships));
  const accounts = status.sponsorships.map((value) => {
    const account = object(value);
    check(
      account.repository === repository &&
        typeof account.connection === 'string' &&
        account.connection.length > 0 &&
        typeof account.active === 'boolean' &&
        Array.isArray(account.roles) &&
        account.roles.every((role) => typeof role === 'string')
    );
    return {
      connection: account.connection,
      active: account.active,
      roles: account.roles as string[],
      revision: Number(account.revision),
      owner: typeof account.granted_by === 'string' && id(account.granted_by) ? account.granted_by : undefined,
      native: account.native != null,
    };
  });
  const sources = Array.isArray(status.spending_sources)
    ? status.spending_sources.map((value) => {
        const source = object(value),
          native = object(source.native);
        check(
          typeof source.connection === 'string' &&
            /^[0-9a-f]{32}$/.test(source.connection) &&
            source.label === 'This Muse login' &&
            native.authentication === 'muse_login' &&
            typeof native.profile === 'string' &&
            /^[0-9a-f]{32}$/.test(native.profile) &&
            native.credential_sha256 === undefined &&
            typeof source.execution_available === 'boolean'
        );
        const rawTool = source.tool_status == null ? undefined : object(source.tool_status);
        if (rawTool) {
          check(typeof rawTool.state === 'string' && Object.hasOwn(toolStatusLabels, rawTool.state));
          for (const field of ['upstream_version', 'selected_version'])
            check(rawTool[field] == null || (typeof rawTool[field] === 'string' && /^[a-zA-Z0-9._-]{1,32}$/.test(rawTool[field])));
          check(rawTool.checked_at_unix == null || (Number.isSafeInteger(rawTool.checked_at_unix) && Number(rawTool.checked_at_unix) > 0));
          check(rawTool.error == null || (typeof rawTool.error === 'string' && rawTool.error.length <= 256));
        }
        return {
          connection: source.connection as string,
          label: source.label,
          available: source.execution_available,
          authorizable: source.authorizable === true,
          tool: rawTool ? {
            state: rawTool.state as keyof typeof toolStatusLabels,
            latest: rawTool.upstream_version as string | undefined,
            selected: rawTool.selected_version as string | undefined,
            checked: rawTool.checked_at_unix as number | undefined,
            error: rawTool.error as string | undefined,
          } : undefined,
        };
      })
    : [];
  const editable = object(object(access.editable).policy).editable === true;
  const bindings = Array.isArray(status.native_bindings) ? status.native_bindings.map(object) : [];
  const effective = status.effective == null ? {} : object(status.effective);
  const missing = Array.isArray(effective.missing) ? effective.missing.filter((value): value is string => typeof value === 'string') : [];
  const available = status.policy_options == null ? undefined : object(status.policy_options);
  const options =
    available && Array.isArray(available.merge_methods)
      ? {
          nativeSetup: available.native_setup === true,
          controllerGrantRequired: available.controller_grant_required === true,
          targetBranch: typeof available.target_branch === 'string' ? available.target_branch : '',
          mergeMethods: available.merge_methods.filter(
            (value): value is string => typeof value === 'string' && Object.hasOwn(mergeLabels, value)
          ),
        }
      : undefined;
  if (status.policy === null) return {policy: null, accounts, sources, editable, bindings, missing, options};
  const policy = object(status.policy);
  check(policy.repository === repository && Number.isSafeInteger(policy.revision) && Number(policy.revision) > 0);
  const stored = object(policy.roles),
    roles = {} as Selections;
  for (const role of Object.keys(roleNames) as Role[]) {
    const selection = object(stored[role]);
    check(
      typeof selection.harness === 'string' &&
        typeof selection.model === 'string' &&
        (selection.account === undefined || typeof selection.account === 'string')
    );
    roles[role] = {
      ...selection,
      account: selection.account ?? '',
      harness: selection.harness,
      model: selection.model,
    } as Selection;
  }
  return {policy, roles, accounts, sources, editable, bindings, missing, options};
}

const covers = (accounts: readonly Account[], role: Role, connection: string) =>
  accounts.some((account) => account.connection === connection && account.active && account.roles.includes(role));

// authorizeLogins lets each chosen login run the roles it was chosen for,
// through the existing sponsorship command, before the policy names it. It
// returns the updated logins, or the same array when nothing was needed.
async function authorizeLogins(context: RoleContext, signal: AbortSignal, saved: Account[], roles: Selections) {
  const needed = new Map<string, Set<string>>();
  for (const role of Object.keys(roleNames) as Role[]) {
    const account = roles[role].account;
    if (account && !covers(saved, role, account)) needed.set(account, (needed.get(account) ?? new Set()).add(role));
  }
  if (!needed.size) return saved;
  const accounts = [...saved];
  for (const [connection, added] of needed) {
    const stored = accounts.find((account) => account.connection === connection);
    if (stored && (!Number.isSafeInteger(stored.revision) || stored.revision < 1)) throw new RoleSaveError('This login changed somewhere else. The latest is shown; save again.', 409);
    const granted = [...new Set([...(stored?.active ? stored.roles : []), ...added])];
    const response = await context.transport.request(`repositories/${context.repository}/factory/sponsorships/${connection}`, {
      method: 'PUT',
      signal,
      body: JSON.stringify({expected_revision: stored?.revision ?? 0, roles: granted, active: true}),
    });
    if (!response.ok)
      throw new RoleSaveError(
        response.status === 409 || response.status === 412
          ? 'This login changed somewhere else. The latest is shown; save again.'
          : 'This login cannot run work yet. Check its computer and workspace permission.',
        response.status
      );
    const receipt = object(await readSodaJSON(response));
    check(Number.isSafeInteger(receipt.revision) && receipt.revision === (stored?.revision ?? 0) + 1);
    const account: Account = {connection, active: true, roles: granted, revision: Number(receipt.revision), owner: context.actor, native: true};
    accounts.splice(0, accounts.length, ...accounts.filter((other) => other.connection !== connection), account);
  }
  return accounts;
}

async function readSettings(context: RoleContext, signal: AbortSignal): Promise<Settings> {
  const path = `repositories/${context.repository}/factory`;
  const [status, access] = await Promise.all([
    context.transport.request(path, {method: 'GET', signal}),
    context.transport.request(`${path}/access`, {method: 'GET', signal}),
  ]);
  check(status.ok && access.ok);
  return settings(await readSodaJSON(status), await readSodaJSON(access), context.repository);
}

/** The policy actor slots a first policy needs, each bound to the window's own
 * account; undefined when any slot has no single binding for it. */
function accountActors(bindings: readonly Record<string, unknown>[], actor: string | undefined, controllerGrant = '') {
  if (!actor || !id(actor)) return;
  const required = !!controllerGrant;
  const actors: Record<string, Record<string, unknown>> = {};
  for (const [slot, kind] of Object.entries({
    publish_actor: 'git.ref.publish',
    create_actor: 'pull_request.create',
    review_actor: 'pull_request.review.submit',
    merge_actor: 'pull_request.merge',
    ...(required ? {execution_actor: 'execution.launch'} : {}),
  })) {
    const matches = bindings.filter((ref) => ref.actor_id === actor && ref.kind === kind);
    if (matches.length !== 1) return;
    actors[slot] = {...policyActor(matches[0]), ...(required ? {controller_grant_id: controllerGrant} : {})};
  }
  return actors;
}

/** The only model Muse runs agents with. */
const museModel = 'muse-spark-1.3-contributor';

/** Where the person fixes a missing Muse login: App settings when there is no
 * login, the project's Execution controller when Muse is signed in but the
 * project may not run agents on this computer yet; '' when neither applies. */
export type SetupFix = 'muse' | 'computer' | '';
function setupFix(view: Settings): SetupFix {
  if (!view.editable || view.sources.some((candidate) => candidate.authorizable)) return '';
  return view.sources.length ? 'computer' : 'muse';
}

/** Why Factory can't give a project its first settings by itself, or '' when it can. */
function setupBlock(view: Settings, actor: string | undefined) {
  if (!view.editable) return 'This account can’t change this project’s settings.';
  const fix = setupFix(view);
  if (fix === 'muse') return 'Sign in to Muse on this computer first, in App settings. Agents run with your Muse login.';
  if (fix === 'computer') return 'Let this project run agents on this computer first, under Execution controller in its Settings.';
  if (!view.options?.mergeMethods.length) return 'This account can’t merge into this repository.';
  if (view.options.controllerGrantRequired) return 'Fountain projects are set up with the form below.';
  if (!accountActors(view.bindings, actor)) return 'This account can’t publish to this repository.';
  return '';
}

/** First settings for a project that has none, so its first Feed post is read
 * right away: every role uses this computer's Muse login, merges squash into
 * the default branch with no required checks, and automation stays off until
 * the person turns it on in Settings. Returns false, changing nothing, when the
 * project already has settings or needs a choice only the person can make. */
export async function setUpNewProject(context: RoleContext, signal: AbortSignal): Promise<boolean> {
  const view = await readSettings(context, signal);
  if (view.policy !== null || setupBlock(view, context.actor)) return false;
  const source = view.sources.find((candidate) => candidate.authorizable)!;
  const options = view.options!;
  const selection = {account: source.connection, harness: 'muse', model: museModel, profile: {effort: 'low'}};
  const roles: Selections = {'soda-coder': selection, 'soda-reviewer': {...selection}};
  await authorizeLogins(context, signal, view.accounts, roles);
  const response = await context.transport.request(`repositories/${context.repository}/factory/policy`, {
    method: 'PUT',
    signal,
    body: JSON.stringify({
      expected_revision: 0,
      ...policySelections(roles),
      enabled: false,
      paused: false,
      automatic_external_pulls: false,
      target_branch: options.targetBranch,
      merge_method: options.mergeMethods.includes('squash') ? 'squash' : options.mergeMethods[0],
      required_checks: [],
      ...accountActors(view.bindings, context.actor),
      automation_reviewer: null,
    }),
  });
  return response.ok;
}

/** "Set up this project": its first settings, or why they need the person.
 * Resolves with the state that now shows. */
export async function setUpProject(context: RoleContext, signal: AbortSignal): Promise<AutomationState> {
  if (!(await setUpNewProject(context, signal))) {
    const state = await readAutomation(context, signal);
    if (state.needsSetup) throw new RoleSaveError(state.setupBlocked || 'This project could not be set up. Try again.', 400);
    return state;
  }
  return readAutomation(context, signal);
}

// The existing policy command owns CAS, current permissions and actor bindings.
// Preserve unrelated policy settings while editing roles and captured limits.
function policyChange(policy: Record<string, unknown>, roles: Selections) {
  const resolve = policy.resolve_actor == null ? {} : object(policy.resolve_actor);
  return {
    expected_revision: policy.revision,
    ...policySelections(roles),
    enabled: policy.enabled,
    paused: policy.paused,
    automatic_external_pulls: policy.automatic_external_pulls === true,
    target_branch: policy.target_branch,
    merge_method: policy.merge_method,
    required_checks: policy.required_checks,
    attempt_limits: policy.attempt_limits,
    reassessment_minutes: policy.reassessment_minutes,
    publish_actor: policyActor(policy.publish_actor),
    create_actor: policyActor(policy.create_actor),
    review_actor: policyActor(policy.review_actor),
    merge_actor: policyActor(policy.merge_actor),
	...(policy.execution_actor==null?{}:{execution_actor:policyActor(policy.execution_actor)}),
    ...(resolve.actor_id && resolve.actor_id !== '0' ? {resolve_actor: policyActor(resolve)} : {}),
    automation_reviewer: policy.automation_reviewer == null ? null : policyActor(policy.automation_reviewer),
  };
}

// Judgments belong to JEV, not to a Muse login: saving clears any older selection.
function policySelections(roles: Selections) {
  return {roles, judgments: null};
}

/** What a refused policy save means, in plain words. */
function saveRefusal(status: number) {
  return new RoleSaveError(
    status === 400
      ? 'These settings were rejected. Check the branch, CI checks and logins.'
      : status === 409 || status === 412
        ? 'These settings changed somewhere else. The latest is shown; try again.'
        : status === 403
          ? 'You can’t change these settings.'
          : 'The save could not be confirmed. The latest is shown.',
    status
  );
}

export type AutomationRole = Role;
export const automationRoles = roleNames;

/** Something that stops automation from turning on, and the Settings field that fixes it. */
export interface AutomationBlock {
  readonly text: string;
  /** The aria-label of the Settings control to fix it with, when there is one. */
  readonly field?: string;
}

/** One project's automation switch and its quick choices, as the project
 * menu and the top of Settings show them. */
export interface AutomationState {
  readonly on: boolean;
  /** The project has no settings yet; "Set up this project" gives it its first ones. */
  readonly needsSetup: boolean;
  /** Why Factory can't set it up by itself; empty when one click does it. */
  readonly setupBlocked: string;
  /** Where to fix what blocks setup; empty when no page helps. */
  readonly setupFix: SetupFix;
  readonly pickUpPulls: boolean;
  readonly editable: boolean;
  /** Why automation can't turn on; undefined when it can. */
  readonly blocked?: AutomationBlock;
  /** Why pull requests can't be picked up; undefined when they can. */
  readonly blockedPulls?: AutomationBlock;
  readonly targetBranch: string;
  readonly logins: Record<Role, string>;
  readonly loginChoices: Record<Role, readonly {readonly connection: string; readonly label: string}[]>;
  readonly merge: string;
  readonly mergeChoices: readonly {readonly value: string; readonly label: string}[];
}

export type AutomationChange =
  | {readonly on: boolean}
  | {readonly pickUpPulls: boolean}
  | {readonly role: Role; readonly login: string}
  | {readonly merge: string};

// A login can run a role when it already covers it or saving can authorize it.
const eligibleLogin = (view: Settings, role: Role, account: string) =>
  covers(view.accounts, role, account) || view.sources.some((source) => source.connection === account && source.authorizable);

/** The logins a role can use: this computer's Muse logins and any already authorized for it. */
function loginChoices(view: Settings, role: Role) {
  const logins = new Set([
    ...view.sources.filter((login) => login.authorizable).map((login) => login.connection),
    ...view.accounts.filter((account) => account.active && account.roles.includes(role)).map((account) => account.connection),
  ]);
  return [...logins].map((connection) => ({connection, label: view.sources.find((source) => source.connection === connection)?.label ?? connection}));
}

function automationBlock(view: Settings, roles: Selections | undefined): AutomationBlock | undefined {
  const policy = view.policy;
  if (!policy || !roles) return {text: 'Set up this project first.'};
  if (!view.editable) return {text: 'You can’t change this project’s settings with this account.'};
  for (const role of Object.keys(roleNames) as Role[]) {
    const account = roles[role].account, name = roleNames[role];
    if (!account) return {text: `Choose a ${name.toLowerCase()} login first.`, field: `${name} login`};
    if (!eligibleLogin(view, role, account)) return {text: `The ${name.toLowerCase()} login can’t run work. Choose another.`, field: `${name} login`};
  }
  // The server merges automatically only after named CI checks pass.
  if (!Array.isArray(policy.required_checks) || !policy.required_checks.length)
    return {text: 'Name the CI checks that must pass before merging.', field: 'Required CI checks'};
  return undefined;
}

export function automationState(view: Settings, actor?: string): AutomationState {
  const policy = view.policy, roles = view.roles;
  const blocked = automationBlock(view, roles);
  const blockedPulls = blocked ?? (view.options?.controllerGrantRequired
    ? {text: 'Picking up pull requests isn’t available in Fountain.'}
    : roles!['soda-coder'].account !== roles!['soda-reviewer'].account
      ? {text: 'Use the same login for coding and review first.', field: 'Review login'}
      : undefined);
  const merge = typeof policy?.merge_method === 'string' ? policy.merge_method : '';
  return {
    // One switch: off when the settings say so or when someone paused the project.
    on: policy?.enabled === true && policy.paused !== true && !view.missing.includes('dispatch_closed'),
    needsSetup: !policy,
    setupBlocked: policy ? '' : setupBlock(view, actor),
    setupFix: policy ? '' : setupFix(view),
    pickUpPulls: policy?.automatic_external_pulls === true,
    editable: view.editable,
    ...(blocked ? {blocked} : {}),
    ...(blockedPulls ? {blockedPulls} : {}),
    targetBranch: typeof policy?.target_branch === 'string' ? policy.target_branch.replace(/^refs\/heads\//, '') : '',
    logins: {'soda-coder': roles?.['soda-coder'].account ?? '', 'soda-reviewer': roles?.['soda-reviewer'].account ?? ''},
    loginChoices: {'soda-coder': loginChoices(view, 'soda-coder'), 'soda-reviewer': loginChoices(view, 'soda-reviewer')},
    merge,
    mergeChoices: [...new Set([...(view.options?.mergeMethods ?? []), ...(merge ? [merge] : [])])].map((value) => ({value, label: mergeLabels[value] ?? value})),
  };
}

export async function readAutomation(context: RoleContext, signal: AbortSignal) {
  return automationState(await readSettings(context, signal), context.actor);
}

/** Saves one automation choice on top of the latest saved settings and
 * returns the confirmed state. Turning automation on also resumes a project
 * someone paused, so the one switch is the whole answer. Rejects with a
 * plain-language message when it can't. */
export async function changeAutomation(context: RoleContext, signal: AbortSignal, change: AutomationChange): Promise<AutomationState> {
  const view = await readSettings(context, signal);
  const before = automationState(view, context.actor);
  if (!view.policy || !view.roles || !view.editable) throw new RoleSaveError(before.blocked?.text ?? 'You can’t change these settings.', 403);
  if ('on' in change && change.on && before.blocked) throw new RoleSaveError(before.blocked.text, 400);
  if ('pickUpPulls' in change && change.pickUpPulls && before.blockedPulls) throw new RoleSaveError(before.blockedPulls.text, 400);
  const roles = 'role' in change ? {...view.roles, [change.role]: {...view.roles[change.role], account: change.login}} : view.roles;
  const payload = policyChange(view.policy, roles);
  if ('on' in change) Object.assign(payload, {enabled: change.on, paused: false});
  if ('pickUpPulls' in change) payload.automatic_external_pulls = change.pickUpPulls;
  if ('merge' in change) payload.merge_method = change.merge;
  if ('role' in change) await authorizeLogins(context, signal, view.accounts, roles);
  const response = await context.transport.request(`repositories/${context.repository}/factory/policy`, {method: 'PUT', signal, body: JSON.stringify(payload)});
  if (!response.ok) throw saveRefusal(response.status);
  let confirmed = await readSettings(context, signal);
  check(!!confirmed.policy && Number(confirmed.policy.revision) > Number(view.policy.revision));
  if ('on' in change && change.on && confirmed.missing.includes('dispatch_closed')) {
    const resumed = await context.transport.request(`repositories/${context.repository}/factory/actions`, {method: 'POST', signal, body: JSON.stringify({action: 'resume'})});
    confirmed = await readSettings(context, signal);
    if (!resumed.ok || confirmed.missing.includes('dispatch_closed'))
      throw new RoleSaveError('Automation is on, but new work stays paused until stopped runs finish. Check the Work page.', resumed.status);
  }
  return automationState(confirmed, context.actor);
}

/** The message to show for a failed automation change. */
export const automationFailure = (error: unknown) =>
  error instanceof RoleSaveError ? error.message : 'That change could not be confirmed. The latest is shown.';

/** Shared editor. Choosing a login for a role and saving authorizes that login
 * for the roles that use it; it never connects an account or picks a replacement. */
export class SodaFactoryRoleSettings extends LitElement {
  static properties = {
    view: {state: true},
    roles: {state: true},
    setup: {state: true},
    limits: {state: true},
    automationReviewer: {state: true},
    workspaceOpened: {state: true},
    busy: {state: true},
    message: {state: true},
  };
  declare private view: Settings | undefined;
  declare private roles: Selections | undefined;
  declare private busy: boolean;
  declare private message: string;
  declare private setup: SetupDraft;
  declare private limits: LimitDraft;
  declare private automationReviewer: boolean;
  declare private workspaceOpened: boolean;
  private current: RoleContext | undefined;
  private generation = '';
  private lifetime = new AbortController();
  constructor() {
    super();
    this.setup = {targetBranch: '', checks: '', method: '', useAccount: false, controllerGrant: ''};
    this.limits = {active_minutes: '', review_rounds: '', correction_cycles: ''};
    this.busy = false;
    this.automationReviewer = false;
    this.workspaceOpened = false;
    this.message = '';
  }
  protected createRenderRoot() {
    return this;
  }
  set context(context: RoleContext | undefined) {
    if (context) check(id(context.repository) && !!context.transport.generation);
    if (
      context?.transport === this.current?.transport &&
      context?.repository === this.current?.repository &&
      context?.actor === this.current?.actor &&
      context?.scope === this.current?.scope &&
      context?.transport.generation === this.generation
    )
      return;
    this.lifetime.abort();
    this.lifetime = new AbortController();
    this.current = context;
    this.generation = context?.transport.generation ?? '';
    this.view = this.roles = undefined;
    this.workspaceOpened = false;
    this.busy = false;
    this.message = '';
    if (context) void this.refresh();
    this.requestUpdate();
  }
  disconnectedCallback() {
    this.context = undefined;
    super.disconnectedCallback();
  }
  private live(context: RoleContext, signal: AbortSignal) {
    if (signal.aborted || context !== this.current) return false;
    if (context.transport.generation !== this.generation) {
      this.context = undefined;
      return false;
    }
    return true;
  }
  private read(context: RoleContext, signal: AbortSignal) {
    return readSettings(context, signal);
  }
  /** Reads the saved settings; `note` explains why, when a save just failed. */
  private async refresh(note = '') {
    const context = this.current,
      signal = this.lifetime.signal;
    if (!context || this.busy || !this.live(context, signal)) return;
    this.busy = true;
    this.message = note;
    try {
      const view = await this.read(context, signal);
      if (this.live(context, signal)) {
        this.view = view;
        this.automationReviewer = !!view.policy && samePolicyActor(view.policy.automation_reviewer, view.policy.review_actor);
        this.roles = view.roles ?? {
          'soda-coder': {account: '', harness: 'muse', model: ''},
          'soda-reviewer': {account: '', harness: 'muse', model: ''},
        };
        const limits = view.policy ? object(view.policy.attempt_limits) : undefined;
        this.limits = {
          active_minutes: limits ? String(limits.active_minutes) : '',
          review_rounds: limits ? String(limits.review_rounds) : '',
          correction_cycles: limits ? String(limits.correction_cycles) : '',
        };
        this.setup = view.policy
          ? {...this.setup, checks: Array.isArray(view.policy.required_checks) ? view.policy.required_checks.join('\n') : '', method: typeof view.policy.merge_method === 'string' ? view.policy.merge_method : ''}
          : {targetBranch: view.options?.targetBranch ?? '', checks: '', method: '', useAccount: false, controllerGrant: ''};
      }
    } catch {
      if (this.live(context, signal)) {
        this.view = undefined;
        this.message = 'Role settings are unavailable.';
      }
    } finally {
      if (this.live(context, signal)) this.busy = false;
    }
  }
  private choose(role: Role, field: 'account' | 'model', value: string) {
    if (this.roles) this.roles = {...this.roles, [role]: {...this.roles[role], [field]: value}};
  }
  private authorized(role: Role, account: string) {
    return !!this.view && covers(this.view.accounts, role, account);
  }
  // A login is eligible when it already covers the role or saving can authorize it.
  private eligible(role: Role, account: string) {
    return this.authorized(role, account) ||
      !!this.view?.sources.some((source) => source.connection === account && source.authorizable);
  }
  private validSelection(role: Role, initial: boolean) {
    const selected = this.roles?.[role];
    if (!selected) return false;
    return selected.harness === 'muse' && !!selected.model.trim() &&
      ((initial && !selected.account) || this.eligible(role, selected.account));
  }
  private async authorizeLogins(context: RoleContext, signal: AbortSignal, view: Settings, roles: Selections) {
    const accounts = await authorizeLogins(context, signal, view.accounts, roles);
    if (accounts !== view.accounts && this.live(context, signal)) this.view = {...view, accounts};
  }
  private initialActors() {
    const required = this.view?.options?.controllerGrantRequired;
    if (required && !/^[a-f0-9]{32}$/.test(this.setup.controllerGrant)) return;
    return accountActors(this.view?.bindings ?? [], this.current?.actor, required ? this.setup.controllerGrant : '');
  }
  private checks() {
    return this.setup.checks
      .split(/\r?\n/)
      .map((name) => name.trim())
      .filter(Boolean);
  }
  private selectedLimits() {
    const active = Number(this.limits.active_minutes), reviews = Number(this.limits.review_rounds), corrections = Number(this.limits.correction_cycles);
    if (Object.values(this.limits).some(value => !value.trim()) ||
      !Number.isSafeInteger(active) || active < 1 ||
      !Number.isSafeInteger(reviews) || reviews < 1 || reviews > 16 ||
      !Number.isSafeInteger(corrections) || corrections < 0) return;
    return {
      active_minutes: active, review_rounds: reviews, correction_cycles: corrections,
      max_children: this.view?.policy ? object(this.view.policy.attempt_limits).max_children : 0,
    };
  }
  private async save(event: SubmitEvent) {
    event.preventDefault();
    const context = this.current,
      signal = this.lifetime.signal,
      view = this.view,
      roles = this.roles;
    const limits = this.selectedLimits();
    if (!context || !view || !view.editable || !roles || this.busy || !this.live(context, signal) || !limits) return;
    const initial = view.policy === null,
      actors = initial ? this.initialActors() : undefined;
    if (!(Object.keys(roleNames) as Role[]).every((role) => this.validSelection(role, initial))) return;
    if (initial && (!actors || !this.setup.useAccount || !view.options?.mergeMethods.includes(this.setup.method)))
      return;
    const payload = view.policy
      ? policyChange(view.policy, roles)
      : {
          expected_revision: 0,
          ...policySelections(roles),
          enabled: false,
          paused: false,
          automatic_external_pulls: false,
          target_branch: this.setup.targetBranch,
          merge_method: this.setup.method,
          ...actors,
        };
    Object.assign(payload, {required_checks: this.checks(), merge_method: this.setup.method});
    if (!initial && view.options?.controllerGrantRequired && this.setup.controllerGrant) {
      const replacements = this.initialActors();
      if (!replacements) return;
      Object.assign(payload, replacements);
      if ('resolve_actor' in payload && payload.resolve_actor) payload.resolve_actor = {...object(payload.resolve_actor), controller_grant_id: this.setup.controllerGrant};
    }
    Object.assign(payload, {attempt_limits: limits});
    const reviewIdentity = object(payload).review_actor;
    const automationReviewer = this.automationReviewer;
    Object.assign(payload, {automation_reviewer: automationReviewer && reviewIdentity ? policyActor(reviewIdentity) : null});
    this.busy = true;
    this.message = '';
    try {
      await this.authorizeLogins(context, signal, view, roles);
      const response = await context.transport.request(`repositories/${context.repository}/factory/policy`, {
        method: 'PUT',
        signal,
        body: JSON.stringify(payload),
      });
      if (!response.ok) throw saveRefusal(response.status);
      const confirmed = await this.read(context, signal);
      check(
        confirmed.policy && Number(confirmed.policy.revision) > Number(payload.expected_revision) && confirmed.roles
      );
      const submitted = object(payload);
      for (const slot of ['publish_actor', 'create_actor', 'review_actor', 'merge_actor', 'execution_actor', 'resolve_actor']) {
        if (submitted[slot] != null) check(samePolicyActor(confirmed.policy[slot], submitted[slot]));
      }
      check(automationReviewer
        ? samePolicyActor(confirmed.policy.automation_reviewer, reviewIdentity)
        : confirmed.policy.automation_reviewer == null);
      check(
        (Object.keys(roleNames) as Role[]).every((role) =>
          ['account', 'harness', 'model'].every((key) => confirmed.roles![role][key] === roles[role][key])
        )
      );
      check(confirmed.policy.enabled === payload.enabled);
      check(JSON.stringify(confirmed.policy.required_checks) === JSON.stringify(submitted.required_checks));
      const savedLimits = object(confirmed.policy.attempt_limits);
      check(['active_minutes', 'review_rounds', 'correction_cycles'].every(key => savedLimits[key] === object(limits)[key]));
      if (initial)
        check(
          confirmed.policy.enabled === false &&
            confirmed.policy.target_branch === payload.target_branch &&
            confirmed.policy.merge_method === payload.merge_method
        );
      if (this.live(context, signal)) {
        this.view = confirmed;
        this.roles = confirmed.roles;
        this.message = 'Settings saved.';
      }
    } catch (error) {
      if (this.live(context, signal)) {
        const note = automationFailure(error);
        this.message = note;
        // Anything but a rejected form may have changed what is saved: show it.
        if (!(error instanceof RoleSaveError && error.status === 400)) {
          this.busy = false;
          await this.refresh(note);
        }
      }
    } finally {
      if (this.live(context, signal)) this.busy = false;
    }
  }
  /** Flips the automation switch or the pull-request choice and shows what is saved now.
   * When something blocks turning on, it says what and moves to the field that fixes it. */
  private async flip(change: AutomationChange, state: AutomationState) {
    const context = this.current,
      signal = this.lifetime.signal;
    if (!context || this.busy || !this.live(context, signal)) return;
    const block = 'on' in change && change.on ? state.blocked : 'pickUpPulls' in change && change.pickUpPulls ? state.blockedPulls : undefined;
    if (block) {
      this.message = block.text;
      if (block.field) this.querySelector<HTMLElement>(`[aria-label="${block.field}"]`)?.focus();
      return;
    }
    this.busy = true;
    this.message = '';
    let note = '';
    try {
      await changeAutomation(context, signal, change);
    } catch (error) {
      note = automationFailure(error);
    }
    if (!this.live(context, signal)) return;
    this.busy = false;
    await this.refresh(note);
  }
  /** Gives the project its first settings, or says what Factory needs first. */
  private async setUp() {
    const context = this.current,
      signal = this.lifetime.signal;
    if (!context || this.busy || !this.live(context, signal)) return;
    this.busy = true;
    this.message = '';
    let note = '';
    try {
      await setUpProject(context, signal);
    } catch (error) {
      note = automationFailure(error);
    }
    if (!this.live(context, signal)) return;
    this.busy = false;
    await this.refresh(note);
  }
  /** The Execution controller beside these settings, when this host shows one. */
  private controller() {
    return this.closest('.soda-factory-settings')?.querySelector<HTMLElement>('[id$="-controller"]') ?? null;
  }
  private automation(view: Settings) {
    const state = automationState(view, this.current?.actor);
    if (state.needsSetup)
      return html`<section class="soda-factory-automation" aria-label="Automation">
        <div class="soda-factory-automation-row">
          <div>
            <h3>Set up this project</h3>
            <p>Factory uses your Muse login for coding and review and merges with squash into the default branch. You can change any of it after. Automation stays off until you turn it on.</p>
            ${state.setupBlocked ? html`<p role="note" data-tone="warning">${state.setupBlocked}</p>` : ''}
          </div>
          ${state.setupFix === 'computer' && this.controller()
            ? html`<button type="button" @click=${() => this.controller()!.scrollIntoView({block: 'start'})}>Go to Execution controller</button>`
            : state.setupBlocked ? '' : html`<button type="button" ?disabled=${this.busy} @click=${() => void this.setUp()}>Set up this project</button>`}
        </div>
      </section>`;
    const row = (label: string, detail: string, on: boolean, change: AutomationChange, block: AutomationBlock | undefined) =>
      html`<div class="soda-factory-automation-row">
        <div>
          <h3>${label}</h3>
          <p>${detail}</p>
          ${block && !on ? html`<p role="note" data-tone="warning">Can’t turn on yet. ${block.text}</p>` : ''}
        </div>
        <button type="button" role="switch" aria-label=${label} aria-checked=${on ? 'true' : 'false'} ?disabled=${this.busy} @click=${() => void this.flip(change, state)}>
          <span aria-hidden="true"></span>${on ? 'On' : 'Off'}
        </button>
      </div>`;
    return html`<section class="soda-factory-automation" aria-label="Automation">
      ${row('Automation', `Agents take accepted issues, open pull requests, review them and merge into ${state.targetBranch || 'the default branch'}.`, state.on, {on: !state.on}, state.blocked)}
      ${row('Pick up pull requests', 'Take over pull requests from people who can write to this repository when they close an accepted issue.', state.pickUpPulls, {pickUpPulls: !state.pickUpPulls}, state.blocked ? undefined : state.blockedPulls)}
    </section>`;
  }
  protected render() {
    if (!this.current) return html``;
    const view = this.view,
      roles = this.roles;
    const initial = view?.policy === null;
    const canSave =
      view?.editable &&
      roles &&
      this.selectedLimits() &&
      (Object.keys(roleNames) as Role[]).every(
        (role) => this.validSelection(role, initial)
      ) &&
      (!initial ||
        (!!this.initialActors() &&
          this.setup.useAccount &&
          !!this.setup.targetBranch)) &&
      this.checks().length <= 32 &&
      (!view.policy?.enabled || this.checks().length > 0) &&
      (initial ? !!view.options?.mergeMethods.includes(this.setup.method) : !!this.setup.method);
    return html`<section aria-label="Agent role settings">
      ${view ? this.automation(view) : ''}
      <p role="status">${this.message}</p>
      ${view?.options?.nativeSetup ? this.workspaceOpened
        ? html`<soda-factory-workspace-setup .context=${this.current}></soda-factory-workspace-setup>`
        : html`<button type="button" @click=${() => { this.workspaceOpened = true; }}>Workspace setup</button>` : ''}
      <h3>Agent roles</h3>
      <p>
        Choose the model and Muse login for each role. Saving lets that login run those roles. Factory does not
        switch logins automatically.
      </p>
      ${!view && this.message && !this.busy ? html`<button type="button" @click=${() => this.refresh()}>Try again</button>` : ''}
      ${view?.sources.map((source) => source.tool
        ? html`<div role="status">
            <p>${toolStatusLabels[source.tool.state]}</p>
            ${source.tool.latest ? html`<p>Latest release: ${source.tool.latest}</p>` : ''}
            ${source.tool.selected ? html`<p>Selected release: ${source.tool.selected}</p>` : ''}
            ${source.tool.checked ? html`<p>Last verified: <time datetime=${new Date(source.tool.checked * 1000).toISOString()}>${new Date(source.tool.checked * 1000).toLocaleString()}</time></p>` : ''}
            ${source.tool.error ? html`<p>${source.tool.error}</p>` : ''}
          </div>`
        : '')}
      ${view?.sources.some((source) => !source.available) ? html`<p>Execution through this login is currently unavailable. You can still save its role choices.</p>` : ''}
      ${
        view && roles
          ? html`<form @submit=${(event: SubmitEvent) => this.save(event)}>
              ${
                initial
                  ? html`<fieldset ?disabled=${this.busy || !view.editable}>
                      <legend>Set up this project</legend>
                      <p>Choose where agents merge. Automation stays off until you turn it on above.</p>
                      <label
                        >Target branch
                        <input
                          aria-label="Automation target branch"
                          .value=${this.setup.targetBranch}
                          required
                          pattern="refs/heads/.+"
                          maxlength="256"
                          placeholder="refs/heads/main"
                          @input=${(event: Event) => {
                            this.setup = {...this.setup, targetBranch: (event.target as HTMLInputElement).value};
                          }}
                      /></label>
                      <label
                        ><input
                          type="checkbox"
                          .checked=${this.setup.useAccount}
                          @change=${(event: Event) => {
                            this.setup = {...this.setup, useAccount: (event.target as HTMLInputElement).checked};
                          }}
                        />Use ${this.current.accountLabel ?? 'the connected account'} for repository automation.</label
                      >
                      <p>Repository permissions, required reviews and CI are checked again before remote actions.</p>
                      ${!view.options || !view.options.mergeMethods.length || !this.initialActors() ? html`<p>Setup options are unavailable for this connection.</p>` : ''}
                    </fieldset>`
                  : ''
              }
              <fieldset ?disabled=${this.busy || !view.editable}>
                <legend>Merging</legend>
                <label
                  >Required CI checks
                  <textarea
                    aria-label="Required CI checks"
                    .value=${this.setup.checks}
                    ?required=${view.policy?.enabled === true}
                    placeholder="One exact check name per line"
                    @input=${(event: Event) => {
                      this.setup = {...this.setup, checks: (event.target as HTMLTextAreaElement).value};
                    }}
                  ></textarea>
                </label>
                <p>Agents merge only after these checks pass. Automation can't turn on without at least one.</p>
                <label
                  >Merge as
                  <select
                    aria-label="Merge as"
                    required
                    @change=${(event: Event) => {
                      this.setup = {...this.setup, method: (event.target as HTMLSelectElement).value};
                    }}
                  >
                    <option value="" .selected=${!this.setup.method}>Choose how to merge</option>
                    ${automationState(view).mergeChoices.map((choice) => html`<option .value=${choice.value} .selected=${choice.value === this.setup.method}>${choice.label}</option>`)}
                  </select></label
                >
              </fieldset>
              ${view.options?.controllerGrantRequired ? html`
                <label>Fountain operation grant
                  <input aria-label="Fountain operation grant" maxlength="32" pattern="[a-f0-9]{32}" ?required=${initial}
                    .value=${this.setup.controllerGrant}
                    @input=${(event: Event)=>{this.setup={...this.setup,controllerGrant:(event.target as HTMLInputElement).value.trim()};}}>
                </label>
                <p>Choose the grant from an explicit Repository controller authorization. Saving captures it for new work with its original expiry. Leave blank to retain an existing policy's grant. Workspace consent remains separate.</p>
              ` : ''}
              <fieldset ?disabled=${this.busy || !view.editable}>
                <legend>Execution limits</legend>
                <p>Each run gets its own time limit. Reviews and corrections share the captured attempt counts. Waiting does not replenish counts. Changes apply to new attempts.</p>
                ${([
                  ['active_minutes', 'Time limit per run in minutes', 1, undefined],
                  ['review_rounds', 'Maximum independent reviews per attempt', 1, 16],
                  ['correction_cycles', 'Maximum corrections per attempt', 0, undefined],
                ] as const).map(([field, label, min, max]) => html`<label>${label}
                  <input type="number" aria-label=${label} min=${min} max=${max ?? Number.MAX_SAFE_INTEGER} step="1" required
                    .value=${this.limits[field]}
                    @input=${(event: Event) => {this.limits = {...this.limits, [field]: (event.target as HTMLInputElement).value};}} />
                </label>`)}
              </fieldset>
              <fieldset ?disabled=${this.busy || !view.editable}>
                <legend>AI review publication</legend>
                <p>Personal accounts publish AI inspection as comments. GitHub's required approvals and CI still apply.</p>
                <label><input
                  type="checkbox"
                  aria-label="Allow AI votes from this automation identity"
                  .checked=${this.automationReviewer}
                  @change=${(event: Event) => {this.automationReviewer = (event.target as HTMLInputElement).checked;}}
                />Designate review account ${String(view.policy ? object(view.policy.review_actor).actor_id : this.current.actor ?? '')} as an automation identity that may approve or request changes.</label>
                <p>Use this only for an account dedicated to automation. Votes can replace that account's earlier review. Reviews of its own PRs remain comments.</p>
              </fieldset>
              ${(Object.keys(roleNames) as Role[]).map((role) => {
                const selected = roles[role],
                  logins = [...new Set([...view.sources.filter((login) => login.authorizable).map((login) => login.connection),
                    ...view.accounts.filter((account) => account.active && account.roles.includes(role)).map((account) => account.connection)])],
                  source = view.sources.find((source) => source.connection === selected.account);
                return html`<fieldset ?disabled=${this.busy || !view.editable}>
                  <legend>${roleNames[role]}</legend>
                  <label
                    >Provider
                    <select aria-label=${`${roleNames[role]} provider`} disabled>
                      <option selected>${selected.harness === 'muse' ? 'Muse' : 'Unavailable provider'}</option>
                    </select></label
                  >
                  <label
                    >Model
                    <input
                      aria-label=${`${roleNames[role]} model`}
                      .value=${selected.model}
                      maxlength="128"
                      required
                      @input=${(event: Event) => this.choose(role, 'model', (event.target as HTMLInputElement).value)}
                  /></label>
                  <label
                    >Login
                    <select
                      aria-label=${`${roleNames[role]} login`}
                      ?required=${!initial}
                      @change=${(event: Event) => this.choose(role, 'account', (event.target as HTMLSelectElement).value)}
                    >
                      <option value="" .selected=${!selected.account}>Choose a login</option>
                      ${selected.account && !this.eligible(role, selected.account) ? html`<option .value=${selected.account} .selected=${true} disabled>${selected.account} — unavailable for this role</option>` : ''}
                      ${logins.map((login) => html`<option .value=${login} .selected=${login === selected.account}>${view.sources.find((source) => source.connection === login)?.label ?? login}</option>`)}
                    </select></label
                  >
                  ${logins.length === 0 ? html`<p>Sign in to Muse on this computer to choose a login.</p>` : ''}
                </fieldset>`;
              })}
              <p>Only Muse is currently supported.</p>
              <button type="submit" value="save" ?disabled=${this.busy || !canSave}>Save settings</button>
              ${!view.editable ? html`<p>You cannot change this repository's role settings with the current connection.</p>` : ''}
            </form>`
          : this.busy
            ? html`<p>Loading role settings…</p>`
            : ''
      }
    </section>`;
  }
}
customElements.define('soda-factory-role-settings', SodaFactoryRoleSettings);
