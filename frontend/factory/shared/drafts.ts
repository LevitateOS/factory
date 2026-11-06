// Failed writes, including removals, remain authoritative for this document.
// Host/profile/account-scoped keys preserve drafts and pending command identity
// through component remounts even when browser storage is unavailable.
const unwritten = new Map<string, string>();

export function readStored(key: string): string {
  return inspectStored(key).value ?? '';
}

/** Undefined means unreadable, distinct from an absent record. */
export function inspectStored(key: string): {value: string | undefined; durable: boolean} {
  if (unwritten.has(key)) return {value: unwritten.get(key)!, durable: false};
  return inspectPersisted(key);
}

/** Persisted bytes only, so a failed local write cannot hide a newer stored copy. */
export function inspectPersisted(key: string): {value: string | undefined; durable: boolean} {
  try {
    return {value: window.localStorage.getItem(key) ?? '', durable: true};
  } catch {
    return {value: undefined, durable: false};
  }
}

export function writeStored(key: string, value: string): boolean {
  unwritten.set(key, value);
  if (!writePersisted(key, value)) return false;
  unwritten.delete(key);
  return true;
}

/** For owners that retain their own failed writes and persisted baseline. */
export function writePersisted(key: string, value: string): boolean {
  try {
    if (value === '') window.localStorage.removeItem(key);
    else window.localStorage.setItem(key, value);
    return true;
  } catch {
    return false;
  }
}
