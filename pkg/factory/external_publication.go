package factory

import (
	"errors"
	"reflect"
	"strconv"

	"github.com/levitateos/factory/pkg/collaboration"
	"github.com/levitateos/factory/pkg/project"
)

const CommandAdoptExternalPull = "adopt-external-pr"
const ActionInspectExternalPull = "inspect-external-pr"

type ExternalPullSelection struct {
	Issue  int64 `json:"issue,string"`
	Number int64 `json:"number,string"`
}

// ExternalPullPlanning is a real Architect assignment's immutable command.
// Its completed plan can adopt the captured PR; it never advances to an
// initial Builder. Principal is the original local command principal.
type ExternalPullPlanning struct {
	Request   ExternalPullAdoption      `json:"request"`
	Origin    ExternalPublicationOrigin `json:"origin"`
	Principal string                    `json:"principal"`
}

type ExternalPullAdmission struct {
	Request     ExternalPullAdoption        `json:"request"`
	CommandID   string                      `json:"command_id"`
	Planning    *ExternalPullPlanningStatus `json:"planning,omitempty"`
	Publication *Publication                `json:"publication,omitempty"`
}

type ExternalPullPlanningStatus struct {
	Assignment string `json:"assignment"`
	Run        string `json:"run"`
	Stage      string `json:"stage"`
}

func (a Assignment) validateExternalPlanning() error {
	e := a.ExternalPlanning
	if e == nil {
		return nil
	}
	r := e.Request
	if r.Validate() != nil || r.PlanAssignment != "" || e.Origin.Validate(a.Repository) != nil || e.Origin.CommandID != a.ID || e.Origin.Policy != r.Policy || e.Origin.Target != r.Target || e.Origin.Automatic != r.Automatic ||
		a.RetainedPublication != nil || a.ExternalPublication != nil || a.Phase != project.ExecutionInvestigate || a.Role != project.RoleCoder || a.AttemptRoot != a.ID || a.PublicationAssignment != a.ID || a.ParentAssignment != "" ||
		a.Repository != r.Repository || a.Issue != r.Issue || a.Acceptance != r.Acceptance || a.Connection != r.Connection || a.SourceCommit != r.Target.BaseOID || a.Authority.Policy != e.Origin.Policy || e.Principal == "" || len(e.Principal) > 128 {
		return errors.New("external planning differs from its captured command")
	}
	return nil
}

func ExternalPullTarget(repository, number int64) string {
	return "external-pr/" + strconv.FormatInt(repository, 10) + "/" + strconv.FormatInt(number, 10)
}

// PublicationPullTarget is immutable PR identity, not a creation receipt. Both
// own publication and explicit external adoption feed the same delivery owners.
type PublicationPullTarget struct {
	AuthorID       int64  `json:"author_id,string"`
	IssueID        int64  `json:"issue_id,string"`
	HeadRepository int64  `json:"head_repository,string"`
	HeadRef        string `json:"head_ref"`
	BaseRef        string `json:"base_ref"`
	HeadOID        string `json:"head_oid"`
	BaseOID        string `json:"base_oid"`
}

// ExternalPublicationOwner owns the new finite review/correction allowance.
// PlanAssignment names real, completed investigation provenance; it is never
// treated as an initial Builder or a provider publication receipt.
type ExternalPublicationOwner struct {
	PlanAssignment string                    `json:"plan_assignment"`
	Connection     string                    `json:"connection"`
	Origin         ExternalPublicationOrigin `json:"origin"`
}

func (a Assignment) validateExternalPublicationOwner() error {
	e := a.ExternalPublication
	if e == nil || e.Origin.Validate(a.Repository) != nil || e.Origin.CommandID != a.ID || !ValidID(e.PlanAssignment) || e.PlanAssignment == a.ID ||
		e.Connection == "" || len(e.Connection) > 128 || a.Authority.SponsorshipConnection != e.Connection || a.RetainedPublication != nil ||
		!ValidID(a.ID) || a.AttemptRoot != a.ID || a.PublicationAssignment != a.ID || a.ParentAssignment != "" ||
		a.Stage != AssignmentRetained || a.Revision != 0 || a.ActorID <= 0 || a.CreatedUnix < e.Origin.ObservedUnix ||
		!ValidProjectID(a.ProjectID) || a.Role != project.RoleCoder || !ValidPreparationRef(a.Preparation) ||
		a.SourceCommit != e.Origin.Target.BaseOID || a.Subject.Kind != SubjectIssue || a.Subject.Validate() != nil || checkIssueAssignmentScope(a) != nil ||
		a.NativeRev < 1 || a.Phase != "" || a.Run != "" || len(a.RunHistory) != 0 || a.Attempts != 0 ||
		a.Result != nil || len(a.Prompt) != 0 || a.PromptSHA != "" || a.Model != "" || a.Connection != "" ||
		a.Harness != "" || a.HarnessVers != "" || a.NativeTool != nil || a.AdoptedHarness != (project.FactoryHarnessPin{}) ||
		a.Outcome != "" || a.Reason != "" || a.FinishedUnix != 0 || a.AcceptedContext != nil || a.ReviewContext != nil || a.PlanningContext != nil {
		return errors.New("invalid non-executing external publication owner")
	}
	return nil
}

