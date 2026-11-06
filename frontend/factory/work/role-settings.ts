import {LitElement, html} from 'lit';
import {check, id, object, readSodaJSON} from '../api/models.js';
import type {FactoryTransport} from '../api/transport.js';

interface RoleContext {
  readonly transport: FactoryTransport;
  readonly repository: string;
}
const roleNames = {'soda-coder': 'Coding', 'soda-reviewer': 'Review'} as const;
type Role = keyof typeof roleNames;
type Selection = Record<string, unknown> & {account: string; harness: string; model: string};
type Selections = Record<Role, Selection>;
interface Account {
  connection: string;
  active: boolean;
  roles: string[];
}
interface Settings {
  policy: Record<string, unknown> | null;
  roles?: Selections;
  accounts: Account[];
  editable: boolean;
}
class RoleSaveError extends Error {}

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
    return {connection: account.connection, active: account.active, roles: account.roles as string[]};
  });
  const editable = object(object(access.editable).policy).editable === true;
  if (status.policy === null) return {policy: null, accounts, editable};
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
  return {policy, roles, accounts, editable};
}

// The existing policy command owns CAS, current permissions and actor bindings.
// Preserve the rest of that policy while changing only its role selections.
function policyChange(policy: Record<string, unknown>, roles: Selections) {
  const actor = (value: unknown) => {
    const {kind: _kind, ...binding} = object(value);
    return binding;
  };
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
    publish_actor: actor(policy.publish_actor),
    create_actor: actor(policy.create_actor),
    review_actor: actor(policy.review_actor),
    merge_actor: actor(policy.merge_actor),
    ...(resolve.actor_id && resolve.actor_id !== '0' ? {resolve_actor: actor(resolve)} : {}),
  };
}

/** Shared editor; saved sponsorship references are nonsecret spending choices.
 * It neither connects an account nor chooses a replacement for a missing one. */
export class SodaFactoryRoleSettings extends LitElement {
  static properties = {view: {state: true}, roles: {state: true}, busy: {state: true}, message: {state: true}};
  declare private view: Settings | undefined;
  declare private roles: Selections | undefined;
  declare private busy: boolean;
  declare private message: string;
  private current: RoleContext | undefined;
  private generation = '';
  private lifetime = new AbortController();
  constructor() {
    super();
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
        this.roles = view.roles;
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
  private async save(event: SubmitEvent) {
    event.preventDefault();
    const context = this.current,
      signal = this.lifetime.signal,
      view = this.view,
      roles = this.roles;
    if (!context || !view?.policy || !view.editable || !roles || this.busy || !this.live(context, signal)) return;
    if (
      !(Object.keys(roleNames) as Role[]).every(
        (role) => roles[role].harness === 'muse' && this.eligible(role, roles[role].account)
      )
    )
      return;
    const payload = policyChange(view.policy, roles);
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
          response.status === 409 || response.status === 412
            ? 'Policy changed. Refresh before saving again.'
            : response.status === 403
              ? 'Permission to change policy is unavailable. Refresh to check current access.'
              : 'The save could not be confirmed. Refresh before changing roles again.'
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
      if (this.live(context, signal)) {
        this.view = confirmed;
        this.roles = confirmed.roles;
        this.message = 'Role selections saved.';
      }
    } catch (error) {
      if (this.live(context, signal)) {
        this.view = undefined;
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
    const canSave =
      view?.editable &&
      roles &&
      (Object.keys(roleNames) as Role[]).every(
        (role) => roles[role].harness === 'muse' && this.eligible(role, roles[role].account)
      );
    return html`<section aria-label="Agent role settings">
      <h3>Agent roles</h3>
      <p>
        Choose the provider, model and saved paying account for each role. Factory does not switch spending accounts
        automatically.
      </p>
      <p role="status">${this.message}</p>
      <button type="button" ?disabled=${this.busy} @click=${() => this.refresh()}>Refresh roles</button>
      ${
        view?.policy && roles
          ? html`<form @submit=${(event: SubmitEvent) => this.save(event)}>
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
                    >Paying account
                    <select
                      aria-label=${`${roleNames[role]} paying account`}
                      required
                      @change=${(event: Event) => this.choose(role, 'account', (event.target as HTMLSelectElement).value)}
                    >
                      <option value="" .selected=${!selected.account}>Choose a saved account</option>
                      ${selected.account && !this.eligible(role, selected.account) ? html`<option .value=${selected.account} .selected=${true} disabled>${selected.account} — unavailable for this role</option>` : ''}
                      ${accounts.map((account) => html`<option .value=${account.connection} .selected=${account.connection === selected.account}>${account.connection}</option>`)}
                    </select></label
                  >
                  ${accounts.length === 0 ? html`<p>No active saved paying account is available for this role.</p>` : ''}
                </fieldset>`;
              })}
              <p>
                Only Muse is currently supported. Each role keeps its current execution limits. Saving these choices
                retains the current automation policy.
              </p>
              <button type="submit" ?disabled=${this.busy || !canSave}>Save role selections</button>
              ${!view.editable ? html`<p>You cannot change this repository's role settings with the current connection.</p>` : ''}
            </form>`
          : view?.policy === null
            ? html`<p>No repository automation policy is saved yet.</p>`
            : this.busy
              ? html`<p>Loading role settings…</p>`
              : ''
      }
    </section>`;
  }
}
customElements.define('soda-factory-role-settings', SodaFactoryRoleSettings);
