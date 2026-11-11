package factory

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"path"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/levitateos/factory/pkg/collaboration"
)

// ReviewWork is one immutable review authorization: the summary body plus
// the located findings submitted as native inline comments. Native PR and
// issue identities come from the recorded creation receipt, not a PR search.
// Repository, ActorID and PRAuthorID are canonical Factory keys. The reviewer
// owns findings; this work grants no ref publication.
type ReviewWork struct {
	Comments       []FindingComment `json:"comments,omitempty"`
	OperationID    string           `json:"operation_id"`
	AuthRevision   string           `json:"authorization_revision"`
	Repository     int64            `json:"repository"`
	HeadRepository int64            `json:"head_repository,omitempty"`
	ActorID        int64            `json:"actor_id"`
	PRNumber       int64            `json:"pr_number"`
	PRID           int64            `json:"pr_id"`
	IssueID        int64            `json:"issue_id"`
	PRAuthorID     int64            `json:"pr_author_id"`
	HeadRef        string           `json:"head_ref"`
	BaseRef        string           `json:"base_ref"`
	HeadOID        string           `json:"head_oid"`
	BaseOID        string           `json:"base_oid"`
	NativeRev      int64            `json:"native_revision"`
	NotAfter       int64            `json:"not_after"`
	Event          string           `json:"event"`
	// AgentVerdict separates inspection from provider disposition. It is
	// explicit whenever a personal account publishes a comment review.
	AgentVerdict string `json:"agent_verdict,omitempty"`
	// VoteBinding freezes the explicitly designated automation identity at
	// admission. Missing historical bindings never authorize a new native vote.
	VoteBinding *ActorBindingRef `json:"vote_binding,omitempty"`
	Body        string           `json:"body"`
}

// ReviewOperation retains one review run's exact immutable native work and
// the latest attributable operation outcome inside its publication record.
// The work itself is the native tuple; this wrapper owns only its run binding
// and reconciliation state.
type ReviewOperation struct {
	RetryCommand string           `json:"retry_command,omitempty"`
	RetryOf      string           `json:"retry_of,omitempty"`
	RunID        string           `json:"run_id"`
	Work         ReviewWork       `json:"work"`
	Outcome      OperationOutcome `json:"outcome"`
}

// ValidateForPublication checks the durable operation against the exact PR
// identity and candidate history that admitted it. Historical actor, head and
// deadline values remain immutable when current policy or publication state
// later changes.
func (op ReviewOperation) validateReviewWork(p Publication) error {
	if !ValidID(op.RunID) {
		return errors.New("invalid review run operation identity")
	}
	if op.RetryOf == "" {
		if op.RetryCommand != "" || op.Work.OperationID != "review-"+op.RunID {
			return errors.New("invalid original review operation identity")
		}
	} else if !ValidID(op.RetryCommand) || !ValidPublicationOperationID(op.RetryOf) || op.Work.OperationID != "review-retry-"+op.RetryCommand {
		return errors.New("invalid fresh review retry identity")
	}
	if err := op.Work.Validate(); err != nil {
		return err
	}
	if op.Work.Repository != p.Repository || op.Work.PRNumber != p.PRNumber || op.Work.PRID != p.PRID ||
		op.Work.HeadRepositoryID() != p.PullTarget().HeadRepository ||
		op.Work.IssueID != p.PullTarget().IssueID || op.Work.PRAuthorID != p.PullTarget().AuthorID ||
		op.Work.HeadRef != p.PullTarget().HeadRef || op.Work.BaseRef != p.PullTarget().BaseRef || op.Work.BaseOID != p.PullTarget().BaseOID ||
		op.Work.AuthRevision != ReviewAuthRevision(p.AssignmentID, op.RunID) {
		return errors.New("review work differs from its linked publication")
	}
	knownHead := op.Work.HeadOID == p.InitialCandidate()
	for _, correction := range p.Corrections {
		if correction.Effect == OpEffectCommitted && correction.Work != nil && op.Work.HeadOID == correction.Work.Candidate {
			knownHead = true
		}
	}
	if !knownHead {
		return errors.New("review head is outside the publication candidate history")
	}
	return nil
}

func (op ReviewOperation) validateReviewOutcomeIdentity() error {
	if op.Outcome.NotObserved {
		return errors.New("review outcome cannot persist an absent lookup")
	}
	if op.Outcome.OperationID == "" {
		if op.Outcome.InstallationID != "" || op.Outcome.Kind != "" || op.Outcome.ActorID != 0 || op.Outcome.RepositoryID != 0 ||
			op.Outcome.Effect != "" || op.Outcome.Cancellation != "" || op.Outcome.Completion != "" || op.Outcome.Reason != "" || len(op.Outcome.Receipt) != 0 {
			return errors.New("review outcome is incomplete")
		}
	} else if op.Outcome.OperationID != op.Work.OperationID {
		return errors.New("review outcome identity differs")
	}
	if op.Outcome.InstallationID != "" && len(op.Outcome.InstallationID) > 256 {
		return errors.New("review installation identity exceeds bounds")
	}
	return nil
}

