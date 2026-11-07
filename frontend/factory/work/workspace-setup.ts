import {LitElement, html} from 'lit';
import {keyed} from 'lit/directives/keyed.js';
import {check, object, readSodaJSON} from '../api/models.js';
import type {FactoryTransport} from '../api/transport.js';

interface Context {
  transport: FactoryTransport;
  repository: string;
  actor?: string;
}
interface Setup {
  available: boolean;
  reason?: string;
  source?: string;
  target?: string;
  files: Record<string, string>;
  tools: string[];
  saved_recipe: boolean;
  requirement: string;
  approval: string;
  environment_revision: number;
  preparations: Record<string, unknown>[];
}

function setupResponse(raw: unknown): Setup {
  const value = object(raw),
    files = object(value.files);
  check(
    typeof value.available === 'boolean' &&
      typeof value.requirement === 'string' &&
      typeof value.approval === 'string' &&
      Number.isSafeInteger(value.environment_revision) &&
      Array.isArray(value.preparations) &&
      Array.isArray(value.tools) &&
      value.tools.every((tool) => typeof tool === 'string') &&
      typeof value.saved_recipe === 'boolean' &&
      Object.values(files).every((file) => typeof file === 'string')
  );
  if (value.available)
    check(/^[a-f0-9]{40}$/.test(String(value.source)) && String(value.target).startsWith('refs/heads/'));
  return value as unknown as Setup;
}

function executionSettings(files: Record<string, string>) {
  let network: unknown = 'none',
    setup: unknown = 10,
    check: unknown = 10;
  try {
    if (files['setup-network.json']) network = JSON.parse(files['setup-network.json']).access;
  } catch {
    network = '';
  }
  try {
    if (files['setup-limits.json']) {
      const limits = JSON.parse(files['setup-limits.json']);
      setup = limits.setup_seconds;
      check = limits.check_seconds;
    }
  } catch {
    setup = '';
    check = '';
  }
  const seconds = (value: unknown) =>
    typeof value === 'number' && Number.isInteger(value) && value >= 1 && value <= 1800 ? String(value) : '';
  return {
    network: network === 'none' || network === 'public_https' ? network : '',
    setup: seconds(setup),
    check: seconds(check),
  };
}

