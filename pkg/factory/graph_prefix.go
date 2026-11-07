package factory

import (
	"errors"
	"reflect"
	"sort"

	"github.com/levitateos/factory/pkg/collaboration"
)

// GraphPrefixFacts projects only original, confirmed step receipts. It grants
// neither acceptance nor permission to send the next step.
type GraphPrefixFacts struct {
	Repository int64
	Actor      int64
	Effects    []GraphIssueEffect
}

type GraphIssueEffect struct {
	Issue      collaboration.IssueRef
	Created    bool
	Text       *GraphIssueText
	Expected   *GraphIssueText
	Dependency *GraphDependencyEffect
}

type GraphDependencyEffect struct {
	ID       string
	Remove   bool
	Expected []string
}

// ApplyTo predicts the inventory from the original baseline and known effects.
// Current provider state is compared by the caller, never adopted as a baseline.
func (p GraphPrefixFacts) ApplyTo(baseline []CompletionIssueEvidence) ([]CompletionIssueEvidence, error) {
	if p.Repository <= 0 || p.Actor <= 0 || len(p.Effects) > 64 {
		return nil, errors.New("invalid graph prefix scope")
	}
	bodies := map[int64]CompletionIssueEvidence{}
	for _, body := range baseline {
		if body.Validate() != nil {
			return nil, errors.New("invalid graph prefix baseline")
		}
		if _, exists := bodies[body.Index]; exists {
			return nil, errors.New("duplicate graph prefix baseline")
		}
		body.Dependencies = append([]string(nil), body.Dependencies...)
		bodies[body.Index] = body
	}
	for _, effect := range p.Effects {
		if effect.Issue.Kind != collaboration.SubjectIssue || int64(effect.Issue.Repository) != p.Repository || effect.Issue.Number <= 0 || effect.Issue.ExternalID == "" || (effect.Text == nil) == (effect.Dependency == nil) || effect.Created && effect.Text == nil {
			return nil, errors.New("invalid graph prefix effect")
		}
		body, exists := bodies[effect.Issue.Number]
		if effect.Created {
			if exists {
				return nil, errors.New("graph prefix creation replaces existing issue")
			}
			body = CompletionIssueEvidence{Index: effect.Issue.Number}
		} else if !exists {
			return nil, errors.New("graph prefix changes an unknown issue")
		}
		if effect.Text != nil {
			if effect.Created && effect.Expected != nil || !effect.Created && (effect.Expected == nil || body.Title != effect.Expected.Title || body.Body != effect.Expected.Body) {
				return nil, errors.New("graph prefix edit baseline changed")
			}
			body.Title, body.Body = effect.Text.Title, effect.Text.Body
			body.TitleDigest = MCPInputDigest([]byte(body.Title))
			body.ContentDigest = MCPInputDigest([]byte(body.Body))
		} else {
			dep := effect.Dependency
			if !ValidDependencyRef(dep.ID) || dep.ID == effect.Issue.ExternalID || dep.Expected == nil {
				return nil, errors.New("invalid graph prefix dependency")
			}
			expected := append([]string{}, dep.Expected...)
			current := append([]string{}, body.Dependencies...)
			sortGraphDependencies(expected)
			sortGraphDependencies(current)
			if !reflect.DeepEqual(expected, current) {
				return nil, errors.New("graph prefix dependency baseline changed")
			}
			found := false
			next := []string{}
			for _, id := range current {
				if id == dep.ID {
					found = true
				} else {
					next = append(next, id)
				}
			}
			if found != dep.Remove {
				return nil, errors.New("graph prefix dependency effect is inconsistent")
			}
			if !dep.Remove {
				next = append(next, dep.ID)
			}
			sortGraphDependencies(next)
			body.Dependencies = next
		}
		if body.Validate() != nil {
			return nil, errors.New("invalid graph prefix result")
		}
		bodies[body.Index] = body
	}
	result := make([]CompletionIssueEvidence, 0, len(bodies))
	for _, body := range bodies {
		result = append(result, body)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Index < result[j].Index })
	return result, nil
}

func sortGraphDependencies(ids []string) {
	sort.Slice(ids, func(i, j int) bool { return len(ids[i]) < len(ids[j]) || len(ids[i]) == len(ids[j]) && ids[i] < ids[j] })
}
