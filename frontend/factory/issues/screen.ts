import {check, object} from '../api/models.js';
import {SodaFactoryIssue} from './detail.js';
import type {IssueDetailContext} from './detail.js';
import {SodaFactoryWorkJourney} from '../work/journey.js';
import type {JourneyContext} from '../work/journey.js';
import {SodaFactoryIssueComment} from './comment.js';
import type {IssueCommentPorts} from './comment.js';
import {SodaFactoryIssueEditor} from './editor.js';
import type {IssueEditorPorts} from './editor.js';
import {SodaFactoryIssueState} from './state.js';
import type {IssueStatePorts} from './state.js';

/** One composition for native-header Fountain pages and the standalone frame.
 * A current primary issue read gates optional Factory facts and host controls. */
export function mountIssueScreen(
  context: IssueDetailContext,
  journey: JourneyContext | undefined,
  signal: AbortSignal,
  live: () => boolean,
  availability?: (available: boolean) => void,
  comment?: IssueCommentPorts,
  editor?: IssueEditorPorts,
  state?: IssueStatePorts
) {
  if (journey)
    check(
      journey.transport === context.transport &&
        journey.repository === context.repository &&
        journey.issue === context.issue
    );
  if (comment)
    check(
      comment.host === context.host &&
        comment.actor === context.actor &&
        comment.repository === context.repository &&
        comment.issue === context.issue &&
        comment.scope === context.scope &&
        comment.transport === context.transport
    );
  if (editor)
    check(
      editor.host === context.host &&
        editor.actor === context.actor &&
        editor.repository === context.repository &&
        editor.issue === context.issue &&
        editor.scope === context.scope &&
        editor.transport === context.transport
    );
  if (state)
    check(
      state.host === context.host &&
        state.actor === context.actor &&
        state.repository === context.repository &&
        state.issue === context.issue &&
        state.scope === context.scope &&
        state.transport === context.transport
    );
  const element = document.createElement('section'),
    view = new SodaFactoryIssue(),
    facts = new SodaFactoryWorkJourney(),
    composer = new SodaFactoryIssueComment(),
    edit = new SodaFactoryIssueEditor(),
    stateControl = new SodaFactoryIssueState();
  element.className = 'soda-issue-screen';
  if (editor) element.append(edit);
  element.append(view);
  if (state) element.append(stateControl);
  if (comment) element.append(composer);
  if (journey) element.append(facts);
  view.addEventListener('factory-issue-read', (event) => {
    if (signal.aborted || !live() || event.target !== view || !(event instanceof CustomEvent)) return;
    let available = false;
    let external = '';
    try {
      const state = object(event.detail);
      available =
        state.available === true &&
        state.host === context.host &&
        state.repository === context.repository &&
        state.issue === context.issue &&
        state.actor === context.actor &&
        state.scope === context.scope;
      if (available && typeof state.external === 'string' && !!state.external.trim() && state.external.length <= 512)
        external = state.external;
    } catch {
      /* Invalid child display facts retire the optional journey. */
    }
    facts.context = available && journey ? journey : undefined;
    composer.context =
      available && external && comment
        ? {
            ...comment,
            parent: {kind: 'issue', repository: context.repository, number: context.issue, external_id: external},
          }
        : undefined;
    edit.context =
      available && external && editor
        ? {
            ...editor,
            parent: {kind: 'issue', repository: context.repository, number: context.issue, external_id: external},
          }
        : undefined;
    stateControl.context =
      available && external && state
        ? {...state, parent: {kind: 'issue', repository: context.repository, number: context.issue, external_id: external}}
        : undefined;
    availability?.(available);
  });
  composer.addEventListener('issue-comment-saved', (event) => {
    if (!signal.aborted && live() && event.target === composer) void view.refresh();
  });
  edit.addEventListener('issue-edit-saved', (event) => {
    if (!signal.aborted && live() && event.target === edit) void view.refresh();
  });
  stateControl.addEventListener('issue-state-saved', (event) => {
    if (!signal.aborted && live() && event.target === stateControl) void view.refresh();
  });
  view.context = {
    ...context,
    openLink: async (url, childSignal) => {
      check(live());
      const joined = AbortSignal.any([signal, childSignal]);
      joined.throwIfAborted();
      await context.openLink(url, joined);
      check(live());
      joined.throwIfAborted();
    },
  };
  return {
    element,
    refresh() { return view.refresh(); },
    dispose() {
      view.context = undefined;
      facts.context = undefined;
      composer.context = undefined;
      edit.context = undefined;
      stateControl.context = undefined;
      availability?.(false);
      element.remove();
    },
  };
}