func (op ReviewOperation) validateReviewOutcomeEnums() error {
	if op.Outcome.Effect != "" && !ValidOpEffect(op.Outcome.Effect) {
		return errors.New("invalid review operation effect")
	}
	if op.Outcome.Cancellation != "" && !ValidOpCancellation(op.Outcome.Cancellation) {
		return errors.New("invalid review operation cancellation")
	}
	if op.Outcome.Completion != "" && !ValidOpCompletion(op.Outcome.Completion) {
		return errors.New("invalid review operation completion")
	}
	if len(op.Outcome.Reason) > 64 || len(op.Outcome.Receipt) > MaxReviewReceiptBytes {
		return errors.New("review operation outcome exceeds bounds")
	}
	if op.Outcome.Effect == OpEffectCommitted && (op.Outcome.InstallationID == "" || len(op.Outcome.Receipt) == 0 || op.Outcome.Completion == "") {
		return errors.New("committed review lacks its receipt or completion")
	}
	return nil
}

func (op ReviewOperation) validateReviewOutcomeScope() error {
	tombstone := op.Outcome.OperationID != "" && op.Outcome.Kind == "" && op.Outcome.ActorID == 0 && op.Outcome.RepositoryID == 0 &&
		op.Outcome.Effect == OpEffectNotCommitted && op.Outcome.Cancellation == OpCancelCancelled
	if op.Outcome.OperationID != "" && !tombstone && (op.Outcome.InstallationID == "" || op.Outcome.Kind != OpReviewSubmit || op.Outcome.ActorID != op.Work.ActorID || op.Outcome.RepositoryID != op.Work.Repository) {
		return errors.New("review operation outcome scope differs")
	}
	return nil
}

func (op ReviewOperation) ValidateForPublication(p Publication) error {
	if err := op.validateReviewWork(p); err != nil {
		return err
	}
	if err := op.validateReviewOutcomeIdentity(); err != nil {
		return err
	}
	if err := op.validateReviewOutcomeEnums(); err != nil {
		return err
	}
	return op.validateReviewOutcomeScope()
}

// WithOutcome adopts one native lookup/cancel/submit response without
// permitting the stable review identity or a terminal result to move.
func (op ReviewOperation) WithOutcome(outcome OperationOutcome) (ReviewOperation, error) {
	if outcome.NotObserved || outcome.OperationID != op.Work.OperationID {
		return ReviewOperation{}, errors.New("review outcome identity differs")
	}
	tombstone := outcome.Kind == "" && outcome.ActorID == 0 && outcome.RepositoryID == 0 &&
		outcome.Effect == OpEffectNotCommitted && outcome.Cancellation == OpCancelCancelled
	if !tombstone && (outcome.InstallationID == "" || outcome.Kind != OpReviewSubmit ||
		outcome.ActorID != op.Work.ActorID || outcome.RepositoryID != op.Work.Repository) {
		return ReviewOperation{}, errors.New("review outcome scope differs")
	}
	prior := op.Outcome
	if prior.InstallationID != "" && prior.InstallationID != outcome.InstallationID {
		return ReviewOperation{}, errors.New("review installation identity changed")
	}
	step := collaboration.OperationStep{
		Old: prior.State(), Next: outcome.State(),
		ReceiptChanged: len(prior.Receipt) > 0 && string(prior.Receipt) != string(outcome.Receipt),
	}
	if collaboration.FollowProvider.Advance(step) != collaboration.AdvanceAdopt {
		return ReviewOperation{}, errors.New("review terminal effect or receipt changed")
	}
	op.Outcome = outcome
	return op, nil
}

// ValidateTarget checks the exact target before findings or operation identity
// exist. Reading the target creates no verdict or submission authority.
func (w ReviewWork) ValidateTarget() error {
	if w.VoteBinding != nil && (w.VoteBinding.Validate() != nil || w.VoteBinding.Kind != OpReviewSubmit || w.VoteBinding.ActorID != w.ActorID || w.Event == "COMMENT") {
		return errors.New("invalid automation vote binding")
	}
	if w.Repository <= 0 || w.HeadRepository < 0 || w.ActorID <= 0 || w.PRNumber <= 0 || w.PRID <= 0 || w.IssueID <= 0 || w.PRAuthorID <= 0 {
		return errors.New("invalid review target")
	}
	if !ValidTargetBranch(w.HeadRef) || !ValidTargetBranch(w.BaseRef) || w.HeadRef == w.BaseRef || !ValidCommit(w.HeadOID) || !ValidCommit(w.BaseOID) {
		return errors.New("invalid exact review candidate")
	}
	return nil
}

// Missing historical head-repository bindings mean the workflow repository.
// A fork must always retain its distinct canonical repository explicitly.
func (w ReviewWork) HeadRepositoryID() int64 {
	if w.HeadRepository != 0 {
		return w.HeadRepository
	}
	return w.Repository
}

// Validate checks persisted semantic inputs. Deadline freshness belongs to the
// caller at dispatch; expired historical work remains valid for reconciliation.
func (w ReviewWork) Validate() error {
	if err := w.ValidateTarget(); err != nil {
		return err
	}
	if w.ActorID == w.PRAuthorID && (w.Event != "COMMENT" || (w.AgentVerdict != "APPROVED" && w.AgentVerdict != "REQUEST_CHANGES")) {
		return errors.New("author account inspection requires an explicit agent verdict and comment review")
	}
	if !ValidPublicationOperationID(w.OperationID) || strings.TrimSpace(w.AuthRevision) == "" || len(w.AuthRevision) > 512 || w.NativeRev < 1 || w.NotAfter <= 0 || len(w.Body) > 65536 {
		return errors.New("invalid review authorization")
	}
	switch w.Disposition() {
	case "APPROVED":
	case "REQUEST_CHANGES":
		if strings.TrimSpace(w.Body) == "" {
			return errors.New("requested changes need findings")
		}
	default:
		return errors.New("invalid review disposition")
	}
	if w.Event == "COMMENT" {
		if w.AgentVerdict != "APPROVED" && w.AgentVerdict != "REQUEST_CHANGES" {
			return errors.New("agent inspection comment requires an explicit verdict")
		}
		if strings.TrimSpace(w.Body) == "" {
			return errors.New("agent inspection comment needs its report")
		}
	} else if w.Event != w.Disposition() {
		return errors.New("provider review differs from its agent verdict")
	}
	if err := validateFindingComments(w.Comments); err != nil {
		return err
	}
	return nil
}

