import {LitElement, html} from 'lit';
import {ScopedBinding} from '../shared/read-state.js';
import {check, id, readSodaJSON} from '../api/models.js';
import type {FactoryTransport} from '../api/transport.js';
import {activityKinds, parseFactoryActivity} from '../api/activity-response.js';
import type {ActivityKind, ActivitySettings, ActivityItem, FactoryActivity} from '../api/activity-response.js';
import {activityTimestamp} from '../shared/format.js';
import {
  readActivityDraft,
  sameActivityDraft,
  sameActivitySettings,
  saveActivityDraft,
  settleActivityDraft,
} from './activity-draft.js';
import type {ActivityAddress, ActivityDraft} from './activity-draft.js';
export interface ActivityContext {
  readonly transport: FactoryTransport;
  readonly repository: string;
  readonly editable: boolean;
  readonly commandScope?: (signal: AbortSignal) => Promise<string>;
  /** Host navigation may omit unsafe rows, or keep them as text by returning {}. */
  readonly presentItem?: (item: ActivityItem, activity: FactoryActivity) => {readonly href?: string} | undefined;
}
const labels: Record<ActivityKind, string> = {
  issue_created: 'Issues created',
  pr_created: 'Pull requests created',
  verified_merge: 'Verified merges and original issue closure',
};
class ActivityRequestError extends Error {
  constructor(readonly status: number) {
    super(
      status === 409 || status === 412
        ? 'Activity settings changed. Refresh before saving again.'
        : status === 403
          ? 'Only the current repository owner or administrator can save activity settings.'
          : 'Factory activity is unavailable.'
    );
  }
}
export class SodaFactoryActivity extends LitElement {
  static properties = {
    activity: {state: true},
    message: {state: true},
    busy: {state: true},
    draft: {state: true},
    ready: {state: true},
  };
  declare private activity: FactoryActivity | undefined;
  declare private message: string;
  declare private busy: boolean;
  declare private draft: ActivityDraft | undefined;
  declare private ready: boolean;
  private bound = new ScopedBinding<ActivityContext>(
    (a, b) => a.transport === b.transport && a.repository === b.repository,
    () => {
      this.context = undefined;
    }
  );
  private confirmedUnix = 0;
  private address: ActivityAddress | undefined;
  constructor() {
    super();
    this.message = '';
    this.busy = this.ready = false;
  }
  protected createRenderRoot() {
    return this;
  }
  set context(context: ActivityContext | undefined) {
    if (context)
      check(
        id(context.repository) &&
          typeof context.editable === 'boolean' &&
          typeof context.transport.generation === 'string' &&
          !!context.transport.generation
      );
    const prior = this.bound.current;
    if (this.bound.bind(context) === 'same') {
      if (context?.presentItem !== prior?.presentItem || context?.editable !== prior?.editable) this.requestUpdate();
      return;
    }
    this.activity = undefined;
    this.draft = undefined;
    this.address = undefined;
    this.confirmedUnix = 0;
    this.busy = this.ready = false;
    this.message = '';
    if (context) void this.load();
  }
  disconnectedCallback() {
    this.context = undefined;
    super.disconnectedCallback();
  }
  private async identity(context: ActivityContext, serial: number): Promise<boolean> {
    this.ready = false;
    try {
      if (!context.commandScope) throw Error('Identity missing');
      const scope = await context.commandScope(AbortSignal.any([this.bound.signal, AbortSignal.timeout(15000)]));
      if (!this.bound.live(serial, context)) return false;
      const address = {scope, repository: context.repository},
        stored = readActivityDraft(address);
      this.address = address;
      this.draft = stored.draft;
      if (stored.blocked) {
        this.message = 'Saved activity preferences are unreadable or belong to another scope. They were preserved.';
        return false;
      }
      this.ready = true;
      this.message = stored.draft ? 'Saved activity preferences restored. Review them before saving.' : '';
      return true;
    } catch {
      if (this.bound.live(serial, context))
        this.message = 'Activity account identity is unavailable. Refresh before saving preferences.';
      return false;
    }
  }
  private async request(context: ActivityContext, serial: number, draft?: ActivityDraft) {
    const response = await context.transport.request(`repositories/${context.repository}/factory/activity`, {
      method: draft ? 'PUT' : 'GET',
      signal: AbortSignal.any([this.bound.signal, AbortSignal.timeout(15000)]),
      ...(draft
        ? {
            headers: {'Content-Type': 'application/json'},
            body: JSON.stringify({expected_revision: draft.expected_revision, settings: draft.settings}),
          }
        : {}),
    });
    if (!this.bound.live(serial, context)) throw Error('Activity context retired');
    if (!response.ok) throw new ActivityRequestError(response.status);
    return parseFactoryActivity(await readSodaJSON(response), context.repository);
  }
  private async load(save = false) {
    const context = this.bound.current,
      serial = this.bound.serial;
    if (!context || !this.bound.live(serial, context) || this.busy) return;
    const shown = this.draft,
      scope = this.address?.scope;
    this.busy = true;
    try {
      const admitted = await this.identity(context, serial);
      if (!this.bound.live(serial, context)) return;
      let issued: ActivityDraft | undefined;
      const address = this.address;
      if (save) {
        if (!admitted || !this.bound.current?.editable || !address) return;
        if (scope !== address.scope || !shown || !this.draft || !sameActivityDraft(shown, this.draft)) {
          this.ready = false;
          this.message = 'Saved preferences or account changed. Refresh before saving.';
          return;
        }
        issued = {...this.draft, submitted: true};
        const durable = saveActivityDraft(address, issued, this.draft);
        this.draft = issued;
        if (!durable) {
          this.message =
            'Activity preferences could not be saved locally. No request was sent; retry when storage is available.';
          return;
        }
      }
      const activity = await this.request(context, serial, issued);
      if (!this.bound.live(serial, context)) return;
      if (issued)
        check(activity.revision > issued.expected_revision && sameActivitySettings(activity.settings, issued.settings));
      if (
        this.draft?.submitted &&
        this.ready &&
        address &&
        activity.revision > this.draft.expected_revision &&
        sameActivitySettings(activity.settings, this.draft.settings)
      ) {
        check(settleActivityDraft(address, this.draft));
        this.draft = undefined;
      }
      this.activity = activity;
      this.confirmedUnix = Math.floor(Date.now() / 1000);
      if (admitted)
        this.message = issued
          ? 'Activity display settings saved.'
          : this.draft
            ? 'Saved activity preferences restored. Review them before saving.'
            : '';
    } catch (error) {
      if (!this.bound.live(serial, context)) return;
      this.ready = false;
      if (error instanceof ActivityRequestError && [401, 403].includes(error.status)) {
        this.activity = undefined;
        this.confirmedUnix = 0;
      }
      this.message = error instanceof ActivityRequestError ? error.message : 'Factory activity is unavailable.';
      if (this.activity) this.message += ' Last confirmed activity is shown; refresh before saving.';
    } finally {
      if (this.bound.live(serial, context)) this.busy = false;
    }
  }
  private choose(kind: ActivityKind, event: Event) {
    const context = this.bound.current;
    if (
      !context ||
      !this.bound.live(this.bound.serial, context) ||
      !context.editable ||
      !this.ready ||
      this.busy ||
      !this.activity ||
      !this.address
    )
      return;
    const prior = this.draft;
    const draft: ActivityDraft = {
      expected_revision: prior?.expected_revision ?? this.activity.revision,
      settings: {...(prior?.settings ?? this.activity.settings), [kind]: (event.target as HTMLInputElement).checked},
      submitted: false,
    };
    try {
      const stored = saveActivityDraft(this.address, draft, prior);
      this.draft = draft;
      this.message = stored
        ? 'Activity preferences selected. Save to apply them.'
        : 'Preferences are retained in this window. Storage is unavailable; no request was sent.';
    } catch {
      this.ready = false;
      this.message = 'Saved preferences were replaced. Refresh before editing.';
    }
  }
  private save = (event: SubmitEvent) => {
    event.preventDefault();
    void this.load(true);
  };
  private reviewCurrent = () => {
    if (
      !this.bound.current ||
      !this.bound.live(this.bound.serial, this.bound.current) ||
      !this.bound.current.editable ||
      !this.ready ||
      this.busy ||
      !this.draft ||
      !this.activity ||
      !this.address
    )
      return;
    try {
      const draft = {...this.draft, expected_revision: this.activity.revision, submitted: false};
      saveActivityDraft(this.address, draft, this.draft);
      this.draft = draft;
      this.message = 'Current activity revision selected. Save to apply these preferences.';
    } catch {
      this.ready = false;
      this.message = 'Saved preferences were replaced. Refresh before editing.';
    }
  };
  protected render() {
    if (!this.bound.current) return html``;
    const activity = this.activity,
      confirmed = activityTimestamp(this.confirmedUnix);
    return html`<section aria-label="Factory activity">
      <h3>Factory activity</h3>
      <p data-activity-message aria-live="polite">${this.message}</p>
      ${activity && confirmed ? html`<p>Last confirmed <time datetime=${confirmed.iso}>${confirmed.text}</time>.</p>` : ''}
      <button type="button" class="ui button" ?disabled=${this.busy} @click=${() => this.load()}>
        Refresh activity
      </button>
      ${activity ? this.renderFeed(activity) : ''}
    </section>`;
  }
  private renderFeed(activity: FactoryActivity) {
    const items = activity.items.flatMap((item) => {
      const link = this.bound.current?.presentItem ? this.bound.current.presentItem(item, activity) : {};
      return link ? [{item, link}] : [];
    });
    const visible = items.filter(({item}) => activity.settings[item.kind]);
    const observed = activityTimestamp(activity.observed_unix),
      settings = this.draft?.settings ?? activity.settings;
    const disabled = this.busy || !this.ready || !this.bound.current?.editable;
    return html`<p>
        Local activity shows recorded work. Display settings do not change execution or send notifications.
      </p>
      ${observed ? html`<p>Feed observed <time datetime=${observed.iso}>${observed.text}</time>.</p>` : html`<p>Feed observation time unknown.</p>`}
      ${activity.truncated ? html`<p>Older entries exist beyond this page.</p>` : ''}
      <ul>
        ${visible.map(({item, link}) => html`<li>${link.href ? html`<a href=${link.href}>${item.summary}</a>` : item.summary} ${renderItemTime(item.created_unix)}</li>`)}
      </ul>
      ${items.length === 0 ? html`<p>No recorded activity yet.</p>` : ''}${items.length > 0 && !visible.length ? html`<p>No visible recorded activity.</p>` : ''}
      <form @submit=${this.save}>
        <fieldset ?disabled=${disabled}>
          <legend>Activity visibility</legend>
          ${activityKinds.map((kind) => html`<label><input type="checkbox" name=${kind} .checked=${settings[kind]} @change=${(event: Event) => this.choose(kind, event)} />${labels[kind]}</label>`)}
        </fieldset>
        <button class="ui button" type="submit" ?disabled=${disabled || !this.draft}>
          Save activity display settings
        </button>
      </form>
      ${this.draft && this.draft.expected_revision !== activity.revision ? html`<button type="button" class="ui button" ?disabled=${disabled} @click=${this.reviewCurrent}>Review preferences against current activity</button>` : ''}`;
  }
}
function renderItemTime(createdUnix: number) {
  const stamp = activityTimestamp(createdUnix);
  return stamp ? html`<time datetime=${stamp.iso}>${stamp.text}</time>` : html`<span>time unknown</span>`;
}
customElements.define('soda-factory-activity', SodaFactoryActivity);
