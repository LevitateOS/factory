import {LitElement, html} from 'lit';

/** One row of the quick switcher; `run` is the frame's own action for it. */
export interface JumpItem {
  readonly group: string;
  readonly label: string;
  readonly meta: string;
  readonly disabled?: boolean;
  readonly run: () => void;
}

/** The "Jump to…" quick switcher: a modal dialog with a filter field and a
 * grouped listbox. The frame supplies the rows and opens it; choosing a row
 * closes the dialog and runs the row's action. Focus returns to the opener. */
export class SodaFactoryJump extends LitElement {
  static properties = {items: {attribute: false}, query: {state: true}, active: {state: true}};
  declare items: readonly JumpItem[];
  declare private query: string;
  declare private active: number;
  private opener: HTMLElement | undefined;

  constructor() {
    super();
    this.items = [];
    this.query = '';
    this.active = 0;
  }
  protected createRenderRoot() {
    return this;
  }
  protected firstUpdated() {
    this.dialog.addEventListener('close', this.closed);
  }
  private get dialog() {
    return this.querySelector('dialog')!;
  }
  get isOpen() {
    return this.querySelector('dialog')?.open === true;
  }
  /** Contains the field the user types in, so the frame can leave it alone. */
  owns(node: EventTarget | null) {
    return node instanceof Node && this.contains(node);
  }
  show(opener?: HTMLElement) {
    if (this.isOpen) return;
    this.opener = opener ?? (document.activeElement instanceof HTMLElement ? document.activeElement : undefined);
    this.query = '';
    this.active = 0;
    this.dialog.showModal();
    void this.updateComplete.then(() => this.querySelector<HTMLInputElement>('input')?.focus());
  }
  toggle(opener?: HTMLElement) {
    if (this.isOpen) this.dialog.close();
    else this.show(opener);
  }
  private filtered() {
    const words = this.query.trim().toLowerCase().split(/\s+/).filter(Boolean);
    return this.items.filter((item) => {
      const text = (item.label + ' ' + item.meta + ' ' + item.group).toLowerCase();
      return words.every((word) => text.includes(word));
    });
  }
  private choose(item: JumpItem | undefined) {
    if (!item || item.disabled) return;
    this.dialog.close();
    item.run();
  }
  private closed = () => {
    const opener = this.opener;
    this.opener = undefined;
    if (opener?.isConnected) opener.focus();
  };
  private keydown = (event: KeyboardEvent) => {
    const rows = this.filtered();
    if (event.key === 'ArrowDown' || event.key === 'ArrowUp') {
      event.preventDefault();
      if (!rows.length) return;
      this.active = (this.active + (event.key === 'ArrowDown' ? 1 : rows.length - 1)) % rows.length;
      void this.updateComplete.then(() => this.querySelector('[aria-selected="true"]')?.scrollIntoView({block: 'nearest'}));
    } else if (event.key === 'Enter') {
      event.preventDefault();
      this.choose(rows[this.active]);
    }
  };
  protected render() {
    const rows = this.filtered();
    const active = Math.min(this.active, Math.max(rows.length - 1, 0));
    const groups: {label: string; rows: {item: JumpItem; index: number}[]}[] = [];
    rows.forEach((item, index) => {
      let group = groups.find((found) => found.label === item.group);
      if (!group) groups.push((group = {label: item.group, rows: []}));
      group.rows.push({item, index});
    });
    return html`<dialog
      class="soda-factory-jump"
      aria-label="Jump to"
      @click=${(event: Event) => {
        if (event.target === this.dialog) this.dialog.close();
      }}
    >
      <div class="soda-factory-jump-field">
        <svg viewBox="0 0 24 24" width="18" height="18" aria-hidden="true" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round"><circle cx="11" cy="11" r="6.5"></circle><path d="m16 16 4.5 4.5"></path></svg>
        <input
          type="text"
          role="combobox"
          aria-label="Jump to a project or page"
          aria-expanded="true"
          aria-controls="soda-factory-jump-list"
          aria-autocomplete="list"
          aria-activedescendant=${rows.length ? 'soda-factory-jump-' + active : ''}
          placeholder="Jump to a project or page…"
          autocomplete="off"
          spellcheck="false"
          .value=${this.query}
          @input=${(event: Event) => {
            this.query = (event.target as HTMLInputElement).value;
            this.active = 0;
          }}
          @keydown=${this.keydown}
        />
        <kbd>Esc</kbd>
      </div>
      <div class="soda-factory-jump-list" id="soda-factory-jump-list" role="listbox" aria-label="Results">
        ${groups.map(
          (group, at) => html`<div role="group" aria-labelledby=${'soda-factory-jump-group-' + at}>
            <p class="soda-factory-jump-group" id=${'soda-factory-jump-group-' + at}>${group.label}</p>
            ${group.rows.map(
              ({item, index}) => html`<div
                role="option"
                id=${'soda-factory-jump-' + index}
                aria-selected=${index === active ? 'true' : 'false'}
                aria-disabled=${item.disabled ? 'true' : 'false'}
                @mousedown=${(event: Event) => event.preventDefault()}
                @click=${() => this.choose(item)}
              >
                <span class="soda-factory-jump-label">${item.label}</span><span class="soda-factory-jump-meta">${item.meta}</span>
              </div>`
            )}
          </div>`
        )}
        ${rows.length ? '' : html`<p class="soda-factory-jump-empty" role="status">Nothing matches. Try a project name or a page.</p>`}
      </div>
      <p class="soda-factory-jump-hint"><span>↑ ↓ move</span><span>Enter open</span><span>${rows.length === 1 ? '1 result' : rows.length + ' results'}</span></p>
    </dialog>`;
  }
}
customElements.define('soda-factory-jump', SodaFactoryJump);