// Disposition is the agent's inspection result, never proof of provider approval.
// Older records carry it in Event and remain readable without rewriting them.
func (w ReviewWork) Disposition() string {
	if w.AgentVerdict != "" {
		return w.AgentVerdict
	}
	return w.Event
}

// ReviewObservation reports the exact current native target after equal idle
// revision observations. PRAuthorID is canonical. Native review history is
// checked in that same bracket.
type ReviewObservation struct {
	NativeRev      int64  `json:"native_revision"`
	ObservedUnix   int64  `json:"observed_unix"`
	PRID           int64  `json:"pr_id"`
	PRNumber       int64  `json:"pr_number"`
	IssueID        int64  `json:"issue_id"`
	PRAuthorID     int64  `json:"pr_author_id"`
	HeadRepository int64  `json:"head_repository,omitempty"`
	HeadRef        string `json:"head_ref"`
	BaseRef        string `json:"base_ref"`
	HeadOID        string `json:"head_oid"`
	BaseOID        string `json:"base_oid"`
}

// ReviewAuthRevision binds one submitted review to its assignment and run:
// the opaque native authorization revision, never a credential.
func ReviewAuthRevision(assignmentID, runID string) string {
	return "soda-assignment:" + assignmentID + ":review-run:" + runID
}

// ReviewReport is one reviewer agent's genuine verdict on an exact
// candidate: approve it or request changes with concrete findings.
type ReviewReport struct {
	Verdict  string   `json:"verdict"`
	Summary  string   `json:"summary"`
	Body     string   `json:"body"`
	Findings []string `json:"findings"`
}

// Validate checks the verdict shape. Approvals carry a summary; requested
// changes carry findings in the body, never an empty report.
func (r ReviewReport) Validate() error {
	switch r.Verdict {
	case "approve":
	case "request-changes":
		if strings.TrimSpace(r.Body) == "" {
			return errors.New("requested changes need findings")
		}
	default:
		return errors.New("invalid review verdict")
	}
	if len(r.Body) > 65536 || len(r.Summary) > 4096 {
		return errors.New("review report exceeds its bound")
	}
	if len(r.Findings) > 64 {
		return errors.New("review report has too many findings")
	}
	for _, finding := range r.Findings {
		if len(finding) > 4096 {
			return errors.New("review finding is too large")
		}
	}
	// The complete encoded report enforces the same ceiling the parser
	// admits: field bounds alone cannot bound JSON escaping.
	encoded, err := json.Marshal(r)
	if err != nil || len(encoded) > MaxReviewReportBytes {
		return errors.New("review report exceeds its bound")
	}
	return nil
}

// Frozen D04 review/finding bounds (FW06C). At most MaxFindingsPerReport
// findings per report and MaxOutstandingFindings outstanding carried
// findings; one finding's combined defect/evidence/outcome prose fits
// MaxFindingProseBytes; the encoded native review payload fits
// MaxReviewPayloadBytes, enforced on the native operation by FW16 and on
// the Soda adapter by FW18 against this same ceiling; one review-specific
// native receipt fits MaxReviewReceiptBytes. The 1 MiB review-history cap
// (MaxPublicationReviewBytes) counts full encoded work plus reserved
// receipt capacity at the review-specific size. Other operation receipt
// limits stay unchanged. The N adapter admits review receipts up to the
// review-specific cap; every generic native receipt stays at 4 KiB.
const (
	MaxFindingsPerReport   = 64
	MaxOutstandingFindings = 64
	MaxFindingProseBytes   = 4096
	MaxReviewPayloadBytes  = 48 << 10
	MaxReviewReceiptBytes  = 16 << 10
)

// MaxReviewReportBytes bounds the complete fenced reviewer report block
// the parser attempts to decode. A maximum report carries a 4 KiB summary,
// a 64 KiB body and 64 findings of 4 KiB prose each; every byte may expand
// to six in JSON (encoding/json HTML escaping), so the worst valid encoding
// stays below 2 MiB. Reports at the persisted field bounds always parse;
// only oversized blocks refuse before decoding.
const MaxReviewReportBytes = 2 << 20

// MaxFindingCommentBodyBytes bounds one rendered native inline comment:
// the finding prose plus its stable key line and labels.
const MaxFindingCommentBodyBytes = MaxFindingProseBytes + 256

// Finding severities and fix kinds. Fix verification covers checks-only
// outcomes; informational findings use verification with an explicit
// no-action required outcome, never an empty field.
const (
	FindingSeverityCritical = "critical"
	FindingSeverityMajor    = "major"
	FindingSeverityMinor    = "minor"
	FindingFixCode          = "code"
	FindingFixVerification  = "verification"
)

