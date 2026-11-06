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

/** The same retained composer with a pull parent and a separate draft key. */
export function pullCommentStoryPeer(): IssueCommentContext {
  const repository = '7', issue = '12', host = 'a'.repeat(32), actor = '1';
  const parent = {kind: 'pull' as const, repository, number: issue, external_id: 'native-pull-12'};
  const scope = 'storybook:pull-comment';
  const head = 'a'.repeat(40);
  const observed = {state: 'available', revision: 'declared-pull-comment', observed_at: '2026-11-03T12:00:00Z'};
  const context: IssueCommentContext = {
    host, actor, repository, issue, kind: 'pull', scope, parent,
    transport: {
      generation: 'declared-pull-comment', assetBase: '/',
      async request(path, init) {
        if (path.endsWith('/capabilities')) return Response.json({
          repository, actor, issue: null, pull: parent, head, base: head,
          connection: 'declared', observation: observed, actions: {'pull.comment': {state: 'allowed'}},
        });
        if (path.endsWith('/comments')) {
          const command = (JSON.parse(String(init?.body)) as {command_id: string}).command_id;
          return Response.json({command_id: command, observation: 'found', effect: 'committed',
            cancellation: 'none', completion: 'complete', finished: true});
        }
        if (path.endsWith('/pulls/' + issue)) return Response.json({
          ref: parent, observation: observed, title: 'Example pull request', body: '', state: 'open',
          draft: false, author: null, base: {repository: null, name: 'main', commit: head},
          head: {repository: null, name: 'review', commit: head},
          created_at: null, updated_at: null, merged_at: null,
        });
        return Response.json({error: 'Not declared'}, {status: 404});
      },
    },
    commandScope: async () => scope,
  };
  return context;
}
