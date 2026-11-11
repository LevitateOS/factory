package factory

import (
	"errors"
	"strconv"
	"strings"
)

// Combined subscribed-feed query/cursor contract (R42 preparation).
//
// The combined feed reads newest-first across projects with a stable
// unique tie-break and bounded keyset paging; ordering never depends
// on whichever page arrived last. Entries come from canonical record
// owners only: existing workflow records (assignments, judgments,
// corrections rendered from the journey readback owners) and, once
// R32 produces them, communication posts. No fixture-only DTO or
// parallel dashboard may serve this contract.
//
// Subscription membership is a filter within already permitted
// activity, never a grant: the serving layer rechecks access per
// entry — including after revocation, account changes or a
// subscription change — and forbidden entries and counts must not
// leak. A cut page names its resume cursor and marks itself
// incomplete instead of rendering a partial window as the whole feed.

// Feed entry kinds admitted to the combined feed.
const (
	// FeedKindRecord is one canonical workflow record rendered from
	// its existing owner (assignment, judgment, correction).
	FeedKindRecord = "record"
	// FeedKindPost is one R32 communication post, comment or revision.
	FeedKindPost = "post"
)

// feedCursorVersion versions the opaque feed cursor.
const feedCursorVersion = "v1"

// FeedPageLimit bounds one combined feed window, mirroring the
// existing 256-row read windows.
const FeedPageLimit = 256

// Feed milestone activities: consequential Factory/Jev events that
// are their own feed entries instead of
// suffixes folded into a worker record's status. A milestone entry
// keeps Kind "record", carries the milestone record's own identity
// in ID, names its bound assignment when available, and receives its own
// global order key when recorded.
const (
	// FeedActivityMerged is one confirmed native merge (Factory).
	FeedActivityMerged = "merged"
	// FeedActivityCompleted is one verified completion event (Factory).
	FeedActivityCompleted = "completed"
	// FeedActivityJudgment is one recorded Jev judgment bound to its
	// repository/assignment (Jev).
	FeedActivityJudgment = "judgment"
)

// FeedState is the closed classification of one feed entry, computed by
// the server from separately recorded facts. Status stays display text;
// clients render State and never parse Status.
type FeedState string

const (
	FeedStateNeedsYou   FeedState = "needs_you"
	FeedStateProblem    FeedState = "problem"
	FeedStateWaiting    FeedState = "waiting"
	FeedStateInProgress FeedState = "in_progress"
	FeedStateDone       FeedState = "done"
	FeedStateDecided    FeedState = "decided"
	FeedStateInfo       FeedState = "info"
)

// FeedEntry is one combined feed member: a stable unique identity
// with its global order key and scope. Seq is the table-global
// sequence behind the owning record (as factory_assignments.seq;
// communication posts draw from the same sequence), unique across
// projects and kinds, so newest-first by seq is deterministic with
// no second key. Actor, Author, Project, At and Status are optional
// server-derived enrichment: the serving layer fills them from the
// canonical owner (never from caller input) and Lane C renders them.
// Records may arrive unenriched until their canonical producer fills
// them; posts always carry them. Issue, PR, Ref and Summary are
// additive recorded content (Lane B2): the bound native issue and
// pull request, the candidate commit the record produced, and a
// bounded preview of the recorded result summary. The full content
// stays with its canonical owner; an unsuppliable field stays empty.
// Activity and Assignment are additive milestone identity (Lane B3):
// empty on assignment records and posts. Payload is the additive
// typed component content projected from the canonical record;
// a record that supplies no typed content omits it entirely.
// Repository is a canonical Factory key encoded as a decimal string on the wire.
type FeedEntry struct {
	Kind          string       `json:"kind"`
	ID            string       `json:"id"`
	ParentID      string       `json:"parent_id,omitempty"`
	ParentAuthor  string       `json:"parent_author,omitempty"`
	ParentActor   string       `json:"parent_actor,omitempty"`
	ParentIssue   int64        `json:"parent_issue,omitempty"`
	Repository    int64        `json:"repository,string"`
	Seq           int64        `json:"seq"`
	Actor         string       `json:"actor,omitempty"`
	Author        string       `json:"author,omitempty"`
	Project       string       `json:"project,omitempty"`
	At            int64        `json:"at,omitempty"`
	Status        string       `json:"status,omitempty"`
	State         FeedState    `json:"state,omitempty"`
	Issue         int64        `json:"issue,omitempty"`
	PR            int64        `json:"pr,omitempty"`
	Ref           string       `json:"ref,omitempty"`
	Summary       string       `json:"summary,omitempty"`
	Activity      string       `json:"activity,omitempty"`
	Assignment    string       `json:"assignment,omitempty"`
	EventIdentity string       `json:"event_identity,omitempty"`
	Payload       *FeedPayload `json:"payload,omitempty"`
	// SourcePost is a human post's stored comment with its pipeline outcome,
	// served with the window so the feed needs no per-post read.
	Post any `json:"post,omitempty"`
}

