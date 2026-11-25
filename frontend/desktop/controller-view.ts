import {LitElement} from 'lit';

/** A light-DOM view that re-renders whenever its controller reports a change.
 * The controller owns state and requests; a view sends nothing of its own, so
 * several views of one controller cost no extra requests. */
export class ControllerView<C extends {subscribe(listener: () => void): () => void}> extends LitElement {
  static properties = {controller: {attribute: false}};
  declare controller: C | undefined;
  private unsubscribe: (() => void) | undefined;
  protected createRenderRoot() {
    return this;
  }
  connectedCallback() {
    super.connectedCallback();
    this.subscribe();
  }
  disconnectedCallback() {
    this.unsubscribe?.();
    this.unsubscribe = undefined;
    super.disconnectedCallback();
  }
  protected willUpdate(changed: Map<PropertyKey, unknown>) {
    if (changed.has('controller') && this.isConnected) this.subscribe();
  }
  private subscribe() {
    this.unsubscribe?.();
    this.unsubscribe = this.controller?.subscribe(() => this.requestUpdate());
  }
}

/** Change notification shared by the sign-in controllers. */
export class Observed {
  private readonly listeners = new Set<() => void>();
  subscribe(listener: () => void) {
    this.listeners.add(listener);
    return () => this.listeners.delete(listener);
  }
  protected notify() {
    for (const listener of this.listeners) listener();
  }
}
