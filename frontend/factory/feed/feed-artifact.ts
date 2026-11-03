import {LitElement, html} from 'lit';
import type {FeedArtifact} from './feed-response.js';
import type {FactoryTransport} from '../api/transport.js';
import {id} from '../api/models.js';

/** A host-admitted runtime/account lifetime, scoped to this artifact's repository. */
export interface FeedArtifactContext {
  readonly transport: FactoryTransport;
  readonly repository: string;
  readonly generation: string;
  readonly signal: AbortSignal;
}
const maxBytes = 8 * 1024 * 1024;
const textBytes = 64 * 1024;
const empty = () => ({busy: false, open: false, error: '', url: '', text: '', truncated: false});
const mediaType = (artifact: FeedArtifact) => artifact.mediaType.toLowerCase().split(';')[0]?.trim() ?? '';

export class SodaFeedArtifact extends LitElement {
  static properties = {state: {state: true}};
  declare private state: ReturnType<typeof empty>;
  private current: FeedArtifactContext | undefined;
  private source: FeedArtifact | undefined;
  private cancel: AbortController | undefined;
  private serial = 0;
  private retired = () => this.reset();
  constructor() {
    super();
    this.state = empty();
  }
  protected createRenderRoot() {
    return this;
  }
  get artifact(): FeedArtifact | undefined {
    return this.source;
  }
  set artifact(artifact: FeedArtifact | undefined) {
    if (
      artifact?.id === this.source?.id &&
      artifact?.href === this.source?.href &&
      artifact?.name === this.source?.name &&
      artifact?.mediaType === this.source?.mediaType
    )
      return;
    this.reset();
    this.source = artifact ? {...artifact} : undefined;
    this.requestUpdate();
  }
  get context(): FeedArtifactContext | undefined {
    return this.current;
  }
  set context(context: FeedArtifactContext | undefined) {
    const prior = this.current;
    if (
      context?.transport === prior?.transport &&
      context?.repository === prior?.repository &&
      context?.generation === prior?.generation &&
      context?.signal === prior?.signal
    )
      return;
    prior?.signal.removeEventListener('abort', this.retired);
    this.reset();
    this.current = context ? {...context} : undefined;
    this.current?.signal.addEventListener('abort', this.retired, {once: true});
    this.requestUpdate();
  }
  disconnectedCallback() {
    this.context = undefined;
    super.disconnectedCallback();
  }
  private reset() {
    this.serial++;
    this.cancel?.abort();
    this.cancel = undefined;
    if (this.state.url) URL.revokeObjectURL(this.state.url);
    this.state = empty();
  }
  private bindingLive(context: FeedArtifactContext) {
    return (
      !context.signal.aborted &&
      !!context.generation &&
      context.transport.generation === context.generation &&
      id(context.repository)
    );
  }
  private live(serial: number, context: FeedArtifactContext, artifact: FeedArtifact) {
    if (serial !== this.serial || context !== this.current || artifact !== this.source) return false;
    if (!this.bindingLive(context)) {
      this.reset();
      return false;
    }
    return true;
  }
  private async preview() {
    const context = this.current,
      artifact = this.source,
      serial = this.serial;
    if (!context || !artifact || !this.live(serial, context, artifact) || this.state.busy) return;
    if (this.state.url) {
      this.state = {...this.state, open: !this.state.open};
      return;
    }
    const path = 'factory/artifacts/' + artifact.id + '?repository=' + context.repository;
    if (!/^[a-f0-9]{32}$/.test(artifact.id) || artifact.href !== '/api/' + path) {
      this.state = {...this.state, error: 'This artifact does not match its project feed.'};
      return;
    }
    this.state = {...empty(), busy: true};
    const controller = new AbortController();
    this.cancel = controller;
    const signal = AbortSignal.any([controller.signal, context.signal, AbortSignal.timeout(15000)]);
    let reader: ReadableStreamDefaultReader<Uint8Array> | undefined;
    const aborted = () => {
      if (reader) void reader.cancel().catch(() => {});
      if (serial !== this.serial) return;
      this.serial++;
      this.state = {...empty(), error: this.bindingLive(context) ? 'The artifact preview timed out. Try again.' : ''};
    };
    signal.addEventListener('abort', aborted, {once: true});
    try {
      signal.throwIfAborted();
      const response = await context.transport.request(path, {method: 'GET', signal});
      if (!this.live(serial, context, artifact) || signal.aborted) {
        void response.body?.cancel().catch(() => {});
        return;
      }
      if (!response.ok || !response.body || Number(response.headers.get('Content-Length')) > maxBytes) {
        void response.body?.cancel().catch(() => {});
        throw Error(
          !response.ok
            ? [401, 403, 404].includes(response.status)
              ? 'This artifact is no longer available to your account.'
              : 'The artifact could not be loaded. Try again.'
            : 'This artifact exceeds the preview limit.'
        );
      }
      reader = response.body.getReader();
      const chunks: Uint8Array<ArrayBuffer>[] = [];
      let bytes = 0;
      for (;;) {
        const part = await reader.read();
        if (!this.live(serial, context, artifact) || signal.aborted) return;
        if (part.done) break;
        bytes += part.value.byteLength;
        if (bytes > maxBytes) {
          void reader.cancel().catch(() => {});
          throw Error('This artifact exceeds the preview limit.');
        }
        chunks.push(new Uint8Array(part.value));
      }
      const type = mediaType(artifact),
        blob = new Blob(chunks, {type});
      const isText = type.startsWith('text/') || ['application/json', 'application/xml'].includes(type);
      const text = isText ? await blob.slice(0, textBytes).text() : '';
      if (!this.live(serial, context, artifact) || signal.aborted) return;
      this.state = {
        busy: false,
        open: true,
        error: '',
        url: URL.createObjectURL(blob),
        text,
        truncated: isText && bytes > textBytes,
      };
    } catch (error) {
      if (!this.live(serial, context, artifact) || signal.aborted) return;
      this.state = {...empty(), error: error instanceof Error ? error.message : 'The artifact could not be loaded.'};
    } finally {
      signal.removeEventListener('abort', aborted);
      reader?.releaseLock();
      if (this.cancel === controller) this.cancel = undefined;
    }
  }
  protected render() {
    const artifact = this.source,
      context = this.current,
      serial = this.serial;
    if (!artifact || !context || !this.bindingLive(context)) return html``;
    const type = mediaType(artifact);
    return html`<section class="soda-feed-artifact" aria-label=${artifact.name}>
      <header>
        <div><strong>${artifact.name}</strong><span>${type}</span></div>
        <button
          type="button"
          @click=${() => {
            if (this.live(serial, context, artifact)) void this.preview();
          }}
          ?disabled=${this.state.busy}
          aria-expanded=${this.state.open}
        >
          ${this.state.busy ? 'Loading…' : this.state.open ? 'Hide preview' : 'Preview'}
        </button>
      </header>
      ${this.state.error ? html`<p role="alert">${this.state.error}</p>` : ''}
      ${
        this.state.open && this.state.url
          ? html`<div class="soda-feed-artifact-preview">
              ${
                /^image\/(png|jpeg|webp|gif|avif)$/.test(type)
                  ? html`<img src=${this.state.url} alt=${artifact.name} />`
                  : /^video\/(mp4|webm|ogg)$/.test(type)
                    ? html`<video
                        controls
                        preload="metadata"
                        src=${this.state.url}
                        aria-label=${artifact.name}
                      ></video>`
                    : /^audio\/(mpeg|mp4|webm|ogg|wav|x-wav)$/.test(type)
                      ? html`<audio
                          controls
                          preload="metadata"
                          src=${this.state.url}
                          aria-label=${artifact.name}
                        ></audio>`
                      : type.startsWith('text/') || ['application/json', 'application/xml'].includes(type)
                        ? html`<pre tabindex="0">${this.state.text || '(Empty file)'}</pre>
                            ${this.state.truncated ? html`<p class="soda-feed-block-note">Showing the first 64 KiB. Download for the complete file.</p>` : ''}`
                        : html`<p>This file is available to download.</p>`
              }
              <a href=${this.state.url} download=${artifact.name}>Download ${artifact.name}</a>
            </div>`
          : ''
      }
    </section>`;
  }
}
if (typeof customElements !== 'undefined' && !customElements.get('soda-feed-artifact'))
  customElements.define('soda-feed-artifact', SodaFeedArtifact);
declare global {
  interface HTMLElementTagNameMap {
    'soda-feed-artifact': SodaFeedArtifact;
  }
}
