import {check, id, object} from '../api/models.js';
import {decisionID} from '../api/request-response.js';
import type {AllowanceHold} from './allowance.js';

// Combined feed wire contract. The server owns identity, visibility,
// cursors and every bound on the JSON it sends; the browser reads only
// what each view needs. Field names arrive in snake_case and are mapped
// to camelCase once by `camelize`; the interfaces below mirror the Go
// JSON (internal/factory/feed_contract.go), where omitted lists, totals
// and strings stay absent.

export type FeedEntryKind = 'record' | 'post';
export const feedEntryKinds: readonly FeedEntryKind[] = ['record', 'post'];

export type FeedCommunicationKind =
  | 'ask'
  | 'answer'
  | 'reply'
  | 'flag'
  | 'learned'
  | 'suggest'
  | 'partial'
  | 'blocked'
  | 'stuck'
  | 'record_decision'
  | 'finish_incomplete'
  | 'system'
  | 'library_maintenance'
  | 'artifact'
  | 'journey'
  | 'diagnosis'
  | 'repair_draft';
export const feedStates = ['needs_you', 'problem', 'waiting', 'in_progress', 'done', 'decided', 'info'] as const;
export type FeedState = (typeof feedStates)[number];

export type FeedActivity = 'merged' | 'completed' | 'judgment' | FeedCommunicationKind;

export interface FeedEntry {
  kind: FeedEntryKind;
  id: string;
  parentId?: string;
  parentAuthor?: string;
  parentActor?: string;
  parentIssue?: number;
  repository: string;
  seq: number;
  actor?: string;
  author?: string;
  project?: string;
  at?: number;
  status?: string;
  // The server's classification of the entry; status is display text only.
  state?: FeedState;
  issue?: number;
  pr?: number;
  ref?: string;
  summary?: string;
  activity?: FeedActivity;
  assignment?: string;
  eventIdentity?: string;
  payload?: FeedPayload;
  // Human posts carry their stored post and pipeline outcome, so one
  // feed read renders the post without a per-post request.
  post?: FeedPostDetail;
}

export interface FeedPlanTask {
  id: string;
  objective?: string;
  lane?: string;
  dependsOn?: string[];
}

export interface FeedInspected {
  path: string;
  commit?: string;
  evidence?: string;
}

export interface FeedPlan {
  approach?: string;
  findings?: string[];
  unresolved?: string[];
  inspected?: FeedInspected[];
  inspectedTotal?: number;
  tasks?: FeedPlanTask[];
}

export interface FeedTaskStates {
  tasks?: {id: string; state?: string}[];
}

export interface FeedImplementation {
  candidate?: string;
}

export interface FeedFinding {
  key: string;
  severity?: string;
  defect?: string;
  evidence?: string;
  requiredOutcome?: string;
  location?: {path: string; side?: string; line?: number};
}

export interface FeedAssessment {
  key: string;
  status?: string;
  evidence?: string;
}

export interface FeedReview {
  verdict?: string;
  summary?: string;
  findings?: string[];
  newFindings?: FeedFinding[];
  newFindingsTotal?: number;
  clear?: {head?: string; assessments?: FeedAssessment[]};
}

export interface FeedCorrection {
  newCandidate?: string;
  findings?: FeedFinding[];
  dispositions?: {key: string; disposition?: string; changedPaths?: string[]; commits?: string[]}[];
}

export interface FeedIntentTask {
  key: string;
  objective?: string;
  dependsOn?: string[];
}

export interface FeedGraphDelta {
  summary?: string;
  outcome?: string;
  retainedIssues?: number[];
  changes?: {reason?: string; tasks?: FeedIntentTask[]}[];
  changesTotal?: number;
  attention?: {issue?: number; reason?: string}[];
}

export interface FeedCheckFailure {
  check?: string;
  detail?: string;
}

export interface FeedVerification {
  outcome?: string;
  checks?: {id: string; outcome?: string; artifactDigest?: string}[];
  checksTotal?: number;
  failures?: FeedCheckFailure[];
}