export class SodaFactoryWorkspaceSetup extends LitElement {
  static properties = {view: {state: true}, busy: {state: true}, message: {state: true}};
  declare private view: Setup | undefined;
  declare private busy: boolean;
  declare private message: string;
  private current: Context | undefined;
  private lifetime = new AbortController();
  private command: Record<string, unknown> | undefined;
  constructor() {
    super();
    this.busy = false;
    this.message = '';
  }
  protected createRenderRoot() {
    return this;
  }
  set context(value: Context | undefined) {
    if (
      value?.transport === this.current?.transport &&
      value?.repository === this.current?.repository &&
      value?.actor === this.current?.actor
    )
      return;
    this.lifetime.abort();
    this.lifetime = new AbortController();
    this.current = value;
    this.view = undefined;
    this.command = undefined;
    this.message = '';
    this.busy = false;
    this.requestUpdate();
    if (value) void this.refresh();
  }
  disconnectedCallback() {
    this.lifetime.abort();
    super.disconnectedCallback();
  }
  private live(context: Context, signal: AbortSignal) {
    return this.current === context && !signal.aborted;
  }
  private async refresh() {
    const context = this.current,
      signal = this.lifetime.signal;
    if (!context || this.busy) return;
    this.busy = true;
    try {
      const response = await context.transport.request(`repositories/${context.repository}/factory/setup`, {signal});
      if (!response.ok)
        throw new Error('Workspace setup could not be read. Check the repository connection and refresh.');
      const view = setupResponse(await readSodaJSON(response));
      if (this.live(context, signal)) {
        this.view = view;
        this.command = undefined;
        this.message = '';
      }
    } catch (error) {
      if (this.live(context, signal))
        this.message = error instanceof Error ? error.message : 'Workspace setup is unavailable.';
    } finally {
      if (this.live(context, signal)) this.busy = false;
    }
  }
  private async approve(event: SubmitEvent) {
    event.preventDefault();
    const context = this.current,
      view = this.view,
      signal = this.lifetime.signal;
    if (!context || !view?.available || this.busy) return;
    const form = event.currentTarget as HTMLFormElement;
    if (!form.reportValidity()) return;
    const values = new FormData(form);
    const files: Record<string, string> = {};
    for (const [name, content] of values)
      if (name.startsWith('file:') && typeof content === 'string' && content) files[name.slice(5)] = content;
    files['setup-network.json'] = JSON.stringify({access: values.get('network')});
    files['setup-limits.json'] = JSON.stringify({
      setup_seconds: Number(values.get('setup_seconds')),
      check_seconds: Number(values.get('check_seconds')),
    });
    const payload = {
      source: view.source,
      target: view.target,
      files,
      tools: String(values.get('tools') ?? '')
        .split(/[\s,]+/)
        .filter(Boolean),
      requirement: view.requirement,
      approval: view.approval,
      environment_revision: view.environment_revision,
    };
    const previous = this.command;
    if (!previous || JSON.stringify(previous.payload) !== JSON.stringify(payload))
      this.command = {command_id: crypto.randomUUID().replaceAll('-', ''), payload};
    const command = this.command!;
    this.busy = true;
    this.message = 'Preparing the approved source. Refresh to observe setup and check results.';
    try {
      const response = await context.transport.request(`repositories/${context.repository}/factory/setup`, {
        method: 'PUT',
        signal,
        body: JSON.stringify({command_id: command.command_id, ...payload}),
      });
      if (!response.ok) {
        if (response.status === 409 || response.status === 412)
          throw new Error('Source, permission or approval changed. Refresh and review the current setup.');
        throw new Error('Setup was not confirmed. Refresh its recorded state before submitting another approval.');
      }
      const next = setupResponse(await readSodaJSON(response));
      if (this.live(context, signal)) {
        this.view = next;
        this.command = undefined;
        this.message = 'Approval recorded. Readiness requires passing setup and checks for both roles.';
      }
    } catch (error) {
      if (this.live(context, signal))
        this.message = error instanceof Error ? error.message : 'Setup was not confirmed.';
    } finally {
      if (this.live(context, signal)) this.busy = false;
    }
  }
  protected render() {
    const view = this.view,
      settings = executionSettings(view?.files ?? {});
    return html`<section aria-label="Workspace setup">
      <h3>Workspace setup</h3>
      <p>
        Review the exact source and scripts before allowing setup. Each role gets its own isolated workspace. Setup does
        not start an AI run.
      </p>
      <p role="status">${this.message || view?.reason || ''}</p>
      <button type="button" ?disabled=${this.busy} @click=${() => this.refresh()}>Refresh workspace setup</button>
      ${view?.preparations.map((entry) => {
        const state = object(entry.state);
        return html`<div>
          <p>
            ${state.role === 'soda-coder' ? 'Builder' : 'Reviewer'}:
            ${entry.current === true ? String(state.phase || 'not started') : 'saved result from an earlier session'}
            ${entry.current === true && state.ready === true ? ' — ready' : ''}
          </p>
          ${
            state.output
              ? html`<details>
                  <summary>${entry.output_truncated === true ? 'Setup output (excerpt)' : 'Setup output'}</summary>
                  <pre>${String(state.output)}</pre>
                </details>`
              : ''
          }
        </div>`;
      })}
      ${
        view?.available
          ? html`<p>Source: <code>${view.target}</code> at <code>${view.source}</code></p>
              <p>
                ${view.saved_recipe ? 'These scripts and tools are from your saved approval. Review them for the selected source.' : 'Scripts are read from the selected source when present. Supply the setup and checks this project needs.'}
              </p>
              ${keyed(
                `${view.source}:${view.requirement}:${view.approval}`,
                html`<form @submit=${(event: SubmitEvent) => this.approve(event)}>
                  ${[
                    ...new Set([
                      'setup.sh',
                      'check.sh',
                      ...Object.keys(view.files).filter(
                        (name) => name !== 'setup-network.json' && name !== 'setup-limits.json'
                      ),
                    ]),
                  ].map(
                    (name) => html`<label
                      >${name}
                      <textarea
                        name=${`file:${name}`}
                        rows=${name.endsWith('.sh') ? 7 : 3}
                        maxlength="32768"
                        required
                        .value=${view.files[name] ?? ''}
                      ></textarea>
                    </label>`
                  )}
                  <label
                    >Dependency download access<select name="network" required .value=${settings.network}>
                      <option value="" disabled>Choose access</option>
                      <option value="none">Offline</option>
                      <option value="public_https">Public HTTPS downloads</option>
                    </select></label
                  >
                  <label
                    >Setup deadline (seconds)<input
                      name="setup_seconds"
                      type="number"
                      min="1"
                      max="1800"
                      step="1"
                      required
                      .value=${settings.setup}
                  /></label>
                  <label
                    >Check deadline (seconds)<input
                      name="check_seconds"
                      type="number"
                      min="1"
                      max="1800"
                      step="1"
                      required
                      .value=${settings.check}
                  /></label>
                  <label
                    >Required tools (up to eight, separated by spaces)<input
                      name="tools"
                      .value=${view.tools.join(' ')}
                      required
                  /></label>
                  <label
                    ><input type="checkbox" required /> I approve these scripts, tools, network access and limits for
                    this source.</label
                  >
                  <button type="submit" ?disabled=${this.busy}>Approve and prepare both roles</button>
                </form>`
              )} `
          : ''
      }
    </section>`;
  }
}
customElements.define('soda-factory-workspace-setup', SodaFactoryWorkspaceSetup);
