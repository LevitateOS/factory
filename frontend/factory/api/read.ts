import type {FactoryTransport} from './transport.js';
import {readSodaJSON} from './models.js';
import {sodaErrorDetail} from './error-response.js';
import type {SodaErrorDetail} from './error-response.js';

export class FactoryReadError extends Error {
  constructor(
    readonly status: number,
    readonly detail: SodaErrorDetail
  ) {
    super(detail.message ?? `Factory read failed (${status}).`);
  }
}

export async function getJSON(transport: FactoryTransport, path: string, signal?: AbortSignal): Promise<unknown> {
  const generation = transport.generation;
  const lifetime = signal ? AbortSignal.any([signal, AbortSignal.timeout(15000)]) : AbortSignal.timeout(15000);
  lifetime.throwIfAborted();
  const response = await transport.request(path, {method: 'GET', signal: lifetime});
  lifetime.throwIfAborted();
  if (transport.generation !== generation) throw Error('Factory runtime binding changed.');
  if (!response.ok)
    throw new FactoryReadError(response.status, sodaErrorDetail(await readSodaJSON(response).catch(() => undefined)));
  const value = await readSodaJSON(response);
  lifetime.throwIfAborted();
  if (transport.generation !== generation) throw Error('Factory runtime binding changed.');
  return value;
}

// readFailureText maps one failed read to missing-or-unavailable display
// state. Access loss names itself; anything else reports unavailable.
export function readFailureText(
  status: number,
  detail: SodaErrorDetail,
  missingText: string,
  failedText: string
): {missing: boolean; text: string} {
  if (status === 404) return {missing: true, text: missingText};
  if (status === 401 || status === 403)
    return {missing: false, text: detail.message ?? 'Access changed. Sign in again or ask for access.'};
  return {missing: false, text: detail.message ?? failedText};
}

export function failureInput(error: unknown): {status: number; detail: SodaErrorDetail} {
  if (error instanceof FactoryReadError) return {status: error.status, detail: error.detail};
  return {status: 0, detail: {}};
}
