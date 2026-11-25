import {LitElement, html, nothing} from 'lit';
import type {TemplateResult} from 'lit';
import type {Observed} from './controller-view.js';

/** What the sign-in controllers already know; nothing here is a new read. */
export interface FirstRunFacts {
  /** Factory's runtime is ready (the controllers' `ready`). */
  runtime: boolean;
  /** Muse reported that a sign-in finished in this window. Muse keeps its own
   * saved login and reports no signed-in state, so a returning person's Muse
   * step stays unconfirmed. */
  museSignedIn: boolean;
  /** Saved GitHub accounts, as labels, whose credentials are usable. */
  github: readonly string[];
}

type StepState = 'done' | 'current' | 'pending';
interface Step {
  id: string;
  title: string;
  state: StepState;
  badge: string;
  line: string;
  /** The existing view's section, or undefined when the step has none. */
  body?: TemplateResult | HTMLElement | undefined;
}

/** The guided first run. The three sign-in views are the same elements the
 * Accounts page shows; this only sequences and collapses their sections. A
 * collapsed section is hidden, never removed, so its controls stay in the
 * document. The current step is the first one not done; a person may open any
 * other step. The frame hides this region once it has a usable context. */
export class SodaDesktopFirstRun extends LitElement {
  /** Read at each render, from the controllers' current state. */
  facts: () => FirstRunFacts = () => ({runtime: false, museSignedIn: false, github: []});
  homes: Readonly<Record<string, HTMLElement>> = {};
  /** Steps the person opened or closed, over the derived default. */
  private readonly opened = new Map<string, boolean>();
  private sources: (() => void)[] = [];

  protected createRenderRoot() {
    return this;
  }
  /** Re-render whenever one of the controllers the facts come from changes. */
  follow(sources: readonly Observed[]) {
    for (const stop of this.sources) stop();
    this.sources = sources.map((source) => source.subscribe(() => this.requestUpdate()));
  }
  disconnectedCallback() {
    for (const stop of this.sources) stop();
    this.sources = [];
    super.disconnectedCallback();
  }
  private steps(): Step[] {
    const {runtime, museSignedIn, github} = this.facts(),
      account = github.length > 0;
    const done = [runtime, museSignedIn, account, false];
    // Muse cannot report a saved login, so once GitHub is usable its step no
    // longer holds the person back.
    const waiting = done.map((value, index) => !value && !(index === 1 && account));
    const current = waiting.indexOf(true);
    const state = (index: number): StepState => (done[index] ? 'done' : index === current ? 'current' : 'pending');
    const home = (id: string) => this.homes[id];
    return [
      {
        id: 'runtime',
        title: 'Start Factory on this computer',
        state: state(0),
        badge: runtime ? 'Done' : 'Starting',
        line: runtime ? 'Factory is ready.' : 'Factory is starting. The toolbar says when it is ready or needs a retry.',
      },
      {
        id: 'muse',
        title: 'Sign in to Muse',
        state: state(1),
        badge: museSignedIn ? 'Done' : state(1) === 'current' ? 'Waiting for you' : account ? 'Not confirmed' : 'Next',
        line: museSignedIn
          ? 'Muse sign-in finished.'
          : 'Muse runs the agents. It keeps its own saved login, and its current release is verified for new work.',
        body: home('muse'),
      },
      {
        id: 'github',
        title: 'Add a GitHub account',
        state: state(2),
        badge: account ? 'Done' : state(2) === 'current' ? 'Waiting for you' : 'Next',
        line: account
          ? github.join(', ')
          : 'Sign in to access your GitHub repositories. GitHub will ask for repository and profile access.',
        body: home('github'),
      },
      {
        id: 'project',
        title: 'Add your first project',
        state: state(3),
        badge: state(3) === 'current' ? 'Waiting for you' : 'Next',
        line: 'Pick a repository from your account, or open it by owner/name or repository URL.',
        body: state(3) === 'current'
          ? html`<p>Choose your account and repository, or find one by name, in the connection panel below.</p>
              <button type="button" class="first-run-go" @click=${() => document.querySelector<HTMLElement>('#factory-account')?.focus()}>Go to the repository picker</button>`
          : undefined,
      },
    ];
  }
  private toggle(id: string, open: boolean) {
    this.opened.set(id, !open);
    this.requestUpdate();
  }
  protected render() {
    const steps = this.steps();
    return html`<div class="first-run">
      <header>
        <h2>Set up Factory</h2>
        <p>Four steps to your first project. You can stop after any step and pick it up again from Accounts.</p>
      </header>
      <ol class="first-run-steps">
        ${steps.map((step, index) => {
          const open = this.opened.get(step.id) ?? step.state === 'current',
            bodyId = `first-run-${step.id}-body`;
          return html`<li class="first-run-step" data-state=${step.state} aria-current=${step.state === 'current' ? 'step' : 'false'}>
            <div class="first-run-mark" aria-hidden="true">${step.state === 'done' ? '✓' : index + 1}</div>
            <div class="first-run-main">
              <div class="first-run-head">
                ${step.body
                  ? html`<button type="button" class="first-run-title" aria-expanded=${open} aria-controls=${bodyId} @click=${() => this.toggle(step.id, open)}>${step.title}</button>`
                  : html`<span class="first-run-title">${step.title}</span>`}
                <span class="first-run-badge">${step.badge}</span>
              </div>
              ${open && step.body ? nothing : html`<p class="first-run-line">${step.line}</p>`}
              ${step.body ? html`<div id=${bodyId} class="first-run-body" ?hidden=${!open}>${step.body}</div>` : nothing}
            </div>
          </li>`;
        })}
      </ol>
      <p class="first-run-note">Letting this computer run agents is a separate choice. You make it later in Settings, under Execution controller.</p>
    </div>
    <aside class="first-run-aside">
      <section>
        <h2>What this release offers</h2>
        <dl>
          <div><dt>Muse</dt><dd>Offered</dd></div>
          <div><dt>Other providers</dt><dd>None offered</dd></div>
          <div><dt>GitHub repositories</dt><dd>Offered</dd></div>
        </dl>
      </section>
      <details class="first-run-fountain">
        <summary>Fountain <span class="first-run-badge">Not qualified yet</span></summary>
        <p>Fountain sign-in and repositories are not part of setup in this release. Most Fountain journeys have not been qualified. Its sign-in remains available here.</p>
        ${this.homes['fountain']}
      </details>
    </aside>`;
  }
}
customElements.define('soda-desktop-first-run', SodaDesktopFirstRun);
