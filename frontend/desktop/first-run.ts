import {LitElement, html, nothing} from 'lit';
import type {TemplateResult} from 'lit';
import type {Observed} from './controller-view.js';
import {renderFactoryWordmark} from '../factory/shared/factory-mark.js';

/** What the sign-in controllers already know; nothing here is a new read. */
export interface FirstRunFacts {
  /** Factory's runtime is ready (the controllers' `ready`). */
  runtime: boolean;
  /** Muse signed in (a sign-in finished here, or Muse reported a saved login),
   * not signed in (Muse said so), or unknown (Muse has not answered). */
  muse: 'signed_in' | 'signed_out' | 'unknown';
  /** The JEV key state, undefined until read. */
  jev: 'none' | 'connected' | 'refused' | 'locked' | undefined;
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
  facts: () => FirstRunFacts = () => ({runtime: false, muse: 'unknown', jev: undefined, github: []});
  homes: Readonly<Record<string, HTMLElement>> = {};
  /** Steps the person opened or closed, over the derived default. */
  private readonly opened = new Map<string, boolean>();
  private sources: (() => void)[] = [];
  /** The person chose to connect JEV later; kept for this Mac. */
  private jevSkipped = readSkip();

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
  /** "Skip for now" on the JEV step: setup moves on, and Accounts keeps the step. */
  skipJev() {
    this.jevSkipped = true;
    writeSkip();
    this.opened.delete('jev');
    this.requestUpdate();
  }
  private steps(): Step[] {
    const {runtime, muse, jev, github} = this.facts(),
      museSignedIn = muse === 'signed_in',
      account = github.length > 0,
      jevConnected = jev === 'connected';
    const done = [runtime, museSignedIn, jevConnected, account, false];
    // Until Muse answers, a usable GitHub account lets setup go on past the Muse
    // step. A skipped JEV step does not hold it back either.
    const waiting = done.map((value, index) => !value && !(index === 1 && account && muse === 'unknown') && !(index === 2 && this.jevSkipped));
    const current = waiting.indexOf(true);
    const state = (index: number): StepState => (done[index] ? 'done' : index === current ? 'current' : 'pending');
    const home = (id: string) => this.homes[id];
    return [
      {
        id: 'runtime',
        title: 'Start Factory on this computer',
        state: state(0),
        badge: runtime ? 'Done' : 'Starting',
        line: runtime ? 'Factory is ready.' : 'Factory is starting. A notice at the top says if it needs a retry.',
      },
      {
        id: 'muse',
        title: 'Sign in to Muse',
        state: state(1),
        badge: museSignedIn
          ? 'Done'
          : muse === 'signed_out'
            ? 'Not signed in'
            : state(1) === 'current'
              ? 'Waiting for you'
              : account
                ? 'Not confirmed'
                : 'Next',
        line: museSignedIn
          ? 'Muse is signed in.'
          : 'Muse runs the agents. It keeps its own saved login, and its current release is verified for new work.',
        body: home('muse'),
      },
      {
        id: 'jev',
        title: 'Connect JEV',
        state: state(2),
        badge: jevConnected
          ? 'Done'
          : jev === 'refused'
            ? 'Key refused'
            : state(2) === 'current'
              ? 'Waiting for you'
              : this.jevSkipped
                ? 'Skipped'
                : 'Next',
        line: jevConnected
          ? 'JEV is connected with your TypeSafe key.'
          : 'JEV decides which agent picks up each post, checks plans and decides when work can be delivered. It needs a TypeSafe API key. Until then, posts and plans wait.',
        body: home('jev'),
      },
      {
        id: 'github',
        title: 'Add a GitHub account',
        state: state(3),
        badge: account ? 'Done' : state(3) === 'current' ? 'Waiting for you' : 'Next',
        line: account
          ? github.join(', ')
          : 'Sign in to access your GitHub repositories. GitHub will ask for repository and profile access.',
        body: home('github'),
      },
      {
        id: 'project',
        title: 'Add your first project',
        state: state(4),
        badge: state(4) === 'current' ? 'Waiting for you' : 'Next',
        line: 'Pick a repository from your account, or open it by owner/name or repository URL.',
        body: home('connection'),
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
        ${renderFactoryWordmark()}
        <h2>Set up Factory</h2>
        <p>Five steps to your first project. You can stop after any step and pick it up again from App settings.</p>
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
          <div><dt>JEV, for decisions</dt><dd>Offered</dd></div>
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

const SKIP = 'factory.first-run.jev-skipped';
function readSkip() {
  try {
    return localStorage.getItem(SKIP) === '1';
  } catch {
    return false;
  }
}
function writeSkip() {
  try {
    localStorage.setItem(SKIP, '1');
  } catch {
    // Without storage the skip lasts until the window closes.
  }
}