// ValidFindingKey reports whether key is a stable 32-hex finding identity.
// Keys share the factory identity alphabet; they are preserved through
// review rounds, and new defects get new keys.
func ValidFindingKey(key string) bool { return ValidID(key) }

// FindingLocation is one finding's optional native anchor: diff path,
// old|new side and one-based actual diff line.
type FindingLocation struct {
	Path string `json:"path"`
	Side string `json:"side"`
	Line int64  `json:"line"`
}

// Validate checks the anchor shape. Non-line findings stay unlocated and
// must be represented in the review summary instead.
func (l FindingLocation) Validate() error {
	if l.Path == "" || len(l.Path) > 1024 || l.Path != strings.TrimSpace(l.Path) {
		return errors.New("invalid finding location path")
	}
	switch l.Side {
	case "old", "new":
	default:
		return errors.New("invalid finding location side")
	}
	if l.Line < 1 {
		return errors.New("invalid finding location line")
	}
	return nil
}

// Finding is one Soda keyed finding (D06): stable key, origin review run
// and native review, severity, actionability, defect, evidence, required
// outcome, fix kind and optional native location. Soda severity,
// dispositions and Jev state never enter Fountain policy; the native
// comment carries the key plus its prose.
type Finding struct {
	Location        *FindingLocation `json:"location,omitempty"`
	Key             string           `json:"key"`
	OriginRun       string           `json:"origin_run"`
	Severity        string           `json:"severity"`
	Defect          string           `json:"defect"`
	Evidence        string           `json:"evidence"`
	RequiredOutcome string           `json:"required_outcome"`
	FixKind         string           `json:"fix_kind"`
	OriginReview    int64            `json:"origin_review,omitempty"`
	Actionable      bool             `json:"actionable"`
}

// Validate checks the complete finding shape. Every field except the
// location and the not-yet-natively-recorded origin review is required;
// informational findings record their rationale as evidence with an
// explicit no-action outcome.
func (f Finding) Validate() error {
	if !ValidFindingKey(f.Key) {
		return errors.New("invalid finding key")
	}
	if !ValidID(f.OriginRun) {
		return errors.New("invalid finding origin run")
	}
	if f.OriginReview < 0 {
		return errors.New("invalid finding origin review")
	}
	switch f.Severity {
	case FindingSeverityCritical, FindingSeverityMajor, FindingSeverityMinor:
	default:
		return errors.New("invalid finding severity")
	}
	if strings.TrimSpace(f.Defect) == "" || strings.TrimSpace(f.Evidence) == "" || strings.TrimSpace(f.RequiredOutcome) == "" {
		return errors.New("finding lacks its defect, evidence or required outcome")
	}
	if len(f.Defect)+len(f.Evidence)+len(f.RequiredOutcome) > MaxFindingProseBytes {
		return errors.New("finding prose exceeds its bound")
	}
	switch f.FixKind {
	case FindingFixCode, FindingFixVerification:
	default:
		return errors.New("invalid finding fix kind")
	}
	if f.Location != nil {
		if err := f.Location.Validate(); err != nil {
			return err
		}
	}
	return nil
}

// FindingSetDigest binds one finding set to its sorted keys: the input
// digest carried by review cycles and the exact digest covered by
// correction dispositions. Key order never affects the digest; any added,
// dropped or re-keyed finding changes it.
func FindingSetDigest(keys []string) (string, error) {
	if len(keys) > MaxOutstandingFindings {
		return "", errors.New("finding set exceeds its bound")
	}
	sorted := append([]string(nil), keys...)
	for _, key := range sorted {
		if !ValidFindingKey(key) {
			return "", errors.New("finding set carries an invalid key")
		}
	}
	sort.Strings(sorted)
	for i := 1; i < len(sorted); i++ {
		if sorted[i] == sorted[i-1] {
			return "", errors.New("finding set carries a duplicate key")
		}
	}
	sum := sha256.Sum256([]byte("finding-set-v1\x00" + strings.Join(sorted, "\x00")))
	return hex.EncodeToString(sum[:]), nil
}

// FindingComment is one located finding's stable native inline comment:
// the factory finding key as the opaque native correlation key, the exact
// diff anchor and the rendered prose body. The key never carries severity,
// disposition or classifier state; those travel in the prose body, which
// the host stores in the native comment alongside the key.
type FindingComment struct {
	Key  string `json:"key"`
	Path string `json:"path"`
	Side string `json:"side"`
	Line int64  `json:"line"`
	Body string `json:"body"`
}

// Validate checks one inline comment: a stable finding key, an exact
// old|new diff anchor, a one-based line and a bounded prose body.
func (c FindingComment) Validate() error {
	if !ValidFindingKey(c.Key) {
		return errors.New("invalid finding comment key")
	}
	if c.Path == "" || len(c.Path) > 1024 || c.Path != strings.TrimSpace(c.Path) {
		return errors.New("invalid finding comment path")
	}
	switch c.Side {
	case "old", "new":
	default:
		return errors.New("invalid finding comment side")
	}
	if c.Line < 1 {
		return errors.New("invalid finding comment line")
	}
	if strings.TrimSpace(c.Body) == "" || len(c.Body) > MaxFindingCommentBodyBytes {
		return errors.New("finding comment body outside its bound")
	}
	return nil
}

