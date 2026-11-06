import {issueActionAllowed} from './capability-response.js';
import type {IssueRef} from './collaboration.js';
export {commandOutcome as commentOutcome} from './command-response.js';
export type {CommandOutcome as CommentOutcome} from './command-response.js';

export function issueCommentAllowed(value: unknown, repository: string, actor: string, parent: IssueRef): boolean {
  return issueActionAllowed(value, repository, actor, parent, 'issue.comment');
}
