package control

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"time"

	"github.com/levitateos/factory/pkg/factory"
	"github.com/levitateos/factory/pkg/store"
)

// Acceptance refusal reasons. Bounded codes: controls expose why a
// decision was refused or is no longer valid, never the evidence bytes.
const (
	RefusalStaleEvidence        = "stale_evidence"
	RefusalIncompleteEvidence   = "incomplete_evidence"
	RefusalNativeBusy           = "native_busy"
	RefusalSnapshotUnavailable  = "snapshot_unavailable"
	RefusalIssueHidden          = "issue_hidden"
	RefusalObjectiveChanged     = "objective_changed"
	RefusalSourceMissing        = "source_missing"
	RefusalSourceHidden         = "source_hidden"
	RefusalSourceChanged        = "source_changed"
	RefusalEdgeHidden           = "edge_hidden"
	RefusalEdgeChanged          = "edge_changed"
	RefusalPrereqUnknown        = "prereq_route_unknown"
	RefusalPrereqStale          = "prereq_route_stale"
	RefusalCreationUnverified   = "creation_unverified"
	RefusalCreationEdited       = "creation_edited"
	RefusalCreationBlocked      = "creation_blocked"
	RefusalCreationFactory      = "creation_factory_issue"
	RefusalCreationUnauthorized = "creation_unauthorized"
)

// AcceptanceRefusal reports why an acceptance decision was refused.
type AcceptanceRefusal struct {
	Reason string `json:"reason"`
}

func (e *AcceptanceRefusal) Error() string { return "issue acceptance refused: " + e.Reason }

func refuseAcceptance(reason string) *AcceptanceRefusal { return &AcceptanceRefusal{Reason: reason} }

// AcceptanceIssueView is the bracketed native objective: exact digests,
// body version, native title/lifecycle occurrences and creation provenance at one idle
// native revision.
type AcceptanceIssueView struct {
	ID                  string
	Index               string
	TitleDigest         string
	TitleOccurrence     string
	ClosingOccurrence   string
	LifecycleOccurrence string
	ContentDigest       string
	PosterID            string
	ContentVer          int
	ClosedUnix          int64
	CreatedUnix         int64
	Verified            bool
	FirstCreated        bool
	Visible             bool
	Closed              bool
	IsPull              bool
}

// AcceptanceComment is one bracketed native comment revision.
type AcceptanceComment struct {
	ID         string
	Digest     string
	ContentVer int
	Visible    bool
}

// AcceptanceEdge is one direct blocked-by edge occurrence.
type AcceptanceEdge struct {
	Occurrence string
	DependsOn  string
	Visible    bool
}

// AcceptanceEvidence is one revision-bound native evidence set for an
// acceptance decision: the objective, the requested selected comments and
// the complete direct edge set.
type AcceptanceEvidence struct {
	Issue        AcceptanceIssueView
	Comments     []AcceptanceComment
	Dependencies []AcceptanceEdge
	Revision     int64
}

// AcceptanceSource brackets one native evidence read between equal idle
// native revision observations. Transport failures arrive as acceptance
// refusals with a stale, incomplete, busy or unavailable reason; any other
// error reports an unusable source.
type AcceptanceSource interface {
	ReadAcceptanceEvidence(ctx context.Context, repository, issue string, commentIDs []string) (AcceptanceEvidence, error)
}

// AcceptanceResult reports the saved decision and the current head of its chain.
type AcceptanceResult struct {
	Publications factory.PublicationWithdrawal `json:"publications"`
	Merges       factory.MergeWithdrawal       `json:"merges"`
	DecisionID   string                        `json:"decision_id"`
	Head         string                        `json:"head"`
	Depth        int64                         `json:"depth"`
}

// AcceptanceAuthorization binds a trusted caller's current authority to this
// operation. It is rechecked after evidence reads and immediately before local
// persistence or replay; nil never admits a caller.
type AcceptanceAuthorization func(context.Context) error

// AdmitAcceptance records the maintainer's exact reviewed inputs after checking
// current native evidence. Recorded decisions remain immutable; repeating one
// checks current visibility without approving any newer inputs.
func (c *Coordinator) AdmitAcceptance(ctx context.Context, decision factory.Acceptance, authorize AcceptanceAuthorization) (AcceptanceResult, error) {
	return c.admitAcceptance(ctx, c.AcceptanceReads, decision, authorize)
}

// AdmitAcceptanceUsing keeps a native request's observed account/source scoped
// to this command without mutating the shared background coordinator ports.
func (c *Coordinator) AdmitAcceptanceUsing(ctx context.Context, source AcceptanceSource, decision factory.Acceptance, authorize AcceptanceAuthorization) (AcceptanceResult, error) {
	return c.admitAcceptance(ctx, source, decision, authorize)
}