export interface FeedDecomposition {
  attention?: string;
  candidates?: {key?: string; rationale?: string; tasks?: FeedIntentTask[]}[];
}

export interface FeedJudgmentCandidate {
  id: string;
  issue?: number;
  objective?: string;
}

export interface FeedJudgment {
  kind: 'priority' | 'delivery';
  selected?: string;
  outcome?: string;
  effective?: string;
  probability?: number;
  confidence?: number;
  probabilities?: Record<string, number>;
  candidates?: FeedJudgmentCandidate[];
}

export interface FeedMerge {
  mergedCommit?: string;
  originalClosedUnix?: number;
}

export interface FeedArtifact {
  id: string;
  name: string;
  mediaType: string;
  href: string;
}

export interface FeedGraphItem {
  id?: string;
  label: string;
  detail?: string;
  state?: string;
  dependsOn?: string[];
}

export interface FeedSourceExcerpt {
  path: string;
  revision?: string;
  startLine: number;
  code: string;
  after?: string;
  highlightLines?: number[];
}

export interface FeedCommunication {
  kind: FeedCommunicationKind;
  // A controller-written Factory event's kind; worker reports carry none.
  componentId?: string;
  title: string;
  summary: string;
  question?: string;
  questionState?: 'open' | 'answered' | 'replied' | 'incorporated';
  disposition?: 'applied' | 'refused' | 'incorporated';
  dispositionReason?: string;
  resolution?: string;
  scope?: string;
  severity?: 'info' | 'warning' | 'critical';
  evidenceRefs?: string[];
  options?: string[];
  completed?: string[];
  remaining?: string[];
  attempts?: string[];
  decision?: string;
  rationale?: string;
  alternatives?: string[];
  parentId?: string;
  items?: FeedGraphItem[];
  artifacts?: FeedArtifact[];
  commits?: {revision: string; summary: string}[];
  source?: FeedSourceExcerpt;
  libraryItemId?: string;
  libraryRevision?: number;
  before?: string;
  after?: string;
  intentSource?: {intent: string; revision: string; predecessor?: string; state: string; owner?: string};
  planSource?: {attemptRoot: string; plan: string; digest: string; sourceCommit: string; generation?: number};
}

// FeedPayload is one record's typed result. At most one family is set;
// `tasks` may accompany an implementation or a correction.
export interface FeedPayload {
  communication?: FeedCommunication;
  plan?: FeedPlan;
  tasks?: FeedTaskStates;
  implementation?: FeedImplementation;
  review?: FeedReview;
  correction?: FeedCorrection;
  reassessment?: FeedGraphDelta;
  repair?: FeedGraphDelta;
  verification?: FeedVerification;
  decomposition?: FeedDecomposition;
  judgment?: FeedJudgment;
  merge?: FeedMerge;
}

export interface FeedPageView {
  allowanceHolds?: AllowanceHold[];
  entries: FeedEntry[];
  next: string;
  truncated: boolean;
}

export interface FeedSubscriptionView {
  subscriber: string;
  repositories: string[];
  kinds: FeedEntryKind[];
  revision: number;
}

export function feedEntryKey(entry: FeedEntry): string {
  return `${entry.kind}:${entry.id}`;
}

// Map keys that are data (option identities), not field names.
const opaqueKeys = new Set(['probabilities', 'consultation', 'contribution']);

// camelize copies one server JSON value with its snake_case field names in
// camelCase. Values are not checked: the server already bounds them.
export function camelize(value: unknown): unknown {
  if (Array.isArray(value)) return value.map(camelize);
  if (value === null || typeof value !== 'object') return value;
  const out: Record<string, unknown> = {};
  for (const [key, field] of Object.entries(value)) {
    out[key.replace(/_([a-z0-9])/g, (_, letter: string) => letter.toUpperCase())] = opaqueKeys.has(key)
      ? field
      : camelize(field);
  }
  return out;
}

