import {check} from '../api/models.js';
import {decisionID} from '../api/request-response.js';
import {feedReviewSource} from './feed-response.js';
import {readReview, retainReview, settleReview, sameReview} from './review-record.js';
import type {ReviewAddress, PendingReview} from './review-record.js';

export interface ReviewHandoffResult {
  message: string;
  path?: string;
}
export interface ReviewHandoffSession {
  active(): boolean;
  json(path: string, init?: RequestInit): Promise<unknown>;
  destination(intent: string): string | undefined;
}

/** Recovering only reads the source-bound receipt; explicit retry keeps the
 * original revision, intent and accepted predecessor. */
export async function reviewHandoff(
  address: ReviewAddress,
  revision: number,
  mode: 'open' | 'recover' | 'retry',
  session: ReviewHandoffSession
): Promise<ReviewHandoffResult | undefined> {
  const {post, repository} = address,
    stored = readReview(address);
  if (stored.blocked)
    return {message: 'The saved change review cannot be read safely. Its bytes are kept; check storage and try again.'};
  let pending = stored.pending;
  if (mode !== 'open' && !pending) return {message: 'No saved change review was found. No review was submitted.'};
  const path = 'factory/feed/posts/' + post;
  let source = feedReviewSource(await session.json(path), post, repository);
  if (!session.active()) return;
  const captured: PendingReview = {
    revision: pending?.revision ?? revision,
    intent: source.change.intent,
    acceptedRevision: source.change.acceptedRevision,
  };
  check(source.revision === captured.revision && (!pending || sameReview(pending, captured)));
  if (source.change.state === 'unavailable')
    return {message: 'The proposed change detail is currently unavailable. Check progress to reload it.'};
  if (!source.change.canReview)
    return {message: 'Current access to the request owner’s review is required. Any saved change review is kept.'};
  if (source.change.state === 'review_required') {
    if (pending && mode !== 'retry')
      return {
        message:
          'No recorded review draft was confirmed. The saved handoff is kept; explicitly retry it to submit the original review.',
      };
    pending = pending ?? captured;
    const retained = retainReview(address, pending);
    if (retained !== 'saved')
      return {
        message:
          retained === 'unavailable'
            ? 'The review is kept in this window but could not be saved for recovery. Enable storage, then retry the saved change review. Nothing was submitted.'
            : 'Another or unreadable change review is saved. Its record is kept; check it before submitting.',
      };
    const current = readReview(address);
    check(current.durable && current.pending && !current.blocked && sameReview(current.pending, pending));
    source = feedReviewSource(
      await session.json(path + '/review', {
        method: 'POST',
        headers: {'Content-Type': 'application/json'},
        body: JSON.stringify({revision: pending.revision}),
      }),
      post,
      repository
    );
    if (!session.active()) return;
    check(
      source.revision === pending.revision &&
        source.change.intent === pending.intent &&
        source.change.acceptedRevision === pending.acceptedRevision
    );
  }
  const change = source.change;
  check(
    change.canReview && (change.state === 'draft' || change.state === 'accepted') && decisionID(change.draftRevision)
  );
  if (pending)
    check(
      sameReview(pending, {revision: source.revision, intent: change.intent, acceptedRevision: change.acceptedRevision})
    );
  const destination = session.destination(change.intent);
  check(destination !== undefined);
  if (pending) check(settleReview(address, pending));
  return {
    message:
      change.state === 'accepted'
        ? 'Opening the accepted change.'
        : 'Change saved for review. Review and accept the direction in the same build request.',
    path: destination,
  };
}
