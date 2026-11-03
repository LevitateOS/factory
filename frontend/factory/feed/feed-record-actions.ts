import {html} from 'lit';
import type {TemplateResult} from 'lit';
import type {FeedFinding, FeedPayload} from './feed-response.js';
import {actionComparison, actionIcon, actionItems, actionList, actionState, mergeDrawing} from './feed-actions.js';
import {renderFeedGraph} from './feed-graph.js';

function scene(action: string, content: TemplateResult): TemplateResult {
  return html`<div class="soda-feed-action soda-canonical-context" data-action=${action}>${content}</div>`;
}

function findingPanel(finding: FeedFinding): TemplateResult {
  return html`<article class="soda-canonical-finding">
    <header>
      <span class="soda-action-path"
        >${finding.location ? `${finding.location.path}${finding.location.line ? `:${finding.location.line}` : ''}` : 'Finding location not supplied'}</span
      ><span>${finding.severity ?? 'Review finding'} · ${finding.key}</span>
    </header>
    <div>
      <p><strong>${finding.defect ?? 'Finding detail not supplied'}</strong></p>
      ${
        finding.requiredOutcome
          ? html`<div class="soda-action-next">
              <span aria-hidden="true">↳</span>
              <div>
                <h4>Required behavior</h4>
                <p>${finding.requiredOutcome}</p>
              </div>
            </div>`
          : ''
      }
      ${
        finding.evidence
          ? html`<div class="soda-action-reason">
              <h4>Evidence</h4>
              <p>${finding.evidence}</p>
            </div>`
          : ''
      }
    </div>
  </article>`;
}