// validateFindingComments checks one inline comment list: bounded count,
// individually valid comments, unique correlation keys and no duplicate
// (path, side, line) anchors in one submission. Defects sharing an anchor
// consolidate into one finding before submission.
func validateFindingComments(comments []FindingComment) error {
	if len(comments) > MaxFindingsPerReport {
		return errors.New("review carries too many finding comments")
	}
	keys := make(map[string]struct{}, len(comments))
	anchors := make(map[FindingComment]struct{}, len(comments))
	for _, comment := range comments {
		if err := comment.Validate(); err != nil {
			return err
		}
		if _, dup := keys[comment.Key]; dup {
			return errors.New("review carries a duplicate finding key")
		}
		keys[comment.Key] = struct{}{}
		anchor := FindingComment{Path: comment.Path, Side: comment.Side, Line: comment.Line}
		if _, dup := anchors[anchor]; dup {
			return errors.New("review carries a duplicate finding anchor")
		}
		anchors[anchor] = struct{}{}
	}
	return nil
}

// findingCommentBody renders one finding's stable native prose: the key on
// its own first line (the host stores key plus prose), then severity, fix
// kind, defect, evidence and required outcome. The same finding always
// renders the same bytes.
func findingCommentBody(f Finding) string {
	var body strings.Builder
	body.WriteString(f.Key)
	body.WriteString("\nseverity: ")
	body.WriteString(f.Severity)
	body.WriteString("\nfix: ")
	body.WriteString(f.FixKind)
	body.WriteString("\n\n")
	body.WriteString(f.Defect)
	body.WriteString("\n\nEvidence:\n")
	body.WriteString(f.Evidence)
	body.WriteString("\n\nRequired outcome:\n")
	body.WriteString(f.RequiredOutcome)
	return body.String()
}

// InlineComment renders one located finding as its stable native comment.
// Non-line findings have no diff anchor and belong in the review summary
// instead; they never become inline comments.
func (f Finding) InlineComment() (FindingComment, error) {
	if err := f.Validate(); err != nil {
		return FindingComment{}, err
	}
	if f.Location == nil {
		return FindingComment{}, errors.New("non-line finding belongs in the review summary")
	}
	return FindingComment{
		Key:  f.Key,
		Path: f.Location.Path,
		Side: f.Location.Side,
		Line: f.Location.Line,
		Body: findingCommentBody(f),
	}, nil
}

// ReviewWorkEqual reports whether two recorded review authorizations carry
// the identical immutable tuple, comparing inline finding comments
// element-wise. Reconciliation, recovery and store prefix checks round-trip
// persisted work through this comparison instead of struct identity, which
// no longer applies to the comment list.
func ReviewWorkEqual(a, b ReviewWork) bool {
	if (a.VoteBinding == nil) != (b.VoteBinding == nil) || a.VoteBinding != nil && *a.VoteBinding != *b.VoteBinding {
		return false
	}
	if len(a.Comments) != len(b.Comments) {
		return false
	}
	for i := range a.Comments {
		if a.Comments[i] != b.Comments[i] {
			return false
		}
	}
	return a.OperationID == b.OperationID && a.AuthRevision == b.AuthRevision &&
		a.Repository == b.Repository && a.HeadRepository == b.HeadRepository && a.ActorID == b.ActorID && a.PRNumber == b.PRNumber &&
		a.PRID == b.PRID && a.IssueID == b.IssueID && a.PRAuthorID == b.PRAuthorID &&
		a.HeadRef == b.HeadRef && a.BaseRef == b.BaseRef && a.HeadOID == b.HeadOID &&
		a.BaseOID == b.BaseOID && a.NativeRev == b.NativeRev && a.NotAfter == b.NotAfter &&
		a.Event == b.Event && a.AgentVerdict == b.AgentVerdict && a.Body == b.Body
}

// ReviewCommentMapping binds one submitted finding key to its committed
// native inline comment: the stable native/factory identity mapping. Keys
// are preserved through review rounds; the native comment ID is adopted
// from the submission receipt and never invented.
type ReviewCommentMapping struct {
	Key       string
	CommentID int64
}

// Validate checks one mapping: a stable finding key and a positive native
// comment record.
func (m ReviewCommentMapping) Validate() error {
	if !ValidFindingKey(m.Key) {
		return errors.New("invalid finding comment mapping key")
	}
	if m.CommentID <= 0 {
		return errors.New("invalid mapped native comment")
	}
	return nil
}

// ReviewThreadAnchor records the original selection on one provider comment.
// A nil pointer means unknown; recorded native blame coordinates are separate.
// Field order is the canonical digest encoding shared with the native SDK.
type ReviewThreadAnchor struct {
	Base      string `json:"base"`
	Head      string `json:"head"`
	Path      string `json:"path"`
	Side      string `json:"side"`
	Scope     string `json:"scope"`
	StartLine int64  `json:"start_line"`
	EndLine   int64  `json:"end_line"`
}

func (a ReviewThreadAnchor) Validate() error {
	for _, oid := range []string{a.Base, a.Head} {
		if len(oid) != 40 && len(oid) != 64 || oid != strings.ToLower(oid) {
			return errors.New("invalid original thread commit")
		}
		if _, err := hex.DecodeString(oid); err != nil {
			return errors.New("invalid original thread commit")
		}
	}
	if a.Path == "" || len(a.Path) > 4096 || !utf8.ValidString(a.Path) || strings.ContainsRune(a.Path, 0) || strings.HasPrefix(a.Path, "/") || path.Clean(a.Path) != a.Path || a.Path == "." || a.Path == ".." || strings.HasPrefix(a.Path, "../") || a.Side != "old" && a.Side != "new" {
		return errors.New("invalid original thread path or side")
	}
	if a.Scope == "file" && a.StartLine == 0 && a.EndLine == 0 || a.Scope == "lines" && a.StartLine > 0 && a.EndLine >= a.StartLine && a.EndLine <= 9007199254740991 {
		return nil
	}
	return errors.New("invalid original thread range")
}

