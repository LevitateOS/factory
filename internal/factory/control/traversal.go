package control

import (
	"sync"

	"github.com/levitateos/sodaos/internal/factory"
)

// DispatchQueueCursor keeps bounded dispatch passes moving through the
// deterministic oldest-first queue rather than revisiting its prefix.

// DispatchQueueCursor is the fair-rotation position inside the
// deterministic oldest-first queued listing. A nil cursor disables
// rotation: the pass visits from the beginning and advances nothing.
type DispatchQueueCursor struct {
	mu        sync.Mutex
	set       bool
	firstSeen int64
	repo      int64
	issue     int64
}

// NewDispatchQueueCursor returns a cursor starting at the beginning of
// the queued listing.
func NewDispatchQueueCursor() *DispatchQueueCursor { return &DispatchQueueCursor{} }

// start reports the queued query position: the exclusive cursor when
// rotation already advanced, otherwise the beginning.
func (q *DispatchQueueCursor) start() (firstSeen, repo, issue int64, hasCursor bool) {
	if q == nil {
		return 0, 0, 0, false
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	if !q.set {
		return 0, 0, 0, false
	}
	return q.firstSeen, q.repo, q.issue, true
}

// advance continues after the last visited control, or restarts at the
// beginning once a pass observed the end of the listing.
func (q *DispatchQueueCursor) advance(last factory.IssueControl, complete bool) {
	if q == nil {
		return
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	if complete {
		q.set = false
		return
	}
	q.set, q.firstSeen, q.repo, q.issue = true, last.FirstSeenUnix, last.Repository, last.Issue
}

// reset restarts rotation at the beginning of the listing.
func (q *DispatchQueueCursor) reset() {
	if q == nil {
		return
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	q.set = false
}
