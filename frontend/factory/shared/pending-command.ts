import {readSodaJSON} from '../api/models.js';
import {sodaErrorDetail} from '../api/error-response.js';
import type {RetainedPending, RetainResult} from './retained-record.js';

/** Storage of one retained command per address; satisfied by the per-flow record modules. */
export interface CommandStorage<T, A> {
  read(address: A): RetainedPending<T>;
  retain(address: A, pending: T): RetainResult;
  settle(address: A, pending: T): boolean;
  same(a: T, b: T): boolean;
}

/** Explicit lifecycle of a receipt-based command. `command` is the retained identity. */
export type PendingCommandState<T> =
  | {kind: 'unresolved'}
  | {kind: 'ready'}
  | {kind: 'retained'; command: T; firstSend: boolean}
  | {kind: 'awaiting'; command: T; firstSend: boolean}
  | {kind: 'confirmed'; command: T}
  | {kind: 'unknown'; command: T}
  | {kind: 'refused'; discardable: boolean}
  | {kind: 'conflict'}
  | {kind: 'blocked'};

export type RefusalVerdict = 'discard' | 'keep';
/** Only a refusal proven to precede persistence of the command may answer 'discard'. */
export type ClassifyRefusal = (status: number, code: string | undefined) => RefusalVerdict;
export const keepRefusals: ClassifyRefusal = () => 'keep';

export type SubmitOutcome =
  | {kind: 'not-saved'; result: 'unavailable' | 'conflict'}
  | {kind: 'retired'}
  | {kind: 'confirmed'}
  | {kind: 'record-changed'}
  | {kind: 'refused'; status: number; code: string | undefined; body: unknown; discarded: boolean}
  | {kind: 'unknown'; error: unknown};

export interface SubmitOptions<T> {
  command: T;
  /** False once the owning view was retired; no state changes follow. */
  live: () => boolean;
  send: () => Promise<Response>;
  /** Validates the 2xx body against the command; throws unless it is the exact receipt. */
  receipt: (body: unknown) => void;
  classifyRefusal: ClassifyRefusal;
}

/** Plain controller (no Lit dependency); `changed` is the host's requestUpdate. */
export class PendingCommandController<T extends object, A extends {scope: string}> {
  state: PendingCommandState<T> = {kind: 'unresolved'};
  address: A | undefined;
  /** The unsettled retained command, if any. */
  pending: T | undefined;
  /** The last command whose receipt confirmed (settled); cleared by the next admission. */
  confirmed: T | undefined;
  private attempted = false;
  constructor(
    private storage: CommandStorage<T, A>,
    private changed: () => void
  ) {}
  /** The flag views gate dispatch on. */
  get ready(): boolean {
    const s = this.state;
    return s.kind === 'ready' || s.kind === 'retained' || s.kind === 'awaiting' || s.kind === 'confirmed' || s.kind === 'unknown' || (s.kind === 'refused' && !s.discardable);
  }
  private to(state: PendingCommandState<T>) {
    this.state = state;
    this.changed();
  }
  /** Context retired: forget everything in memory. Storage is untouched. */
  reset() {
    this.address = this.pending = this.confirmed = undefined;
    this.attempted = false;
    this.to({kind: 'unresolved'});
  }
  /** Identity or record could not be read; replay is impossible until re-admitted. */
  unresolve(keepPending = false) {
    if (!keepPending) this.pending = undefined;
    this.to({kind: 'unresolved'});
  }
  /** Identity is being resolved again. */
  resolving() {
    this.to({kind: 'unresolved'});
  }
  /** Reads the stored record for the admitted address. A recovered command that is not the one
   * already in memory has an unknown history, so it can never be a first send. */
  adopt(address: A): 'ready' | 'blocked' {
    const stored = this.storage.read(address);
    const unchanged =
      !!stored.pending &&
      this.address?.scope === address.scope &&
      !!this.pending &&
      this.storage.same(stored.pending, this.pending);
    this.attempted = stored.pending ? (unchanged ? this.attempted : true) : false;
    if (this.address?.scope !== address.scope || stored.pending) this.confirmed = undefined;
    this.address = address;
    this.pending = stored.pending;
    if (stored.blocked) {
      this.to({kind: 'blocked'});
      return 'blocked';
    }
    this.to(
      stored.pending
        ? {kind: 'unknown', command: stored.pending}
        : this.confirmed
          ? {kind: 'confirmed', command: this.confirmed}
          : {kind: 'ready'}
    );
    return 'ready';
  }
  /** Re-admits the account immediately before dispatch and compares what the user was shown with
   * what is now stored. 'changed' leaves the controller not ready: another explicit action is
   * needed after the recovered record has been seen. */
  async readmit(resolve: () => Promise<boolean>): Promise<'ready' | 'unresolved' | 'changed'> {
    const shown = this.pending,
      scope = this.address?.scope;
    if (!(await resolve())) return 'unresolved';
    if (
      scope !== this.address?.scope ||
      (shown ? !this.pending || !this.storage.same(shown, this.pending) : this.pending)
    ) {
      this.to({kind: 'conflict'});
      return 'changed';
    }
    return 'ready';
  }
  /** Observed elsewhere (a read) that the retained command was applied; settle it. */
  settleObserved(command: T): boolean {
    if (!this.address || !this.storage.settle(this.address, command)) {
      this.to({kind: 'conflict'});
      return false;
    }
    this.pending = undefined;
    this.to({kind: 'ready'});
    return true;
  }
  /** The single dispatch path: retain, send once, then classify the reply. */
  async submit(options: SubmitOptions<T>): Promise<SubmitOutcome> {
    const {command, live} = options,
      address = this.address;
    if (!address) throw Error('Pending command has no admitted address');
    const firstSend = !this.attempted;
    this.pending = command;
    this.to({kind: 'retained', command, firstSend});
    const retained = this.storage.retain(address, command);
    if (retained !== 'saved') {
      if (retained === 'conflict') this.to({kind: 'conflict'});
      return {kind: 'not-saved', result: retained};
    }
    this.attempted = true;
    this.to({kind: 'awaiting', command, firstSend});
    try {
      const response = await options.send();
      let body: unknown;
      try {
        body = await readSodaJSON(response);
      } catch (error) {
        if (response.ok) throw error;
      }
      if (!live()) return {kind: 'retired'};
      if (!response.ok) {
        const detail = sodaErrorDetail(body),
          raw = typeof body === 'object' && body !== null ? (body as Record<string, unknown>).error : undefined,
          code = typeof raw === 'string' ? raw : detail.code,
          discard =
            firstSend &&
            options.classifyRefusal(response.status, code) === 'discard' &&
            this.storage.settle(address, command);
        if (discard) this.pending = undefined;
        this.to(discard ? {kind: 'refused', discardable: true} : {kind: 'unknown', command});
        return {kind: 'refused', status: response.status, code, body, discarded: discard};
      }
      options.receipt(body);
      if (!live()) return {kind: 'retired'};
      if (this.address?.scope !== address.scope || !this.storage.settle(address, command)) {
        this.to({kind: 'conflict'});
        return {kind: 'record-changed'};
      }
      this.pending = undefined;
      this.confirmed = command;
      this.to({kind: 'confirmed', command});
      return {kind: 'confirmed'};
    } catch (error) {
      if (!live()) return {kind: 'retired'};
      this.to({kind: 'unknown', command});
      return {kind: 'unknown', error};
    }
  }
}