func (a *ReviewThreadAnchor) Equal(b *ReviewThreadAnchor) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

// ResolveConversationMember is one thread member behind a resolution
// digest: member ID, body, content version, location and resolver state,
// in native (created_unix, id) order with the anchored root first.
type ResolveConversationMember struct {
	OriginalAnchor *ReviewThreadAnchor
	Body           string
	Location       string
	ResolverState  string
	ID             int64
	ContentVer     int64
}

// ResolveConversationLine renders one member in the canonical digest
// encoding shared with the X SDK twin: decimal ID, body, decimal content
// version, location, resolver state and original-anchor JSON (null for unknown
// context) joined by NUL bytes. Both sides
// must reproduce these bytes exactly; the digest comparison refuses any
// divergence as changed content.
func ResolveConversationLine(m ResolveConversationMember) string {
	anchor, _ := json.Marshal(m.OriginalAnchor)
	return strconv.FormatInt(m.ID, 10) + "\x00" + m.Body + "\x00" +
		strconv.FormatInt(m.ContentVer, 10) + "\x00" + m.Location + "\x00" + m.ResolverState + "\x00" + string(anchor)
}

// ConversationDigest binds the expected complete conversation behind a
// resolve request: member count plus the ordered member lines. Any reply,
// edit, unresolve or resolver-state change alters the digest and refuses
// the stale resolution. Review snapshots (FW17) record this digest;
// resolution (FW17) recomputes it from live native state.
func ConversationDigest(lines []string) (string, error) {
	if len(lines) == 0 {
		return "", errors.New("conversation digest needs its members")
	}
	sum := sha256.Sum256([]byte(strconv.Itoa(len(lines)) + "\n" + strings.Join(lines, "\n")))
	return hex.EncodeToString(sum[:]), nil
}

// ThreadEvidence is the recorded conversation a resolve request must
// match: repository, PR, anchored root comment, complete digest and
// whether the root mapping was deleted. Deletion invalidates the mapping
// and cannot silently transfer accepted resolution to another root.
type ThreadEvidence struct {
	Digest        string
	Repository    int64
	PRNumber      int64
	RootCommentID int64
	Deleted       bool
}

// ResolveRequest is one conditional thread-resolve intent (D06): exact
// PR/head/base, anchored root comment, expected complete conversation
// digest and resolved=true. Replies are members, never resolution
// targets; only resolution is supported, never unresolve via this
// operation.
type ResolveRequest struct {
	HeadRef       string `json:"head_ref"`
	BaseRef       string `json:"base_ref"`
	HeadOID       string `json:"head_oid"`
	BaseOID       string `json:"base_oid"`
	Digest        string `json:"digest"`
	Repository    int64  `json:"repository"`
	PRNumber      int64  `json:"pr_number"`
	PRID          int64  `json:"pr_id"`
	RootCommentID int64  `json:"root_comment_id"`
	ActorID       int64  `json:"actor_id"`
	Resolved      bool   `json:"resolved"`
}

// ValidateForEvidence checks the resolve intent against its enrolled
// binding, reviewer independence and recorded thread evidence. A missing
// or wrong-kind binding, a wrong actor, the PR author's own identity, a
// reply passed as root, a deleted root or changed content all refuse;
// exact replay against unchanged evidence validates.
func (r ResolveRequest) validateResolveBinding(binding ActorBindingRef, prAuthorID int64) error {
	if binding == (ActorBindingRef{}) || binding.Kind != OpThreadResolve {
		return errors.New("missing resolve binding")
	}
	if err := binding.Validate(); err != nil {
		return errors.New("invalid resolve binding")
	}
	if r.ActorID <= 0 || r.ActorID != binding.ActorID {
		return errors.New("resolve actor differs from its binding")
	}
	if prAuthorID <= 0 || r.ActorID == prAuthorID {
		return errors.New("resolve actor needs its independent reviewer")
	}
	return nil
}

func (r ResolveRequest) validateResolveTarget() error {
	if r.Repository <= 0 || r.PRNumber <= 0 || r.PRID <= 0 || r.RootCommentID <= 0 {
		return errors.New("invalid resolve target")
	}
	if !ValidTargetBranch(r.HeadRef) || !ValidTargetBranch(r.BaseRef) || r.HeadRef == r.BaseRef {
		return errors.New("invalid resolve refs")
	}
	if !ValidCommit(r.HeadOID) || !ValidCommit(r.BaseOID) || r.HeadOID == r.BaseOID {
		return errors.New("resolve candidate is not a fresh exact commit")
	}
	if !r.Resolved {
		return errors.New("resolve request must resolve")
	}
	if !ValidDigest(r.Digest) {
		return errors.New("invalid resolve conversation digest")
	}
	return nil
}

func (r ResolveRequest) validateResolveEvidence(evidence ThreadEvidence) error {
	if evidence.Deleted {
		return errors.New("resolve root deleted")
	}
	if evidence.Repository != r.Repository || evidence.PRNumber != r.PRNumber || evidence.RootCommentID != r.RootCommentID {
		return errors.New("resolve target differs from its recorded root")
	}
	if !ValidDigest(evidence.Digest) || evidence.Digest != r.Digest {
		return errors.New("resolve conversation changed under its request")
	}
	return nil
}

