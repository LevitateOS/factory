import {issueDetailStoryPeer} from './issue-detail-fixtures.js';
import type {IssueCommentContext} from '../factory/issues/comment.js';
import {commentRecordKey} from '../factory/issues/comment-record.js';
import {factoryCommandId} from '../factory/api/run-response.js';

export type IssueCommentStoryState = 'ready' | 'denied' | 'offline' | 'pending';
export function issueCommentStoryPeer(state: IssueCommentStoryState) {
  const peer = issueDetailStoryPeer(),
    detail = peer.context;
  const parent = {
    kind: 'issue' as const,
    repository: detail.repository,
    number: detail.issue,
    external_id: 'native-issue-101',
  };
  const context: IssueCommentContext = {
    transport: {
      ...detail.transport,
      async request(path, init) {
        if (state === 'offline') throw Error('Declared offline peer');
        if (path.endsWith('/capabilities'))
          return Response.json({
            repository: detail.repository,
            actor: detail.actor,
            issue: parent,
            pull: null,
            head: null,
            base: null,
            observation: {state: 'available', revision: 'declared-comment', observed_at: '2026-11-03T12:00:00Z'},
            actions: {'issue.comment': {state: state === 'denied' ? 'denied' : 'allowed', reason: ''}},
          });
        if (path.includes('/comments')) {
          const command =
            path.split('/commands/')[1]?.split('/')[0] ??
            (JSON.parse(String(init?.body)) as {command_id: string}).command_id;
          return Response.json({
            command_id: command,
            observation: 'found',
            effect: 'pending',
            cancellation: 'none',
            completion: 'pending',
            finished: false,
          });
        }
        return detail.transport.request(path, init);
      },
    },
    host: detail.host,
    actor: detail.actor,
    scope: detail.scope + ':comment:' + state,
    repository: detail.repository,
    issue: detail.issue,
    parent,
    commandScope: async () => context.scope,
  };
  if (state === 'pending') {
    const draft = {revision: factoryCommandId(), body: 'Original saved comment awaiting reconciliation.'};
    localStorage.setItem(
      commentRecordKey(context),
      JSON.stringify({
        scope: context.scope,
        host: context.host,
        actor: context.actor,
        repository: context.repository,
        issue: context.issue,
        writing: {
          draft,
          pending: {
            command: factoryCommandId(),
            body: draft.body,
            external: parent.external_id,
            draft: draft.revision,
            cancel: false,
          },
        },
      })
    );
  }
  return context;
}