// parseFeedPageView reads one served window. An entry that cannot even be
// identified is skipped; it never blanks the rest of the page.
export function parseFeedPageView(value: unknown): FeedPageView {
  const data = object(value);
  check(Array.isArray(data.entries));
  const entries = data.entries.flatMap((raw): FeedEntry[] => {
    try {
      const entry = object(raw);
      check(typeof entry.id === 'string' && id(entry.repository) && typeof entry.seq === 'number');
      const parsed = camelize({...entry, post: undefined}) as FeedEntry;
      if (!feedStates.includes(parsed.state as FeedState)) delete parsed.state;
      if (entry.post !== undefined) parsed.post = parseFeedPostDetail(entry.post);
      else delete parsed.post;
      return [parsed];
    } catch {
      return [];
    }
  });
  const allowanceHolds = data.allowance_holds === undefined ? undefined : (() => {
    check(Array.isArray(data.allowance_holds) && data.allowance_holds.length <= 512);
    return data.allowance_holds.flatMap((raw): AllowanceHold[] => {
      if (!raw || typeof raw !== 'object') return [];
      const hold = raw as Record<string, unknown>;
      if (!id(hold.repository)) return [];
      const issue = typeof hold.issue === 'string' && id(hold.issue) ? hold.issue : undefined;
      return [{repository: hold.repository, ...(issue ? {issue} : {})}];
    });
  })();
  return {
    entries, next: typeof data.next === 'string' ? data.next : '', truncated: data.truncated === true,
    ...(allowanceHolds === undefined ? {} : {allowanceHolds}),
  };
}

export function parseFeedSubscriptionView(value: unknown): FeedSubscriptionView {
  const data = object(value);
  check(typeof data.subscriber === 'string' && typeof data.revision === 'number');
  if (Array.isArray(data.repositories)) check(data.repositories.every(id));
  return {
    subscriber: data.subscriber,
    repositories: Array.isArray(data.repositories) ? (data.repositories as string[]) : [],
    kinds: Array.isArray(data.kinds) ? (data.kinds as FeedEntryKind[]) : [],
    revision: data.revision,
  };
}

// Decimal IDs remain strings; length then lexical order equals positive BIGINT order.
export function compareRepositoryIDs(a: string, b: string): number {
  return a.length - b.length || (a < b ? -1 : a > b ? 1 : 0);
}

export interface FeedQueryOptions {
  repositories: string[];
  kinds: FeedEntryKind[];
  limit: number;
  cursor: string;
  view?: string;
  search?: string;
  role?: string;
  current?: boolean;
}

// feedQueryString renders one feed query: comma-separated repository
// scope, optional kinds, a bounded window and the resume cursor. An empty
// cursor reads from the latest.
export function feedQueryString(query: FeedQueryOptions): string {
  const params = new URLSearchParams();
  params.set('repositories', [...new Set(query.repositories)].sort(compareRepositoryIDs).join(','));
  if (query.kinds.length > 0) params.set('kinds', query.kinds.join(','));
  params.set('limit', String(query.limit));
  if (query.cursor !== '') params.set('cursor', query.cursor);
  if (query.view && query.view !== 'all') params.set('view', query.view);
  if (query.search) params.set('search', query.search);
  if (query.role) params.set('role', query.role);
  if (query.current) params.set('current', 'true');
  return `factory/feed?${params.toString()}`;
}

// PostSource is the server-resolved snapshot of the feed entry one
// post was submitted against.
export interface PostSource {
  kind: FeedEntryKind;
  id: string;
  issue?: number;
  pr?: number;
  ref?: string;
  status?: string;
  summary?: string;
  product?: {intent: string; acceptedRevision: string};
}

export interface PostProductChange {
  intent: string;
  acceptedRevision: string;
  draftRevision?: string;
  state: 'review_required' | 'draft' | 'accepted' | 'superseded' | 'unavailable';
  canReview: boolean;
}

