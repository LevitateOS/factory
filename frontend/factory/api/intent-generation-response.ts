import {check, object} from './models.js';
import {decisionID} from './request-response.js';

export interface IntentTaskBinding {
  readonly task: string;
  readonly issue: string;
}
export interface IntentGeneration {
  readonly state: 'pending' | 'committed' | 'refused' | 'completed';
  readonly revision_id: string;
  readonly intent_id: string;
  readonly bindings: readonly IntentTaskBinding[];
}
function generationBinding(task: string, value: unknown): IntentTaskBinding {
  check(decisionID(task));
  check(typeof value === 'number' && Number.isSafeInteger(value) && value > 0);
  return {task, issue: String(value)};
}
// intentGeneration binds one native-work generation record to its exact
// intent and revision, with task-to-issue mappings as decimal strings.
export function intentGeneration(value: unknown, intent: string, revision: string): IntentGeneration {
  const data = object(value);
  check(data.intent_id === intent && data.revision_id === revision);
  check(
    data.state === 'pending' || data.state === 'committed' || data.state === 'refused' || data.state === 'completed'
  );
  const raw = data.task_issue_ids === undefined ? {} : object(data.task_issue_ids);
  const tasks = Object.keys(raw);
  check(tasks.length <= 64);
  return {
    state: data.state,
    revision_id: revision,
    intent_id: intent,
    bindings: tasks.map((task) => generationBinding(task, raw[task])),
  };
}
