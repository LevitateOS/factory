import {LitElement, html} from 'lit';
import {check} from '../api/models.js';
import type {FactoryNewProject, FactoryProjectsPort} from './projects.js';

export interface FactoryNewProjectContext {
  /** The account the new repository belongs to, as the sidebar shows it. */
  readonly accountLabel: string;
  readonly port: FactoryProjectsPort & {create: NonNullable<FactoryProjectsPort['create']>};
}

/** The repository name GitHub accepts for what the person typed: spaces become
 * dashes and other characters GitHub refuses are dropped. */
export function projectRepositoryName(name: string) {
  return name
    .trim()
    .replace(/\s+/g, '-')
    .replace(/[^A-Za-z0-9._-]/g, '')
    .replace(/^[.]+|\.git$/gi, '')
    .slice(0, 100);
}

/** New project: one name, an optional sentence and who can see it. The host
 * creates the repository, connects this window to it and opens its Feed. */
export class SodaFactoryNewProject extends LitElement {
  static properties = {name: {state: true}, description: {state: true}, visibility: {state: true}, busy: {state: true}, message: {state: true}};
  declare private name: string;
  declare private description: string;
  declare private visibility: FactoryNewProject['visibility'];
  declare private busy: boolean;
  declare private message: string;
  private current: FactoryNewProjectContext | undefined;
  constructor() {
    super();
    this.name = this.description = this.message = '';
    this.visibility = 'private';
    this.busy = false;
  }
  protected createRenderRoot() {
    return this;
  }
  set context(context: FactoryNewProjectContext | undefined) {
    if (context === this.current) return;
    if (context) check(typeof context.port.create === 'function' && typeof context.accountLabel === 'string');
    this.current = context;
    this.message = '';
    this.busy = false;
  }
  disconnectedCallback() {
    this.current = undefined;
    super.disconnectedCallback();
  }
  private submit = async (event: Event) => {
    event.preventDefault();
    const context = this.current,
      name = projectRepositoryName(this.name);
    if (!context || this.busy) return;
    if (!name) {
      this.message = 'Give the project a name using letters or numbers.';
      return;
    }
    this.busy = true;
    this.message = '';
    try {
      await context.port.create({name, description: this.description.trim().replace(/\s+/g, ' ').slice(0, 350), visibility: this.visibility});
    } catch (error) {
      if (this.current !== context) return;
      const text = error instanceof Error ? error.message : '';
      this.message = text && text.length <= 200 && !/[\u0000-\u001f\u007f]/.test(text) ? text : 'The project could not be created. Try again.';
    }
    if (this.current === context) this.busy = false;
  };
  protected render() {
    const context = this.current;
    if (!context) return html``;
    const name = projectRepositoryName(this.name);
    return html`<section class="soda-factory-projects-page soda-factory-new-project" aria-labelledby="new-project-title">
      <header>
        <div>
          <h2 id="new-project-title">New project</h2>
          <p>Starts an empty project in ${context.accountLabel}'s GitHub account. Then ask for what you want in its Feed.</p>
        </div>
      </header>
      ${this.message ? html`<p class="soda-factory-projects-message" role="alert">${this.message}</p>` : ''}
      <form @submit=${this.submit}>
        <label>
          <span>Name</span>
          <input
            type="text"
            name="name"
            required
            maxlength="100"
            autocomplete="off"
            .value=${this.name}
            ?disabled=${this.busy}
            @input=${(event: Event) => (this.name = (event.target as HTMLInputElement).value)}
          />
        </label>
        ${name && name !== this.name.trim() ? html`<p class="soda-factory-projects-note">It will be called “${name}”.</p>` : ''}
        <label>
          <span>What is it? (optional)</span>
          <input
            type="text"
            name="description"
            maxlength="350"
            autocomplete="off"
            .value=${this.description}
            ?disabled=${this.busy}
            @input=${(event: Event) => (this.description = (event.target as HTMLInputElement).value)}
          />
        </label>
        <fieldset ?disabled=${this.busy}>
          <legend>Who can see it</legend>
          <label><input type="radio" name="visibility" value="private" .checked=${this.visibility === 'private'} @change=${() => (this.visibility = 'private')} /> Only people you invite</label>
          <label><input type="radio" name="visibility" value="public" .checked=${this.visibility === 'public'} @change=${() => (this.visibility = 'public')} /> Anyone</label>
        </fieldset>
        <button type="submit" data-tone="primary" ?disabled=${this.busy || !name} aria-busy=${this.busy ? 'true' : 'false'}>
          ${this.busy ? 'Creating…' : 'Create project'}
        </button>
      </form>
    </section>`;
  }
}
customElements.define('soda-factory-new-project', SodaFactoryNewProject);
