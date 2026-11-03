// Soda response contracts used by its two browser components. No copied Forgejo authority.

// Never accept HTML, invalid UTF-8 or an unbounded body as Soda JSON.
async function sodaJSONReader(response: Response, maxBytes: number) {
  const json = /^application\/json(?:;|$)/i.test(response.headers.get('Content-Type') || '');
  if (!json || Number(response.headers.get('Content-Length')) > maxBytes) {
    await response.body?.cancel();
    throw Error('Invalid Soda response');
  }
  if (!response.body) throw Error('Missing Soda response body');
  return response.body.getReader();
}

export async function readSodaJSON(response: Response, maxBytes = 4 * 1024 * 1024): Promise<unknown> {
  if (!Number.isSafeInteger(maxBytes) || maxBytes < 1 || maxBytes > 4 * 1024 * 1024)
    throw Error('Invalid Soda response bound');
  const reader = await sodaJSONReader(response, maxBytes),
    decoder = new TextDecoder('utf-8', {fatal: true});
  let size = 0,
    text = '';
  try {
    for (;;) {
      const {done, value} = await reader.read();
      if (done) break;
      size += value.byteLength;
      if (size > maxBytes) throw Error('Oversized Soda response');
      text += decoder.decode(value, {stream: true});
    }
    return JSON.parse(text + decoder.decode());
  } catch (error) {
    await reader.cancel();
    throw error;
  } finally {
    reader.releaseLock();
  }
}
export function object(value: unknown): Record<string, unknown> {
  if (value === null || typeof value !== 'object' || Array.isArray(value)) throw Error('Invalid Soda object');
  return value as Record<string, unknown>; // The object shape is checked; properties remain unknown.
}
export function check(condition: unknown): asserts condition {
  if (!condition) throw Error('Invalid or mismatched Soda response');
}
// Lenient readers for SodaOS's own JSON. A view reads only the fields it
// uses, with a default when a field is missing or has another type; extra
// fields and unknown enum values are kept as plain data, never refused.
export const record = (value: unknown): Record<string, unknown> =>
  value !== null && typeof value === 'object' && !Array.isArray(value) ? (value as Record<string, unknown>) : {};
export const list = (value: unknown): unknown[] => (Array.isArray(value) ? value : []);
export const text = (value: unknown, fallback = ''): string => (typeof value === 'string' ? value : fallback);
export const num = (value: unknown, fallback = 0): number =>
  typeof value === 'number' && Number.isFinite(value) ? value : fallback;
export const flag = (value: unknown): boolean => value === true;
export const optionalText = (value: unknown): string | undefined => (typeof value === 'string' ? value : undefined);
export const optionalNum = (value: unknown): number | undefined =>
  typeof value === 'number' && Number.isFinite(value) ? value : undefined;
export const texts = (value: unknown): string[] =>
  list(value).filter((item): item is string => typeof item === 'string');
export const id = (value: unknown): value is string =>
  typeof value === 'string' && /^[1-9][0-9]{0,18}$/.test(value) && BigInt(value) <= 9223372036854775807n;
export const projectId = (value: unknown): value is string =>
  typeof value === 'string' && /^p[0-9a-f]{24}$/.test(value);
export const fingerprint = (value: unknown): value is string =>
  typeof value === 'string' && /^SHA256:[A-Za-z0-9+/]{43}$/.test(value);
export class SodaRequestError extends Error {
  constructor(
    readonly status: number,
    readonly code?: string
  ) {
    super('Soda request failed');
  }
}

export async function readSodaErrorCode(response: Response): Promise<string | undefined> {
  try {
    const error = object(object(await readSodaJSON(response)).error);
    if (typeof error.code === 'string' && /^[a-z_]{1,64}$/.test(error.code)) return error.code;
  } catch {
    /* Untrusted response text is never shown. */
  }
}