// ExternalPullAdoption is an explicit command, including the immutable expected
// native target and selected source. An authenticated observation must match
// these facts before Store admission; reads alone never fund execution.
type ExternalPullAdoption struct {
	Policy         int64                 `json:"policy"`
	Repository     int64                 `json:"repository,string"`
	Issue          int64                 `json:"issue,string"`
	Acceptance     string                `json:"acceptance"`
	PlanAssignment string                `json:"plan_assignment"`
	PRNumber       int64                 `json:"pr_number,string"`
	PRID           int64                 `json:"pr_id,string"`
	Target         PublicationPullTarget `json:"target"`
	Connection     string                `json:"connection"`
	Limits         AttemptLimits         `json:"limits"`
	ForkConsent    bool                  `json:"fork_consent"`
	Automatic      bool                  `json:"automatic,omitempty"`
}

func (r ExternalPullAdoption) Validate() error {
	t := r.Target
	if r.Policy < 1 || r.Repository < 1 || r.Issue < 1 || !project.ValidDecisionID(r.Acceptance) || r.PlanAssignment != "" && !ValidID(r.PlanAssignment) || r.PRNumber < 1 || r.PRID < 1 || r.Connection == "" || len(r.Connection) > 128 || r.Limits.Validate() != nil || r.Limits.ReviewRounds < 1 ||
		t.AuthorID < 1 || t.IssueID < 1 || t.HeadRepository < 1 || !ValidTargetBranch(t.HeadRef) || !ValidTargetBranch(t.BaseRef) || !ValidCommit(t.HeadOID) || !ValidCommit(t.BaseOID) || t.HeadOID == t.BaseOID {
		return errors.New("invalid external PR adoption")
	}
	if (r.Target.HeadRepository != r.Repository) != r.ForkConsent {
		return errors.New("external fork consent does not match its selected repository")
	}
	if r.Automatic && r.ForkConsent {
		return errors.New("automatic external adoption cannot execute forks")
	}
	return nil
}

func (p Publication) PullSource() *project.FactoryPullSource {
	if p.External == nil {
		return nil
	}
	t := p.PullTarget()
	return &project.FactoryPullSource{Publication: p.ID, Repository: p.Repository, Number: p.PRNumber, PullID: p.PRID, IssueID: t.IssueID, AuthorID: t.AuthorID, HeadRepository: t.HeadRepository, HeadRef: t.HeadRef, BaseRef: t.BaseRef, Head: p.Candidate, Base: t.BaseOID}
}

// AutomaticExternalAdoption preserves the original issue and authority while
// checking the publication's current candidate after an admitted correction.
func (p Publication) AutomaticExternalAdoption(policy RepositoryPolicy) (ExternalPullAdoption, bool) {
	if p.External == nil || !p.External.Automatic {
		return ExternalPullAdoption{}, false
	}
	target := p.PullTarget()
	target.HeadOID = p.Candidate
	return ExternalPullAdoption{Automatic: true, Policy: p.External.Policy, Repository: p.Repository, Issue: p.Issue, Acceptance: p.Acceptance,
		PRNumber: p.PRNumber, PRID: p.PRID, Target: target, Connection: p.Authority.SponsorshipConnection, Limits: policy.AttemptLimits}, true
}

