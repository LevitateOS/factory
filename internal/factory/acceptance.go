// Issue acceptance records bind exact native requirements to the maintainer
// who approved them: the objective title/body revision, selected
// question/answer/resolution comments, and every direct prerequisite edge
// with its required outcome. An acceptance is an approval receipt and
// run-input snapshot, never an alternate issue editor: native text and
// relationships stay canonical, and only explicitly selected revisions
// enter accepted requirements.
package factory

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strconv"

	"github.com/levitateos/sodaos/internal/project"
)

// Prerequisite outcomes. Code prerequisites unblock on the attributable
// factory completion of the accepted prerequisite inputs; result
// prerequisites need an accepted exact native resolution.
const (
	PrereqCode   = "code"
	PrereqResult = "result"
)

// MaxAcceptedSources bounds each selected section to one native page.
const MaxAcceptedSources = 50

// OriginalOccurrence is explicit native absence in complete visible history.
// Empty occurrence evidence is unknown, never original input authority.
const OriginalOccurrence = "original"

// SelectedSource binds one exact native comment revision by its native ID,
// content version and body digest. Poster and timestamps are context, never
// revision evidence.
type SelectedSource struct {
	ID             string `json:"id"`
	Digest         string `json:"digest"`
	ContentVersion int    `json:"content_version"`
}

func (s SelectedSource) Validate() error {
	if !decimalNativeID(s.ID) || s.ContentVersion < 0 || !ValidDigest(s.Digest) {
		return errors.New("invalid selected native source")
	}
	return nil
}

// AcceptedPrerequisite binds one direct blocked-by edge occurrence with its
// required outcome. Occurrence and endpoint IDs are verified against the
// native edge set; the endpoint repository/issue locator is the
// maintainer-asserted address used to track the prerequisite's acceptance
// head. Code prerequisites name the prerequisite's current accepted-input
// revision, which must already exist.
type AcceptedPrerequisite struct {
	Occurrence       string `json:"occurrence"`
	DependsOn        string `json:"depends_on"`
	PrereqAcceptance string `json:"prerequisite_acceptance,omitempty"`
	EndpointRepo     int64  `json:"endpoint_repository,string"`
	EndpointIssue    int64  `json:"endpoint_issue,string"`
	Outcome          string `json:"outcome"`
}

func (p AcceptedPrerequisite) Validate() error {
	if !decimalNativeID(p.Occurrence) || !decimalNativeID(p.DependsOn) {
		return errors.New("invalid prerequisite edge")
	}
	if p.EndpointRepo <= 0 || p.EndpointIssue <= 0 {
		return errors.New("invalid prerequisite endpoint")
	}
	switch p.Outcome {
	case PrereqCode:
		if !project.ValidDecisionID(p.PrereqAcceptance) {
			return errors.New("code prerequisite needs its accepted-input revision")
		}
	case PrereqResult:
		if p.PrereqAcceptance != "" {
			return errors.New("result prerequisite names no factory acceptance")
		}
	default:
		return errors.New("invalid prerequisite outcome")
	}
	return nil
}

// Acceptance is one immutable maintainer decision for a native issue: the
// exact objective revision, selected sources, complete prerequisite set and
// approved resolutions observed at one native revision. Decisions form a
// predecessor chain per repository/issue; only the head can authorize new
// work. Initial acceptances bind the verified original title/body only.
type Acceptance struct {
	ID              string                 `json:"id"`
	Predecessor     string                 `json:"predecessor,omitempty"`
	IssueIndex      string                 `json:"issue_index"`
	TitleDigest     string                 `json:"title_digest"`
	TitleOccurrence string                 `json:"title_occurrence"`
	ContentDigest   string                 `json:"content_digest"`
	Sources         []SelectedSource       `json:"sources,omitempty"`
	Prerequisites   []AcceptedPrerequisite `json:"prerequisites,omitempty"`
	Resolutions     []SelectedSource       `json:"resolutions,omitempty"`
	Repository      int64                  `json:"repository,string"`
	Approver        int64                  `json:"approver,string"`
	NativeRev       int64                  `json:"native_revision"`
	ContentVersion  int                    `json:"content_version"`
	Initial         bool                   `json:"initial,omitempty"`
}

func decimalNativeID(id string) bool {
	if id == "" || id[0] == '0' || len(id) > 20 {
		return false
	}
	value, err := strconv.ParseInt(id, 10, 64)
	return err == nil && value > 0 && strconv.FormatInt(value, 10) == id
}

// ValidNativeOccurrence requires an immutable native record ID.
func ValidNativeOccurrence(id string) bool { return decimalNativeID(id) }

// ValidTitleOccurrence distinguishes an explicit original title from a
// later native title change. Empty evidence is never original evidence.
func ValidTitleOccurrence(id string) bool {
	return id == OriginalOccurrence || decimalNativeID(id)
}

func validSourceList(sources []SelectedSource) error {
	if len(sources) > MaxAcceptedSources {
		return errors.New("too many selected native sources")
	}
	seen := make(map[string]bool, len(sources))
	for _, source := range sources {
		if err := source.Validate(); err != nil {
			return err
		}
		if seen[source.ID] {
			return errors.New("duplicate selected native source")
		}
		seen[source.ID] = true
	}
	return nil
}

// Validate rejects malformed acceptances. Shape only: admission verifies
// every selected revision against a bracketed native snapshot.
func (a Acceptance) validateScope() error {
	if !project.ValidDecisionID(a.ID) || (a.Predecessor != "" && !project.ValidDecisionID(a.Predecessor)) || a.Predecessor == a.ID {
		return errors.New("invalid acceptance decision identity")
	}
	if a.Repository <= 0 || !decimalNativeID(a.IssueIndex) || a.Approver <= 0 || a.NativeRev < 1 {
		return errors.New("invalid acceptance scope")
	}
	if !ValidDigest(a.TitleDigest) || !ValidDigest(a.ContentDigest) || a.ContentVersion < 0 {
		return errors.New("invalid acceptance objective")
	}
	if !ValidTitleOccurrence(a.TitleOccurrence) {
		return errors.New("invalid accepted title occurrence")
	}
	return nil
}

func (a Acceptance) validatePrerequisites() error {
	if err := validSourceList(a.Sources); err != nil {
		return err
	}
	if err := validSourceList(a.Resolutions); err != nil {
		return err
	}
	if len(a.Prerequisites) > MaxAcceptedSources {
		return errors.New("too many accepted prerequisites")
	}
	seen := make(map[string]bool, len(a.Prerequisites))
	for _, prereq := range a.Prerequisites {
		if err := prereq.Validate(); err != nil {
			return err
		}
		if seen[prereq.Occurrence] {
			return errors.New("duplicate prerequisite occurrence")
		}
		seen[prereq.Occurrence] = true
	}
	if a.Initial && (len(a.Sources) != 0 || len(a.Prerequisites) != 0 || len(a.Resolutions) != 0) {
		return errors.New("initial acceptance binds the objective only")
	}
	return nil
}

func (a Acceptance) Validate() error {
	if err := a.validateScope(); err != nil {
		return err
	}
	return a.validatePrerequisites()
}

// InitialAcceptanceID derives the deterministic decision ID for the
// verified original-creation path, so duplicate creation observations
// replay one decision instead of admitting another.
func InitialAcceptanceID(repository int64, issue string) string {
	sum := sha256.Sum256([]byte("st06-initial:" + strconv.FormatInt(repository, 10) + ":" + issue))
	return "d" + hex.EncodeToString(sum[:])[:24]
}
