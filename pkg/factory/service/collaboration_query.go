package service

import "github.com/levitateos/factory/pkg/collaboration"

// State selects an open (default), closed or all-state list. Number selects
// any-state detail. Cursors remain scoped provider facts, never identity.
type CollaborationQuery struct {
	Cursor string
	Number int64
	Limit  int
	State  string
}

func (q CollaborationQuery) valid() bool {
	return q.Limit >= 1 && q.Limit <= 50 && q.Number >= 0 &&
		(q.Number == 0 || q.Cursor == "" && q.Limit == 1 && q.State == "") &&
		(q.State == "" || q.State == "open" || q.State == "closed" || q.State == "all") &&
		(q.Cursor == "" || collaboration.ValidText(q.Cursor, 4096))
}

func validCollaborationPage[T any](page collaboration.Page[T], limit int) bool {
	return page.Observation.State == collaboration.ReadAvailable && page.Observation.Revision != "" &&
		page.Items != nil && len(page.Items) <= limit && page.Complete == (page.Next == "")
}