// ExternalPublicationOrigin records observation and explicit execution consent;
// it has no branch/PR-create operation, model result or fabricated Builder run.
// Store admission must bind the command, current policy/source and exact native
// observation before a publication can acquire this origin.
type ExternalPublicationOrigin struct {
	Target              PublicationPullTarget      `json:"target"`
	CommandID           string                     `json:"command_id"`
	Host                collaboration.HostID       `json:"host"`
	Profile             collaboration.ProfileID    `json:"profile"`
	Connection          collaboration.ConnectionID `json:"connection"`
	ActorID             int64                      `json:"actor_id,string"`
	Policy              int64                      `json:"policy"`
	ObservationDigest   string                     `json:"observation_digest"`
	ObservedUnix        int64                      `json:"observed_unix"`
	ObservationRevision int64                      `json:"observation_revision"`
	// A fork needs its own explicit consent for this exact source repository,
	// head and command. Missing source execution support must still refuse it.
	ForkConsent string `json:"fork_consent,omitempty"`
	Automatic   bool   `json:"automatic,omitempty"`
}

func (o ExternalPublicationOrigin) validateFields() error {
	t := o.Target
	if t.AuthorID < 1 || t.IssueID < 1 || t.HeadRepository < 1 || !ValidTargetBranch(t.HeadRef) || !ValidTargetBranch(t.BaseRef) ||
		!ValidCommit(t.HeadOID) || !ValidCommit(t.BaseOID) || t.HeadOID == t.BaseOID || !ValidID(o.CommandID) ||
		!collaboration.ValidID(string(o.Host)) || !collaboration.ValidID(string(o.Profile)) || !collaboration.ValidID(string(o.Connection)) ||
		o.ActorID < 1 || o.Policy < 1 || o.ObservationRevision < 1 || !ValidDigest(o.ObservationDigest) || o.ObservedUnix <= 0 {
		return errors.New("invalid external publication origin")
	}
	return nil
}

func (o ExternalPublicationOrigin) Validate(repository int64) error {
	if repository < 1 || o.validateFields() != nil {
		return errors.New("invalid external publication origin")
	}
	t := o.Target
	if t.HeadRepository != repository {
		if o.Automatic {
			return errors.New("automatic external adoption cannot execute forks")
		}
		if o.ForkConsent != o.CommandID {
			return errors.New("external fork lacks exact explicit execution consent")
		}
	} else if o.ForkConsent != "" {
		return errors.New("same-repository adoption carries no fork consent")
	}
	if t.HeadRepository == repository && t.HeadRef == t.BaseRef {
		return errors.New("external PR head and base references must differ")
	}
	return nil
}

func (p Publication) PullTarget() PublicationPullTarget {
	if p.External != nil {
		return p.External.Target
	}
	t := PublicationPullTarget{IssueID: p.PRCreate.IssueID, HeadRepository: p.Repository, HeadRef: p.PRCreate.HeadRef, BaseRef: p.PRCreate.BaseRef, HeadOID: p.PRCreate.HeadOID, BaseOID: p.PRCreate.BaseOID}
	if p.PRCreate.Work != nil {
		t.AuthorID = p.PRCreate.Work.ActorID
	}
	return t
}

func (p Publication) HasPullTarget() bool {
	return p.PRNumber > 0 && p.PRID > 0 && (p.External != nil || p.PRCreate.Work != nil && p.PRCreate.Effect == OpEffectCommitted)
}

func (p Publication) InitialCandidate() string {
	if p.External != nil {
		return p.External.Target.HeadOID
	}
	if p.Publish.Work != nil {
		return p.Publish.Work.Candidate
	}
	return ""
}

func (p Publication) PublicationRef() string {
	if p.External != nil {
		return p.External.Target.HeadRef
	}
	return PublishBranchName(p.AssignmentID)
}

func (p Publication) validateExternalOrigin() error {
	if p.External == nil {
		return nil
	}
	o := p.External
	if err := o.Validate(p.Repository); err != nil {
		return err
	}
	if o.CommandID != p.ID || p.AssignmentID != p.ID || o.Target.BaseRef != p.TargetBranch || o.Target.BaseOID != p.BaseSHA ||
		p.PRNumber < 1 || p.PRID < 1 || p.CreatedUnix < o.ObservedUnix ||
		p.Stage != PublicationPublished && p.Stage != PublicationFenced && p.Stage != PublicationWithdrawn ||
		!reflect.DeepEqual(p.Publish, PublicationOperation{Kind: OpRefPublish}) || !reflect.DeepEqual(p.PRCreate, PublicationOperation{Kind: OpPRCreate}) {
		return errors.New("external origin cannot claim Factory creation effects")
	}
	return nil
}
