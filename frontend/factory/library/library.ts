import {LitElement, html} from 'lit';
import type {PropertyValues} from 'lit';
import {check, id} from '../api/models.js';
import type {FactoryTransport} from '../api/transport.js';
import {libraryID, libraryPage, libraryRead} from '../api/library-response.js';
import type {LibraryItem, LibraryHistory} from '../api/library-response.js';
import {getJSON, FactoryReadError} from '../api/read.js';

export interface LibraryContext {
  readonly transport: FactoryTransport;
  readonly repository: string;
}
/** Host navigation facts, never a provider URL or authority to read another scope. */
export interface LibrarySource {
  readonly repository: string;
  readonly id: string;
  readonly item: string;
  readonly revision: number;
}
const empty = () => ({
  busy: false,
  message: '',
  items: [] as LibraryItem[],
  next: '',
  item: undefined as LibraryItem | undefined,
  history: [] as LibraryHistory[],
});

export class SodaFactoryLibrary extends LitElement {
  static properties = {itemID: {attribute: false}, pendingSource: {attribute: false}, view: {state: true}};
  declare itemID: string | undefined;
  declare pendingSource: string | undefined;
  declare private view: ReturnType<typeof empty>;
  private current: LibraryContext | undefined;
  private generation = '';
  private query = '';
  private kind = '';
  private history = false;
  private selectedID: string | undefined;
  private serial = 0;
  private cancel = new AbortController();
  constructor() {
    super();
    this.view = empty();
  }
  protected createRenderRoot() {
    return this;
  }
  get context(): LibraryContext | undefined {
    return this.current;
  }
  set context(context: LibraryContext | undefined) {
    if (context) check(id(context.repository) && !!context.transport.generation);
    if (
      context?.transport === this.current?.transport &&
      context?.repository === this.current?.repository &&
      context?.transport.generation === this.generation
    ) {
      this.current = context;
      return;
    }
    this.cancel.abort();
    this.serial++;
    this.current = context;
    this.generation = context?.transport.generation ?? '';
    this.query = '';
    this.kind = '';
    this.history = false;
    this.selectedID = undefined;
    this.view = empty();
    this.requestUpdate('context');
  }
  protected willUpdate(changed: PropertyValues) {
    if ((changed.has('context') || changed.has('itemID')) && this.current) {
      if (this.itemID) void this.openItem(this.itemID);
      else void this.search();
    }
  }
  disconnectedCallback() {
    this.context = undefined;
    super.disconnectedCallback();
  }
  private live(serial: number, context: LibraryContext) {
    if (serial !== this.serial || this.current?.transport !== context.transport) return false;
    if (context.transport.generation !== this.generation) {
      this.context = undefined;
      return false;
    }
    return true;
  }
  // Feed hosts use this event to retire delayed source-post navigation.
  private notifyNavigation() {
    this.dispatchEvent(new CustomEvent('library-navigate', {bubbles: true, composed: true}));
  }
  private begin() {
    const context = this.current;
    if (!context || !this.live(this.serial, context)) return;
    this.cancel.abort();
    this.cancel = new AbortController();
    const serial = ++this.serial;
    this.notifyNavigation();
    return {context, serial, signal: this.cancel.signal};
  }
  private failed(error: unknown, serial: number, context: LibraryContext) {
    if (!this.live(serial, context)) return;
    const privateUnavailable = error instanceof FactoryReadError && [401, 403, 404].includes(error.status);
    if (privateUnavailable) this.view = empty();
    const retained = this.view.items.length || this.view.item ? ' Last confirmed library items are shown.' : '';
    this.view = {
      ...this.view,
      busy: false,
      message:
        (privateUnavailable
          ? 'This library item is not available to your account.'
          : 'The library could not be loaded. Try again.') + retained,
    };
  }
  refresh() {
    return this.selectedID ? this.openItem(this.selectedID, true) : this.search();
  }
  private async search(more = false) {
    const read = this.begin();
    if (!read) return;
    const {context, serial, signal} = read,
      cursor = more ? this.view.next : '';
    this.selectedID = undefined;
    if (!more) this.view = empty();
    this.view = {...this.view, busy: true, message: '', item: undefined, history: []};
    const params = new URLSearchParams({repository: context.repository, query: this.query, kind: this.kind});
    if (this.history) params.set('history', 'true');
    if (cursor) params.set('cursor', cursor);
    try {
      const page = libraryPage(
        await getJSON(context.transport, 'factory/library?' + params, signal),
        context.repository,
        cursor,
        this.kind,
        this.history
      );
      if (!this.live(serial, context)) return;
      this.view = {
        ...this.view,
        busy: false,
        items: more ? [...this.view.items, ...page.items] : page.items,
        next: page.next,
      };
    } catch (error) {
      this.failed(error, serial, context);
    }
  }
  private async openItem(itemID: string, retain = false) {
    const read = this.begin();
    if (!read) return;
    const {context, serial, signal} = read;
    this.selectedID = itemID;
    if (!retain) this.view = empty();
    if (!libraryID(itemID)) {
      this.view = {...empty(), message: 'This library item is not available.'};
      return;
    }
    this.view = {...this.view, busy: true, message: ''};
    try {
      const data = libraryRead(
        await getJSON(
          context.transport,
          'factory/library/item?' + new URLSearchParams({repository: context.repository, id: itemID, history: 'true'}),
          signal
        ),
        context.repository,
        itemID
      );
      if (!this.live(serial, context)) return;
      this.view = {...this.view, busy: false, ...data};
    } catch (error) {
      this.failed(error, serial, context);
    }
  }
  private related(label: string, ids: string[]) {
    if (!ids.length) return '';
    return html`<section>
      <h4>${label}</h4>
      <div class="soda-library-relations">
        ${ids.map((id) => html`<button type="button" @click=${() => void this.openItem(id)}>Open item ${id.slice(0, 8)}</button>`)}
      </div>
    </section>`;
  }
  private content(item: LibraryItem) {
    return html`<div class="soda-library-body">
      <p>${item.body}</p>
      ${
        item.decision
          ? html`<section>
              <h4>Decision</h4>
              <p>${item.decision}</p>
            </section>`
          : ''
      }
      ${
        item.rationale
          ? html`<section>
              <h4>Reason</h4>
              <p>${item.rationale}</p>
            </section>`
          : ''
      }
      ${
        item.alternatives.length
          ? html`<section>
              <h4>Alternatives considered</h4>
              <ul>
                ${item.alternatives.map((alternative) => html`<li>${alternative}</li>`)}
              </ul>
            </section>`
          : ''
      }
      ${
        item.evidence.length
          ? html`<details>
              <summary>Evidence · ${item.evidence.length}</summary>
              <ul>
                ${item.evidence.map((ref) => html`<li><code>${ref}</code></li>`)}
              </ul>
            </details>`
          : ''
      }
    </div>`;
  }
  private sourceLink(item: LibraryItem) {
    if (!libraryID(item.source)) return '';
    const pending = this.pendingSource === item.source,
      context = this.current,
      serial = this.serial;
    return html`<button
      type="button"
      ?disabled=${pending}
      @click=${() => {
        if (!context || !this.live(serial, context)) return;
        this.dispatchEvent(
          new CustomEvent<LibrarySource>('library-source-open', {
            bubbles: true,
            composed: true,
            detail: {repository: context.repository, id: item.source, item: item.id, revision: item.revision},
          })
        );
      }}
    >
      ${pending ? 'Opening source…' : 'Open source post'}
    </button>`;
  }
  protected render() {
    if (!this.current) return html``;
    const item = this.view.item;
    return html`<section class="soda-library" aria-label="Project knowledge and decisions" aria-busy=${this.view.busy}>
      <p class="soda-library-intro">Shared knowledge and decisions for this project.</p>
      <form
        class="soda-library-search"
        @submit=${(event: Event) => {
          event.preventDefault();
          void this.search();
        }}
      >
        <label
          >Search the library<input
            type="search"
            maxlength="256"
            .value=${this.query}
            @input=${(event: Event) => {
              this.query = (event.target as HTMLInputElement).value;
              this.notifyNavigation();
            }}
        /></label>
        <label
          >Show<select
            aria-label="Item type"
            .value=${this.kind}
            @change=${(event: Event) => {
              this.kind = (event.target as HTMLSelectElement).value;
              void this.search();
            }}
          >
            <option value="">Knowledge and decisions</option>
            <option value="knowledge">Knowledge</option>
            <option value="decision">Decisions</option>
          </select></label
        >
        <button type="submit" ?disabled=${this.view.busy}>Search</button>
        <label
          ><input
            type="checkbox"
            .checked=${this.history}
            @change=${(event: Event) => {
              if (event.target instanceof HTMLInputElement) {
                this.history = event.target.checked;
                void this.search();
              }
            }}
          />Include removed items</label
        >
      </form>
      ${
        this.view.message
          ? html`<p role="alert">${this.view.message}</p>
              <button type="button" ?disabled=${this.view.busy} @click=${() => this.refresh()}>
                Retry library read
              </button>`
          : ''
      }
      ${this.view.busy ? html`<p role="status">Loading library…</p>` : ''}
      ${
        item
          ? html`<article class="soda-library-detail">
              <button type="button" class="soda-library-back" @click=${() => void this.search()}>
                ← Back to results
              </button>
              <div class="soda-library-item-meta">
                <span>${item.kind === 'decision' ? 'Decision' : 'Knowledge'} · revision ${item.revision}</span
                ><span>${item.removed ? 'Removed' : item.state}</span>
              </div>
              <h3>${item.title}</h3>
              <p class="soda-library-scope">${item.scope}</p>
              <p class="soda-library-item-meta">
                ${item.role}${item.updated ? ` · ${new Date(item.updated * 1000).toLocaleString()}` : ''}
              </p>
              ${this.sourceLink(item)}${this.content(item)}${this.related('Replaced by', item.replacements)}${this.related('Derived from', item.derived)}
              ${
                item.relations.length
                  ? html`<section>
                      <h4>Related items</h4>
                      <ul class="soda-library-relations">
                        ${item.relations.map((relation) => html`<li><button type="button" @click=${() => void this.openItem(relation.target)}>${relation.kind.replaceAll('_', ' ')} · ${relation.target.slice(0, 8)}</button>${relation.evidence.length ? html`<p>${relation.evidence.join(' · ')}</p>` : ''}</li>`)}
                      </ul>
                    </section>`
                  : ''
              }
              <details class="soda-library-history">
                <summary>Recent revisions · ${this.view.history.length}</summary>
                ${this.view.history.map(
                  (revision) =>
                    html`<details>
                      <summary>
                        Revision ${revision.item.revision} · ${revision.reason || 'Recorded'} · ${revision.actor}
                      </summary>
                      <h4>${revision.item.title}</h4>
                      <p>${revision.item.scope}</p>
                      ${this.sourceLink(revision.item)}${this.content(revision.item)}
                    </details>`
                )}
              </details>
            </article>`
          : html`<div class="soda-library-results">
                ${this.view.items.map(
                  (entry) =>
                    html`<article>
                      <div class="soda-library-item-meta">
                        <span>${entry.kind === 'decision' ? 'Decision' : 'Knowledge'} · revision ${entry.revision}</span
                        ><span>${entry.state}</span>
                      </div>
                      <h3>
                        <button type="button" @click=${() => void this.openItem(entry.id)}>${entry.title}</button>
                      </h3>
                      <p class="soda-library-excerpt">${entry.body}</p>
                      <p class="soda-library-item-meta">${entry.scope} · ${entry.role}</p>
                    </article>`
                )}
              </div>
              ${!this.view.busy && !this.view.message && !this.view.items.length ? html`<p class="soda-library-empty">${this.query || this.kind ? 'No matching items. Try a different search.' : 'No knowledge or decisions recorded yet. Worker observations and decisions will appear here.'}</p>` : ''}${this.view.next ? html`<button type="button" ?disabled=${this.view.busy} @click=${() => void this.search(true)}>More library items</button>` : ''}`
      }
    </section>`;
  }
}
if (typeof customElements !== 'undefined' && !customElements.get('soda-factory-library'))
  customElements.define('soda-factory-library', SodaFactoryLibrary);
declare global {
  interface HTMLElementTagNameMap {
    'soda-factory-library': SodaFactoryLibrary;
  }
}
