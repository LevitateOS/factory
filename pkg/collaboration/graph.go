package collaboration

import (
	"encoding/json"
	"errors"
	"strings"
	"unicode/utf8"
)

// GraphIntent is the semantic payload of MutateGraph. Repository, actor,
// authority, deadline and required atomicity belong to OperationIntent. No
// provider-native authorization envelope enters this payload. Unsupported graph
// edits/edges/atomicity must be refused, never simulated with weaker writes.
type GraphIntent struct {
	Creates      []GraphCreate     `json:"creates"`
	Edits        []GraphEdit       `json:"edits"`
	Dependencies []GraphDependency `json:"dependencies"`
}

type GraphCreate struct {
	Key   string `json:"key"`
	Title string `json:"title"`
	Body  string `json:"body"`
}

type IssueContent struct {
	Title    string   `json:"title"`
	Body     string   `json:"body"`
	Revision Revision `json:"revision"`
}

type GraphEdit struct {
	Issue    IssueRef     `json:"issue"`
	Expected IssueContent `json:"expected"`
	Title    string       `json:"title"`
	Body     string       `json:"body"`
}

// GraphRef selects exactly one existing issue or request-local creation key.
type GraphRef struct {
	Issue *IssueRef `json:"issue"`
	Key   string    `json:"key"`
}

// Adds require edge absence; removes require presence. Removing an edge involving
// a request-local creation is invalid. Direction: Issue depends on Prerequisite.
type GraphDependency struct {
	Issue        GraphRef `json:"issue"`
	Prerequisite GraphRef `json:"prerequisite"`
	Remove       bool     `json:"remove"`
}

// GraphReceipt maps request keys and edited issues to provider identities.
// Untouched/delivered issues have no entry. Content revisions are opaque and
// retain provider zero versions explicitly (e.g. "0"), never invented increments.
type GraphReceipt struct {
	Repository   RepositoryID             `json:"repository,string"`
	Actor        ActorID                  `json:"actor,string"`
	Issues       []GraphIssueReceipt      `json:"issues"`
	Dependencies []GraphDependencyReceipt `json:"dependencies"`
}

type GraphIssueReceipt struct {
	Key             string   `json:"key"`
	Issue           IssueRef `json:"issue"`
	ContentRevision Revision `json:"content_revision"`
}

type GraphDependencyReceipt struct {
	Issue        IssueRef `json:"issue"`
	Prerequisite IssueRef `json:"prerequisite"`
	Removed      bool     `json:"removed"`
}

var ErrInvalidGraph = errors.New("invalid collaboration graph contract")

// Validate applies the current Factory graph bounds and repository scope. It
// does not admit the actor, validate acyclicity against a remote graph, or claim
// the provider can implement the requested transaction and conditional writes.
func (g GraphIntent) Validate(repository RepositoryID) error {
	if err := g.ValidateShape(repository); err != nil {
		return err
	}
	raw, err := json.Marshal(g)
	if err != nil || len(raw) > 48<<10 {
		return ErrInvalidGraph
	}
	return nil
}

// ValidateShape checks semantic scope, references and text bounds. Adapters
// use it before enforcing the byte bound on their actual provider encoding;
// expanded neutral references must not reduce an existing provider wire limit.
func (g GraphIntent) ValidateShape(repository RepositoryID) error {
	n := len(g.Creates) + len(g.Edits) + len(g.Dependencies)
	if repository <= 0 || n == 0 || n > 64 {
		return ErrInvalidGraph
	}
	keys := map[string]bool{}
	for _, c := range g.Creates {
		if !validGraphKey(c.Key) || keys[c.Key] || !validIssueText(c.Title, c.Body) {
			return ErrInvalidGraph
		}
		keys[c.Key] = true
	}
	edits := map[string]bool{}
	refs := map[string]IssueRef{}
	numbers := map[int64]string{}
	observe := func(i IssueRef) bool {
		if !validGraphIssue(i, repository) {
			return false
		}
		if previous, ok := refs[i.ExternalID]; ok && previous != i {
			return false
		}
		if previous, ok := numbers[i.Number]; ok && previous != i.ExternalID {
			return false
		}
		refs[i.ExternalID], numbers[i.Number] = i, i.ExternalID
		return true
	}
	for _, e := range g.Edits {
		if !observe(e.Issue) || edits[e.Issue.ExternalID] ||
			!ValidText(string(e.Expected.Revision), 256) || !validIssueText(e.Title, e.Body) ||
			!validStoredIssueText(e.Expected.Title, e.Expected.Body) {
			return ErrInvalidGraph
		}
		edits[e.Issue.ExternalID] = true
	}
	edges := map[string]bool{}
	for _, e := range g.Dependencies {
		if (e.Issue.Issue != nil && !observe(*e.Issue.Issue)) ||
			(e.Prerequisite.Issue != nil && !observe(*e.Prerequisite.Issue)) {
			return ErrInvalidGraph
		}
		a, b := graphRefKey(e.Issue, repository, keys), graphRefKey(e.Prerequisite, repository, keys)
		edge := a + "\x00" + b
		if a == "" || b == "" || a == b || edges[edge] ||
			(e.Remove && (e.Issue.Key != "" || e.Prerequisite.Key != "")) {
			return ErrInvalidGraph
		}
		edges[edge] = true
	}
	return nil
}

func graphRefKey(r GraphRef, repository RepositoryID, keys map[string]bool) string {
	if r.Issue != nil && r.Key == "" && validGraphIssue(*r.Issue, repository) {
		return "id:" + r.Issue.ExternalID
	}
	if r.Issue == nil && keys[r.Key] {
		return "key:" + r.Key
	}
	return ""
}

func validGraphIssue(i IssueRef, repository RepositoryID) bool {
	return i.Kind == SubjectIssue && i.Repository == repository && i.Number > 0 && ValidText(i.ExternalID, 1024)
}

func validGraphKey(s string) bool {
	if s == "" || len(s) > 64 {
		return false
	}
	for _, c := range s {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_') {
			return false
		}
	}
	return true
}

func validStoredIssueText(title, body string) bool {
	return len(title) <= 255 && len(body) <= 64<<10 && utf8.ValidString(title) && utf8.ValidString(body) &&
		!strings.ContainsRune(title, 0) && !strings.ContainsRune(body, 0)
}

func validIssueText(title, body string) bool {
	return title != "" && strings.TrimSpace(title) == title && validStoredIssueText(title, body)
}
