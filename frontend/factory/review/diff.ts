import {LitElement, css, html, type PropertyValues} from 'lit';
import {
  Virtualizer,
  VirtualizedFileDiff,
  parsePatchFiles,
  type FileDiffMetadata,
  type SelectedLineRange,
} from '@pierre/diffs';
import type {ChangedFile, ReviewAnchor, ReviewLocation} from '../api/collaboration.js';
import {parseChangedFile, parseReviewAnchor} from '../api/collaboration-response.js';

export interface ReviewDraft {
  anchor: ReviewAnchor;
  body: string;
}
export interface DiffThread {
  external_id: string;
  outdated: boolean | null;
  resolution: string;
  anchor: ReviewAnchor | null;
  recorded_location: ReviewLocation | null;
  comments: {items: readonly {comment: {body: string}}[] | null; next?: string};
}

function sameFile(anchor: ReviewAnchor | null, file: ChangedFile): boolean {
  if (!anchor) return false;
  const a = anchor.diff,
    b = file.diff;
  return (
    a.base === b.base &&
    a.head === b.head &&
    a.pull.kind === b.pull.kind &&
    a.pull.repository === b.pull.repository &&
    a.pull.external_id === b.pull.external_id &&
    a.pull.number === b.pull.number &&
    anchor.old_path === file.old_path &&
    anchor.new_path === file.new_path
  );
}

/** Production renderer shared by both hosts. Drafts are controlled by the host;
 * renderer recycling never owns their identity, contents or persistence. */
export class SodaFactoryDiff extends LitElement {
  static properties = {
    file: {attribute: false},
    threads: {attribute: false},
    draft: {attribute: false},
    allowDraft: {type: Boolean},
    scheme: {type: String},
    layout: {state: true},
    selection: {state: true},
    message: {state: true},
  };
  declare file: ChangedFile | null;
  declare threads: readonly DiffThread[];
  declare draft: ReviewDraft | null;
  declare allowDraft: boolean;
  declare scheme: 'light' | 'dark';
  declare private layout: 'split' | 'unified';
  declare private selection: ReviewAnchor | null;
  declare private message: string;
  private renderer: VirtualizedFileDiff<DiffThread> | undefined;
  private virtualizer: Virtualizer | undefined;
  private metadata: FileDiffMetadata | undefined;

  constructor() {
    super();
    this.file = null;
    this.threads = [];
    this.draft = null;
    this.allowDraft = true;
    this.scheme = 'light';
    this.layout = 'split';
    this.selection = null;
    this.message = '';
  }

  static styles = css`
    :host {
      display: block;
      color: var(--soda-page-fg, CanvasText);
      background: var(--soda-page-bg, Canvas);
      font: inherit;
    }
    header,
    .tools,
    form {
      display: flex;
      gap: 0.75rem;
      align-items: center;
      flex-wrap: wrap;
      padding: 0.75rem;
    }
    header {
      justify-content: space-between;
      border-bottom: 1px solid var(--soda-border, #8886);
    }
    h3,
    p {
      margin: 0.5rem 0.75rem;
    }
    h3 {
      overflow-wrap: anywhere;
    }
    button,
    input,
    select,
    textarea {
      font: inherit;
      color: inherit;
      background: var(--soda-page-bg, Canvas);
      border: 1px solid var(--soda-border, #888);
      border-radius: 4px;
      padding: 0.4rem;
    }
    button {
      cursor: pointer;
    }
    button:disabled {
      cursor: default;
      opacity: 0.5;
    }
    input {
      width: 5rem;
    }
    label {
      display: flex;
      align-items: center;
      gap: 0.4rem;
    }
    .viewport {
      height: 480px;
      overflow: auto;
      position: relative;
      border-block: 1px solid var(--soda-border, #8886);
    }
    .content {
      position: relative;
      min-height: 1px;
    }
    .composer {
      padding: 0.75rem;
    }
    textarea {
      display: block;
      width: 100%;
      box-sizing: border-box;
      min-height: 6rem;
      margin-block: 0.5rem;
    }
    .thread {
      padding: 0.75rem;
      white-space: pre-wrap;
      overflow-wrap: anywhere;
    }
    summary {
      cursor: pointer;
    }
  `;

  connectedCallback() {
    super.connectedCallback();
    void this.updateComplete.then(() => {
      if (this.isConnected && !this.renderer) this.mount();
    });
  }

  disconnectedCallback() {
    this.dispose();
    super.disconnectedCallback();
  }

  protected updated(changed: PropertyValues) {
    if (changed.has('file')) this.selection = null;
    if (changed.has('file') || changed.has('threads') || changed.has('layout') || changed.has('scheme') || changed.has('allowDraft')) this.mount();
  }

  private dispose() {
    this.renderer?.cleanUp();
    this.virtualizer?.cleanUp();
    this.renderer = undefined;
    this.virtualizer = undefined;
    this.metadata = undefined;
  }