// FeedPayload is one record's typed component content, projected by
// the server from the canonical typed result (assignment result,
// plan tasks, judgment receipt, merge or completion record). At
// most one role family is set, matching the record's phase or
// milestone activity; Tasks may accompany an implementation or
// correction. Every list is a bounded preview in recorded order
// with its honest total beside it; the full content stays with its
// canonical owner. Nothing here is inferred: a field the record
// does not supply stays absent.
type FeedPayload struct {
	Communication  *FeedCommunication  `json:"communication,omitempty"`
	Plan           *FeedPlan           `json:"plan,omitempty"`
	Tasks          *FeedTaskStates     `json:"tasks,omitempty"`
	Implementation *FeedImplementation `json:"implementation,omitempty"`
	Review         *FeedReview         `json:"review,omitempty"`
	Correction     *FeedCorrection     `json:"correction,omitempty"`
	Reassessment   *FeedReassessment   `json:"reassessment,omitempty"`
	Repair         *FeedRepair         `json:"repair,omitempty"`
	Verification   *FeedVerification   `json:"verification,omitempty"`
	Decomposition  *FeedDecomposition  `json:"decomposition,omitempty"`
	Judgment       *FeedJudgment       `json:"judgment,omitempty"`
	Merge          *FeedMerge          `json:"merge,omitempty"`
	Completion     *FeedCompletion     `json:"completion,omitempty"`
}

// FeedPlan projects one investigated plan: approach, evidence and
// the task/lane/check graph.
type FeedPlan struct {
	Approach           string          `json:"approach,omitempty"`
	SourceCommit       string          `json:"source_commit,omitempty"`
	Generation         int             `json:"generation,omitempty"`
	Findings           []string        `json:"findings,omitempty"`
	FindingsTotal      int             `json:"findings_total,omitempty"`
	Unresolved         []string        `json:"unresolved,omitempty"`
	Rejected           []FeedRejected  `json:"rejected,omitempty"`
	Inspected          []FeedInspected `json:"inspected,omitempty"`
	InspectedTotal     int             `json:"inspected_total,omitempty"`
	AcceptanceCoverage []string        `json:"acceptance_coverage,omitempty"`
	Tasks              []FeedPlanTask  `json:"tasks,omitempty"`
	TasksTotal         int             `json:"tasks_total,omitempty"`
	Lanes              []FeedPlanLane  `json:"lanes,omitempty"`
	Checks             []FeedPlanCheck `json:"checks,omitempty"`
	ChecksTotal        int             `json:"checks_total,omitempty"`
}

type FeedPlanTask struct {
	ID        string   `json:"id"`
	Objective string   `json:"objective,omitempty"`
	Lane      string   `json:"lane,omitempty"`
	DependsOn []string `json:"depends_on,omitempty"`
	Outputs   []string `json:"outputs,omitempty"`
	Checks    []string `json:"checks,omitempty"`
}

type FeedPlanLane struct {
	ID    string   `json:"id"`
	Tasks []string `json:"tasks,omitempty"`
}

type FeedPlanCheck struct {
	ID       string   `json:"id"`
	Argv     []string `json:"argv,omitempty"`
	Scope    string   `json:"scope,omitempty"`
	Required bool     `json:"required,omitempty"`
}

type FeedInspected struct {
	Path     string `json:"path"`
	Commit   string `json:"commit,omitempty"`
	Evidence string `json:"evidence,omitempty"`
}

type FeedRejected struct {
	Approach string `json:"approach,omitempty"`
	Reason   string `json:"reason,omitempty"`
}