// PostPipelineState is one stored post request: its pipeline states,
// the linked native issue and the author's retry right.
export interface PostPipelineState {
  issueMaker?: {post: string; state: string; detail?: string; checked?: number};
  postId: string;
  revision: number;
  intake: string;
  judgment: string;
  route?: string;
  draft: string;
  effect: string;
  issue?: number;
  existingIssue?: number;
  detail?: string;
  canRetry?: boolean;
  attempts?: number;
  attemptLimit?: number;
  source?: PostSource;
  productChange?: PostProductChange;
  /** JEV's routing Choice: each option weighed, the pick and her confidence. */
  routing?: PostRouting;
  /** The collaborator the next stage waits for: jev, product_lead or forge. */
  stalled?: string;
  authoring?: {
    state: 'queued' | 'waiting' | 'running' | 'settling' | 'failed' | 'unknown';
    reason?: string;
    detail?: string;
    intent?: string;
    run?: string;
    startedUnix?: number;
    sinceUnix?: number;
    checkedUnix?: number;
  };
}

export interface PostRouting {
  selected: string;
  confidence: number;
  options: {key: string; label: string; probability: number}[];
}

// postSourceForEntry names the feed entry one reply answers: a post by
// its post identity, a record by its assignment identity.
export function postSourceForEntry(entry: FeedEntry): {kind: FeedEntryKind; id: string} | undefined {
  const id = entry.id;
  return /^[a-f0-9]{32}$/.test(id) ? {kind: entry.kind, id} : undefined;
}

export interface FeedPostBody {
  repository: string;
  body: string;
  issue?: number;
  parent_id?: string;
  idempotency_key: string;
}

export interface FeedContribution {
  source: string;
  revision: number;
  state: string;
  profile?: string;
  reply?: string;
  body: string;
  context?: string;
  roles: unknown[];
  replies: unknown[];
  calls?: {
    assignment: string;
    question: string;
    answer: {selected: string; probabilities: Record<string, number>; confidence: number; model: string};
  }[];
  minimum_probability: number;
  minimum_confidence: number;
  probability_threshold?: number;
  confidence_threshold?: number;
  policy_version?: string;
}

export interface FeedConsultation {
  snapshot: string;
  group_id: string;
  assignment: string;
  source_revision?: string;
  tool: string;
  kind: string;
  state?: unknown;
  instructions?: unknown;
  criteria?: unknown;
  questions?: Record<string, unknown>;
  model?: string;
  status: 'pending' | 'returned' | 'failed' | 'unavailable' | 'uncertain';
  answer?: Record<string, unknown>;
  created_unix: number;
  updated_unix: number;
}

export interface FeedPostDetail {
  postId: string;
  /** A reply is the same post object with an explicit parent post. */
  parentId?: string;
  // Human replies carry their stored pipeline; worker posts do not.
  pipeline?: PostPipelineState;
  body: string;
  authorName: string;
  at: number;
  edited: boolean;
  removed: boolean;
  actor?: string;
  issueScope?: number;
  status?: string;
  communication?: FeedCommunication;
  consultation?: FeedConsultation;
  contribution?: FeedContribution;
}

// parseFeedPostDetail reads one post as the server serves it (post GET,
// replies page or an enriched feed post). Deleted or revoked sources keep
// their place without text.
export function parseFeedPostDetail(value: unknown): FeedPostDetail {
  const raw = object(value);
  check(typeof raw.post_id === 'string');
  if (raw.repository !== undefined) check(id(raw.repository));
  const data = camelize(raw) as Record<string, unknown> & PostPipelineState;
  const removed = raw.deleted === true || raw.revoked === true;
  const parent = raw.parent_id || object(raw.communication ?? {}).parent_id || object(raw.source ?? {}).id;
  return {
    postId: data.postId,
    ...(typeof parent === 'string' && /^[a-f0-9]{32}$/.test(parent) ? {parentId: parent} : {}),
    ...(raw.judgment !== undefined && !removed ? {pipeline: data} : {}),
    body: typeof raw.body === 'string' ? raw.body : '',
    authorName: typeof raw.author_name === 'string' ? raw.author_name : '',
    at: typeof raw.at === 'number' ? raw.at : 0,
    edited: raw.edited === true,
    removed,
    ...(typeof raw.actor === 'string' && raw.actor ? {actor: raw.actor} : {}),
    ...(typeof raw.issue_scope === 'number' && Number.isSafeInteger(raw.issue_scope) && raw.issue_scope > 0
      ? {issueScope: raw.issue_scope}
      : {}),
    ...(typeof raw.status === 'string' && raw.status ? {status: raw.status} : {}),
    ...(raw.communication ? {communication: data.communication as FeedCommunication} : {}),
    ...(raw.consultation ? {consultation: raw.consultation as FeedConsultation} : {}),
    ...(raw.contribution ? {contribution: raw.contribution as FeedContribution} : {}),
  };
}

