import {html} from 'lit';
import type {FeedConsultation, FeedContribution} from './feed-response.js';
import {object} from '../api/models.js';
import {roleLabel} from './roles.js';
import {renderJevAnswer} from '../shared/jev-answer.js';

function recorded(value: unknown): string {
  return typeof value === 'string' ? value : (JSON.stringify(value, null, 2) ?? '');
}

export function renderContribution(review: FeedContribution, scope?: {entryKey: string}) {
  const role = roleLabel(review.profile) ?? 'The selected role';
  const status: Record<string, string> = {
    pending: 'JEV is checking whether another role can help.',
    invited: `${role} was invited. Waiting for its actual reply.`,
    responded: `${role} replied in this conversation.`,
    not_invited: 'No additional role cleared the contribution gate.',
    unavailable: 'Contribution assessment unavailable; no role was invited.',
    stale: 'The source changed during assessment; this invitation was not used.',
    role_unavailable: 'The selected role is no longer running.',
  };
  return html`<section class="soda-feed-consultation" aria-label="Role contribution assessment">
    <p role="status">${status[review.state] ?? 'Contribution outcome unconfirmed.'}</p>
    ${review.reply && /^[a-f0-9]{32}$/.test(review.reply) ? html`<p class="soda-feed-role-reply"><a href=${'#post-' + review.reply} data-role-reply-open=${review.reply} data-role-reply-entry=${scope?.entryKey ?? ''}>Read the role’s reply</a> <span role="status" data-role-reply-status></span></p>` : ''}
    <details>
      <summary>Why this role was considered</summary>
      <p>
        Recorded source revision ${review.revision}. The invitation permits one discussion reply within existing work.
      </p>
      ${typeof review.probability_threshold === 'number' && typeof review.confidence_threshold === 'number' ? html`<p>Each of three judgments must select the role with at least ${Math.round(review.probability_threshold * 100)}% probability and ${Math.round(review.confidence_threshold * 100)}% confidence. Policy: ${review.policy_version ?? 'recorded'}.</p>` : ''}
      ${(review.calls ?? []).map((call) => {
        const role = review.roles.map(object).find((role) => role.assignment === call.assignment);
        return html`<details>
          <summary>
            ${roleLabel(typeof role?.profile === 'string' ? role.profile : undefined) ?? 'Role'} · JEV
            ${call.answer.selected === 'contribute' ? 'contribution useful' : 'no contribution'} ·
            ${Math.round((call.answer.probabilities.contribute ?? 0) * 100)}%
          </summary>
          <p>${call.question}</p>
          <pre>${recorded(call.answer)}</pre>
        </details>`;
      })}
      <details>
        <summary>Context supplied</summary>
        <pre>
${recorded({source: review.body, context: review.context, roles: review.roles, existing_replies: review.replies})}</pre>
      </details>
    </details>
  </section>`;
}

// These are the actual recorded tool inputs and outputs. The worker's reason
// and resulting decision are separate authored communications in the thread.
export function renderConsultation(call: FeedConsultation) {
  const envelope = object(call.answer ?? {});
  const answers = object(envelope.answers ?? {});
  const questions = call.questions ?? {judgment: {instructions: call.instructions, criteria: call.criteria}};
  const state = {
    pending: 'Waiting for JEV',
    returned: 'JEV returned a result',
    failed: 'JEV request failed',
    unavailable: 'JEV unavailable',
    uncertain: 'JEV outcome unconfirmed',
  }[call.status];
  const next =
    call.status === 'returned'
      ? undefined
      : {
          pending: 'The recorded question is kept. Refresh replies checks the stored outcome again.',
          failed:
            'The recorded request failed. No result was recorded for this request. Refresh replies checks the stored outcome again.',
          unavailable:
            'JEV is unavailable. No result was recorded for this request. Refresh replies checks the stored outcome again.',
          uncertain: 'The outcome is unconfirmed, not successful. Refresh replies checks the stored outcome again.',
        }[call.status];
  return html`<section class="soda-feed-consultation" aria-label="Recorded JEV consultation">
    <p><strong>Asked JEV</strong> · <span role="status">${state}</span>${call.model ? html` · ${call.model}` : ''}</p>
    ${next ? html`<p>${next}</p>` : ''}
    ${Object.entries(questions).map(([id, value]) => {
      const question = typeof value === 'string' ? {instructions: value} : object(value);
      const answer = answers[id] ?? envelope.answer;
      return html`<div class="soda-feed-consultation-question">
        <p class="soda-feed-body-text">${recorded(question.instructions)}</p>
        ${call.status === 'returned' && answer ? renderJevAnswer(answer, question.criteria) : ''}
        <details>
          <summary>Original question, options and result</summary>
          <pre>
${recorded({question: id, instructions: question.instructions, criteria: question.criteria, ...(answer ? {result: answer} : {})})}</pre>
        </details>
      </div>`;
    })}
    <details>
      <summary>Context sent to JEV</summary>
      <pre>${recorded(call.state)}</pre>
      ${call.source_revision ? html`<p>Source revision: <code>${call.source_revision}</code></p>` : ''}
      <p>Call <code>${call.snapshot}</code></p>
    </details>
  </section>`;
}
