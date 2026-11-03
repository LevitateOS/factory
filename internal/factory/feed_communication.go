package factory

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/levitateos/sodaos/internal/project"
)

// Feed communication contract (R32.2/R32.14, R42.2): durable comment
// identity/revision/scope plus the intake/judgment/draft/native-effect
// pipeline behind one comment POST. The comment body is a human or
// worker submission; every derived fact (route, draft, issue link) is
// server-recorded with the exact source revision bound through each
// stage, so a retry recovers the original instead of duplicating it.
// Reads never spend or dispatch: funding applies to judgment calls
// only, and native effects apply through the issue/graph creator only.

// SourcePost routing outcomes from the funded JEV Choice assessment.
const (
	// PostRouteNewIssue invokes Issue Maker for capture and refinement.
	PostRouteNewIssue = "new_issue"
	// PostRouteExistingWork links the comment to already tracked work.
	PostRouteExistingWork = "existing_work"
	// PostRouteDiscussion keeps the comment as discussion only: no
	// issue is ever created from it.
	PostRouteDiscussion = "discussion"
	// PostRouteClarify marks the comment as waiting on its author's
	// clarification before any further routing.
	PostRouteClarify = "clarify"
)

// SourcePost pipeline states. Intake, judgment, draft and native effect
// advance separately so call admission, funding, comment save,
// judgment, draft and native commit stay distinguishable.
const (
	PostIntakeRecorded  = "recorded"
	PostJudgmentPending = "pending"
	PostJudgmentApplied = "applied"
	PostJudgmentFailed  = "failed"
	PostDraftPending    = "pending"
	PostDraftApplied    = "applied"
	PostDraftInvalid    = "invalid"
	PostEffectPending   = "pending"
	PostEffectApplied   = "applied"
	PostEffectVerified  = "verified"
	PostEffectFailed    = "failed"
	PostEffectBlocked   = "blocked"
)

// PostAttemptLimit bounds funded judgment/draft/native retries behind
// one comment revision: a lost effect reply reconciles by re-reading
// native state, never by blind repeated spending.
const PostAttemptLimit = 6

// PostBodyLimit bounds one comment submission.
const PostBodyLimit = 4096

var postID = regexp.MustCompile(`^[a-f0-9]{32}$`)

var postKey = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// SourcePost source kinds: the feed entry a comment was submitted
// against. The server resolves the named entry inside the caller's
// visible repository at submission time and persists the resolved
// snapshot; caller-supplied source facts never enter judgment or
// authoring directly.
const (
	// PostSourceRecord names one canonical workflow record entry
	// (a factory assignment rendered into the feed).
	PostSourceRecord = "record"
	// PostSourcePost names one stored communication post entry.
	PostSourcePost = "post"
)

// PostRequest is one comment POST body: the repository scope, the
// submission text, the optional bound issue and reply parent, the
// optional source record/post the comment answers, and the
// caller-chosen idempotency key. The author is the host-admitted
// caller, never a body field. Repository is a canonical Factory key;
// JSON carries its decimal string so browser submissions remain exact.
type PostRequest struct {
	Repository int64  `json:"repository,string"`
	Body       string `json:"body"`
	Issue      int64  `json:"issue,omitempty"`
	ParentID   string `json:"parent_id,omitempty"`
	// The controller resolves provenance from ParentID; it is never supplied by a caller.
	SourceKind     string `json:"-"`
	SourceID       string `json:"-"`
	IdempotencyKey string `json:"idempotency_key"`
}

// Validate admits one comment request with its scope, bounded body and
// idempotency key.
func (r PostRequest) Validate() error {
	if r.Repository <= 0 {
		return errors.New("comment needs its repository scope")
	}
	body := strings.TrimSpace(r.Body)
	if body == "" || utf8.RuneCountInString(r.Body) > PostBodyLimit {
		return errors.New("comment needs its bounded body")
	}
	if r.Issue < 0 {
		return errors.New("comment needs its non-negative issue scope")
	}
	if r.ParentID != "" && !postID.MatchString(r.ParentID) {
		return errors.New("comment needs its known reply parent")
	}
	if (r.SourceKind == "") != (r.SourceID == "") {
		return errors.New("comment source needs its kind and identity together")
	}
	if r.SourceKind != "" && r.SourceKind != PostSourceRecord && r.SourceKind != PostSourcePost {
		return errors.New("comment needs its known source kind")
	}
	if r.SourceID != "" && !postID.MatchString(r.SourceID) {
		return errors.New("comment needs its known source identity")
	}
	if !postKey.MatchString(r.IdempotencyKey) {
		return errors.New("comment needs its idempotency key")
	}
	return nil
}

