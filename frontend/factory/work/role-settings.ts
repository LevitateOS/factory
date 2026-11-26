import {LitElement, html} from 'lit';
import {check, id, object, readSodaJSON} from '../api/models.js';
import type {FactoryTransport} from '../api/transport.js';
import './workspace-setup.js';

interface RoleContext {
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
  if (status.policy === null) return {policy: null, accounts, sources, editable, bindings, options};
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
  return {policy, roles, accounts, sources, editable, bindings, options};
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

/** First settings for a project that has none, so its first Feed post is read
 * right away: every role uses this computer's Muse login, merges squash into
 * the default branch with no required checks, and automation stays off until
 * the person turns it on in Settings. Returns false, changing nothing, when the
 * project already has settings or needs a choice only the person can make. */
export async function setUpNewProject(context: RoleContext, signal: AbortSignal): Promise<boolean> {
  const view = await readSettings(context, signal);
  const source = view.sources.find((candidate) => candidate.authorizable);
  const options = view.options;
  const actors = accountActors(view.bindings, context.actor);
  if (view.policy !== null || !view.editable || !source || !options || options.controllerGrantRequired || !actors || !options.mergeMethods.length) return false;
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
      ...actors,
      automation_reviewer: null,
    }),
  });
  return response.ok;
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
  };
}

// Judgments belong to JEV, not to a Muse login: saving clears any older selection.
function policySelections(roles: Selections) {
  return {roles, judgments: null};
}

/** Shared editor. Choosing a login for a role and saving authorizes that login
 * for the roles that use it; it never connects an account or picks a replacement. */
