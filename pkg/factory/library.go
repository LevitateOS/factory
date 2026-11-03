package factory

import "errors"

var ErrLibraryQuery = errors.New("invalid library search")

// LibraryQuery carries existing search narrowing. History includes removed
// records; CurrentOnly still excludes removed/superseded/retracted observations.
type LibraryQuery struct {
	Query       string
	Kind        string
	Cursor      string
	History     bool
	CurrentOnly bool
}

func (q LibraryQuery) Validate(repository int64) error {
	if repository < 1 || len(q.Query) > 512 || (q.Cursor != "" && !ValidID(q.Cursor)) || (q.Kind != "" && q.Kind != "knowledge" && q.Kind != "decision") {
		return ErrLibraryQuery
	}
	return nil
}

// LibraryReadGuidance is included in newly rendered phase prompts and the
// admitted context read.
const LibraryReadGuidance = "You can search and read the project library with search_library and read_library_item.\n"

// LibraryItem is a project-scoped observation or recorded decision.
type LibraryItem struct {
	ID           string            `json:"id"`
	Repository   int64             `json:"repository"`
	Revision     int64             `json:"revision"`
	Kind         string            `json:"kind"`
	Title        string            `json:"title"`
	Body         string            `json:"body"`
	Scope        string            `json:"scope"`
	EvidenceRefs []string          `json:"evidence_refs"`
	Decision     string            `json:"decision,omitempty"`
	Rationale    string            `json:"rationale,omitempty"`
	Alternatives []string          `json:"alternatives,omitempty"`
	OwnerRole    string            `json:"owner_role"`
	Assignment   string            `json:"assignment"`
	SourcePost   string            `json:"source_post"`
	State        string            `json:"state"`
	Removed      bool              `json:"removed"`
	Replacements []string          `json:"replacements,omitempty"`
	Relations    []LibraryRelation `json:"relations,omitempty"`
	DerivedFrom  []string          `json:"derived_from,omitempty"`
	UpdatedAt    int64             `json:"updated_at"`
}
type LibraryRelation struct {
	Kind     string   `json:"kind"`
	Target   string   `json:"target"`
	Evidence []string `json:"evidence,omitempty"`
}
type LibraryRevision struct {
	Item   LibraryItem `json:"item"`
	Reason string      `json:"reason"`
	Actor  string      `json:"actor"`
}
type LibraryPage struct {
	Items      []LibraryItem `json:"items"`
	NextCursor string        `json:"next_cursor,omitempty"`
	Complete   bool          `json:"complete"`
}
type LibraryRead struct {
	Item    LibraryItem       `json:"item"`
	History []LibraryRevision `json:"history"`
}

func MCPIsLibraryRead(tool MCPTool) bool {
	switch tool {
	case MCPToolSearchLibrary, MCPToolReadLibraryItem:
		return true
	}
	return false
}