// PostSourceSummaryLimit bounds one persisted source summary: the
// same preview bound the feed serving layer applies to records.
const PostSourceSummaryLimit = 512

// PostSource is the server-resolved snapshot of the feed entry one
// comment was submitted against: its kind and stable identity, the
// bound native issue and pull request where recorded, the produced
// candidate ref, its recorded status and a bounded summary preview.
// It is resolved inside the caller's visible repository at
// submission time and persisted with the comment, so judgment and
// authoring consume authorized recorded evidence instead of
// caller-asserted facts or a later re-read of moved work.
type PostSource struct {
	Kind    string             `json:"kind"`
	ID      string             `json:"id"`
	Issue   int64              `json:"issue,omitempty"`
	PR      int64              `json:"pr,omitempty"`
	Ref     string             `json:"ref,omitempty"`
	Status  string             `json:"status,omitempty"`
	Summary string             `json:"summary,omitempty"`
	Product *PostProductSource `json:"product,omitempty"`
}

// PostProductSource is supplied only by the canonical feed source resolver.
// It names the accepted product discussed, never a caller-selected latest head.
type PostProductSource struct {
	Intent           string `json:"intent"`
	AcceptedRevision string `json:"accepted_revision"`
}

// Validate admits one resolved comment source snapshot.
func (s PostSource) Validate() error {
	if s.Kind != PostSourceRecord && s.Kind != PostSourcePost {
		return errors.New("comment source needs its known kind")
	}
	if !postID.MatchString(s.ID) {
		return errors.New("comment source needs its stable identity")
	}
	if s.Issue < 0 || s.PR < 0 {
		return errors.New("comment source needs its non-negative issue and pull request")
	}
	if len(s.Ref) > 256 || len(s.Status) > 128 || len([]rune(s.Summary)) > PostSourceSummaryLimit {
		return errors.New("comment source evidence outside its bound")
	}
	if s.Product != nil && (!project.ValidDecisionID(s.Product.Intent) || !project.ValidDecisionID(s.Product.AcceptedRevision)) {
		return errors.New("comment product source needs its exact accepted revision")
	}
	return nil
}

// SourcePost work candidate origins, strongest binding first.
const (
	PostCandidateBound  = "bound"
	PostCandidateSource = "source"
	PostCandidateParent = "parent"
	PostCandidateRecent = "recent"
)

// PostWorkCandidate is one already-tracked issue the routing
// judgment may link a comment to: the native issue identity plus a
// bounded recorded summary and status, and how the candidate reached
// the judgment (bound issue scope, source snapshot, reply parent or
// recent repository work).
type PostWorkCandidate struct {
	Title        string `json:"title,omitempty"`
	Objective    string `json:"objective,omitempty"`
	WorkerStatus string `json:"execution_status,omitempty"`
	Issue        int64  `json:"issue"`
	Summary      string `json:"summary,omitempty"`
	Status       string `json:"status,omitempty"`
	Origin       string `json:"origin"`
}

// Validate admits one existing-work candidate.
func (c PostWorkCandidate) Validate() error {
	if c.Issue <= 0 {
		return errors.New("comment work candidate needs its issue")
	}
	switch c.Origin {
	case PostCandidateBound, PostCandidateSource, PostCandidateParent, PostCandidateRecent:
	default:
		return errors.New("comment work candidate needs its known origin")
	}
	if len([]rune(c.Summary)) > PostSourceSummaryLimit || len(c.Status) > 128 || len([]rune(c.Objective)) > 4000 || len(c.WorkerStatus) > 128 {
		return errors.New("comment work candidate evidence outside its bound")
	}
	return nil
}

