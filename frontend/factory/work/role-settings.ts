import {LitElement, html} from 'lit';
import {check, id, object, readSodaJSON} from '../api/models.js';
import type {FactoryTransport} from '../api/transport.js';

interface RoleContext {
  readonly transport: FactoryTransport;
  readonly repository: string;
  readonly actor?: string;
  readonly accountLabel?: string;
}
const roleNames = {'soda-coder': 'Coding', 'soda-reviewer': 'Review'} as const;
const toolStatusLabels = {
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
  minutes: number;
}
interface SpendingSource {
  connection: string;
  label: string;
  available: boolean;
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
  options: {targetBranch: string; mergeMethods: string[]} | undefined;
}
interface SetupDraft {
  targetBranch: string;
  checks: string;
  method: string;
  useAccount: boolean;
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
      minutes: Number(account.allowance_minutes),
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

// The existing policy command owns CAS, current permissions and actor bindings.
// Preserve the rest of that policy while changing only its role selections.
function policyChange(policy: Record<string, unknown>, roles: Selections) {
  const resolve = policy.resolve_actor == null ? {} : object(policy.resolve_actor);
  return {
    expected_revision: policy.revision,
    roles,
    enabled: policy.enabled,
    paused: policy.paused,
    target_branch: policy.target_branch,
    merge_method: policy.merge_method,
    required_checks: policy.required_checks,
    attempt_limits: policy.attempt_limits,
    reassessment_minutes: policy.reassessment_minutes,
    intent_minutes: policy.intent_minutes,
    publish_actor: policyActor(policy.publish_actor),
    create_actor: policyActor(policy.create_actor),
    review_actor: policyActor(policy.review_actor),
    merge_actor: policyActor(policy.merge_actor),
    ...(resolve.actor_id && resolve.actor_id !== '0' ? {resolve_actor: policyActor(resolve)} : {}),
  };
}

/** Shared editor; saved sponsorship references are nonsecret spending choices.
 * It neither connects an account nor chooses a replacement for a missing one. */
export class SodaFactoryRoleSettings extends LitElement {
  static properties = {
    view: {state: true},
    roles: {state: true},
    setup: {state: true},
    busy: {state: true},
    message: {state: true},
  };
  declare private view: Settings | undefined;
  declare private roles: Selections | undefined;
  declare private busy: boolean;
  declare private message: string;
  declare private setup: SetupDraft;
  private current: RoleContext | undefined;
  private generation = '';
  private lifetime = new AbortController();
  constructor() {
    super();
    this.setup = {targetBranch: '', checks: '', method: '', useAccount: false};
    this.busy = false;
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
      context?.transport.generation === this.generation
    )
      return;
    this.lifetime.abort();
    this.lifetime = new AbortController();
    this.current = context;
    this.generation = context?.transport.generation ?? '';
    this.view = this.roles = undefined;
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
  private async read(context: RoleContext, signal: AbortSignal): Promise<Settings> {
    const path = `repositories/${context.repository}/factory`;
    const [status, access] = await Promise.all([
      context.transport.request(path, {method: 'GET', signal}),
      context.transport.request(`${path}/access`, {method: 'GET', signal}),
    ]);
    check(status.ok && access.ok);
    return settings(await readSodaJSON(status), await readSodaJSON(access), context.repository);
  }
  private async refresh() {
    const context = this.current,
      signal = this.lifetime.signal;
    if (!context || this.busy || !this.live(context, signal)) return;
    this.busy = true;
    this.message = '';
    try {
      const view = await this.read(context, signal);
      if (this.live(context, signal)) {
        this.view = view;
        this.roles = view.roles ?? {
          'soda-coder': {account: '', harness: 'muse', model: ''},
          'soda-reviewer': {account: '', harness: 'muse', model: ''},
        };
        if (!view.policy)
          this.setup = {targetBranch: view.options?.targetBranch ?? '', checks: '', method: '', useAccount: false};
      }
    } catch {
      if (this.live(context, signal)) {
        this.view = undefined;
        this.message = 'Role settings are unavailable. Refresh to try again.';
      }
    } finally {
      if (this.live(context, signal)) this.busy = false;
    }
  }
  private choose(role: Role, field: 'account' | 'model', value: string) {
    if (this.roles) this.roles = {...this.roles, [role]: {...this.roles[role], [field]: value}};
  }
  private eligible(role: Role, account: string) {
    return !!this.view?.accounts.some(
      (source) => source.connection === account && source.active && source.roles.includes(role)
    );
  }
  private async saveFunding(event: SubmitEvent) {
    event.preventDefault();
    const context = this.current,
      signal = this.lifetime.signal,
      view = this.view;
    if (!context || !view?.editable || this.busy || !this.live(context, signal)) return;
    const form = event.currentTarget as HTMLFormElement;
    if (!form.reportValidity()) return;
    const values = new FormData(form),
      connection = String(values.get('source') ?? ''),
      minutes = Number(values.get('minutes')),
      active = event.submitter?.getAttribute('value') !== 'withdraw';
    const source = view.sources.find((source) => source.connection === connection);
    if (!source || !Number.isSafeInteger(minutes) || minutes < 1 || minutes > 10080) return;
    const stored = view.accounts.find((account) => account.connection === connection);
    if (stored && (!Number.isSafeInteger(stored.revision) || stored.revision < 1)) return;
    this.busy = true;
    this.message = '';
    try {
      const response = await context.transport.request(
        `repositories/${context.repository}/factory/sponsorships/${connection}`,
        {
          method: 'PUT',
          signal,
          body: JSON.stringify({
            expected_revision: stored?.revision ?? 0,
            roles: Object.keys(roleNames),
            allowance_minutes: minutes,
            active,
          }),
        }
      );
      if (!response.ok)
        throw new RoleSaveError(
          'The source allowance could not be saved. Refresh before trying again.',
          response.status
        );
      const confirmed = await this.read(context, signal),
        account = confirmed.accounts.find((account) => account.connection === connection);
      check(
        account?.active === active &&
          account.minutes === minutes &&
          account.roles.length === 2 &&
          Object.keys(roleNames).every((role) => account.roles.includes(role))
      );
      if (this.live(context, signal)) {
        this.view = confirmed;
        this.message = active
          ? 'Source allowance saved. Select it separately for each role.'
          : 'Source allowance withdrawn. Existing work may still need retirement.';
      }
    } catch {
      if (this.live(context, signal)) {
        this.view = undefined;
        this.message = 'The source allowance could not be confirmed. Refresh before changing it again.';
      }
    } finally {
      if (this.live(context, signal)) this.busy = false;
    }
  }
  private initialActors() {
    const actor = this.current?.actor;
    if (!actor || !id(actor)) return;
    const actors: Record<string, Record<string, unknown>> = {};
    for (const [slot, kind] of Object.entries({
      publish_actor: 'git.ref.publish',
      create_actor: 'pull_request.create',
      review_actor: 'pull_request.review.submit',
      merge_actor: 'pull_request.merge',
    })) {
      const matches = this.view?.bindings.filter((ref) => ref.actor_id === actor && ref.kind === kind) ?? [];
      if (matches.length !== 1) return;
      actors[slot] = policyActor(matches[0]);
    }
    return actors;
  }
  private checks() {
    return this.setup.checks
      .split(/\r?\n/)
      .map((name) => name.trim())
      .filter(Boolean);
  }
  private async save(event: SubmitEvent) {
    event.preventDefault();
    const context = this.current,
      signal = this.lifetime.signal,
      view = this.view,
      roles = this.roles;
    if (!context || !view || !view.editable || !roles || this.busy || !this.live(context, signal)) return;
    const initial = view.policy === null,
      actors = initial ? this.initialActors() : undefined;
    if (
      !(Object.keys(roleNames) as Role[]).every(
        (role) =>
          roles[role].harness === 'muse' &&
          ((initial && !roles[role].account) || this.eligible(role, roles[role].account))
      )
    )
      return;
    if (initial && (!actors || !this.setup.useAccount || !view.options?.mergeMethods.includes(this.setup.method)))
      return;
    const payload = view.policy
      ? policyChange(view.policy, roles)
      : {
          expected_revision: 0,
          roles,
          enabled: false,
          paused: false,
          target_branch: this.setup.targetBranch,
          merge_method: this.setup.method,
          required_checks: this.checks(),
          ...actors,
        };
    this.busy = true;
    this.message = '';
    try {
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
              ? 'Policy changed. Refresh before saving again.'
              : response.status === 403
                ? 'Permission to change policy is unavailable. Refresh to check current access.'
                : 'The save could not be confirmed. Refresh before changing roles again.',
          response.status
        );
      }
      const confirmed = await this.read(context, signal);
      check(
        confirmed.policy && Number(confirmed.policy.revision) > Number(payload.expected_revision) && confirmed.roles
      );
      check(
        (Object.keys(roleNames) as Role[]).every((role) =>
          ['account', 'harness', 'model'].every((key) => confirmed.roles![role][key] === roles[role][key])
        )
      );
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
        this.message = initial ? 'Repository policy saved with automation disabled.' : 'Role selections saved.';
      }
    } catch (error) {
      if (this.live(context, signal)) {
        if (!(error instanceof RoleSaveError && error.status === 400)) this.view = undefined;
        this.message =
          error instanceof RoleSaveError
            ? error.message
            : 'The save could not be confirmed. Refresh before changing roles again.';
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
      (Object.keys(roleNames) as Role[]).every(
        (role) =>
          roles[role].harness === 'muse' &&
          !!roles[role].model.trim() &&
          ((initial && !roles[role].account) || this.eligible(role, roles[role].account))
      ) &&
      (!initial ||
        (!!this.initialActors() &&
          this.setup.useAccount &&
          !!this.setup.targetBranch &&
          this.checks().length > 0 &&
          this.checks().length <= 32 &&
          view.options?.mergeMethods.includes(this.setup.method)));
    return html`<section aria-label="Agent role settings">
      <h3>Agent roles</h3>
      <p>
        Choose the provider, model and saved spending source for each role. Factory does not switch sources
        automatically.
      </p>
      <p role="status">${this.message}</p>
      <button type="button" ?disabled=${this.busy} @click=${() => this.refresh()}>Refresh roles</button>
      ${
        view?.sources.length
          ? html`<form @submit=${(event: SubmitEvent) => this.saveFunding(event)}>
              <fieldset ?disabled=${this.busy || !view.editable}>
                <legend>Allow a spending source</legend>
                <p>
                  Muse manages its saved login and account choice. Factory does not copy its credentials or pin a
                  subscription.
                </p>
                <label
                  >Source
                  <select name="source" aria-label="Spending source" required>
                    <option value="">Choose a source</option>
                    ${view.sources.map((source) => html`<option .value=${source.connection}>${source.label}</option>`)}
                  </select></label
                >
                <label
                  >Run allowance in minutes
                  <input
                    name="minutes"
                    aria-label="Source allowance in minutes"
                    type="number"
                    min="1"
                    max="10080"
                    step="1"
                    value="30"
                    required
                /></label>
                <p>
                  This repository shares the allowance across Coding and Review. It is Factory's run-time allowance, not
                  a provider invoice limit.
                </p>
                ${view.sources.map((source) => source.tool
                  ? html`<div role="status">
                      <p>${toolStatusLabels[source.tool.state]}</p>
                      ${source.tool.latest ? html`<p>Latest release: ${source.tool.latest}</p>` : ''}
                      ${source.tool.selected ? html`<p>Selected release: ${source.tool.selected}</p>` : ''}
                      ${source.tool.checked ? html`<p>Last verified: <time datetime=${new Date(source.tool.checked * 1000).toISOString()}>${new Date(source.tool.checked * 1000).toLocaleString()}</time></p>` : ''}
                      ${source.tool.error ? html`<p>${source.tool.error}</p>` : ''}
                    </div>`
                  : '')}
                ${view.sources.some((source) => !source.available) ? html`<p>Execution through this source is currently unavailable. You can save its allowance and role choices.</p>` : ''}
                <button type="submit" value="save">Save source allowance</button>
                <button type="submit" value="withdraw">Withdraw source allowance</button>
              </fieldset>
            </form>`
          : ''
      }
      ${
        view && roles
          ? html`<form @submit=${(event: SubmitEvent) => this.save(event)}>
              ${
                initial
                  ? html`<fieldset ?disabled=${this.busy || !view.editable}>
                      <legend>Set up repository automation</legend>
                      <p>
                        Save the full pipeline policy with automation disabled. Execution setup and funding are still
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
                      ${!view.options || !view.options.mergeMethods.length || !this.initialActors() ? html`<p>Setup options are unavailable for this connection. Refresh before saving.</p>` : ''}
                    </fieldset>`
                  : ''
              }
              ${(Object.keys(roleNames) as Role[]).map((role) => {
                const selected = roles[role],
                  accounts = view.accounts.filter((account) => account.active && account.roles.includes(role));
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
                    >Spending source
                    <select
                      aria-label=${`${roleNames[role]} spending source`}
                      ?required=${!initial}
                      @change=${(event: Event) => this.choose(role, 'account', (event.target as HTMLSelectElement).value)}
                    >
                      <option value="" .selected=${!selected.account}>Choose a saved source</option>
                      ${selected.account && !this.eligible(role, selected.account) ? html`<option .value=${selected.account} .selected=${true} disabled>${selected.account} — unavailable for this role</option>` : ''}
                      ${accounts.map((account) => html`<option .value=${account.connection} .selected=${account.connection === selected.account}>${view.sources.find((source) => source.connection === account.connection)?.label ?? account.connection}</option>`)}
                    </select></label
                  >
                  ${accounts.length === 0 ? html`<p>No active saved spending source is available for this role.</p>` : ''}
                </fieldset>`;
              })}
              <p>
                Only Muse is currently supported.
                ${initial ? 'Automation stays disabled until execution setup and funding are ready.' : 'Each role keeps its current execution limits. Saving these choices retains the current automation policy.'}
              </p>
              <button type="submit" ?disabled=${this.busy || !canSave}>
                ${initial ? 'Save disabled automation policy' : 'Save role selections'}
              </button>
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
