package collaboration

import "time"

// Revision is an opaque provider resource version, not a global sequence or an
// authorization receipt. CommitID supports either Git object hash format.
type Revision string
type CommitID string
type SubjectKind string

const (
	SubjectIssue SubjectKind = "issue"
	SubjectPull  SubjectKind = "pull"
)

type ReadState string

const (
	ReadAvailable   ReadState = "available"
	ReadDenied      ReadState = "denied"
	ReadNotFound    ReadState = "not_found"
	ReadUnavailable ReadState = "unavailable"
	ReadUnknown     ReadState = "unknown"
)

// Observation describes this bounded read. Complete applies only to its stated
// resource/page; it does not promise a complete event history or atomic snapshot.
type Observation struct {
	State      ReadState `json:"state"`
	Revision   Revision  `json:"revision"`
	ObservedAt time.Time `json:"observed_at"`
	Reason     string    `json:"reason,omitempty"`
}

type Page[T any] struct {
	Observation Observation `json:"observation"`
	Items       []T         `json:"items"`
	Next        string      `json:"next"`
	Complete    bool        `json:"complete"`
}

type Action string

const (
	ReadIssues           Action = "issues.read"
	CreateIssue          Action = "issue.create"
	EditIssue            Action = "issue.edit"
	CommentIssue         Action = "issue.comment"
	ReadPulls            Action = "pulls.read"
	CreatePull           Action = "pull.create"
	EditPull             Action = "pull.edit"
	CommentPull          Action = "pull.comment"
	ReviewPull           Action = "pull.review"
	CommentReview        Action = "pull.review.comment"
	ApproveReview        Action = "pull.review.approve"
	RequestChangesReview Action = "pull.review.request_changes"
	ResolveThread        Action = "thread.resolve"
	MergePull            Action = "pull.merge"
	ReadChecks           Action = "checks.read"
	ReadCheckJobs        Action = "checks.jobs.read"
)

type Availability string

const (
	Allowed     Availability = "allowed"
	Denied      Availability = "denied"
	Unsupported Availability = "unsupported"
	Unavailable Availability = "unavailable"
	Unknown     Availability = "unknown"
)

type Capability struct {
	State  Availability `json:"state"`
	Reason string       `json:"reason,omitempty"`
}

// Capabilities are presentation facts for one actor/connection/resource read.
// Missing actions mean unknown. Even allowed never grants execution, spending or
// permission to mutate: application admission rechecks current provider facts.
type Capabilities struct {
	Head        *CommitID             `json:"head"`
	Base        *CommitID             `json:"base"` // current target branch tip, not diff merge base
	Issue       *IssueRef             `json:"issue"`
	Pull        *PullRef              `json:"pull"`
	Repository  RepositoryID          `json:"repository,string"`
	Actor       ActorID               `json:"actor,string"`
	Connection  ConnectionID          `json:"connection"`
	Observation Observation           `json:"observation"`
	Actions     map[Action]Capability `json:"actions"`
}

// External IDs identify records; Number is the display/route index in this repo.
// Separate Go types prevent confusing a PR with a normal issue.
type IssueRef struct {
	Kind       SubjectKind  `json:"kind"`
	Repository RepositoryID `json:"repository,string"`
	ExternalID string       `json:"external_id"`
	Number     int64        `json:"number,string"`
}
type PullRef struct {
	Kind       SubjectKind  `json:"kind"`
	Repository RepositoryID `json:"repository,string"`
	ExternalID string       `json:"external_id"`
	Number     int64        `json:"number,string"`
}

type Label struct {
	ExternalID string `json:"external_id"`
	Name       string `json:"name"`
	Color      string `json:"color"`
}

type Issue struct {
	Ref          IssueRef      `json:"ref"`
	Observation  Observation   `json:"observation"`
	Title        string        `json:"title"`
	Body         string        `json:"body"`
	State        string        `json:"state"`  // open, closed, unknown
	Locked       *bool         `json:"locked"` // null means not observed
	Author       *ActorRef     `json:"author"`
	Assignees    []ActorRef    `json:"assignees"`
	Labels       []Label       `json:"labels"`
	CreatedAt    *time.Time    `json:"created_at"`
	UpdatedAt    *time.Time    `json:"updated_at"`
	ClosedAt     *time.Time    `json:"closed_at"`
	Capabilities *Capabilities `json:"capabilities"`
}

type GitRef struct {
	Repository *RepositoryRef `json:"repository"`
	Name       string         `json:"name"`
	Commit     *CommitID      `json:"commit"`
}