// PostRecord is one durable communication record: stable identity,
// per-edit revision, scope and authorship. Edits add revisions; the
// identity survives. Deleted or revoked comments keep their tombstone
// so bound pipeline rows still resolve.
type PostRecord struct {
	ID         string `json:"id"`
	Revision   int64  `json:"revision"`
	Repository int64  `json:"repository"`
	Author     string `json:"author"`
	Issue      int64  `json:"issue,omitempty"`
	ParentID   string `json:"parent_id,omitempty"`
	Edited     bool   `json:"edited"`
	Deleted    bool   `json:"deleted"`
	Revoked    bool   `json:"revoked"`
}

// Validate admits one stored comment record.
func (r PostRecord) Validate() error {
	if !postID.MatchString(r.ID) {
		return errors.New("comment record needs its stable identity")
	}
	if r.Revision < 0 {
		return errors.New("comment record needs its non-negative revision")
	}
	if r.Repository <= 0 {
		return errors.New("comment record needs its repository scope")
	}
	if r.Author == "" {
		return errors.New("comment record needs its author")
	}
	if r.Issue < 0 {
		return errors.New("comment record needs its non-negative issue scope")
	}
	if r.ParentID != "" && !postID.MatchString(r.ParentID) {
		return errors.New("comment record needs its known reply parent")
	}
	return nil
}

// PostIntake is the durable per-comment request row: the bound
// source revision plus the separate intake/judgment/draft/effect
// states, the assessed route, the linked native issue and the bounded
// attempt count shared by judgment, draft and native retries.
// PostRouting is JEV's recorded routing Choice for one comment: every
// option it weighed in plain words with its probability, the selection
// and how sure it was. The feed draws it as JEV's own reply.
type PostRouting struct {
	Selected   string              `json:"selected"`
	Confidence float64             `json:"confidence"`
	Options    []PostRoutingOption `json:"options"`
}

// PostRoutingOption is one weighed outcome of a routing Choice.
type PostRoutingOption struct {
	Key         string  `json:"key"`
	Label       string  `json:"label"`
	Probability float64 `json:"probability"`
}

type PostIntake struct {
	SourcePost string `json:"source_post"`
	Revision   int64  `json:"revision"`
	Intake     string `json:"intake"`
	Judgment   string `json:"judgment"`
	Route      string `json:"route,omitempty"`
	Draft      string `json:"draft"`
	Effect     string `json:"effect"`
	Issue      int64  `json:"issue"`
	Attempts   int    `json:"attempts"`
}

// PostAuthoring distinguishes a pending draft's actual planning state.
// CheckedUnix records a service check, not an agent activity heartbeat.
type PostAuthoring struct {
	State       string `json:"state"`
	Reason      string `json:"reason,omitempty"`
	Detail      string `json:"detail,omitempty"`
	Intent      string `json:"intent,omitempty"`
	Run         string `json:"run,omitempty"`
	StartedUnix int64  `json:"started_unix,omitempty"`
	SinceUnix   int64  `json:"since_unix,omitempty"`
	CheckedUnix int64  `json:"checked_unix,omitempty"`
}

const (
	PostAuthoringQueued   = "queued"
	PostAuthoringWaiting  = "waiting"
	PostAuthoringRunning  = "running"
	PostAuthoringSettling = "settling"
	PostAuthoringFailed   = "failed"
	PostAuthoringUnknown  = "unknown"
)

func (p PostAuthoring) Validate() error {
	switch p.State {
	case PostAuthoringQueued, PostAuthoringWaiting, PostAuthoringRunning, PostAuthoringSettling, PostAuthoringFailed, PostAuthoringUnknown:
		return nil
	default:
		return errors.New("unknown comment authoring state")
	}
}

// PostDecisionID binds authoring records to the original comment revision.
func PostDecisionID(kind, comment string, revision int64) string {
	sum := sha256.Sum256([]byte("soda-comment-intent-v1:" + kind + ":" + comment + ":" + strconv.FormatInt(revision, 10)))
	return "d" + hex.EncodeToString(sum[:])[:24]
}

// AdmitPostJudgment reports whether one funded judgment call may run
// for the pipeline: intake recorded, judgment still pending, attempts
// remaining. Reads and re-serves never pass this gate.
func (p PostIntake) AdmitPostJudgment() bool {
	return p.Intake == PostIntakeRecorded && p.Judgment == PostJudgmentPending && p.Attempts < PostAttemptLimit
}

