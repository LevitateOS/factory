import {LitElement, html} from 'lit';
import {keyed} from 'lit/directives/keyed.js';
import {check, object, readSodaJSON} from '../api/models.js';
import type {FactoryTransport} from '../api/transport.js';
import {PendingRecord} from '../shared/retained-record.js';

interface Context {
  transport: FactoryTransport;
  repository: string;
  actor?: string;
  scope?: string;
}
interface SetupCommand {
  command_id: string;
  payload: Record<string, unknown>;
}

function pendingRecord(context: Context): PendingRecord<SetupCommand> {
  check(typeof context.scope === 'string' && context.scope.length > 0 && context.scope.length <= 1024 &&
    !/[\u0000-\u001f\u007f]/.test(context.scope) && /^[1-9][0-9]*$/.test(context.repository));
  return new PendingRecord(
    `soda-work-setup:v1:${encodeURIComponent(context.scope)}:${context.repository}`,
    1024 * 1024,
    (raw) => {
      const row = object(JSON.parse(raw)), command = object(row.command), payload = object(command.payload);
      check(Object.keys(row).length === 3 && row.scope === context.scope && row.repository === context.repository &&
        Object.keys(command).length === 2 && typeof command.command_id === 'string' && /^[a-f0-9]{32}$/.test(command.command_id) &&
        Object.keys(payload).length === 8 && ['source', 'target', 'files', 'source_inputs', 'tools', 'requirement', 'approval', 'environment_revision'].every(key => Object.hasOwn(payload, key)));
      check(typeof payload.source === 'string' && /^[a-f0-9]{40}$/.test(payload.source) &&
        typeof payload.target === 'string' && payload.target.startsWith('refs/heads/') &&
        typeof payload.requirement === 'string' && typeof payload.approval === 'string' &&
        Number.isSafeInteger(payload.environment_revision) && Number(payload.environment_revision) > 0 &&
        Array.isArray(payload.tools) && payload.tools.every(tool => typeof tool === 'string') &&
        Object.values(object(payload.files)).every(file => typeof file === 'string') &&
        Object.values(object(payload.source_inputs)).every(digest => typeof digest === 'string' && /^[a-f0-9]{64}$/.test(digest)));
      return {command_id: command.command_id, payload};
    },
    (command) => JSON.stringify({scope: context.scope, repository: context.repository, command}),
    (a, b) => JSON.stringify(a) === JSON.stringify(b)
  );
}
interface Setup {
  available: boolean;
  reason?: string;
  source?: string;
  target?: string;
  files: Record<string, string>;
  source_files: Record<string, string>;
  source_inputs: Record<string, string>;
  selected_paths: string[];
  missing_paths: string[];
  tools: string[];
  saved_recipe: boolean;
  requirement: string;
  approval: string;
  environment_revision: number;
  preparations: Record<string, unknown>[];
}