  private mount() {
    this.dispose();
    const viewport = this.renderRoot.querySelector<HTMLElement>('.viewport');
    const content = this.renderRoot.querySelector<HTMLElement>('.content');
    if (!content || !viewport || !this.file || this.file.patch_state !== 'complete') return;
    content.replaceChildren();
    try {
      const file = parseChangedFile(this.file);
      if (file.patch === null || file.patch === '') return;
      // GitHub's per-file patch omits file headers. Supply quoted, canonical
      // paths; full git patches must still describe exactly this one file.
      const patch = file.patch.startsWith('@@')
        ? `diff --git ${JSON.stringify('a/' + (file.old_path || file.new_path))} ${JSON.stringify('b/' + (file.new_path || file.old_path))}\n--- ${file.old_path ? JSON.stringify('a/' + file.old_path) : '/dev/null'}\n+++ ${file.new_path ? JSON.stringify('b/' + file.new_path) : '/dev/null'}\n${file.patch}`
        : file.patch;
      const files = parsePatchFiles(patch, JSON.stringify([file.diff, file.old_path, file.new_path]), true).flatMap(
        (p) => p.files
      );
      if (files.length !== 1) throw Error('Expected one file patch.');
      const metadata = files[0];
      if (
        !metadata ||
        metadata.name !== (file.new_path || file.old_path) ||
        (metadata.prevName !== undefined && metadata.prevName !== file.old_path)
      )
        throw Error('Patch paths do not match this file.');
      this.metadata = metadata;
      const virtualizer = new Virtualizer({overscrollSize: 400});
      this.virtualizer = virtualizer;
      virtualizer.setup(viewport, content);
      const renderer = new VirtualizedFileDiff<DiffThread>(
        {
          diffStyle: this.layout,
          theme: {light: 'pierre-light', dark: 'pierre-dark'},
          themeType: this.scheme,
          preferredHighlighter: 'shiki-js',
          disableFileHeader: true,
          disableErrorHandling: true,
          enableLineSelection: this.allowDraft,
          onLineSelected: (range) => {
            if (this.metadata !== metadata) return;
            if (range) this.selectRange(range);
            else this.selection = null;
          },
          renderAnnotation: ({metadata: thread}) => {
            const node = document.createElement('div');
            node.className = 'factory-inline-thread';
            node.setAttribute('role', 'note');
            node.dataset.threadId = thread.external_id;
            node.textContent = `${thread.outdated ? 'Outdated · ' : ''}${thread.resolution}\n${thread.comments.items?.map((c) => c.comment.body).join('\n') ?? 'Comments unavailable'}${thread.comments.next ? '\nMore comments in discussion' : ''}`;
            return node;
          },
        },
        virtualizer
      );
      this.renderer = renderer;
      renderer.render({
        fileDiff: metadata,
        containerWrapper: content,
        lineAnnotations: this.threads.flatMap((t) => {
          const anchor = t.anchor;
          if (!anchor || !sameFile(anchor, file)) return [];
          return [
            {
              side: anchor.side === 'old' ? ('deletions' as const) : ('additions' as const),
              lineNumber: anchor.lines?.end ?? 0,
              metadata: t,
            },
          ];
        }),
      });
      if (this.selection && sameFile(this.selection, file))
        renderer.setSelectedLines(
          {
            start: this.selection.lines?.start ?? 1,
            end: this.selection.lines?.end ?? 1,
            side: this.selection.side === 'old' ? 'deletions' : 'additions',
          },
          {notify: false}
        );
      this.message = '';
    } catch {
      this.dispose();
      content.replaceChildren();
      this.message = 'This patch could not be rendered safely. Line comments are unavailable.';
    }
  }

  private selectRange(range: SelectedLineRange) {
    this.selection = null;
    this.renderer?.setSelectedLines(null, {notify: false});
    const file = this.file,
      metadata = this.metadata;
    if (!file || !metadata || !range.side || (range.endSide && range.endSide !== range.side)) {
      this.message = 'Select lines on one side of the diff.';
      return;
    }
    const start = Math.min(range.start, range.end),
      end = Math.max(range.start, range.end);
    const old = range.side === 'deletions';
    const present = (line: number) =>
      metadata.hunks.some(
        (h) =>
          line >= (old ? h.deletionStart : h.additionStart) &&
          line < (old ? h.deletionStart + h.deletionCount : h.additionStart + h.additionCount)
      );
    if (
      !Number.isSafeInteger(start) ||
      !Number.isSafeInteger(end) ||
      start < 1 ||
      end - start > 10000 ||
      !Array.from({length: end - start + 1}, (_, i) => start + i).every(present)
    ) {
      this.message = 'Select lines present in this patch.';
      return;
    }
    this.selection = parseReviewAnchor({
      diff: file.diff,
      old_path: file.old_path,
      new_path: file.new_path,
      side: old ? 'old' : 'new',
      scope: 'lines',
      lines: {start, end},
    });
    this.renderer?.setSelectedLines({start, end, side: range.side}, {notify: false});
    this.message = '';
    this.dispatchEvent(
      new CustomEvent<ReviewAnchor>('factory-review-selection', {detail: this.selection, bubbles: true, composed: true})
    );
  }