func (c *Coordinator) admitAcceptance(ctx context.Context, source AcceptanceSource, decision factory.Acceptance, authorize AcceptanceAuthorization) (AcceptanceResult, error) {
	bounded, stop := context.WithTimeout(ctx, 30*time.Second)
	defer stop()
	bounded, ownsPass, cancelPass := c.readinessPass(bounded)
	defer cancelPass()
	if authorize == nil {
		return AcceptanceResult{}, ErrIneffectiveAuthority
	}
	if err := authorize(bounded); err != nil {
		return AcceptanceResult{}, err
	}
	if err := decision.Validate(); err != nil {
		return AcceptanceResult{}, err
	}
	if decision.Initial {
		return AcceptanceResult{}, refuseAcceptance(RefusalCreationUnverified)
	}
	stored, lookupErr := c.Store.AcceptanceDecision(bounded, decision.ID)
	if lookupErr == nil {
		saved, err := json.Marshal(stored)
		if err != nil {
			return AcceptanceResult{}, err
		}
		requested, err := json.Marshal(decision)
		if err != nil {
			return AcceptanceResult{}, err
		}
		if !bytes.Equal(saved, requested) {
			return AcceptanceResult{}, store.ErrCommandConflict
		}
	} else if !errors.Is(lookupErr, store.ErrNotFound) {
		return AcceptanceResult{}, lookupErr
	}
	evidence, err := c.readAcceptanceEvidenceUsing(bounded, source, decision)
	if err != nil {
		return AcceptanceResult{}, err
	}
	if lookupErr == nil {
		if err = verifyAcceptanceVisibility(decision, evidence); err != nil {
			return AcceptanceResult{}, err
		}
	} else {
		if err = c.verifyAcceptance(bounded, decision, evidence); err != nil {
			return AcceptanceResult{}, err
		}
	}
	if err = authorize(bounded); err != nil {
		return AcceptanceResult{}, err
	}
	if err = bounded.Err(); err != nil {
		return AcceptanceResult{}, err
	}
	if errors.Is(lookupErr, store.ErrNotFound) {
		if err = c.Store.AdmitAcceptanceDecision(bounded, decision); err != nil {
			return AcceptanceResult{}, err
		}
	}
	issue := mustIssueIndex(decision.IssueIndex)
	head, err := c.Store.AcceptanceHead(bounded, decision.Repository, issue)
	if err != nil {
		return AcceptanceResult{}, err
	}
	depth, err := c.Store.AcceptanceDepth(bounded, decision.Repository, issue)
	if err != nil {
		return AcceptanceResult{}, err
	}
	result := AcceptanceResult{DecisionID: decision.ID, Head: head, Depth: depth}
	if decision.Predecessor != "" {
		result.Publications = c.cancelAcceptancePublications(bounded, decision.Repository, issue, decision.Predecessor)
		result.Merges = c.cancelAcceptanceMerges(bounded, decision.Repository, issue, decision.Predecessor)
	}
	if ownsPass {
		_, _ = c.drainReadinessWork(bounded, "")
	}
	return result, nil
}

// verifyAcceptanceVisibility enforces the visibility subset of
// verifyAcceptance: the bracketed issue, every selected source and
// every current edge must still be visible. Established refusal
// codes; no revision, digest, version, edge-set or prerequisite
// equality.
func verifyAcceptanceVisibility(decision factory.Acceptance, evidence AcceptanceEvidence) error {
	issue := evidence.Issue
	if issue.Index != decision.IssueIndex {
		return refuseAcceptance(RefusalIncompleteEvidence)
	}
	if !issue.Visible {
		return refuseAcceptance(RefusalIssueHidden)
	}
	comments := make(map[string]AcceptanceComment, len(evidence.Comments))
	for _, comment := range evidence.Comments {
		comments[comment.ID] = comment
	}
	for _, section := range [][]factory.SelectedSource{decision.Sources, decision.Resolutions} {
		for _, source := range section {
			comment, ok := comments[source.ID]
			if !ok {
				return refuseAcceptance(RefusalSourceMissing)
			}
			if !comment.Visible {
				return refuseAcceptance(RefusalSourceHidden)
			}
		}
	}
	for _, edge := range evidence.Dependencies {
		if !edge.Visible {
			return refuseAcceptance(RefusalEdgeHidden)
		}
	}
	return nil
}

func mustIssueIndex(index string) int64 {
	value, _ := strconv.ParseInt(index, 10, 64)
	return value
}
