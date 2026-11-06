import {record} from './models.js';

const nonEmpty = (value: unknown): string | undefined =>
  typeof value === 'string' && value !== '' ? value : undefined;

export interface SodaErrorDetail {
  code?: string;
  message?: string;
}
// sodaErrorDetail reads the shared `{error: {code, message}}` refusal
// envelope. Unknown shapes yield no detail, never a guessed reason.
export function sodaErrorDetail(value: unknown): SodaErrorDetail {
  const error = record(record(value).error);
  const code = nonEmpty(error.code);
  const message = nonEmpty(error.message);
  return {
    ...(code === undefined ? {} : {code}),
    ...(message === undefined ? {} : {message}),
  };
}