// FeedTaskStates projects the observed per-task states behind one
// implementation or correction record (checkpoint lanes).
type FeedTaskStates struct {
	Tasks []FeedTaskState `json:"tasks,omitempty"`
	Total int             `json:"total,omitempty"`
}

type FeedTaskState struct {
	ID    string `json:"id"`
	State string `json:"state,omitempty"`
}

// FeedImplementation projects one implementation receipt.
type FeedImplementation struct {
	Candidate      string   `json:"candidate,omitempty"`
	DiagnosticRefs []string `json:"diagnostic_refs,omitempty"`
}

// FeedFinding projects one canonical review finding.
type FeedFinding struct {
	Key             string               `json:"key"`
	Severity        string               `json:"severity,omitempty"`
	Defect          string               `json:"defect,omitempty"`
	Evidence        string               `json:"evidence,omitempty"`
	RequiredOutcome string               `json:"required_outcome,omitempty"`
	FixKind         string               `json:"fix_kind,omitempty"`
	Actionable      bool                 `json:"actionable,omitempty"`
	Location        *FeedFindingLocation `json:"location,omitempty"`
}

type FeedFindingLocation struct {
	Path string `json:"path"`
	Side string `json:"side,omitempty"`
	Line int64  `json:"line,omitempty"`
}

type FeedAssessment struct {
	Key      string `json:"key"`
	Status   string `json:"status,omitempty"`
	Evidence string `json:"evidence,omitempty"`
}

// FeedReview projects one review result: the native verdict report
// and/or the typed cycle verdict recorded for the phase.
type FeedReview struct {
	Verdict          string           `json:"verdict,omitempty"`
	Summary          string           `json:"summary,omitempty"`
	CycleID          string           `json:"cycle_id,omitempty"`
	Findings         []string         `json:"findings,omitempty"`
	FindingsTotal    int              `json:"findings_total,omitempty"`
	NewFindings      []FeedFinding    `json:"new_findings,omitempty"`
	NewFindingsTotal int              `json:"new_findings_total,omitempty"`
	Assessments      []FeedAssessment `json:"assessments,omitempty"`
	AssessmentsTotal int              `json:"assessments_total,omitempty"`
	Clear            *FeedClearReview `json:"clear,omitempty"`
}

type FeedClearReview struct {
	Head             string           `json:"head,omitempty"`
	Base             string           `json:"base,omitempty"`
	CoverageDigest   string           `json:"coverage_digest,omitempty"`
	Assessments      []FeedAssessment `json:"assessments,omitempty"`
	AssessmentsTotal int              `json:"assessments_total,omitempty"`
}

// FeedCorrection projects one correction report: the finding set it
// answered (before) with one disposition and its recorded fix
// evidence per finding (after).
type FeedCorrection struct {
	NewCandidate      string            `json:"new_candidate,omitempty"`
	SetDigest         string            `json:"set_digest,omitempty"`
	Findings          []FeedFinding     `json:"findings,omitempty"`
	FindingsTotal     int               `json:"findings_total,omitempty"`
	Dispositions      []FeedDisposition `json:"dispositions,omitempty"`
	DispositionsTotal int               `json:"dispositions_total,omitempty"`
}

type FeedDisposition struct {
	Key          string   `json:"key"`
	Disposition  string   `json:"disposition,omitempty"`
	ChangedPaths []string `json:"changed_paths,omitempty"`
	Commits      []string `json:"commits,omitempty"`
}

type FeedIntentTask struct {
	Key       string   `json:"key"`
	Objective string   `json:"objective,omitempty"`
	DependsOn []string `json:"depends_on,omitempty"`
	Outcomes  []string `json:"outcomes,omitempty"`
}

type FeedIntentChange struct {
	IntentID   string           `json:"intent_id,omitempty"`
	Reason     string           `json:"reason,omitempty"`
	Tasks      []FeedIntentTask `json:"tasks,omitempty"`
	TasksTotal int              `json:"tasks_total,omitempty"`
}

type FeedAttention struct {
	Issue  int64  `json:"issue,omitempty"`
	Reason string `json:"reason,omitempty"`
}

type FeedCoverage struct {
	IntentID               string   `json:"intent_id,omitempty"`
	OutcomeID              string   `json:"outcome_id,omitempty"`
	TaskKeys               []string `json:"task_keys,omitempty"`
	DeliveredAcceptanceIDs []string `json:"delivered_acceptance_ids,omitempty"`
}