function setupResponse(raw: unknown): Setup {
  const value = object(raw),
    files = object(value.files),
    sourceFiles = object(value.source_files),
    sourceInputs = object(value.source_inputs);
  check(
    typeof value.available === 'boolean' &&
      typeof value.requirement === 'string' &&
      typeof value.approval === 'string' &&
      Number.isSafeInteger(value.environment_revision) &&
      Array.isArray(value.preparations) &&
      Array.isArray(value.tools) &&
      value.tools.every((tool) => typeof tool === 'string') &&
      typeof value.saved_recipe === 'boolean' &&
      Array.isArray(value.selected_paths) &&
      value.selected_paths.every((path) => typeof path === 'string') &&
      Array.isArray(value.missing_paths) &&
      value.missing_paths.every((path) => typeof path === 'string') &&
      Object.values(sourceFiles).every((file) => typeof file === 'string') &&
      Object.values(sourceInputs).every((digest) => typeof digest === 'string' && /^[0-9a-f]{64}$/.test(digest)) &&
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
  static properties = {view: {state: true}, busy: {state: true}, message: {state: true}, selectionCurrent: {state: true}};
  declare private view: Setup | undefined;
  declare private busy: boolean;
  declare private message: string;
  declare private selectionCurrent: boolean;
  private current: Context | undefined;
  private lifetime = new AbortController();
  private command: SetupCommand | undefined;
  private record: PendingRecord<SetupCommand> | undefined;
  private recoveryBlocked = false;
  constructor() {
    super();
    this.busy = false;
    this.message = '';
    this.selectionCurrent = false;
  }
  protected createRenderRoot() {
    return this;
  }
  set context(value: Context | undefined) {
    if (
      value?.transport === this.current?.transport &&
      value?.repository === this.current?.repository &&
      value?.actor === this.current?.actor && value?.scope === this.current?.scope
    )
      return;
    this.lifetime.abort();
    this.lifetime = new AbortController();
    this.current = value;
    this.view = undefined;
    this.selectionCurrent = false;
    this.command = undefined;
    this.record = undefined;
    this.recoveryBlocked = false;
    if (value) {
      try {
        this.record = pendingRecord(value);
        const saved = this.record.read();
        this.command = saved.pending;
        this.recoveryBlocked = saved.blocked;
      } catch { this.recoveryBlocked = true; }
    }
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
  private async refresh(paths?: string[]) {
    const context = this.current,
      signal = this.lifetime.signal;
    if (!context || this.busy) return;
    this.selectionCurrent = false;
    this.busy = true;
    try {
      const response = await context.transport.request(
        `repositories/${context.repository}/factory/setup`,
        paths ? {signal, method: 'POST', body: JSON.stringify({paths})} : {signal}
      );
      if (!response.ok)
        throw new Error('Workspace setup could not be read. Check the repository connection and refresh.');
      const view = setupResponse(await readSodaJSON(response));
      if (this.live(context, signal)) {
        this.view = view;
        this.selectionCurrent = true;
        this.message = '';
      }
    } catch (error) {
      if (this.live(context, signal))
        this.message = error instanceof Error ? error.message : 'Workspace setup is unavailable.';
    } finally {
      if (this.live(context, signal)) this.busy = false;
    }
  }
  private async preview(event: SubmitEvent) {
    event.preventDefault();
    this.selectionCurrent = false;
    const form = event.currentTarget as HTMLFormElement;
    if (!form.reportValidity()) return;
    const paths = String(new FormData(form).get('paths') ?? '')
      .split(/\r?\n/)
      .map((path) => path.trim())
      .filter(Boolean);
    if (paths.length > 64) {
      this.message = 'Select at most 64 source files.';
      return;
    }
    await this.refresh(paths);
  }
  private async approve(event: SubmitEvent) {
    event.preventDefault();
    const context = this.current,
      view = this.view,
      signal = this.lifetime.signal;
    if (!context || !view?.available || !this.selectionCurrent || this.busy || this.command || this.recoveryBlocked || !this.record) return;
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
      source_inputs: view.source_inputs,
      tools: String(values.get('tools') ?? '')
        .split(/[\s,]+/)
        .filter(Boolean),
      requirement: view.requirement,
      approval: view.approval,
      environment_revision: view.environment_revision,
    };
    const command = {command_id: crypto.randomUUID().replaceAll('-', ''), payload};
    this.command = command;
    await this.submit(context, signal, command, form);
  }
  private async retry(event: SubmitEvent) {
    event.preventDefault();
    const form = event.currentTarget as HTMLFormElement, context = this.current, command = this.command;
    if (!form.reportValidity() || !context || !command || this.busy || this.recoveryBlocked) return;
    await this.submit(context, this.lifetime.signal, command, form);
  }
  private async submit(context: Context, signal: AbortSignal, command: SetupCommand, form: HTMLFormElement) {
    const record = this.record;
    const retained = record?.retain(command);
    if (!record || retained !== 'saved') {
      const saved = record?.read();
      if (saved?.pending) this.command = saved.pending;
      this.recoveryBlocked = saved?.blocked ?? true;
      this.message = retained === 'conflict' ? 'A different or unreadable setup approval is saved. Nothing was submitted. Resolve that approval before continuing.' :
        'The approval could not be saved for recovery. Nothing was submitted. Restore local storage and retry the saved approval.';
      this.requestUpdate();
      return;
    }
    this.busy = true;
    this.message = 'Preparing the approved source. Refresh to observe setup and check results.';
    try {
      const response = await context.transport.request(`repositories/${context.repository}/factory/setup`, {
        method: 'PUT',
        signal,
        body: JSON.stringify({command_id: command.command_id, ...command.payload}),
      });
      if (!response.ok) {
        if (response.status === 422 || response.status === 409) {
          const refused = object(await readSodaJSON(response));
          const code = object(refused.error).code;
          if (response.status === 409 && code === 'setup_recovery_unavailable')
            throw new Error('This saved approval predates exact command receipts. Automatic recovery is unavailable. Its records are preserved and setup was not replayed.');
          if (response.status === 422 && code === 'invalid_setup_input') {
            record.settle(command);
            const saved = record.read();
            if (this.live(context, signal)) { this.command = saved.pending; this.recoveryBlocked = saved.blocked; }
            throw new Error('Review the scripts, tools, source selection and limits. No setup approval was recorded.');
          }
        }
        if (response.status === 409 || response.status === 412)
          throw new Error('Source, permission or approval changed. Refresh and review the current setup.');
        throw new Error('Setup was not confirmed. Refresh its recorded state before submitting another approval.');
      }
      const next = setupResponse(await readSodaJSON(response));
      if (this.live(context, signal)) {
        const consent = form.querySelector<HTMLInputElement>('input[type="checkbox"]');
        if (consent) consent.checked = false;
        this.view = next;
        const settled = record.settle(command);
        const remaining = record.read();
        this.command = remaining.pending;
        this.recoveryBlocked = remaining.blocked;
        this.message = settled ? 'Approval recorded. Readiness requires passing setup and checks for both roles.' :
          'Approval recorded. A different saved approval remains; refresh before continuing.';
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
      ${this.recoveryBlocked ? html`<p role="status">The saved setup approval cannot be read safely. Restore its local record before approving another setup.</p>` : ''}
      ${this.command ? html`<form @submit=${(event: SubmitEvent) => this.retry(event)}>
        <p>A saved setup approval is still unconfirmed. Retry it before creating another approval.</p>
        <details><summary>Saved source, scripts, tools, network access and limits</summary>
          <p>Source: <code>${String(this.command.payload.target)}</code> at <code>${String(this.command.payload.source)}</code></p>
          <p>Tools: ${(this.command.payload.tools as string[]).join(' ')}</p>
          ${Object.entries(object(this.command.payload.files)).map(([name, contents]) => html`<h4>${name}</h4><pre>${String(contents)}</pre>`)}
          <p>Source files: ${Object.keys(object(this.command.payload.source_inputs)).join(', ')}</p>
        </details>
        <label><input type="checkbox" required /> I reviewed this saved approval and want to retry it unchanged.</label>
        <button type="submit" ?disabled=${this.busy || this.recoveryBlocked}>Retry saved approval</button>
      </form>` : ''}
      <button type="button" ?disabled=${this.busy} @click=${() => this.refresh()}>Refresh workspace setup</button>
      ${view?.preparations.map((entry) => {
        const state = object(entry.state);
        return html`<div>
          <p>
            ${state.role === 'soda-coder' ? 'Builder' : 'Reviewer'}:
            ${entry.current === true ? String(state.phase || 'not started') : 'saved result for another source or session'}
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
              <form @submit=${(event: SubmitEvent) => this.preview(event)}>
                <label
                  >Source files for planning (one repository-relative path per line)
                  <textarea
                    name="paths"
                    rows="4"
                    maxlength="16384"
                    required
                    ?disabled=${this.busy}
                    @input=${() => { this.selectionCurrent = false; }}
                    .value=${view.selected_paths.join('\n')}
                  ></textarea>
                </label>
                <p>
                  Load and review this selection before editing setup. Each file is limited to 32 KiB. Selected files
                  and the setup/check scripts together must fit the 128 KiB planning limit.
                </p>
                <button type="submit" ?disabled=${this.busy}>Load selected files</button>
              </form>
              ${view.missing_paths.length ? html`<p role="status">Selected files not found: ${view.missing_paths.join(', ')}. Update the selection before approving.</p>` : ''}
              ${Object.entries(view.source_files).map(
                ([name, content]) =>
                  html`<details>
                    <summary>${name}</summary>
                    <pre>${content}</pre>
                  </details>`
              )}
              <p>
                ${view.saved_recipe ? 'These scripts and tools are from your saved approval. Review them for the selected source.' : 'Scripts are read from the selected source when present. Supply the setup and checks this project needs.'}
              </p>
              ${keyed(
                `${view.source}:${view.requirement}:${view.approval}:${JSON.stringify(view.source_inputs)}:${this.selectionCurrent}`,
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
                    this source and the loaded source-file selection.</label
                  >
                  <button
                    type="submit"
                    ?disabled=${this.busy || !this.selectionCurrent || this.recoveryBlocked || !!this.command || view.missing_paths.length > 0 || Object.keys(view.source_inputs).length === 0}
                  >
                    Approve and prepare both roles
                  </button>
                </form>`
              )} `
          : ''
      }
    </section>`;
  }
}
customElements.define('soda-factory-workspace-setup', SodaFactoryWorkspaceSetup);