// The typed result of one work record, rendered once from the recorded payload.
export function renderRecordAction(payload: FeedPayload | undefined): TemplateResult | undefined {
  if (!payload || payload.communication) return undefined;
  const plan = payload.plan;
  if (plan) {
    const tasks = plan.tasks ?? [],
      inspected = plan.inspected ?? [],
      findings = plan.findings ?? [],
      unresolved = plan.unresolved ?? [];
    return scene(
      'plan',
      html`
        ${plan.approach ? html`<p>${plan.approach}</p>` : ''}
        ${
          tasks.length
            ? renderFeedGraph(
                tasks.map((task) => ({
                  id: task.id,
                  label: task.objective ?? task.id,
                  dependsOn: task.dependsOn ?? [],
                  detail: [task.id, task.lane ? `Lane ${task.lane}` : ''].filter(Boolean).join(' · '),
                })),
                'Implementation order'
              )
            : ''
        }
        ${
          inspected.length
            ? html`<details class="soda-action-more">
                <summary>Sources investigated · ${plan.inspectedTotal ?? inspected.length}</summary>
                <section class="soda-action-investigation">
                  <header>
                    ${actionIcon('investigation')}
                    <h4>Source and evidence</h4>
                  </header>
                  ${actionItems(
                    inspected.map((item) => ({
                      label: item.path,
                      detail:
                        item.evidence ??
                        (item.commit ? `Revision ${item.commit.slice(0, 12)}` : 'No observation supplied'),
                    })),
                    'files',
                    false
                  )}${findings.length ? html`<section class="soda-investigation-insight">${actionList('What was learned', findings)}</section>` : ''}
                </section>
              </details>`
            : ''
        }
        ${unresolved.length ? html`<div class="soda-action-blocker">${actionList('Still needs an answer', unresolved)}</div>` : ''}
      `
    );
  }
  const review = payload.review;
  if (review) {
    const newFindings = review.newFindings ?? [],
      total = review.newFindingsTotal ?? newFindings.length;
    return scene(
      'review',
      html`
        ${
          newFindings.length
            ? html`${newFindings.slice(0, 3).map(findingPanel)}${total > 3 ? html`<p class="soda-feed-block-note">Showing 3 of ${total} findings. Open the pull request for more.</p>` : ''}`
            : review.findings?.length
              ? actionItems(
                  review.findings.map((label) => ({label})),
                  'findings',
                  false
                )
              : ''
        }
        ${
          review.clear
            ? html`<section class="soda-action-checksheet">
                <header>
                  ${actionIcon('checks')}
                  <h4>Review coverage</h4>
                  ${review.clear.head ? html`<code title=${review.clear.head}>${review.clear.head.slice(0, 12)}</code>` : ''}
                </header>
                ${actionItems(
                  (review.clear.assessments ?? []).map((item) => ({
                    label: item.key,
                    ...(item.evidence ? {detail: item.evidence} : {}),
                    ...(item.status ? {state: item.status} : {}),
                  })),
                  'checks',
                  false
                )}
              </section>`
            : ''
        }
        ${review.verdict ? actionState(review.verdict, false) : ''}
        ${review.summary ? html`<p>${review.summary}</p>` : ''}
      `
    );
  }
  const correction = payload.correction;
  if (correction) {
    const dispositions = correction.dispositions ?? [],
      findings = correction.findings ?? [];
    return scene(
      'correction',
      html`
        ${dispositions.slice(0, 3).map((disposition) => {
          const finding = findings.find((item) => item.key === disposition.key);
          return html`<section class="soda-action-correction">
            <h4>${actionIcon('correction')} ${disposition.key}</h4>
            ${actionComparison(finding?.defect, disposition.disposition, ['Finding', 'Reported correction'])}
            ${
              disposition.changedPaths?.length
                ? html`<div class="soda-action-reason">
                    <h4>Changed files</h4>
                    ${actionItems(
                      disposition.changedPaths.map((label) => ({label})),
                      'files',
                      false
                    )}
                  </div>`
                : ''
            }
            ${
              disposition.commits?.length
                ? html`<ul class="soda-action-commits" aria-label="Correction commits">
                    ${disposition.commits.map((commit) => html`<li><code title=${commit}>${commit.slice(0, 12)}</code></li>`)}
                  </ul>`
                : ''
            }
          </section>`;
        })}
        ${findings
          .filter((finding) => !dispositions.some((item) => item.key === finding.key))
          .slice(0, 3)
          .map(
            (finding) =>
              html`${findingPanel(finding)}
                <p class="soda-feed-block-note">No correction disposition recorded.</p>`
          )}
        <div class="soda-action-next">
          <span aria-hidden="true">↳</span>
          <div>
            <h4>${correction.newCandidate ? 'Candidate submitted for review' : 'Correction incomplete'}</h4>
            <p>
              ${correction.newCandidate ? correction.newCandidate.slice(0, 12) : 'No changed candidate has been recorded.'}
            </p>
          </div>
        </div>
      `
    );
  }
  const judgment = payload.judgment;
  if (judgment) {
    const candidates = judgment.candidates ?? [];
    const selected = candidates.find((item) => item.id === judgment.selected);
    return scene(
      'decision',
      html`<div class="soda-action-decision">
        ${
          candidates.length
            ? html`<section class="soda-decision-inputs">
                <h4>Work considered</h4>
                ${actionItems(
                  candidates.map((item) => ({
                    label: item.objective ?? item.id,
                    ...(item.issue ? {detail: `Issue #${item.issue}`} : {}),
                    state: item.id === judgment.selected ? 'Selected' : 'Considered',
                  })),
                  'options',
                  false
                )}
              </section>`
            : ''
        }
        <div class="soda-decision-route">
          <span aria-hidden="true">↓</span><strong>JEV</strong><span aria-hidden="true">↓</span>
        </div>
        <section class="soda-decision-choice">
          <h4>Recorded decision</h4>
          <p>${selected?.objective ?? judgment.selected ?? judgment.outcome ?? 'No decision recorded'}</p>
          ${
            judgment.effective
              ? html`<div class="soda-action-reason">
                  <h4>Effect</h4>
                  <p>${judgment.effective}</p>
                </div>`
              : ''
          }
        </section>
      </div>`
    );
  }
  const delta = payload.reassessment ?? payload.repair;
  if (delta)
    return scene(
      'reassessment',
      html`<div class="soda-action-reassessment">
        <header>
          ${actionIcon('comparison')}
          <h4>Proposed plan changes</h4>
          <span>${delta.changesTotal ?? delta.changes?.length ?? 0} changes</span>
        </header>
        ${actionItems(
          (delta.changes ?? []).map((change) => ({
            label: change.reason ?? 'Change reason not supplied',
            detail: (change.tasks ?? []).map((task) => task.objective ?? task.key).join(' · '),
          })),
          'changes',
          false
        )}
        ${
          delta.retainedIssues?.length
            ? html`<section class="soda-reassessment-retained">
                <h4>Work preserved</h4>
                <p>${delta.retainedIssues.map((issue) => `#${issue}`).join(' · ')}</p>
              </section>`
            : ''
        }
        ${
          delta.attention?.length
            ? html`<section class="soda-action-blocker">
                ${actionItems(
                  delta.attention.map((item) => ({
                    label: item.issue ? `Issue #${item.issue}` : 'Attention needed',
                    detail: item.reason ?? 'Reason not supplied',
                  })),
                  'obstacles',
                  false
                )}
              </section>`
            : ''
        }
        <p class="soda-feed-block-note">The recorded plan remains in effect until these changes are adopted.</p>
      </div>`
    );
  const verification = payload.verification;
  if (verification)
    return scene(
      'verification',
      html`
        ${
          verification.failures?.length
            ? html`<section class="soda-action-blocker">
                ${actionItems(
                  verification.failures.map((item) => ({
                    label: item.check ?? 'Check failure',
                    detail: item.detail ?? 'No detail recorded',
                    state: 'Failed',
                  })),
                  'checks',
                  false
                )}
              </section>`
            : ''
        }
        <section class="soda-action-checksheet">
          <header>
            ${actionIcon('checks')}
            <h4>Application checks</h4>
            <span>${verification.checksTotal ?? verification.checks?.length ?? 0} recorded</span>
          </header>
          ${actionItems(
            (verification.checks ?? []).map((item) => ({
              label: item.id,
              ...(item.outcome ? {state: item.outcome} : {}),
              ...(item.artifactDigest ? {detail: `Artifact ${item.artifactDigest.slice(0, 12)}`} : {}),
            })),
            'checks',
            false
          )}
        </section>
        ${verification.outcome ? actionState(verification.outcome, false) : ''}
      `
    );
  const merge = payload.merge;
  if (merge)
    return scene(
      'merge',
      html`<div class="soda-action-merge">
        ${mergeDrawing()}
        <div>
          <h4>Change integrated</h4>
          ${merge.mergedCommit ? html`<p class="soda-action-path">${merge.mergedCommit.slice(0, 12)}</p>` : ''}
          <div class="soda-action-next">
            <span aria-hidden="true">↳</span>
            <div>
              <h4>Original issue</h4>
              <p>${merge.originalClosedUnix ? 'Closure confirmed' : 'Closure not yet recorded'}</p>
            </div>
          </div>
        </div>
      </div>`
    );
  const implementation = payload.implementation;
  if (implementation || payload.tasks)
    return scene(
      'implementation',
      html`
        ${
          implementation?.candidate
            ? html`<section class="soda-action-result">
                <span class="soda-result-seal" aria-hidden="true">${actionIcon('result')}</span>
                <div>
                  <h4>Implementation checkpoint</h4>
                  <code title=${implementation.candidate}>${implementation.candidate.slice(0, 12)}</code>
                  <p class="soda-feed-block-note">Candidate recorded for the next review.</p>
                </div>
              </section>`
            : ''
        }
        ${
          payload.tasks
            ? actionItems(
                (payload.tasks.tasks ?? []).map((task) => ({
                  label: task.id,
                  ...(task.state ? {state: task.state} : {}),
                })),
                'tasks',
                false
              )
            : ''
        }
      `
    );
  const decomposition = payload.decomposition;
  if (decomposition)
    return scene(
      'decomposition',
      html`
        ${decomposition.attention ? html`<div class="soda-action-blocker"><p>${decomposition.attention}</p></div>` : ''}
        ${(decomposition.candidates ?? []).slice(0, 2).map(
          (candidate) =>
            html`<section class="soda-action-brief">
              <h4>${candidate.key ?? 'Proposed work plan'}</h4>
              ${candidate.rationale ? html`<p>${candidate.rationale}</p>` : ''}${renderFeedGraph(
                (candidate.tasks ?? []).map((task) => ({
                  id: task.key,
                  label: task.objective ?? task.key,
                  dependsOn: task.dependsOn ?? [],
                })),
                'Proposed issue dependencies'
              )}
            </section>`
        )}
      `
    );
  return undefined;
}
