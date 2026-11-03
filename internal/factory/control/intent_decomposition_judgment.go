package control

import (
	"github.com/levitateos/sodaos/internal/factory"
)

// effectiveDecompositionScope projects the revision to the scope the
// decomposition gate authorizes, mirroring the native payload selector:
// decided task-bound choices, accepted-or-corrected assumptions as global
// constraints, and declined ideas as explicit rejection evidence. Tasks
// stay whole; pending ideas are excluded so unsubmitted choices gain no
// authority and cannot veto an independent subset. The projection feeds
// judgment evidence only; the original revision stays authoritative for
// CAS, reservations, authority, and native guard inputs.
func effectiveDecompositionScope(r factory.IntentRevision) factory.IntentRevision {
	bound := map[string]bool{}
	for _, task := range r.Tasks {
		for _, id := range task.IdeaIDs {
			bound[id] = true
		}
	}
	effective := map[string]bool{}
	for _, id := range r.EffectiveOutcomes {
		effective[id] = true
	}
	scoped := r
	scoped.Ideas = nil
	for _, idea := range r.Ideas {
		if idea.Decision == "decline" {
			scoped.Ideas = append(scoped.Ideas, idea)
			continue
		}
		if idea.Decision != "accept" && idea.Decision != "edit" {
			continue
		}
		if len(effective) > 0 && !effective[idea.ID] {
			continue
		}
		if !idea.Assumption && !bound[idea.ID] {
			continue
		}
		scoped.Ideas = append(scoped.Ideas, idea)
	}
	return scoped
}