// FeedReassessment projects one reassessment report's deltas
// against the retained issue set.
type FeedReassessment struct {
	Summary        string             `json:"summary,omitempty"`
	RetainedIssues []int64            `json:"retained_issues,omitempty"`
	Changes        []FeedIntentChange `json:"changes,omitempty"`
	ChangesTotal   int                `json:"changes_total,omitempty"`
	Attention      []FeedAttention    `json:"attention,omitempty"`
	Coverage       []FeedCoverage     `json:"coverage,omitempty"`
	CoverageTotal  int                `json:"coverage_total,omitempty"`
	InspectedTotal int                `json:"inspected_total,omitempty"`
}

type FeedCheckFailure struct {
	Check  string `json:"check,omitempty"`
	Detail string `json:"detail,omitempty"`
}

// FeedRepair projects one stall or delivery repair report
// with its recorded causes and the proposed graph deltas.
type FeedRepair struct {
	Outcome        string             `json:"outcome,omitempty"`
	Causes         []FeedCheckFailure `json:"causes,omitempty"`
	RetainedIssues []int64            `json:"retained_issues,omitempty"`
	Changes        []FeedIntentChange `json:"changes,omitempty"`
	ChangesTotal   int                `json:"changes_total,omitempty"`
	Attention      []FeedAttention    `json:"attention,omitempty"`
	Coverage       []FeedCoverage     `json:"coverage,omitempty"`
	CoverageTotal  int                `json:"coverage_total,omitempty"`
	InspectedTotal int                `json:"inspected_total,omitempty"`
}

// FeedVerification projects one application verification report:
// outcome coverage and trusted check receipts.
type FeedVerification struct {
	Outcome        string             `json:"outcome,omitempty"`
	DeliveryID     string             `json:"delivery_id,omitempty"`
	Checks         []FeedCheckReceipt `json:"checks,omitempty"`
	ChecksTotal    int                `json:"checks_total,omitempty"`
	Failures       []FeedCheckFailure `json:"failures,omitempty"`
	Coverage       []FeedCoverage     `json:"coverage,omitempty"`
	CoverageTotal  int                `json:"coverage_total,omitempty"`
	ArtifactsTotal int                `json:"artifacts_total,omitempty"`
	EvidenceTotal  int                `json:"evidence_total,omitempty"`
}

type FeedCheckReceipt struct {
	ID             string `json:"id"`
	Outcome        string `json:"outcome,omitempty"`
	ArtifactDigest string `json:"artifact_digest,omitempty"`
}

// FeedDecomposition projects one product decomposition answer:
// its candidate issue graphs or its recorded attention.
type FeedDecomposition struct {
	Operation       string                `json:"operation,omitempty"`
	Attention       string                `json:"attention,omitempty"`
	Candidates      []FeedDecompCandidate `json:"candidates,omitempty"`
	CandidatesTotal int                   `json:"candidates_total,omitempty"`
}

type FeedDecompCandidate struct {
	Key        string           `json:"key,omitempty"`
	Rationale  string           `json:"rationale,omitempty"`
	Tasks      []FeedIntentTask `json:"tasks,omitempty"`
	TasksTotal int              `json:"tasks_total,omitempty"`
}

// FeedJudgment projects one recorded Jev judgment receipt:
// the supplied candidates, the recorded selection/distribution and
// the effective outcome. No rationale is carried: none is recorded.
type FeedJudgment struct {
	Kind            string                  `json:"kind"`
	Selected        string                  `json:"selected,omitempty"`
	Outcome         string                  `json:"outcome,omitempty"`
	Effective       string                  `json:"effective,omitempty"`
	Model           string                  `json:"model,omitempty"`
	Probability     *float64                `json:"probability,omitempty"`
	Confidence      *float64                `json:"confidence,omitempty"`
	Probabilities   map[string]float64      `json:"probabilities,omitempty"`
	Candidates      []FeedJudgmentCandidate `json:"candidates,omitempty"`
	CandidatesTotal int                     `json:"candidates_total,omitempty"`
}

type FeedJudgmentCandidate struct {
	ID         string `json:"id"`
	Repository int64  `json:"repository,omitempty,string"`
	Issue      int64  `json:"issue,omitempty"`
	Objective  string `json:"objective,omitempty"`
}

