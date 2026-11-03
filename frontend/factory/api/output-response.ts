import {check, object} from './models.js';

export interface FactoryOutput {
  bytes: Uint8Array;
  cursor: number;
  next: number;
  gap: boolean;
  truncated: boolean;
}
function factoryOutputBytes(data: unknown): Uint8Array {
  check(typeof data === 'string');
  let decoded = '';
  try {
    decoded = atob(data);
  } catch {
    check(false);
  }
  return Uint8Array.from(decoded, (c) => c.charCodeAt(0));
}
// factoryOutputFrame admits one output slice with server-chosen cursors. The
// cursor advance must equal the delivered bytes; a gap jumps the viewer to
// the recorded size instead of inventing bytes.
export function factoryOutputFrame(value: unknown): FactoryOutput {
  const frame = object(value);
  check(
    typeof frame.cursor === 'number' &&
      Number.isSafeInteger(frame.cursor) &&
      frame.cursor >= 0 &&
      typeof frame.next === 'number' &&
      Number.isSafeInteger(frame.next) &&
      frame.next >= frame.cursor
  );
  const bytes = factoryOutputBytes(frame.data);
  check(frame.next - frame.cursor === bytes.length);
  return {bytes, cursor: frame.cursor, next: frame.next, gap: frame.gap === true, truncated: frame.truncated === true};
}
// factoryOutputCursor admits one output slice against the viewer's position
// and returns the advanced cursor. A truncated first frame jumps a
// zero-cursor viewer into the trailing window; a gap jumps to the recorded
// size; otherwise the cursor must continue exactly.
export function factoryOutputCursor(viewer: number, frame: FactoryOutput): number {
  check(Number.isSafeInteger(viewer) && viewer >= 0);
  if (frame.cursor !== viewer && !frame.gap && !(frame.truncated && viewer === 0)) throw Error('cursor');
  return frame.next;
}
// factoryClosedReason admits the terminal frame of an attachment. Any reason
// ends the view; unknown reasons still end it rather than stalling.
export function factoryClosedReason(value: unknown): string {
  const frame = object(value);
  return typeof frame.reason === 'string' && frame.reason ? frame.reason : 'closed';
}
