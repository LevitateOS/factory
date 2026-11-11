import {check, readSodaJSON} from '../api/models.js';
import {commandOutcome, type CommandOutcome} from '../api/command-response.js';
import type {FactoryTransport} from '../api/transport.js';
import type {RetainedPending, RetainResult} from './retained-record.js';

export type JournalAction = 'submit' | 'retry' | 'lookup' | 'cancel';
/** What goes over the wire: a retry re-sends the original submission. */
export type JournalWire = Exclude<JournalAction, 'retry'>;
export type FinishedOutcome = Extract<CommandOutcome, {kind: 'finished'}>;

/** A retained collaboration command. `cancel` is persisted: once requested, the command is never retried. */
export interface JournalPending {
  command: string;
  cancel: boolean;
}
export interface JournalContext {
  readonly scope: string;
  readonly transport: FactoryTransport;
  commandScope(signal: AbortSignal): Promise<string>;
}
/** Storage of one retained command; satisfied by PendingRecord. */
export interface JournalStore<T> {
  read(): RetainedPending<T>;
  retain(pending: T): RetainResult;
  settle(pending: T): boolean;
}
/** The flow's own nouns for outcomes that leave the command unfinished. */
export interface JournalWords {
  attention: string;
  /** Replaces `attention` when the effect was already committed. */
  attentionCommitted?: string;
  completing: string;
  unconfirmed: string;
}
/** The flow's own messages once the command finished. */
export interface JournalFinish {
  committed: string;
  /** Committed although cancel was requested; defaults to `committed`. */
  tooLate?: string;
  cancelled: string;
  none: string;
}
export interface JournalRequest<O extends CommandOutcome> {
  /** `submit` posts here; lookup and cancel address `commands/<id>`. */
  endpoints: {submit: string; commands: string};
  body: object;
  /** Defaults to the plain command response. */
  parse?: (body: unknown, command: string) => O;
}
export interface JournalOptions<T, C> {
  /** Identity of the binding; any change retires it. Must include the transport generation. */
  key: (context: C) => string;
  words: JournalWords;
  /** Show a message to the user. */
  say: (message: string) => void;
  /** The binding went stale; the host drops its context. */
  retire: () => void;
  /** Present for flows that retain the command on its own (not inside a draft). */
  store?: (context: C) => JournalStore<T>;
  /** Re-binding the very same context object is a no-op only when set; otherwise an equal key suffices. */
  byObject?: boolean;
}

/** Plain controller (no Lit dependency) for the human collaboration command journal:
 * submit, lookup, retry and cancel against a provider journal with an unknown outcome. */
export class JournalCommand<T extends JournalPending, C extends JournalContext> {
  context: C | undefined;
  /** Retained command and storage state; only maintained when `store` is given. */
  pending: T | undefined;
  blocked = false;
  durable = true;
  private lifetime = new AbortController();
  private binding = '';
  private generation = '';
  constructor(private options: JournalOptions<T, C>) {}
  get signal() {
    return this.lifetime.signal;
  }
  /** False when `context` is already bound. Otherwise aborts the old lifetime and forgets everything. */
  rebind(context: C | undefined): boolean {
    const key = context && this.options.key(context);
    if (context && this.context && key === this.binding && (!this.options.byObject || this.context === context))
      return false;
    this.lifetime.abort();
    this.lifetime = new AbortController();
    this.context = undefined;
    this.binding = '';
    this.pending = undefined;
    this.blocked = false;
    this.durable = true;
    return true;
  }
  /** Binds a validated context after `rebind` returned true. */
  attach(context: C) {
    check(typeof context.commandScope === 'function' && !!context.transport.generation);
    this.context = context;
    this.binding = this.options.key(context);
    this.generation = context.transport.generation;
  }
  /** False once the binding was replaced; a key mismatch retires it. */
  live(context: C, signal: AbortSignal): boolean {
    if (
      this.context !== context ||
      this.lifetime.signal !== signal ||
      signal.aborted ||
      context.transport.generation !== this.generation
    )
      return false;
    if (this.options.key(context) !== this.binding) {
      this.options.retire();
      return false;
    }
    return true;
  }
  async identity(context: C, signal: AbortSignal) {
    signal.throwIfAborted();
    const scope = await context.commandScope(AbortSignal.any([signal, AbortSignal.timeout(15000)]));
    check(this.live(context, signal) && scope === context.scope);
  }
  private store(context: C) {
    const store = this.options.store;
    check(store);
    return store(context);
  }
  /** Reads the retained command from storage. */
  restore(context: C): RetainedPending<T> {
    const saved = this.store(context).read();
    this.pending = saved.pending;
    this.blocked = saved.blocked;
    this.durable = saved.durable;
    return saved;
  }
  /** The stored command, after checking storage is readable and durable. */
  stored(context: C): T | undefined {
    const saved = this.store(context).read();
    check(!saved.blocked && saved.durable);
    return saved.pending;
  }
  retain(context: C, pending: T) {
    check(this.store(context).retain(pending) === 'saved');
    this.pending = pending;
  }
  /** Persists the cancel request so no later retry of this command is possible. */
  requestCancel(context: C, pending: T) {
    this.retain(context, {...pending, cancel: true});
  }
  settle(context: C, pending: T) {
    check(this.store(context).settle(pending));
    this.restore(context);
  }
  /** Sends one journal request and parses the outcome; unfinished outcomes are reported with the flow's words. */
  async deliver<O extends CommandOutcome = CommandOutcome>(
    context: C,
    signal: AbortSignal,
    action: JournalWire,
    pending: T,
    request: JournalRequest<O>
  ): Promise<O> {
    check(this.live(context, signal));
    const {endpoints} = request,
      command = endpoints.commands + '/' + pending.command;
    const response = await context.transport.request(
      action === 'submit' ? endpoints.submit : command + (action === 'cancel' ? '/cancel' : ''),
      {
        method: action === 'lookup' ? 'GET' : 'POST',
        headers: {'Content-Type': 'application/json'},
        signal: AbortSignal.any([signal, AbortSignal.timeout(35000)]),
        ...(action === 'lookup' ? {} : {body: JSON.stringify(action === 'submit' ? request.body : {})}),
      }
    );
    check(this.live(context, signal) && response.ok);
    const parse = request.parse ?? (commandOutcome as (body: unknown, command: string) => O);
    const outcome = parse(await readSodaJSON(response), pending.command);
    check(this.live(context, signal));
    if (outcome.kind !== 'finished') this.options.say(this.unfinished(outcome));
    return outcome;
  }
  private unfinished(outcome: CommandOutcome): string {
    const words = this.options.words;
    if (outcome.kind === 'needs_intervention')
      return outcome.effect === 'committed' && words.attentionCommitted ? words.attentionCommitted : words.attention;
    return outcome.kind === 'committed_completing' ? words.completing : words.unconfirmed;
  }
  /** The terminal message follows the persisted cancel flag, never the click that observed the outcome. */
  finished(pending: JournalPending, outcome: FinishedOutcome, words: JournalFinish): string {
    if (outcome.effect === 'committed') return pending.cancel ? (words.tooLate ?? words.committed) : words.committed;
    return pending.cancel ? words.cancelled : words.none;
  }
}