export class SodaFactoryRoleSettings extends LitElement {
  static properties = {
    view: {state: true},
    roles: {state: true},
    setup: {state: true},
    limits: {state: true},
    automationReviewer: {state: true},
    automaticExternalPulls: {state: true},
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
  declare private automaticExternalPulls: boolean;
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
    this.automaticExternalPulls = false;
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
    this.automaticExternalPulls = false;
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
        this.automaticExternalPulls = view.policy?.automatic_external_pulls === true;
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
        if (!view.policy)
          this.setup = {targetBranch: view.options?.targetBranch ?? '', checks: '', method: '', useAccount: false, controllerGrant: ''};
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
  private automaticPullsReady() {
    const roles = this.roles;
    if (!roles || this.view?.options?.controllerGrantRequired === true) return false;
    const account = roles['soda-coder'].account;
    return !!account && roles['soda-reviewer'].account === account &&
      (Object.keys(roleNames) as Role[]).every(role => this.validSelection(role, false)) &&
      !!this.selectedLimits();
  }
  private async save(event: SubmitEvent) {
    event.preventDefault();
    const context = this.current,
      signal = this.lifetime.signal,
      view = this.view,
      disabling = event.submitter?.getAttribute('value') === 'disable',
      enrolling = event.submitter?.getAttribute('value') === 'enroll',
      roles = disabling ? view?.roles : this.roles;
    const limits = this.selectedLimits();
    if (!context || !view || !view.editable || !roles || this.busy || !this.live(context, signal) || (!disabling && !limits)) return;
    if (!disabling && this.automaticExternalPulls && !this.automaticPullsReady()) return;
    const initial = view.policy === null,
      actors = initial ? this.initialActors() : undefined;
    if (initial && (disabling || enrolling)) return;
    if (
      !disabling && !(Object.keys(roleNames) as Role[]).every(
        (role) => this.validSelection(role, initial)
      )
    )
      return;
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
          required_checks: this.checks(),
          ...actors,
        };
    if (enrolling || disabling) payload.enabled = enrolling;
    if (!initial && !disabling && view.options?.controllerGrantRequired && this.setup.controllerGrant) {
      const replacements = this.initialActors();
      if (!replacements) return;
      Object.assign(payload, replacements);
      if ('resolve_actor' in payload && payload.resolve_actor) payload.resolve_actor = {...object(payload.resolve_actor), controller_grant_id: this.setup.controllerGrant};
    }
    if (!disabling) Object.assign(payload, {attempt_limits: limits});
    if (!disabling) payload.automatic_external_pulls = this.automaticExternalPulls;
    const reviewIdentity = object(payload).review_actor;
    const automationReviewer = disabling ? !!view.policy?.automation_reviewer : this.automationReviewer;
    Object.assign(payload, {automation_reviewer: disabling
      ? view.policy?.automation_reviewer == null ? null : policyActor(view.policy.automation_reviewer)
      : automationReviewer && reviewIdentity ? policyActor(reviewIdentity) : null});
    this.busy = true;
    this.message = '';
    try {
      if (!disabling) await this.authorizeLogins(context, signal, view, roles);
      const response = await context.transport.request(`repositories/${context.repository}/factory/policy`, {
        method: 'PUT',
        signal,
        body: JSON.stringify(payload),
      });
      if (!response.ok) {
        throw new RoleSaveError(
          response.status === 400
            ? 'Policy settings were rejected. Check the branch, CI checks and role choices.'
            : response.status === 409 || response.status === 412
              ? 'These settings changed somewhere else. The latest is shown; save again.'
              : response.status === 403
                ? 'You can’t change these settings.'
                : 'The save could not be confirmed. The latest is shown.',
          response.status
        );
      }
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
      check(confirmed.policy.automatic_external_pulls === payload.automatic_external_pulls);
      if (!disabling) {
        const savedLimits = object(confirmed.policy.attempt_limits);
        check(['active_minutes', 'review_rounds', 'correction_cycles'].every(key => savedLimits[key] === object(limits)[key]));
      }
      if (initial)
        check(
          confirmed.policy.enabled === false &&
            confirmed.policy.target_branch === payload.target_branch &&
            confirmed.policy.merge_method === payload.merge_method &&
            JSON.stringify(confirmed.policy.required_checks) === JSON.stringify(payload.required_checks)
        );
      if (this.live(context, signal)) {
        this.view = confirmed;
        this.roles = confirmed.roles;
        this.automaticExternalPulls = confirmed.policy.automatic_external_pulls === true;
        this.message = initial ? 'Repository policy saved with automation disabled.'
          : enrolling ? 'Automation policy enrolled. Existing pauses and execution prerequisites still apply.'
            : disabling ? 'Automation policy disabled. Existing work may still need retirement.' : 'Role selections saved.';
      }
    } catch (error) {
      if (this.live(context, signal)) {
        const note = error instanceof RoleSaveError ? error.message : 'The save could not be confirmed. The latest is shown.';
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
  protected render() {
    if (!this.current) return html``;
    const view = this.view,
      roles = this.roles;
    const initial = view?.policy === null;
    const canSave =
      view?.editable &&
      roles &&
      this.selectedLimits() &&
      (!this.automaticExternalPulls || this.automaticPullsReady()) &&
      (Object.keys(roleNames) as Role[]).every(
        (role) => this.validSelection(role, initial)
      ) &&
      (!initial ||
        (!!this.initialActors() &&
          this.setup.useAccount &&
          !!this.setup.targetBranch &&
          this.checks().length > 0 &&
          this.checks().length <= 32 &&
          view.options?.mergeMethods.includes(this.setup.method)));
    return html`<section aria-label="Agent role settings">
      ${view?.options?.nativeSetup ? this.workspaceOpened
        ? html`<soda-factory-workspace-setup .context=${this.current}></soda-factory-workspace-setup>`
        : html`<button type="button" @click=${() => { this.workspaceOpened = true; }}>Workspace setup</button>` : ''}
      <h3>Agent roles</h3>
      <p>
        Choose the model and Muse login for each role. Saving lets that login run those roles. Factory does not
        switch logins automatically.
      </p>
      <p role="status">${this.message}</p>
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
                      <legend>Set up repository automation</legend>
                      <p>
                        Save the full pipeline policy with automation disabled. Execution setup is still
                        required.
                      </p>
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
                        >Required CI checks
                        <textarea
                          aria-label="Required CI checks"
                          .value=${this.setup.checks}
                          required
                          placeholder="One exact check name per line"
                          @input=${(event: Event) => {
                            this.setup = {...this.setup, checks: (event.target as HTMLTextAreaElement).value};
                          }}
                        ></textarea>
                      </label>
                      <label
                        >Merge method
                        <select
                          aria-label="Automation merge method"
                          required
                          @change=${(event: Event) => {
                            this.setup = {...this.setup, method: (event.target as HTMLSelectElement).value};
                          }}
                        >
                          <option value="" .selected=${!this.setup.method}>Choose an enabled method</option>
                          ${(view.options?.mergeMethods ?? []).map((method) => html`<option .value=${method} .selected=${method === this.setup.method}>${mergeLabels[method]}</option>`)}
                        </select></label
                      >
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
              <fieldset ?disabled=${this.busy || !view.editable}>
                <legend>Automatic external pull requests</legend>
                <label><input
                  type="checkbox"
                  aria-label="Automatically adopt eligible same-repository pull requests"
                  .checked=${this.automaticExternalPulls}
                  ?disabled=${view.options?.controllerGrantRequired === true || (!this.automaticExternalPulls && !this.automaticPullsReady())}
                  @change=${(event: Event) => {this.automaticExternalPulls = (event.target as HTMLInputElement).checked;}}
                />Automatically adopt eligible same-repository pull requests.</label>
                <p>Adoption requires the PR author to be a current repository writer and exactly one explicitly accepted issue linked through the provider's structured closing links. Factory verifies the current head, base and policy target and keeps the existing finite attempt limits. Fork pull requests require an explicit command.</p>
                <p>Select one login shared by Coding and Review. Populate the finite execution limits before enabling this choice.</p>
                <p>This choice is separate from policy enrollment and automatic issue polling.</p>
                ${view.options?.controllerGrantRequired ? html`<p>Automatic external pull request adoption is unavailable in Fountain.</p>` : ''}
              </fieldset>
              <p>
                Only Muse is currently supported.
                ${initial ? 'Automation stays disabled until execution setup is ready.' : 'Saving retains the other automation policy settings and existing attempt captures.'}
              </p>
              <button type="submit" value="save" ?disabled=${this.busy || !canSave}>
                ${initial ? 'Save disabled automation policy' : 'Save role selections'}
              </button>
              ${!initial ? html`
                <p>Policy enrollment permits the full automated delivery workflow. Placement, reviewed setup and available execution are checked before dispatch.</p>
                ${view.policy?.enabled
                  ? html`<button type="submit" value="disable" formnovalidate ?disabled=${this.busy || !view.editable}>Disable automation policy</button>`
                  : html`<button type="submit" value="enroll" ?disabled=${this.busy || !canSave}>Enroll automation policy</button>`}
              ` : ''}
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