// AdmitPostDraft reports whether Product Lead LLM authoring may run:
// the new_issue route judged in, attempts remaining, no applied draft.
func (p PostIntake) AdmitPostDraft() bool {
	return p.Judgment == PostJudgmentApplied && p.Route == PostRouteNewIssue &&
		p.Draft == PostDraftPending && p.Attempts < PostAttemptLimit
}

func (p PostIntake) RetryAllowed() bool {
	if p.Effect == PostEffectApplied || p.Effect == PostEffectVerified {
		return false
	}
	return p.Attempts >= PostAttemptLimit || p.Judgment == PostJudgmentFailed || p.Draft == PostDraftInvalid || p.Effect == PostEffectFailed || p.Route == PostRouteClarify || p.Route == PostRouteExistingWork
}

// AdmitPostEffect reports whether the native issue/graph effect may
// run: draft applied, effect not yet settled, attempts remaining.
func (p PostIntake) AdmitPostEffect() bool {
	if p.Judgment != PostJudgmentApplied || p.Route != PostRouteNewIssue || p.Draft != PostDraftApplied {
		return false
	}
	return (p.Effect == PostEffectPending || p.Effect == PostEffectFailed) && p.Attempts < PostAttemptLimit
}

// PostEffectSettled reports whether the native outcome is final:
// verified, applied, or blocked (deleted/revoked source, lost access).
// Only an unsettled effect may retry or reconcile.
func (p PostIntake) PostEffectSettled() bool {
	return p.Effect == PostEffectVerified || p.Effect == PostEffectApplied || p.Effect == PostEffectBlocked
}

// IssueDraft is one source capture or authored issue proposal bound to
// its exact comment source revision: title, body, objective and
// acceptance plus the Soda work metadata the native creation validates.
type IssueDraft struct {
	SourcePost string `json:"source_post"`
	Revision   int64  `json:"revision"`
	Title      string `json:"title"`
	Body       string `json:"body"`
	Objective  string `json:"objective"`
	Acceptance string `json:"acceptance"`
	Metadata   string `json:"metadata"`
}

// Validate admits one issue draft with its source binding and complete
// authoring: no field may be blank and malformed drafts refuse before
// any native effect.
func (d IssueDraft) Validate() error {
	if !postID.MatchString(d.SourcePost) {
		return errors.New("issue draft needs its comment source")
	}
	if d.Revision < 0 {
		return errors.New("issue draft needs its non-negative source revision")
	}
	if strings.TrimSpace(d.Title) == "" || len(d.Title) > 256 {
		return errors.New("issue draft needs its bounded title")
	}
	if strings.TrimSpace(d.Body) == "" || len(d.Body) > 16384 {
		return errors.New("issue draft needs its bounded body")
	}
	if strings.TrimSpace(d.Objective) == "" || len(d.Objective) > 2048 {
		return errors.New("issue draft needs its bounded objective")
	}
	if strings.TrimSpace(d.Acceptance) == "" || len(d.Acceptance) > 4096 {
		return errors.New("issue draft needs its bounded acceptance")
	}
	if strings.TrimSpace(d.Metadata) == "" || len(d.Metadata) > 2048 {
		return errors.New("issue draft needs its work metadata")
	}
	return nil
}

// PostResponse is one comment POST answer: the durable identity and
// revision plus the separate pipeline states and the issue link once
// the native effect verifies. Retry commands return FeedPostRetryReceipt;
// this projection represents current pipeline facts. Submission answers as
// soon as the comment and its intake are durable; judgment, draft and
// effect continue service-side and are re-read through the comment
// GET. ExistingIssue names the already-tracked issue an
// existing_work judgment selected (the same identity Issue carries
// on that route); Source echoes the persisted resolved source
// snapshot when the submission named one.
type PostResponse struct {
	Attempts      int         `json:"attempts"`
	AttemptLimit  int         `json:"attempt_limit"`
	SourcePost    string      `json:"post_id"`
	Revision      int64       `json:"revision"`
	Intake        string      `json:"intake"`
	Judgment      string      `json:"judgment"`
	Route         string      `json:"route,omitempty"`
	Draft         string      `json:"draft"`
	Effect        string      `json:"effect"`
	Issue         int64       `json:"issue,omitempty"`
	ExistingIssue int64       `json:"existing_issue,omitempty"`
	Source        *PostSource `json:"source,omitempty"`
}
