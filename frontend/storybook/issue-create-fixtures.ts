import type {IssueCreateContext} from '../factory/issues/create.js';
import {factoryCommandId} from '../factory/api/run-response.js';
import {issueCreateRecordKey} from '../factory/issues/create-record.js';
export type IssueCreateStoryState = 'ready' | 'denied' | 'offline' | 'pending' | 'created';
/** Declared presentation peer, with no provider or repository mutations. */
export function issueCreateStoryPeer(state: IssueCreateStoryState = 'ready'): IssueCreateContext {
  const host = 'a'.repeat(32),
    actor = '1',
    repository = '7',
    scope = 'storybook:issue-create:' + state;
  const context: IssueCreateContext = {
    host,
    actor,
    repository,
    scope,
    commandScope: async () => scope,
    openIssue: () => {},
    transport: {
      generation: scope,
      assetBase: '/',
      async request(path, init) {
        if (state === 'offline') throw Error('Declared offline story');
        if (path.endsWith('/capabilities'))
          return Response.json({
            repository,
            actor,
            issue: null,
            pull: null,
            head: null,
            base: null,
            observation: {state: 'available', revision: 'declared-create-peer', observed_at: '2026-11-03T12:00:00Z'},
            actions: {'issue.create': {state: state === 'denied' ? 'denied' : 'allowed'}},
          });
        const command =
          path.split('/commands/')[1]?.split('/')[0] ??
          (JSON.parse(String(init?.body)) as {command_id: string}).command_id;
        return Response.json({command_id: command, observation: 'unknown', finished: false, issue: null});
      },
    },
  };
  if (state === 'pending' || state === 'created') {
    const draft = {
      revision: factoryCommandId(),
      title: 'Preserve the original issue',
      body: '## Original description\n\nWriting stays with its saved command.',
    };
    localStorage.setItem(
      issueCreateRecordKey(context),
      JSON.stringify({
        host,
        actor,
        repository,
        scope,
        writing: {
          draft,
          ...(state === 'pending'
            ? {
                pending: {
                  command: factoryCommandId(),
                  title: draft.title,
                  body: draft.body,
                  draft: draft.revision,
                  cancel: false,
                },
              }
            : {
                created: {
                  command: factoryCommandId(),
                  issue: {kind: 'issue', repository, number: '12', external_id: 'native-12'},
                },
              }),
        },
      })
    );
  }
  return context;
}
