/** A plain local path, without encoded segments, traversal or URL authority. */
export function nativePath(value: unknown): value is string {
  return (
    typeof value === 'string' &&
    /^\/(?:[^/\\?#\s%]+\/)*[^/\\?#\s%]+$/u.test(value) &&
    !value.split('/').some((part) => part === '.' || part === '..')
  );
}

/** A missing/unrepresentable recorded time remains unknown. */
export function activityTimestamp(createdUnix: number): {iso: string; text: string} | undefined {
  if (!Number.isSafeInteger(createdUnix) || createdUnix <= 0) return undefined;
  const date = new Date(createdUnix * 1000);
  if (!Number.isFinite(date.getTime())) return undefined;
  return {iso: date.toISOString(), text: date.toLocaleString()};
}



export function statusText(phase: string, live: boolean, terminal: boolean, exit: number | null | undefined) {
  const ended = terminal && exit !== undefined && exit !== null ? `ended: ${phase} (exit ${exit})` : phase;
  const running = live ? `${phase} · live` : phase;
  return terminal ? ended : running;
}
