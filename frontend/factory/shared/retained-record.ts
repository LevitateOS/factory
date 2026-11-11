import {inspectPersisted, inspectStored, writePersisted, writeStored} from './drafts.js';

export interface RetainedWriting<T> {
  raw: string | undefined;
  writing: T | undefined;
  blocked: boolean;
  durable: boolean;
}
export type RetainResult = 'saved' | 'unavailable' | 'conflict';
const unsaved = new Map<string, {raw: string; baseline: string | undefined}>();

/** Exact observed bytes guard edits across views. Unknown stored bytes and a
 * failed write's persisted baseline survive remounts without being overwritten. */
export class WritingRecord<T extends object> {
  constructor(
    private key: string,
    private decode: (raw: string | undefined) => T | undefined,
    private encode: (writing: T) => string
  ) {}
  read(): RetainedWriting<T> {
    const persisted = inspectPersisted(this.key),
      local = unsaved.get(this.key),
      raw = local?.raw ?? persisted.value,
      writing = this.decode(raw);
    return {
      raw,
      writing,
      durable: !local && persisted.durable,
      blocked: (raw !== '' && !writing) || (!!local && persisted.value !== local.baseline),
    };
  }
  save(expected: string | undefined, writing: T): RetainResult {
    const current = this.read();
    if (current.blocked || current.raw !== expected) return 'conflict';
    const raw = this.encode(writing);
    if (this.decode(raw) === undefined) throw Error('Invalid retained writing');
    const persisted = inspectPersisted(this.key),
      baseline = unsaved.get(this.key)?.baseline ?? expected;
    if (persisted.value !== baseline) return 'conflict';
    unsaved.set(this.key, {raw, baseline});
    if (persisted.value === undefined || !writePersisted(this.key, raw)) return 'unavailable';
    unsaved.delete(this.key);
    return 'saved';
  }
  reload(reconcile: (ours: T | undefined, saved: T | undefined) => T | 'persisted' | 'conflict'): RetainedWriting<T> {
    const persisted = inspectPersisted(this.key),
      local = unsaved.get(this.key);
    if (!local || persisted.value === undefined) return this.read();
    const saved = this.decode(persisted.value);
    if (persisted.value !== '' && !saved) return this.read();
    const result = reconcile(this.decode(local.raw), saved);
    if (result === 'persisted') unsaved.delete(this.key);
    else if (result !== 'conflict') unsaved.set(this.key, {raw: this.encode(result), baseline: persisted.value});
    return this.read();
  }
}

export interface RetainedPending<T> {
  pending: T | undefined;
  blocked: boolean;
  durable: boolean;
}

/** Single pending commands retain their original identity until an exact
 * outcome settles them. Their workflow owns validation and matching. */
export class PendingRecord<T extends object> {
  constructor(
    private key: string,
    private maxChars: number,
    private decode: (raw: string) => T | undefined,
    private encode: (pending: T) => string,
    private same: (a: T, b: T) => boolean
  ) {}
  read(): RetainedPending<T> {
    const stored = inspectStored(this.key);
    if (stored.value === '') return {pending: undefined, blocked: false, durable: stored.durable};
    if (stored.value !== undefined && stored.value.length <= this.maxChars) {
      try {
        const pending = this.decode(stored.value);
        if (pending !== undefined) return {pending, blocked: false, durable: stored.durable};
      } catch {
        // Unsupported and foreign records remain untouched.
      }
    }
    return {pending: undefined, blocked: true, durable: stored.durable};
  }
  retain(pending: T): RetainResult {
    const current = this.read();
    if (current.blocked || (current.pending && !this.same(current.pending, pending))) return 'conflict';
    if (current.pending && current.durable && this.encode(current.pending) === this.encode(pending)) return 'saved';
    return writeStored(this.key, this.encode(pending)) ? 'saved' : 'unavailable';
  }
  settle(pending: T): boolean {
    const current = this.read();
    if (current.blocked || !current.pending || !this.same(current.pending, pending)) return false;
    writeStored(this.key, '');
    return true;
  }
}