func (r ResolveRequest) ValidateForEvidence(binding ActorBindingRef, prAuthorID int64, evidence ThreadEvidence) error {
	if err := r.validateResolveBinding(binding, prAuthorID); err != nil {
		return err
	}
	if err := r.validateResolveTarget(); err != nil {
		return err
	}
	return r.validateResolveEvidence(evidence)
}

// ReviewCycleID derives the deterministic review-cycle identity from
// publication, attempt root, round and phase. New rounds on the same head
// are distinct; replay of a round reuses its identity.
func ReviewCycleID(publicationID, attemptRoot string, round int, phase string) string {
	sum := sha256.Sum256([]byte("review-cycle-v1\x00" + publicationID + "\x00" + attemptRoot + "\x00" +
		strconv.Itoa(round) + "\x00" + phase))
	return hex.EncodeToString(sum[:])[:32]
}

// FindingSnapshotVersion is the only admitted complete finding digest
// version (D06/FW35): canonical typed JSON of the complete sorted finding
// set. Key-only digests (FindingSetDigest) bind cycle inputs and correction
// coverage, but they never authorize correction or merge on their own.
const FindingSnapshotVersion = "finding-snapshot-v2"

// canonicalFinding is the canonical typed encoding of one finding inside
// the complete digest. Every semantic field travels: key, authoritative
// origin/provenance, severity, actionability, defect, evidence, required
// outcome, fix kind and native location. encoding/json emits struct fields
// in declaration order, so the same set always hashes the same bytes.
type canonicalFinding struct {
	Location        *FindingLocation `json:"location,omitempty"`
	Key             string           `json:"key"`
	OriginRun       string           `json:"origin_run"`
	Severity        string           `json:"severity"`
	Defect          string           `json:"defect"`
	Evidence        string           `json:"evidence"`
	RequiredOutcome string           `json:"required_outcome"`
	FixKind         string           `json:"fix_kind"`
	OriginReview    int64            `json:"origin_review"`
	Actionable      bool             `json:"actionable"`
}

// canonicalFindingBytes renders one validated finding's canonical bytes.
// The field sequence mirrors Finding exactly, so the conversion is exact
// and the JSON encoding stays deterministic.
func canonicalFindingBytes(f Finding) ([]byte, error) {
	encoded, err := json.Marshal(canonicalFinding(f))
	if err != nil {
		return nil, errors.New("finding snapshot cannot encode its set")
	}
	return encoded, nil
}

// FindingContentDigest binds one finding set to its complete sorted content:
// every key plus every semantic field above. Any added, dropped, re-keyed
// or same-key rewritten finding changes the digest; key order never does.
// The empty set digests to the version domain alone, so a first review with
// no carried findings still binds an exact (empty) input.
func FindingContentDigest(findings []Finding) (string, error) {
	if len(findings) > MaxOutstandingFindings {
		return "", errors.New("finding set exceeds its bound")
	}
	sorted := append([]Finding(nil), findings...)
	for _, finding := range sorted {
		if err := finding.Validate(); err != nil {
			return "", err
		}
	}
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Key < sorted[j].Key })
	for i := 1; i < len(sorted); i++ {
		if sorted[i].Key == sorted[i-1].Key {
			return "", errors.New("finding set carries a duplicate key")
		}
	}
	var encoded strings.Builder
	encoded.WriteString(FindingSnapshotVersion)
	encoded.WriteByte(0)
	for _, finding := range sorted {
		one, err := canonicalFindingBytes(finding)
		if err != nil {
			return "", err
		}
		encoded.Write(one)
		encoded.WriteByte(0)
	}
	sum := sha256.Sum256([]byte(encoded.String()))
	return hex.EncodeToString(sum[:]), nil
}

// CheckSubmittedFindings binds an agent-submitted finding set to the
// controller's canonical set: same keys and byte-identical canonical
// content per key. A submission that rewrites any defect, provenance,
// severity, actionability, outcome, fix kind or location refuses, however
// unchanged its keys look. Missing, duplicate, unknown or invalid entries
// refuse alongside content drift.
func CheckSubmittedFindings(canonical, submitted []Finding) error {
	if len(canonical) > MaxOutstandingFindings || len(submitted) > MaxOutstandingFindings {
		return errors.New("finding set exceeds its bound")
	}
	byKey := make(map[string][]byte, len(canonical))
	for _, finding := range canonical {
		if err := finding.Validate(); err != nil {
			return err
		}
		encoded, err := canonicalFindingBytes(finding)
		if err != nil {
			return err
		}
		if _, dup := byKey[finding.Key]; dup {
			return errors.New("canonical finding set carries a duplicate key")
		}
		byKey[finding.Key] = encoded
	}
	if len(submitted) != len(canonical) {
		return errors.New("submitted findings answer another finding set")
	}
	for _, finding := range submitted {
		if err := finding.Validate(); err != nil {
			return err
		}
		want, ok := byKey[finding.Key]
		if !ok {
			return errors.New("submitted finding answers an unknown key")
		}
		got, err := canonicalFindingBytes(finding)
		if err != nil {
			return err
		}
		if string(got) != string(want) {
			return errors.New("submitted finding redefines its canonical defect")
		}
	}
	return nil
}

