package service

import (
	"github.com/levitateos/factory/pkg/factory"
	"github.com/levitateos/factory/pkg/factory/control"
	"strconv"
)

type SelectedAcceptanceSource struct {
	ID             string `json:"id"`
	Digest         string `json:"digest"`
	ContentVersion int    `json:"content_version"`
}

type AcceptancePrerequisite struct {
	Occurrence       string `json:"occurrence"`
	DependsOn        string `json:"depends_on"`
	PrereqAcceptance string `json:"prerequisite_acceptance,omitempty"`
	EndpointRepo     string `json:"endpoint_repository"`
	EndpointIssue    string `json:"endpoint_issue"`
	Outcome          string `json:"outcome"`
}

type AcceptanceChange struct {
	DecisionID      string                     `json:"decision_id"`
	Predecessor     string                     `json:"predecessor,omitempty"`
	TitleDigest     string                     `json:"title_digest"`
	TitleOccurrence string                     `json:"title_occurrence"`
	ContentDigest   string                     `json:"content_digest"`
	Sources         []SelectedAcceptanceSource `json:"sources,omitempty"`
	Prerequisites   []AcceptancePrerequisite   `json:"prerequisites,omitempty"`
	Resolutions     []SelectedAcceptanceSource `json:"resolutions,omitempty"`
	NativeRevision  int64                      `json:"native_revision"`
	ContentVersion  int                        `json:"content_version"`
}

// acceptancePreview exposes only the revision-bound public facts needed to
// construct an explicit human acceptance. Native issue text is never returned.
type AcceptancePreview struct {
	Repository   string                        `json:"repository"`
	Issue        string                        `json:"issue"`
	TitleDigest  string                        `json:"title_digest"`
	TitleEvent   string                        `json:"title_occurrence"`
	BodyDigest   string                        `json:"body_digest"`
	ContentVer   int                           `json:"content_version"`
	NativeRev    int64                         `json:"native_revision"`
	Comments     []acceptancePreviewComment    `json:"comments"`
	Dependencies []acceptancePreviewDependency `json:"dependencies"`
}

type acceptancePreviewComment struct {
	ID         string `json:"id"`
	Digest     string `json:"digest"`
	ContentVer int    `json:"content_version"`
}

type acceptancePreviewDependency struct {
	Occurrence string `json:"occurrence"`
	DependsOn  string `json:"depends_on"`
}

func buildAcceptancePreview(repository int64, issue string, commentIDs []string, evidence control.AcceptanceEvidence) (AcceptancePreview, *control.AcceptanceRefusal) {
	if refusal := validateAcceptancePreviewIssue(issue, evidence); refusal != nil {
		return AcceptancePreview{}, refusal
	}
	view := AcceptancePreview{
		Repository: strconv.FormatInt(repository, 10), Issue: issue,
		TitleDigest: evidence.Issue.TitleDigest, TitleEvent: evidence.Issue.TitleOccurrence,
		BodyDigest: evidence.Issue.ContentDigest, ContentVer: evidence.Issue.ContentVer,
		NativeRev:    evidence.Revision,
		Comments:     make([]acceptancePreviewComment, 0, len(commentIDs)),
		Dependencies: make([]acceptancePreviewDependency, 0, len(evidence.Dependencies)),
	}
	comments, refusal := acceptancePreviewComments(commentIDs, evidence.Comments)
	if refusal != nil {
		return AcceptancePreview{}, refusal
	}
	dependencies, refusal := acceptancePreviewDependencies(evidence.Dependencies)
	if refusal != nil {
		return AcceptancePreview{}, refusal
	}
	view.Comments, view.Dependencies = comments, dependencies
	return view, nil
}

func validateAcceptancePreviewIssue(issue string, evidence control.AcceptanceEvidence) *control.AcceptanceRefusal {
	if !evidence.Issue.Visible {
		return &control.AcceptanceRefusal{Reason: control.RefusalIssueHidden}
	}
	valid := evidence.Issue.Index == issue && evidence.Revision > 0 &&
		factory.ValidDigest(evidence.Issue.TitleDigest) && factory.ValidDigest(evidence.Issue.ContentDigest) &&
		factory.ValidTitleOccurrence(evidence.Issue.TitleOccurrence) && evidence.Issue.ContentVer >= 0
	if !valid {
		return &control.AcceptanceRefusal{Reason: control.RefusalIncompleteEvidence}
	}
	return nil
}

func acceptancePreviewComments(commentIDs []string, evidence []control.AcceptanceComment) ([]acceptancePreviewComment, *control.AcceptanceRefusal) {
	if len(evidence) != len(commentIDs) {
		return nil, &control.AcceptanceRefusal{Reason: control.RefusalSourceMissing}
	}
	wanted := make(map[string]bool, len(commentIDs))
	for _, id := range commentIDs {
		wanted[id] = true
	}
	comments := make([]acceptancePreviewComment, 0, len(commentIDs))
	for _, comment := range evidence {
		reason := acceptancePreviewCommentReason(comment, wanted)
		if reason != "" {
			return nil, &control.AcceptanceRefusal{Reason: reason}
		}
		delete(wanted, comment.ID)
		comments = append(comments, acceptancePreviewComment{ID: comment.ID, Digest: comment.Digest, ContentVer: comment.ContentVer})
	}
	if len(wanted) != 0 {
		return nil, &control.AcceptanceRefusal{Reason: control.RefusalSourceMissing}
	}
	return comments, nil
}

func acceptancePreviewCommentReason(comment control.AcceptanceComment, wanted map[string]bool) string {
	if !comment.Visible {
		return control.RefusalSourceHidden
	}
	id, valid := acceptancePositiveID(comment.ID)
	valid = valid && strconv.FormatInt(id, 10) == comment.ID && wanted[comment.ID] && factory.ValidDigest(comment.Digest) && comment.ContentVer >= 0
	if !valid {
		return control.RefusalIncompleteEvidence
	}
	return ""
}

func acceptancePreviewDependencies(evidence []control.AcceptanceEdge) ([]acceptancePreviewDependency, *control.AcceptanceRefusal) {
	dependencies := make([]acceptancePreviewDependency, 0, len(evidence))
	for _, edge := range evidence {
		if !edge.Visible {
			return nil, &control.AcceptanceRefusal{Reason: control.RefusalEdgeHidden}
		}
		occurrence, valid := acceptancePositiveID(edge.Occurrence)
		if !valid || strconv.FormatInt(occurrence, 10) != edge.Occurrence {
			return nil, &control.AcceptanceRefusal{Reason: control.RefusalIncompleteEvidence}
		}
		dependsOn, valid := acceptancePositiveID(edge.DependsOn)
		if !valid || strconv.FormatInt(dependsOn, 10) != edge.DependsOn {
			return nil, &control.AcceptanceRefusal{Reason: control.RefusalIncompleteEvidence}
		}
		dependencies = append(dependencies, acceptancePreviewDependency{Occurrence: edge.Occurrence, DependsOn: edge.DependsOn})
	}
	return dependencies, nil
}
