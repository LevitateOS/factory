import {check, object} from '../api/models.js';
import {inspectStored, readStored, writeStored} from '../shared/drafts.js';
import {newIdempotencyKey, feedRevisionSource} from './feed-response.js';
import {
  readRevision,
  retainRevision,
  settleRevision,
  samePendingRevision,
  sameRevisionWriting,
  revisionBody,
} from './revision-record.js';
import type {RevisionAddress, PendingRevision} from './revision-record.js';

interface RevisionEdit {
  id: string;
  messageKind: 'post' | 'reply';
  revision: number;
  repository: string;
  body: string;
  baseBody: string;
  blocked?: boolean;
  original?: string;
  question?: string;
  current?: string;
  address?: RevisionAddress | undefined;
  pending?: PendingRevision | undefined;
  recoveryBlocked?: boolean;
}
export interface RevisionSubmission {
  readonly edit: Readonly<RevisionEdit>;
  readonly editor: number;
  readonly command: PendingRevision;
  readonly address: RevisionAddress;
  readonly firstAttempt: boolean;
}

/** One editor owns its retained writing and original-command settlement;
 * timeline reads and current account/transport admission remain with the feed. */
export class FeedRevisionEditor {
  private edit: RevisionEdit | undefined;
  private epoch = 0;
  constructor(private scope: string) {}
  get current(): Readonly<RevisionEdit> | undefined {
    return this.edit;
  }
  beginOpen(): number {
    return ++this.epoch;
  }
  isCurrent(editor: number): boolean {
    return editor === this.epoch;
  }
  clear() {
    this.edit = undefined;
  }
  private key(edit: {repository: string; id: string}) {
    return `soda-feed-revision:v2:${this.scope}:${edit.repository}:${edit.id}`;
  }
  private persist() {
    const edit = this.edit;
    if (edit) writeStored(this.key(edit), JSON.stringify({body: edit.body, original: edit.original}));
  }
  private sync(edit: RevisionEdit) {
    const stored = edit.address ? readRevision(edit.address) : undefined;
    edit.pending = stored?.pending;
    edit.recoveryBlocked = !stored || stored.blocked;
  }
  open(
    data: ReturnType<typeof feedRevisionSource>,
    messageKind: 'post' | 'reply',
    address: RevisionAddress | undefined
  ): boolean {
    const edit: RevisionEdit = {
      id: data.post.postId,
      messageKind: data.post.parentId ? 'reply' : messageKind,
      revision: data.revision,
      repository: data.repository,
      body: data.post.body,
      baseBody: data.post.body,
      blocked: !data.canRetry,
      address,
    };
    this.sync(edit);
    if (!data.canRetry && !edit.pending && !edit.recoveryBlocked) return false;
    if (data.route === 'clarify') {
      edit.original = edit.body;
      edit.body = '';
      edit.question =
        typeof data.detail === 'string' && data.detail ? data.detail : 'What should change, and where should it apply?';
    }
    try {
      const saved = object(JSON.parse(readStored(this.key(edit)) || 'null'));
      if (typeof saved.body === 'string') edit.body = saved.body;
      if (typeof saved.original === 'string') edit.original = saved.original;
    } catch {
      // Absence or unreadable unsent writing does not authorize clearing a pending command.
    }
    if (edit.pending && inspectStored(this.key(edit)).value === '') {
      edit.body = edit.pending.writing.body;
      if (edit.pending.writing.original === undefined) delete edit.original;
      else edit.original = edit.pending.writing.original;
    }
    this.edit = edit;
    return true;
  }
  change(field: 'body' | 'original', value: string) {
    if (!this.edit) return;
    this.edit[field] = value;
    this.persist();
  }
  cancel() {
    ++this.epoch;
    if (this.edit) {
      this.sync(this.edit);
      if (!this.edit.pending && !this.edit.recoveryBlocked) writeStored(this.key(this.edit), '');
    }
    this.edit = undefined;
  }
  lookup(): RevisionSubmission | string | undefined {
    const edit = this.edit;
    if (!edit) return;
    this.sync(edit);
    if (!edit.pending || !edit.address)
      return edit.recoveryBlocked
        ? 'The saved edit cannot be read safely. Its bytes and your writing are kept. Check storage and try again.'
        : 'No saved edit was found. Your writing is kept.';
    return {edit, editor: this.epoch, command: edit.pending, address: edit.address, firstAttempt: false};
  }
  prepare(saved: boolean): RevisionSubmission | string | undefined {
    const edit = this.edit;
    if (!edit) return;
    this.sync(edit);
    if (!edit.address || edit.recoveryBlocked || (!saved && (edit.pending || edit.blocked || !edit.body.trim())))
      return;
    let pending = edit.pending;
    const firstAttempt = !pending;
    if (saved && !pending) return;
    if (!pending) {
      const body = revisionBody(edit);
      if ([...body].length > 4000)
        return 'Keep the original request and answer within 4,000 characters. You can shorten the original request below.';
      pending = {
        command: newIdempotencyKey(),
        revision: edit.revision,
        body,
        baseBody: edit.baseBody,
        writing: {body: edit.body, ...(edit.original === undefined ? {} : {original: edit.original})},
      };
    }
    const retained = retainRevision(edit.address, pending);
    this.sync(edit);
    if (retained !== 'saved')
      return retained === 'unavailable'
        ? 'The exact edit is kept in this window, but could not be saved for recovery. Enable storage, then retry the saved edit. Nothing was sent.'
        : 'Another or unreadable edit is already saved. Its record and your writing are kept. Check the saved edit first.';
    return {edit, editor: this.epoch, command: pending, address: edit.address, firstAttempt};
  }
  requireRetained(submission: RevisionSubmission) {
    const current = readRevision(submission.address);
    check(
      !current.blocked && current.durable && current.pending && samePendingRevision(current.pending, submission.command)
    );
  }
  refused(submission: RevisionSubmission) {
    // Only the first, explicitly refused dispatch is known not to have committed.
    if (submission.firstAttempt) settleRevision(submission.address, submission.command);
  }
  showCurrent(submission: RevisionSubmission, source: ReturnType<typeof feedRevisionSource>) {
    const live = this.edit;
    if (!this.isCurrent(submission.editor) || live?.id !== submission.edit.id) return;
    this.edit = {
      ...live,
      revision: source.revision,
      baseBody: source.post.body,
      current: source.post.body,
      blocked: !source.canRetry,
    };
    this.sync(this.edit);
  }
  finish(submission: RevisionSubmission, revision: number): string {
    const {edit, command: pending, editor} = submission;
    if (!settleRevision(submission.address, pending)) {
      if (this.edit) this.sync(this.edit);
      return 'The saved edit changed. Its record and current writing are kept; reopen it to review.';
    }
    const live = this.edit;
    if (live?.id === edit.id) this.sync(live);
    if (!this.isCurrent(editor))
      return 'An earlier edit finished. Current writing is kept; refresh its post to check the result.';
    const newer = live?.id === edit.id && !sameRevisionWriting(live, pending.writing),
      writing = inspectStored(this.key(edit)).value,
      originalWriting = JSON.stringify(pending.writing),
      replaced = writing === undefined || (writing !== '' && writing !== originalWriting);
    if (newer && live) {
      this.edit = {
        ...live,
        revision: Math.max(live.revision, revision),
        baseBody: live.revision > revision ? live.baseBody : pending.body,
      };
      delete this.edit.current;
      if (
        !replaced ||
        writing === JSON.stringify({body: live.body, ...(live.original === undefined ? {} : {original: live.original})})
      )
        this.persist();
    } else {
      if (!replaced) writeStored(this.key(edit), '');
      if (live?.id === edit.id) this.edit = undefined;
    }
    return newer || replaced
      ? 'Request updated. Processing will continue here. Your newer edits are kept, ready to send.'
      : 'Request updated. Processing will continue here.';
  }
}
