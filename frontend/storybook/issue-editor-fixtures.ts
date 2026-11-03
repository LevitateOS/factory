import {issueDetailStoryPeer} from './issue-detail-fixtures.js';
import type {IssueEditorContext} from '../factory/issues/editor.js';
import {editorRecordKey} from '../factory/issues/editor-record.js';
import {factoryCommandId} from '../factory/api/run-response.js';
export type IssueEditorStoryState = 'ready' | 'denied' | 'offline' | 'pending' | 'stale';
const emptyDigest = 'e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855';
export function issueEditorStoryPeer(state: IssueEditorStoryState): IssueEditorContext {
  const peer = issueDetailStoryPeer(),
    detail = peer.context;
  const parent = {
    kind: 'issue' as const,
    repository: detail.repository,
    number: detail.issue,
    external_id: 'native-issue-101',
  };
  const context: IssueEditorContext = {
    ...detail,
    parent,
    scope: detail.scope + ':editor:' + state,
    commandScope: async () => context.scope,
    transport: {
      ...detail.transport,
      async request(path, init) {
        if (state === 'offline') throw Error('Declared offline editor');
        if (path.endsWith('/capabilities'))
          return Response.json({
            repository: detail.repository,
            actor: detail.actor,
            issue: parent,
            pull: null,
            head: null,
            base: null,
            observation: {state: 'available', revision: 'declared-edit-peer', observed_at: '2026-11-03T12:00:00Z'},
            actions: {'issue.edit': {state: state === 'denied' ? 'denied' : 'allowed'}},
          });
        if (path.includes('/edits')) {
          const command =
            path.split('/commands/')[1]?.split('/')[0] ??
            (JSON.parse(String(init?.body)) as {command_id: string}).command_id;
          return Response.json({command_id: command, observation: 'unknown', finished: false});
        }
        const response = await detail.transport.request(path, init),
          value = await response.json();
        return Response.json({
          ...value,
          title: 'Original issue title',
          body: state === 'stale' ? 'Current description changed in another window.' : '',
        });
      },
    },
  };
  if (state === 'pending' || state === 'stale') {
    const draft = {
      revision: factoryCommandId(),
      title: 'Keep the edited title',
      body: '## The desired description\n\nPreserve the original submission and newer writing.',
      original: {external: parent.external_id, title: 'Original issue title', body: '', digest: emptyDigest},
    };
    localStorage.setItem(
      editorRecordKey(context),
      JSON.stringify({
        scope: context.scope,
        host: context.host,
        actor: context.actor,
        repository: context.repository,
        issue: context.issue,
        writing: {
          draft,
          ...(state === 'pending' ? {pending: {command: factoryCommandId(), source: draft, cancel: false}} : {}),
        },
      })
    );
  }
  return context;
}