/** Editable source identity is stricter than a read-only feed projection. */
export function feedRevisionSource(value: unknown, post: string, repository: string) {
  const row = object(value);
  check(/^[a-f0-9]{32}$/.test(post) && id(repository) && row.post_id === post && row.repository === repository);
  check(
    typeof row.revision === 'number' &&
      Number.isSafeInteger(row.revision) &&
      row.revision >= 0 &&
      row.revision < Number.MAX_SAFE_INTEGER &&
      typeof row.body === 'string'
  );
  return {
    post: parseFeedPostDetail(row),
    repository,
    revision: row.revision,
    canRetry: row.can_retry === true && row.deleted !== true && row.revoked !== true,
    route: typeof row.route === 'string' ? row.route : '',
    detail: typeof row.detail === 'string' ? row.detail : '',
  };
}

/** Retry normally advances once; an already-applied draft can retain its revision. */
export function feedRevisionReceipt(
  value: unknown,
  post: string,
  repository: string,
  command: string,
  revision: number,
  bodyDigest: string,
  unchangedBody: boolean
): number {
  const row = object(value);
  check(
    row.post_id === post &&
      row.repository === repository &&
      row.command_id === command &&
      row.expected_revision === revision &&
      row.body_digest === bodyDigest
  );
  check(typeof row.author === 'string' && /^[1-9][0-9]*$/.test(row.author));
  check(typeof row.revision === 'number' && Number.isSafeInteger(row.revision));
  check(row.revision === revision + 1 || (unchangedBody && row.revision === revision));
  return row.revision;
}

export async function feedRevisionBodyDigest(body: string): Promise<string> {
  const bytes = await crypto.subtle.digest('SHA-256', new TextEncoder().encode(body));
  return [...new Uint8Array(bytes)].map((byte) => byte.toString(16).padStart(2, '0')).join('');
}

/** A review handoff is bound to the source revision and accepted predecessor.
 * The recorded draft revision is the store's receipt, not a current head guess. */
export function feedReviewSource(value: unknown, post: string, repository: string) {
  const source = feedRevisionSource(value, post, repository);
  const change = source.post.pipeline?.productChange;
  check(change && decisionID(change.intent) && decisionID(change.acceptedRevision));
  check(['review_required', 'draft', 'accepted', 'superseded', 'unavailable'].includes(change.state) && typeof change.canReview === 'boolean');
  check(change.draftRevision === undefined || decisionID(change.draftRevision));
  return {...source, change};
}

// newIdempotencyKey mints one caller-chosen key per draft, so a resubmit
// after a lost answer recovers the same post.
export function newIdempotencyKey(): string {
  const bytes = new Uint8Array(16);
  crypto.getRandomValues(bytes);
  return [...bytes].map((byte) => byte.toString(16).padStart(2, '0')).join('');
}

export function postAttemptsExhausted(state: PostPipelineState): boolean {
  return state.attempts !== undefined && state.attemptLimit !== undefined && state.attempts >= state.attemptLimit;
}

// mergeFeedEntries folds one older page behind the shown entries,
// newest-first by global order key with each identity once.
export function mergeFeedEntries(confirmed: FeedEntry[], page: FeedEntry[]): FeedEntry[] {
  const byKey = new Map(confirmed.map((entry) => [feedEntryKey(entry), entry]));
  for (const entry of page) byKey.set(feedEntryKey(entry), entry);
  return [...byKey.values()].sort((a, b) => b.seq - a.seq || (a.id < b.id ? -1 : a.id > b.id ? 1 : 0));
}
