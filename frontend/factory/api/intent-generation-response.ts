import type {GenerationState} from './models.js';

export interface IntentTaskBinding {
  readonly task: string;
  readonly issue: string;
}
export interface IntentGeneration {
  readonly state: GenerationState;
  readonly revision_id: string;
  readonly intent_id: string;
  readonly bindings: readonly IntentTaskBinding[];
}
