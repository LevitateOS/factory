package collaboration

// ValidateAgainst checks a translated, committed graph receipt against the
// exact saved intent. It is not proof of provider authenticity or adoption into
// a task acceptance: the owning engine must still check current accepted scope,
// delivered bindings and fresh native issue observations before adoption.
func (r GraphReceipt) ValidateAgainst(g GraphIntent, repository RepositoryID, actor ActorID) error {
	if g.Validate(repository) != nil || actor <= 0 || r.Actor != actor || r.Repository != repository ||
		len(r.Issues) != len(g.Creates)+len(g.Edits) || len(r.Dependencies) != len(g.Dependencies) {
		return ErrInvalidGraph
	}
	creates := map[string]bool{}
	for _, c := range g.Creates {
		creates[c.Key] = true
	}
	edits := map[string]IssueRef{}
	existing := map[string]bool{}
	existingNumbers := map[int64]bool{}
	observe := func(i IssueRef) { existing[i.ExternalID], existingNumbers[i.Number] = true, true }
	for _, e := range g.Edits {
		edits[e.Issue.ExternalID] = e.Issue
		observe(e.Issue)
	}
	for _, d := range g.Dependencies {
		if d.Issue.Issue != nil {
			observe(*d.Issue.Issue)
		}
		if d.Prerequisite.Issue != nil {
			observe(*d.Prerequisite.Issue)
		}
	}
	created := map[string]IssueRef{}
	seen := map[string]bool{}
	numbers := map[int64]bool{}
	for _, i := range r.Issues {
		if !validGraphIssue(i.Issue, repository) || !ValidText(string(i.ContentRevision), 256) ||
			seen[i.Issue.ExternalID] || numbers[i.Issue.Number] {
			return ErrInvalidGraph
		}
		seen[i.Issue.ExternalID], numbers[i.Issue.Number] = true, true
		if i.Key != "" {
			if !creates[i.Key] || created[i.Key].ExternalID != "" || existing[i.Issue.ExternalID] || existingNumbers[i.Issue.Number] {
				return ErrInvalidGraph
			}
			created[i.Key] = i.Issue
		} else if edits[i.Issue.ExternalID] != i.Issue {
			return ErrInvalidGraph
		}
	}
	if len(created) != len(creates) {
		return ErrInvalidGraph
	}
	expected := map[[2]string]bool{}
	resolve := func(ref GraphRef) IssueRef {
		if ref.Issue != nil {
			return *ref.Issue
		}
		return created[ref.Key]
	}
	refs := map[string]IssueRef{}
	for _, d := range g.Dependencies {
		a, b := resolve(d.Issue), resolve(d.Prerequisite)
		expected[[2]string{a.ExternalID, b.ExternalID}] = d.Remove
		refs[a.ExternalID], refs[b.ExternalID] = a, b
	}
	for _, d := range r.Dependencies {
		key := [2]string{d.Issue.ExternalID, d.Prerequisite.ExternalID}
		removed, ok := expected[key]
		if !ok || removed != d.Removed || d.Issue != refs[d.Issue.ExternalID] ||
			d.Prerequisite != refs[d.Prerequisite.ExternalID] {
			return ErrInvalidGraph
		}
		delete(expected, key)
	}
	if len(expected) != 0 {
		return ErrInvalidGraph
	}
	return nil
}
