package control

import (
	"errors"

	"github.com/levitateos/sodaos/internal/factory"
)

// CompletionFreshness is the rechecked release binding for one
// continue decision: the source, preparation, scope, authority and
// evidence the sealed inventory was captured against. Ordinary
// observation timestamps never enter it; material content or source
// changes do. Before a continue releases its event, the driver
// recaptures these bindings and refuses on any mismatch: source,
// scope or authority changes invalidate before effects, and a later
// completion event still fences regardless.
type CompletionFreshness struct {
	SourceCommit         string
	PreparationDigest    string
	ScopeDigest          string
	AuthorityFingerprint string
	EvidenceDigest       string
}

// freshnessFromBindings projects the sealed local bindings plus the
// sealed inventory digest into the release comparison.
func freshnessFromBindings(local factory.CompletionLocal, inventoryDigest string) CompletionFreshness {
	return CompletionFreshness{
		SourceCommit:         local.SourceCommit,
		PreparationDigest:    local.PreparationDigest,
		ScopeDigest:          local.ScopeDigest,
		AuthorityFingerprint: local.AuthorityFingerprint,
		EvidenceDigest:       inventoryDigest,
	}
}

// CheckCompletionFreshness refuses a continue release whose world
// moved: any material binding that differs from the sealed capture
// names its dimension. Equal bindings release. Missing bindings on
// either side refuse rather than assume freshness.
func CheckCompletionFreshness(sealed, current CompletionFreshness) error {
	if sealed.SourceCommit == "" || current.SourceCommit == "" {
		return errors.New("completion freshness lacks its source binding")
	}
	switch {
	case sealed.SourceCommit != current.SourceCommit:
		return errors.New("completion source changed during reassessment")
	case sealed.PreparationDigest != current.PreparationDigest:
		return errors.New("completion preparation changed during reassessment")
	case sealed.ScopeDigest != current.ScopeDigest:
		return errors.New("completion scope changed during reassessment")
	case sealed.AuthorityFingerprint != current.AuthorityFingerprint:
		return errors.New("completion authority changed during reassessment")
	case sealed.EvidenceDigest != current.EvidenceDigest:
		return errors.New("completion evidence changed during reassessment")
	default:
		return nil
	}
}
