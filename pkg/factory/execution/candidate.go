package execution

import (
	"context"
	"errors"
)

var (
	ErrCandidateInvalid = errors.New("execution candidate is not recorded")
	ErrCandidateBounds  = errors.New("execution candidate exceeds bounds")
)

type CandidateRequest struct {
	Address     RunAddress
	Role        string
	Preparation string
	Candidate   string
}

// CandidateBundle contains bounded Git objects for the captured commit. It
// carries no provider homes, credential material or container path selectors.
type CandidateBundle struct {
	Address   RunAddress
	Candidate string
	Bundle    []byte
}

type CandidateExporter interface {
	ExportCandidate(context.Context, CandidateRequest) (CandidateBundle, error)
}