type PullRequest struct {
	Ref          PullRef       `json:"ref"`
	Observation  Observation   `json:"observation"`
	Title        string        `json:"title"`
	Body         string        `json:"body"`
	State        string        `json:"state"`  // open, closed, merged, unknown
	Locked       *bool         `json:"locked"` // null means not observed
	Draft        *bool         `json:"draft"`  // null means not observed
	Author       *ActorRef     `json:"author"`
	Base         GitRef        `json:"base"`
	Head         GitRef        `json:"head"`
	CreatedAt    *time.Time    `json:"created_at"`
	UpdatedAt    *time.Time    `json:"updated_at"`
	MergedAt     *time.Time    `json:"merged_at"`
	Capabilities *Capabilities `json:"capabilities"`
}

type Comment struct {
	ExternalID  string      `json:"external_id"`
	Observation Observation `json:"observation"`
	Author      *ActorRef   `json:"author"`
	Body        string      `json:"body"` // untrusted Markdown, never privileged HTML
	CreatedAt   *time.Time  `json:"created_at"`
	UpdatedAt   *time.Time  `json:"updated_at"`
}

// PR conversation and inline review comments remain distinct resources.
type IssueComment struct {
	Issue   IssueRef `json:"issue"`
	Comment Comment  `json:"comment"`
}
type PullComment struct {
	Pull    PullRef `json:"pull"`
	Comment Comment `json:"comment"`
}

type Review struct {
	ExternalID  string      `json:"external_id"`
	Pull        PullRef     `json:"pull"`
	Observation Observation `json:"observation"`
	Author      *ActorRef   `json:"author"`
	Commit      CommitID    `json:"commit"`
	State       string      `json:"state"` // pending, approved, changes_requested, commented, dismissed, unknown
	Body        string      `json:"body"`
	Stale       *bool       `json:"stale"`
	Official    *bool       `json:"official"`
	SubmittedAt *time.Time  `json:"submitted_at"`
}

type DiffRef struct {
	Pull PullRef  `json:"pull"`
	Base CommitID `json:"base"`
	Head CommitID `json:"head"`
}

// The pair remains explicit even when the diff has no changed files.
type PullFiles struct {
	Diff  DiffRef           `json:"diff"`
	Files Page[ChangedFile] `json:"files"`
}
type LineRange struct {
	Start int `json:"start"`
	End   int `json:"end"`
}

// Anchor always pins the immutable original diff, file pair, side and inclusive
// line range. Scope=file has no range. Never replace its commits after force-push.
type ReviewAnchor struct {
	Diff    DiffRef    `json:"diff"`
	OldPath string     `json:"old_path"`
	NewPath string     `json:"new_path"`
	Side    string     `json:"side"`  // old, new
	Scope   string     `json:"scope"` // file, lines
	Lines   *LineRange `json:"lines"`
}

type ReviewComment struct {
	ReviewID string        `json:"review_id"`
	Comment  Comment       `json:"comment"`
	Anchor   *ReviewAnchor `json:"anchor"`
}

// RecordedLocation describes native stored coordinates without inventing a
// missing original diff pair. It is separate from proven current placement.
type ReviewLocation struct {
	Commit *CommitID `json:"commit"`
	Path   string    `json:"path"`
	Side   string    `json:"side"`
	Line   int64     `json:"line"`
}

type ReviewThread struct {
	ExternalID       string              `json:"external_id"`
	Pull             PullRef             `json:"pull"`
	Observation      Observation         `json:"observation"`
	Anchor           *ReviewAnchor       `json:"anchor"`
	RecordedLocation *ReviewLocation     `json:"recorded_location"`
	CurrentAnchor    *ReviewAnchor       `json:"current_anchor"` // only provider-observed relocation
	Outdated         *bool               `json:"outdated"`
	Resolution       string              `json:"resolution"` // resolved, unresolved, unknown
	Comments         Page[ReviewComment] `json:"comments"`
	Capabilities     *Capabilities       `json:"capabilities"`
}

type ChangedFile struct {
	Diff       DiffRef `json:"diff"`
	OldPath    string  `json:"old_path"`
	NewPath    string  `json:"new_path"`
	Change     string  `json:"change"`      // added, deleted, modified, renamed, copied, unknown
	PatchState string  `json:"patch_state"` // complete, truncated, binary, unavailable, unknown
	Patch      *string `json:"patch"`
	Additions  *int    `json:"additions"`
	Deletions  *int    `json:"deletions"`
}

