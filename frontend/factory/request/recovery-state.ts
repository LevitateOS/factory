// What an interrupted command (save, exploration, planning, generation) shows
// and offers. `unrecorded` is "the server has no record of the pending command";
// `head` says whether the saved request still points at the command's revision
// (resume/retry the same command) or has moved on (release the pending record).
export type RecoveryKind = 'save' | 'exploration' | 'planning' | 'generation';
export type RecoveryState =
  | {kind: 'none'}
  | {kind: 'notice'; notice: string}
  | {kind: 'unrecorded'; head: 'same' | 'moved'; notice: string};
export type Recovery = Record<RecoveryKind, RecoveryState>;

export const freshRecovery = (): Recovery => ({
  save: {kind: 'none'},
  exploration: {kind: 'none'},
  planning: {kind: 'none'},
  generation: {kind: 'none'},
});
export const recoveryText = (state: RecoveryState): string => (state.kind === 'none' ? '' : state.notice);
// Replace the text and keep any resume/release offer already shown.
export const withNotice = (state: RecoveryState, notice: string): RecoveryState =>
  state.kind === 'unrecorded' ? {...state, notice} : notice ? {kind: 'notice', notice} : {kind: 'none'};
// Withdraw the resume/release offer (optionally only one of them) and keep the text.
export const withoutOffer = (state: RecoveryState, head?: 'same' | 'moved'): RecoveryState =>
  state.kind === 'unrecorded' && (!head || state.head === head)
    ? withNotice({kind: 'none'}, state.notice)
    : state;
export const offers = (state: RecoveryState, head: 'same' | 'moved'): boolean =>
  state.kind === 'unrecorded' && state.head === head;
