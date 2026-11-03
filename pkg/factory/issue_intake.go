package factory

import (
	"errors"

	"github.com/levitateos/factory/pkg/collaboration"
)

// IssueIntakeScope is the admitted ledger scope of an opaque provider proof.
// The provider adapter owns proof interpretation/authentication. This record
// grants no implementation, publication, execution or spending permission.
type IssueIntakeScope struct {
	Source     string                     `json:"source"`
	Revision   int64                      `json:"revision"`
	Actor      collaboration.ActorID      `json:"actor,string"`
	Repository collaboration.RepositoryID `json:"repository,string"`
	Digest     string                     `json:"digest"`
	Expires    int64                      `json:"expires"`
}

func (s IssueIntakeScope) Validate() error {
	if !collaboration.ValidID(s.Source) || s.Revision < 0 || s.Actor <= 0 || s.Repository <= 0 || !ValidDigest(s.Digest) || s.Expires <= 0 {
		return errors.New("invalid issue intake scope")
	}
	return nil
}

// IssueMakerMutation is the exact semantic request retained before dispatch.
// Slots/steps are assigned transactionally by the existing issue-maker journal.
// Provider bearer proofs MUST NOT be included in these requests or digests.
type IssueMakerMutation struct {
	Action             string `json:"action"`
	Slot               int    `json:"slot"`
	Step               int    `json:"step"`
	Title              string `json:"title"`
	Body               string `json:"body"`
	ExpectedTitle      string `json:"expected_title,omitempty"`
	ExpectedBodyDigest string `json:"expected_body_digest,omitempty"`
}

func (m IssueMakerMutation) Validate() error {
	if (m.Action != "create" && m.Action != "refine") || m.Slot < 0 || m.Slot >= 16 || m.Step < 0 || m.Step >= 32 ||
		len(m.Title) == 0 || len(m.Title) > 255 || len(m.Body) == 0 || len(m.Body) > 20<<10 ||
		(m.Action == "refine" && (m.ExpectedTitle == "" || !ValidDigest(m.ExpectedBodyDigest))) {
		return errors.New("invalid issue maker mutation")
	}
	return nil
}

type IssueMakerReceipt struct {
	Issue      collaboration.IssueRef `json:"issue"`
	Slot       int                    `json:"slot"`
	Title      string                 `json:"title"`
	BodyDigest string                 `json:"body_digest"`
}