type Check struct {
	Job           *CheckJob    `json:"job"`
	ExternalID    string       `json:"external_id"`
	Repository    RepositoryID `json:"repository,string"`
	Observation   Observation  `json:"observation"`
	Commit        CommitID     `json:"commit"`
	Name          string       `json:"name"`
	Kind          string       `json:"kind"` // status, job
	ProviderState string       `json:"provider_state"`
	Description   string       `json:"description"`
	State         string       `json:"state"`      // pending, running, completed, unknown
	Conclusion    string       `json:"conclusion"` // success, failure, neutral, skipped, cancelled, timed_out, action_required, stale, unknown
	Required      *bool        `json:"required"`
	SourceApp     *string      `json:"source_app,omitempty"`
	DetailsURL    string       `json:"details_url"`
}

// Run identity and job attempts are separate from display names and PR numbers.
// Requiredness stays unknown for jobs: protection contexts select statuses.
type CheckJob struct {
	RunID          string     `json:"run_id"`
	RunNumber      string     `json:"run_number"`
	Workflow       string     `json:"workflow"`
	Ref            string     `json:"ref"`
	Event          string     `json:"event"`
	TriggerEvent   string     `json:"trigger_event"`
	RunState       string     `json:"run_state"`
	NeedApproval   *bool      `json:"need_approval"`
	WorkflowCommit CommitID   `json:"workflow_commit"`
	JobID          string     `json:"job_id"`
	Attempt        string     `json:"attempt"`
	StartedAt      *time.Time `json:"started_at"`
	StoppedAt      *time.Time `json:"stopped_at"`
}

// CheckRequirement preserves a provider app binding. Source is any, app or
// unknown; App is present only for app. These facts grant no merge authority.
type CheckRequirement struct {
	Context string `json:"context"`
	Source  string `json:"source"`
	App     string `json:"app,omitempty"`
}

// Requirements describe observed base branch constraints, not an actor's rights.
// RequirementsComplete is conservative source/representation completeness;
// it never means that the actor can merge or that the requirements are met.
// RequiredContexts stays nil when an unqualified list would lose app bindings.
type BranchProtection struct {
	Repository              RepositoryID       `json:"repository,string"`
	Observation             Observation        `json:"observation"`
	Branch                  string             `json:"branch"`
	Protected               *bool              `json:"protected"`
	StatusChecks            *bool              `json:"status_checks"`
	RequiredContexts        []string           `json:"required_contexts"`
	RequiredChecks          []CheckRequirement `json:"required_checks,omitempty"`
	RequirementsComplete    *bool              `json:"requirements_complete,omitempty"`
	ClassicVisible          *bool              `json:"classic_visible,omitempty"`
	RulesVisible            *bool              `json:"rules_visible,omitempty"`
	UnknownRuleKinds        []string           `json:"unknown_rule_kinds,omitempty"`
	RequireCodeOwnerReviews *bool              `json:"require_code_owner_reviews,omitempty"`
	RequireLastPushApproval *bool              `json:"require_last_push_approval,omitempty"`
	RequireLinearHistory    *bool              `json:"require_linear_history,omitempty"`
	RequireResolvedThreads  *bool              `json:"require_resolved_threads,omitempty"`
	RequiredApprovals       *int64             `json:"required_approvals"`
	BlockRejected           *bool              `json:"block_rejected"`
	BlockReviewRequests     *bool              `json:"block_review_requests"`
	RequireUpToDate         *bool              `json:"require_up_to_date"`
	RequireSigned           *bool              `json:"require_signed"`
	DismissStale            *bool              `json:"dismiss_stale"`
	IgnoreStale             *bool              `json:"ignore_stale"`
	ApplyToAdmins           *bool              `json:"apply_to_admins"`
	ProtectedFiles          string             `json:"protected_files"`
	UnprotectedFiles        string             `json:"unprotected_files"`
}
type PullChecks struct {
	Kind            string            `json:"kind"` // status, job
	Pull            PullRef           `json:"pull"`
	Commit          CommitID          `json:"commit"`
	Observation     Observation       `json:"observation"`
	Checks          Page[Check]       `json:"checks"`
	Protection      *BranchProtection `json:"protection"`
	MissingRequired []string          `json:"missing_required"`
	InvalidRequired []string          `json:"invalid_required"`
	StatusGate      string            `json:"status_gate"` // not_required, pending, success, failure, error, warning, unknown
}
