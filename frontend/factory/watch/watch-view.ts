import {html} from 'lit';
import type {TemplateResult} from 'lit';

/** A render-time projection only. No transport, identity lookup or mutable state. */
export interface FactoryWatchPresentation {
  readonly watching: boolean;
  readonly disabled: boolean;
  readonly canWatch: boolean;
  readonly watchLabel: string;
  readonly title: string;
  readonly status: string;
  readonly message: string;
  /** Recorded status and last output of a finished run, shown collapsed. */
  readonly technical: string;
  readonly notice: boolean;
  readonly screenVisible: boolean;
}
export interface FactoryWatchCommands {
  readonly watch: () => void;
  readonly stop: () => void;
  readonly hide: () => void;
  readonly menuKey: (event: KeyboardEvent) => void;
}
export function renderFactoryWatch(view: FactoryWatchPresentation, commands: FactoryWatchCommands): TemplateResult {
  return html`
    <section class=${'soda-terminal soda-factory' + (view.watching ? ' is-connected' : '')}>
      <div class="soda-terminal-context">
        <span title=${view.status}>${view.title}</span>
        <details class="soda-menu" @keydown=${commands.menuKey}>
          <summary aria-label="Factory view actions" data-action="controls">⋯</summary>
          <div>
            <p class="soda-menu-heading" title=${view.title}><span>${view.title}</span></p>
            <button type="button" class="ui button" ?disabled=${view.disabled} @click=${commands.hide}>
              Hide view
            </button>
          </div>
        </details>
      </div>
      ${
        !view.watching && view.canWatch
          ? html`<button type="button" class="ui primary button" @click=${commands.watch}>${view.watchLabel}</button>`
          : ''
      }
      ${
        view.watching
          ? html`<button type="button" class="ui button" ?disabled=${view.disabled} @click=${commands.stop}>
              Stop watching
            </button>`
          : ''
      }
      <p class=${'soda-terminal-status' + (!view.notice ? ' soda-visually-hidden' : '')} role="status" tabindex="-1">
        ${view.message}
      </p>
      ${
        view.technical
          ? html`<details class="soda-factory-technical">
              <summary>Technical details</summary>
              <pre>${view.technical}</pre>
            </details>`
          : ''
      }
      <!-- Xterm exclusively owns this stable, unconditional node's descendants. -->
      <div
        class="soda-terminal-screen"
        ?hidden=${!view.screenVisible}
        aria-label=${`Read-only factory output for ${view.title}`}
      ></div>
    </section>
  `;
}