// ClearReviewConclusion is the only explicit nothing-found control. Native
// approval, an empty array, silence, process exit or an approve verdict
// without this conclusion never creates clean acceptance.
const ClearReviewConclusion = "clear"

// ClearReviewReport is one reviewer round's explicit clear verdict over an
// exact candidate: the cycle it answers, the complete head/base it covered,
// the coverage digest binding that complete input, and one verified
// assessment per carried actionable finding with evidence. Historical
// verified fixes and declined optional items are history, not carried
// findings: they stay visible in journals but never bar a later clear.
type ClearReviewReport struct {
	Assessments    []FindingAssessment `json:"assessments,omitempty"`
	CycleID        string              `json:"cycle_id"`
	Conclusion     string              `json:"conclusion"`
	Head           string              `json:"head"`
	BaseOID        string              `json:"base_oid"`
	CoverageDigest string              `json:"coverage_digest"`
}

// Validate binds the explicit clear to the controller's canonical carried
// set. Every carried finding validates; every carried actionable finding
// needs its verified assessment with evidence. A still-open assessment, a
// missing assessment, an assessment for an unknown key or a coverage digest
// that does not bind this exact input refuses.
func (r ClearReviewReport) validateScope(carried []Finding) error {
	if r.Conclusion != ClearReviewConclusion {
		return errors.New("review report is not an explicit clear")
	}
	if !ValidID(r.CycleID) {
		return errors.New("invalid clear review cycle")
	}
	if !ValidCommit(r.Head) || !ValidCommit(r.BaseOID) || r.Head == r.BaseOID {
		return errors.New("clear review covers no exact candidate")
	}
	if !ValidDigest(r.CoverageDigest) {
		return errors.New("clear review lacks its complete coverage digest")
	}
	if len(carried) > MaxOutstandingFindings {
		return errors.New("carried finding set exceeds its bound")
	}
	return nil
}

func indexCarriedFindings(carried []Finding) (map[string]Finding, error) {
	byKey := make(map[string]Finding, len(carried))
	for _, finding := range carried {
		if err := finding.Validate(); err != nil {
			return nil, err
		}
		if _, dup := byKey[finding.Key]; dup {
			return nil, errors.New("carried finding set carries a duplicate key")
		}
		byKey[finding.Key] = finding
	}
	return byKey, nil
}

func (r ClearReviewReport) validateAssessments(byKey map[string]Finding) (map[string]bool, error) {
	assessed := make(map[string]bool, len(r.Assessments))
	for _, assessment := range r.Assessments {
		if _, ok := byKey[assessment.Key]; !ok {
			return nil, errors.New("clear assessment answers an unknown finding")
		}
		if assessed[assessment.Key] {
			return nil, errors.New("clear assessment repeats its key")
		}
		assessed[assessment.Key] = true
		if err := assessment.Validate(); err != nil {
			return nil, err
		}
	}
	return assessed, nil
}

func (r ClearReviewReport) validateActionableCoverage(carried []Finding, assessed map[string]bool) error {
	for _, finding := range carried {
		if !finding.Actionable {
			continue
		}
		if !assessed[finding.Key] {
			return errors.New("carried actionable finding lacks its verified assessment")
		}
		for _, candidate := range r.Assessments {
			if candidate.Key == finding.Key && candidate.Status != FindingVerified {
				return errors.New("clear review carries an open finding")
			}
		}
	}
	return nil
}

func (r ClearReviewReport) Validate(carried []Finding) error {
	if err := r.validateScope(carried); err != nil {
		return err
	}
	byKey, err := indexCarriedFindings(carried)
	if err != nil {
		return err
	}
	assessed, err := r.validateAssessments(byKey)
	if err != nil {
		return err
	}
	return r.validateActionableCoverage(carried, assessed)
}

// ClearCoverageDigest binds one clear round's claimed coverage to the
// complete exact input it must have observed: head, base and the content
// digest of the canonical carried set. Omitted carried findings, rewritten
// content or a moved head/base all change the digest, so a stale or partial
// coverage claim refuses at admission.
func ClearCoverageDigest(head, baseOID, contentDigest string) (string, error) {
	if !ValidCommit(head) || !ValidCommit(baseOID) || head == baseOID {
		return "", errors.New("clear coverage covers no exact candidate")
	}
	if !ValidDigest(contentDigest) {
		return "", errors.New("clear coverage lacks its finding content digest")
	}
	sum := sha256.Sum256([]byte("clear-coverage-v1\x00" + head + "\x00" + baseOID + "\x00" + contentDigest))
	return hex.EncodeToString(sum[:]), nil
}

// ReviewOutcome is the attributable native review identity adopted from its
// operation's committed receipt. It is historical evidence for this exact head
// and base; callers must re-observe before using it for a later decision.
// ReviewerID is canonical; review/comment/PR/issue IDs remain native subjects.
// The candidate fields repeat the submitted work the receipt was adopted
// against; the native receipt itself carries only review/comment IDs, the
// per-key inline comment IDs and the host-derived merge base.
type ReviewOutcome struct {
	Operation    OperationOutcome
	Comments     []ReviewCommentMapping
	ReviewID     int64
	CommentID    int64
	ReviewerID   int64
	PRID         int64
	PRNumber     int64
	IssueID      int64
	HeadOID      string
	BaseOID      string
	CommitID     string
	MergeBaseOID string
	Event        string
}
