/** Explicit lifecycle of one scoped read. `previous` is the last value an earlier read produced. */
export type ReadState<T> =
  | {kind: 'idle'}
  | {kind: 'loading'; previous?: T; reason?: string}
  | {kind: 'ready'; value: T}
  | {kind: 'stale'; value: T; reason: string}
  | {kind: 'missing'}
  | {kind: 'unavailable'; reason?: string};

/** The value a view may show: current, retained from a failed refresh, or held through a refresh. */
export function readValue<T>(state: ReadState<T>): T | undefined {
  return state.kind === 'ready' || state.kind === 'stale'
    ? state.value
    : state.kind === 'loading'
      ? state.previous
      : undefined;
}

/** The failure notice a view shows, if any; it survives a refresh until that refresh settles. */
export function readReason<T>(state: ReadState<T>): string {
  return state.kind === 'stale' || state.kind === 'unavailable' || state.kind === 'loading' ? (state.reason ?? '') : '';
}

export function errorReason(error: unknown, fallback: string): string {
  return error instanceof Error ? error.message : fallback;
}

export interface ReadFailure {
  /** Keep the previous value (state becomes stale) instead of dropping it. */
  keep: boolean;
  reason: string;
}

export interface RunOptions<T> {
  /** 'drop' ignores a run requested while one is loading; 'restart' aborts that one and starts anew. */
  policy: 'drop' | 'restart';
  /** Decides what a failed read does to the previous value and what notice it leaves. */
  failure: (error: unknown, previous: T | undefined) => ReadFailure;
}

interface ReadContext {
  readonly transport: {readonly generation: string};
}

/** Plain controller (no Lit dependency); `changed` is the host's requestUpdate. It owns binding to a
 * context key, the serial/generation staleness guard, the abort lifetime and the read lifecycle.
 * `retire` must unbind the host (set its context to undefined). */
export class ScopedRead<C extends ReadContext, T> {
  state: ReadState<T> = {kind: 'idle'};
  current: C | undefined;
  serial = 0;
  private generation = '';
  private lifetime = new AbortController();
  private running: AbortController | undefined;
  private reads = 0;
  constructor(
    private same: (a: C, b: C) => boolean,
    private changed: () => void,
    private retire: () => void
  ) {}
  get busy(): boolean {
    return this.state.kind === 'loading';
  }
  /** Aborted when the binding is retired; for work that is not a run. */
  get signal(): AbortSignal {
    return this.lifetime.signal;
  }
  /** 'same' keeps everything (storing `keep` as the current context); 'rebound' retires the old
   * binding, resets the state to idle and adopts `context`. The host starts the first run. */
  bind(context: C | undefined, keep: C | undefined = context): 'same' | 'rebound' {
    if (context && this.current && this.same(context, this.current) && context.transport.generation === this.generation) {
      this.current = keep;
      return 'same';
    }
    this.lifetime.abort();
    this.lifetime = new AbortController();
    this.generation = context?.transport.generation ?? '';
    this.current = keep;
    this.serial++;
    this.state = {kind: 'idle'};
    this.changed();
    return 'rebound';
  }
  live(serial: number, context: C): boolean {
    if (serial !== this.serial || context.transport !== this.current?.transport) return false;
    if (context.transport.generation !== this.generation) {
      this.retire();
      return false;
    }
    return true;
  }
  private to(state: ReadState<T>): ReadState<T> {
    this.state = state;
    this.changed();
    return state;
  }
  /** Runs one read for the current binding. Returns the settled state, or undefined when the run was
   * dropped, superseded or retired (no state change follows then). */
  async run(
    read: (signal: AbortSignal, previous: T | undefined) => Promise<T>,
    options: RunOptions<T>
  ): Promise<ReadState<T> | undefined> {
    const context = this.current;
    if (!context || !this.live(this.serial, context)) return;
    if (options.policy === 'drop' && this.busy) return;
    const serial = this.serial,
      token = ++this.reads,
      previous = readValue(this.state),
      reason = readReason(this.state);
    this.running?.abort();
    const request = new AbortController();
    this.running = request;
    const signal = AbortSignal.any([request.signal, this.lifetime.signal]);
    const current = () => this.live(serial, context) && token === this.reads;
    this.to({
      kind: 'loading',
      ...(previous === undefined ? {} : {previous}),
      ...(reason === '' ? {} : {reason}),
    });
    try {
      const value = await read(signal, previous);
      if (!current()) return;
      return this.to({kind: 'ready', value});
    } catch (error) {
      if (!current()) return;
      const failure = options.failure(error, previous);
      return this.to(
        failure.keep && previous !== undefined
          ? {kind: 'stale', value: previous, reason: failure.reason}
          : {kind: 'unavailable', reason: failure.reason}
      );
    }
  }
}
