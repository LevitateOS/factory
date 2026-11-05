/** Wire mirror of internal/collaboration records. IDs/revisions stay strings;
 * capabilities are display facts and never an admission or spending grant. */
export type RepositoryID = string;
export type ActorID = string;
export type Revision = string;
export type CommitID = string;
export type Timestamp = string;

export interface RepositoryRef {
  id: RepositoryID;
  host: string;
  external_id: string;
  name: string;
}
export interface ActorRef {
  id: ActorID;
  host: string;
  external_id: string;
  login: string;
}
export interface Observation {
  state: 'available' | 'denied' | 'not_found' | 'unavailable' | 'unknown';
  revision: Revision;
  observed_at: Timestamp;
  reason?: string;
}
export interface Page<T> {
  observation: Observation;
  items: readonly T[] | null;
  next: string;
  complete: boolean;
}
export type Action =
  | 'issues.read'
  | 'issue.create'
  | 'issue.edit'
  | 'issue.comment'
  | 'pulls.read'
  | 'pull.create'
  | 'pull.edit'
  | 'pull.comment'
  | 'pull.review'
  | 'pull.review.comment'
  | 'pull.review.approve'
  | 'pull.review.request_changes'
  | 'thread.resolve'
  | 'pull.merge'
  | 'checks.read'
  | 'checks.jobs.read';
export interface Capability {
  state: 'allowed' | 'denied' | 'unsupported' | 'unavailable' | 'unknown';
  reason?: string;
}
export interface Capabilities {
  head: CommitID | null;
  base: CommitID | null;
  issue: IssueRef | null;
  pull: PullRef | null;
  repository: RepositoryID;
  actor: ActorID;
  connection: string;
  observation: Observation;
  actions: Partial<Record<Action, Capability>> | null;
}
export interface IssueRef {
  kind: 'issue';
  repository: RepositoryID;
  external_id: string;
  number: string;
}
export interface PullRef {
  kind: 'pull';
  repository: RepositoryID;
  external_id: string;
  number: string;
}
export interface Label {
  external_id: string;
  name: string;
  color: string;
}
export interface Issue {
  ref: IssueRef;
  observation: Observation;
  title: string;
  body: string;
  state: 'open' | 'closed' | 'unknown';
  locked?: boolean | null;
  author: ActorRef | null;
  assignees: readonly ActorRef[] | null;
  labels: readonly Label[] | null;
  created_at: Timestamp | null;
  updated_at: Timestamp | null;
  closed_at: Timestamp | null;
  capabilities: Capabilities | null;
}
export interface GitRef {
  repository: RepositoryRef | null;
  name: string;
  commit: CommitID | null;
}
export interface PullRequest {
  ref: PullRef;
  observation: Observation;
  title: string;
  body: string;
  state: 'open' | 'closed' | 'merged' | 'unknown';
  draft: boolean | null;
  locked?: boolean | null;
  author: ActorRef | null;
  base: GitRef;
  head: GitRef;
  created_at: Timestamp | null;
  updated_at: Timestamp | null;
  merged_at: Timestamp | null;
  capabilities: Capabilities | null;
}
export interface Comment {
  external_id: string;
  observation: Observation;
  author: ActorRef | null;
  body: string;
  created_at: Timestamp | null;
  updated_at: Timestamp | null;
}
export interface IssueComment {
  issue: IssueRef;
  comment: Comment;
}
export interface PullComment {
  pull: PullRef;
  comment: Comment;
}
export interface Review {
  external_id: string;
  pull: PullRef;
  observation: Observation;
  author: ActorRef | null;
  commit: CommitID;
  state: 'pending' | 'approved' | 'changes_requested' | 'commented' | 'dismissed' | 'unknown';
  body: string;
  stale: boolean | null;
  official: boolean | null;
  submitted_at: Timestamp | null;
}
export interface DiffRef {
  pull: PullRef;
  base: CommitID;
  head: CommitID;
}
export interface LineRange {
  start: number;
  end: number;
}
export interface ReviewAnchor {
  diff: DiffRef;
  old_path: string;
  new_path: string;
  side: 'old' | 'new';
  scope: 'file' | 'lines';
  lines: LineRange | null;
}
export interface ReviewComment {
  review_id: string | null;
  comment: Comment;
  anchor: ReviewAnchor | null;
}
export interface ReviewLocation {
  commit: CommitID | null;
  path: string;
  side: 'old' | 'new';
  line: number;
}
export interface ReviewThread {
  external_id: string;
  pull: PullRef;
  observation: Observation;
  anchor: ReviewAnchor | null;
  recorded_location: ReviewLocation | null;
  current_anchor: ReviewAnchor | null;
  outdated: boolean | null;
  resolution: 'resolved' | 'unresolved' | 'unknown';
  comments: Page<ReviewComment>;
  capabilities: Capabilities | null;
}
export interface ChangedFile {
  diff: DiffRef;
  old_path: string;
  new_path: string;
  change: 'added' | 'deleted' | 'modified' | 'renamed' | 'copied' | 'unknown';
  patch_state: 'complete' | 'truncated' | 'binary' | 'unavailable' | 'unknown';
  patch: string | null;
  additions: number | null;
  deletions: number | null;
}
export interface Check {
  job: CheckJob | null;
  external_id: string;
  repository: RepositoryID;
  observation: Observation;
  commit: CommitID;
  name: string;
  kind: 'status' | 'job';
  provider_state: string;
  description: string;
  state: 'pending' | 'running' | 'completed' | 'unknown';
  conclusion:
    | 'success'
    | 'failure'
    | 'neutral'
    | 'skipped'
    | 'cancelled'
    | 'timed_out'
    | 'action_required'
    | 'stale'
    | 'unknown';
  required: boolean | null;
  source_app?: string | null;
  details_url: string;
}
export interface CheckJob {
  run_id: string;
  run_number: string;
  workflow: string;
  ref: string;
  event: string;
  trigger_event: string;
  run_state: string;
  need_approval: boolean | null;
  workflow_commit: CommitID;
  job_id: string;
  attempt: string;
  started_at: string | null;
  stopped_at: string | null;
}
export interface BranchProtection {
  repository: RepositoryID;
  observation: Observation;
  branch: string;
  protected: boolean | null;
  status_checks: boolean | null;
  required_contexts: readonly string[] | null;
  required_checks?: readonly {context: string; source: 'any' | 'app' | 'unknown'; app?: string}[];
  requirements_complete?: boolean;
  classic_visible?: boolean;
  rules_visible?: boolean;
  unknown_rule_kinds?: readonly string[];
  require_code_owner_reviews?: boolean | null;
  require_last_push_approval?: boolean | null;
  require_linear_history?: boolean | null;
  require_resolved_threads?: boolean | null;
  required_approvals: number | null;
  block_rejected: boolean | null;
  block_review_requests: boolean | null;
  require_up_to_date: boolean | null;
  require_signed: boolean | null;
  dismiss_stale: boolean | null;
  ignore_stale: boolean | null;
  apply_to_admins: boolean | null;
  protected_files: string;
  unprotected_files: string;
}
export interface PullChecks {
  kind: 'status' | 'job';
  pull: PullRef;
  commit: CommitID;
  observation: Observation;
  checks: Page<Check>;
  protection: BranchProtection | null;
  missing_required: readonly string[];
  invalid_required: readonly string[];
  status_gate: 'not_required' | 'pending' | 'success' | 'failure' | 'error' | 'warning' | 'unknown';
}
