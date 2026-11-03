import {check, id, object} from './models.js';
import type {ChangedFile, CommitID, DiffRef, PullRef, ReviewAnchor} from './collaboration.js';

function boundedText(value: unknown, max: number): string {
  check(typeof value === 'string' && value.length <= max && !value.includes('\0'));
  return value;
}

function commit(value: unknown): CommitID {
  const result = boundedText(value, 64);
  check(/^(?:[a-f0-9]{40}|[a-f0-9]{64})$/i.test(result));
  return result;
}

function pullRef(value: unknown): PullRef {
  const input = object(value);
  check(input.kind === 'pull' && id(input.repository) && id(input.number));
  const external = boundedText(input.external_id, 512);
  check(external.trim() !== '');
  return {kind: 'pull', repository: input.repository, external_id: external, number: input.number};
}

function diffRef(value: unknown): DiffRef {
  const input = object(value);
  return {pull: pullRef(input.pull), base: commit(input.base), head: commit(input.head)};
}

/** Decode the immutable location before a renderer can turn selection into a
 * review draft. It never invents missing commits, sides or inclusive ranges. */
export function parseReviewAnchor(value: unknown): ReviewAnchor {
  const input = object(value);
  const diff = diffRef(input.diff);
  const oldPath = boundedText(input.old_path, 4096);
  const newPath = boundedText(input.new_path, 4096);
  check(input.side === 'old' || input.side === 'new');
  check(input.scope === 'file' || input.scope === 'lines');
  check((input.side === 'old' ? oldPath : newPath) !== '');
  if (input.scope === 'file') {
    check(input.lines === null);
    return {diff, old_path: oldPath, new_path: newPath, side: input.side, scope: 'file', lines: null};
  }
  const range = object(input.lines);
  check(typeof range.start === 'number' && Number.isSafeInteger(range.start) && range.start > 0);
  check(typeof range.end === 'number' && Number.isSafeInteger(range.end) && range.end >= range.start);
  return {
    diff,
    old_path: oldPath,
    new_path: newPath,
    side: input.side,
    scope: 'lines',
    lines: {start: range.start, end: range.end},
  };
}

/** Metadata and unavailable/binary/truncated patch content remain distinct.
 * Unknown new provider states stay unknown rather than becoming complete. */
export function parseChangedFile(value: unknown): ChangedFile {
  const input = object(value);
  const patch = input.patch === null ? null : boundedText(input.patch, 2 * 1024 * 1024);
  const patchState: ChangedFile['patch_state'] =
    input.patch_state === 'complete' ||
    input.patch_state === 'truncated' ||
    input.patch_state === 'binary' ||
    input.patch_state === 'unavailable'
      ? input.patch_state
      : 'unknown';
  if (patchState === 'complete') check(patch !== null);
  const change: ChangedFile['change'] =
    input.change === 'added' ||
    input.change === 'deleted' ||
    input.change === 'modified' ||
    input.change === 'renamed' ||
    input.change === 'copied'
      ? input.change
      : 'unknown';
  const count = (value: unknown): number | null => {
    if (value === null) return null;
    check(typeof value === 'number' && Number.isSafeInteger(value) && value >= 0);
    return value;
  };
  return {
    diff: diffRef(input.diff),
    old_path: boundedText(input.old_path, 4096),
    new_path: boundedText(input.new_path, 4096),
    change,
    patch_state: patchState,
    patch,
    additions: count(input.additions),
    deletions: count(input.deletions),
  };
}