  private keyboardSelection(event: SubmitEvent) {
    event.preventDefault();
    const form = event.currentTarget;
    if (!(form instanceof HTMLFormElement)) return;
    const values = new FormData(form);
    this.selectRange({
      side: values.get('side') === 'old' ? 'deletions' : 'additions',
      start: Number(values.get('start')),
      end: Number(values.get('end')),
    });
  }

  private changeDraft(draft: ReviewDraft | null) {
    this.dispatchEvent(
      new CustomEvent<ReviewDraft | null>('factory-review-draft-change', {detail: draft, bubbles: true, composed: true})
    );
  }

  private startDraft(anchor: ReviewAnchor | null) {
    if (!anchor) return;
    if (this.draft?.body.trim()) {
      this.message = 'Finish or discard the existing draft before starting another.';
      return;
    }
    this.changeDraft({anchor: parseReviewAnchor(anchor), body: ''});
  }

  protected render() {
    const file = this.file;
    if (!file) return html`<p>No file selected.</p>`;
    const complete = file.patch_state === 'complete';
    const draft = this.draft;
    return html`
      <header>
        <h3>${file.new_path || file.old_path}</h3>
        <span>${file.change}</span>
        <button
          @click=${() => {
            this.layout = this.layout === 'split' ? 'unified' : 'split';
          }}
          aria-label="Toggle diff layout"
        >
          ${this.layout === 'split' ? 'Unified view' : 'Split view'}
        </button>
      </header>
      ${file.old_path && file.old_path !== file.new_path ? html`<p>Previously ${file.old_path}</p>` : ''}
      ${
        complete
          ? html`
              ${this.allowDraft ? html`<form @submit=${this.keyboardSelection} aria-label="Select diff lines">
                <label
                  >Side
                  <select name="side">
                    <option value="old">Old</option>
                    <option value="new">New</option>
                  </select></label
                >
                <label>First line <input name="start" type="number" min="1" value="1" required /></label>
                <label>Last line <input name="end" type="number" min="1" value="1" required /></label>
                <button type="submit">Select lines</button>
              </form>` : ''}
              <div class="viewport" tabindex="0" aria-label="File diff"><div class="content"></div></div>
            `
          : html`<p role="status">
              ${file.patch_state === 'binary' ? 'Binary file. No text patch is available.' : file.patch_state === 'truncated' ? 'The provider truncated this patch. Line comments are unavailable.' : 'Patch content is unavailable.'}
            </p>`
      }
      <p role="status">${this.message}</p>
      ${this.allowDraft ? html`<div class="tools">
        <button ?disabled=${!this.selection} @click=${() => this.startDraft(this.selection)}>
          Comment on selection
        </button>
        <button
          @click=${() => this.startDraft(parseReviewAnchor({diff: file.diff, old_path: file.old_path, new_path: file.new_path, side: file.new_path ? 'new' : 'old', scope: 'file', lines: null}))}
        >
          Comment on file
        </button>
      </div>` : ''}
      ${
        this.allowDraft && draft
          ? html`<section class="composer" aria-label="Review draft">
              <p>
                ${draft.anchor.side} ·
                ${draft.anchor.scope === 'file' ? 'file' : `${draft.anchor.lines?.start}–${draft.anchor.lines?.end}`}
              </p>
              ${!sameFile(draft.anchor, file) ? html`<p role="status">Draft retained at its original revision and path. It has not been moved to this diff.</p>` : ''}
              <textarea
                aria-label="Review comment"
                .value=${draft.body}
                @input=${(event: Event) => {
                  if (event.target instanceof HTMLTextAreaElement)
                    this.changeDraft({...draft, body: event.target.value});
                }}
              ></textarea>
              <button @click=${() => this.changeDraft(null)}>Discard draft</button>
            </section>`
          : ''
      }
      ${this.threads
        .filter((t) => !sameFile(t.anchor, file))
        .map(
          (t) =>
            html`<details class="thread">
              <summary>
                ${
                  t.anchor
                    ? html`Original thread · ${t.anchor.old_path || t.anchor.new_path} · ${t.anchor.side}
                      ${t.anchor.lines?.start ?? 'file'}`
                    : html`Recorded thread · ${t.recorded_location?.path ?? 'Location unobserved'} ·
                      ${t.recorded_location?.line ?? ''}`
                }
              </summary>
              <p>
                ${t.anchor ? 'This thread belongs to its original diff revision.' : 'Original diff context is unavailable.'}
              </p>
              ${t.comments.items?.map((c) => html`<p>${c.comment.body}</p>`)}
            </details>`
        )}
    `;
  }
}

customElements.define('soda-factory-diff', SodaFactoryDiff);