// FeedMerge projects one confirmed merge record.
type FeedMerge struct {
	Stage              string `json:"stage,omitempty"`
	Publication        string `json:"publication,omitempty"`
	MergedCommit       string `json:"merged_commit,omitempty"`
	MergedUnix         int64  `json:"merged_unix,omitempty"`
	OriginalClosedUnix int64  `json:"original_closed_unix,omitempty"`
}

// FeedCompletion projects one verified completion event.
type FeedCompletion struct {
	Event        string `json:"event,omitempty"`
	Publication  string `json:"publication,omitempty"`
	Occurrence   string `json:"occurrence,omitempty"`
	MergedCommit string `json:"merged_commit,omitempty"`
	Generation   int64  `json:"generation,omitempty"`
}

// Validate admits one feed entry with a known kind and positive scope.
func (e FeedEntry) Validate() error {
	if e.Kind != FeedKindRecord && e.Kind != FeedKindPost {
		return errors.New("feed entry needs its known kind")
	}
	if e.ID == "" {
		return errors.New("feed entry needs its stable identity")
	}
	if e.Repository <= 0 || e.Seq <= 0 {
		return errors.New("feed entry needs its positive scope and order")
	}
	return nil
}

// FeedQuery scopes one combined feed read: an explicit repository
// allow-list (the subscription filter applied inside permitted
// activity), an optional kind filter, a bounded window and the resume
// cursor. Empty cursor reads from the latest; empty kinds read all.
type FeedQuery struct {
	Repositories []int64  `json:"repositories"`
	Kinds        []string `json:"kinds,omitempty"`
	Limit        int      `json:"limit"`
	Cursor       string   `json:"cursor,omitempty"`
	View         string   `json:"view,omitempty"`
	Search       string   `json:"search,omitempty"`
	Role         string   `json:"role,omitempty"`
	Current      bool     `json:"current,omitempty"`
}

// Validate admits one feed query with a bounded window and a
// well-formed cursor.
func (q FeedQuery) Validate() error {
	switch q.View {
	case "", "all", "attention", "decisions", "knowledge", "evidence":
	default:
		return errors.New("unknown feed view")
	}
	if len(q.Search) > 256 || len(q.Role) > 64 {
		return errors.New("feed search exceeds its bound")
	}
	if len(q.Repositories) == 0 {
		return errors.New("feed query needs its repository scope")
	}
	for _, repository := range q.Repositories {
		if repository <= 0 {
			return errors.New("feed query needs its positive repository scope")
		}
	}
	for _, kind := range q.Kinds {
		if kind != FeedKindRecord && kind != FeedKindPost {
			return errors.New("feed query needs its known kinds")
		}
	}
	if q.Limit <= 0 || q.Limit > FeedPageLimit {
		return errors.New("feed query window is outside its bound")
	}
	if _, err := ParseFeedCursor(q.Cursor); err != nil {
		return err
	}
	return nil
}

// FormatFeedCursor renders the resume cursor behind one entry seq:
// the next page reads strictly older entries.
func FormatFeedCursor(seq int64) string {
	return feedCursorVersion + ":" + strconv.FormatInt(seq, 10)
}

// ParseFeedCursor admits one feed cursor, returning the exclusive
// order bound: entries read with seq below it. Empty admits the
// latest-first read.
func ParseFeedCursor(cursor string) (int64, error) {
	if cursor == "" {
		return 0, nil
	}
	head, tail, ok := strings.Cut(cursor, ":")
	if !ok || head != feedCursorVersion {
		return 0, errors.New("feed cursor needs its versioned order bound")
	}
	seq, err := strconv.ParseInt(tail, 10, 64)
	if err != nil || seq <= 0 {
		return 0, errors.New("feed cursor needs its positive order bound")
	}
	return seq, nil
}

// WindowFeedEntries cuts one newest-first read of up to limit+1
// entries to its page: at most limit members, the resume cursor
// behind the page tail, and whether the bound cut the read. A cut
// page stays incomplete; the caller renders the resume cursor instead
// of the window as the whole feed.
func WindowFeedEntries(entries []FeedEntry, limit int) (page []FeedEntry, next string, truncated bool) {
	if len(entries) > limit {
		page = entries[:limit]
		truncated = true
	} else {
		page = entries
	}
	if len(page) > 0 {
		next = FormatFeedCursor(page[len(page)-1].Seq)
	}
	return page, next, truncated
}
