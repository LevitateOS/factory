package control

import (
	"context"
	"errors"
	"time"

	"github.com/levitateos/factory/pkg/factory"
	"github.com/levitateos/factory/pkg/store"
)

// CreationRecoverySource observes original issues under an enrolled actor.
// Positions belong to the source: retained host task sequence or GitHub issue
// number. They imply neither complete provider event history nor authority.
type CreationRecoverySource interface {
	ReadCreations(ctx context.Context, repository, after int64, limit int) (factory.CreationRecoveryBatch, error)
}

const creationRecoveryPageSize = 16

const idleCreationPollSeconds = 60

// This is scheduling metadata, never a history cursor or admission. A new
// policy or runtime owner polls immediately; restart still resumes the durable
// original-creation cursor. Unix time also includes time spent suspended.
type creationIdlePoll struct {
	position, policyRevision, notBefore int64
}

var ErrCreationProvenanceIncomplete = errors.New("retained native creation provenance is incomplete")

// recoverCreations shares the serving owner and its absolute RPC budget. One
// repository page per pass keeps unavailable or full repositories from starving
// later repositories. Native retention is a boundary, not universal history.
func (c *Coordinator) recoverCreations(ctx context.Context) error {
	if c.Creations == nil {
		return nil
	}
	repository, err := c.nextCreationRepository(ctx)
	if err != nil {
		return err
	}
	if repository == 0 {
		return nil
	}
	after, err := c.Store.CreationRecoveryCursor(ctx, repository)
	if err != nil {
		return err
	}
	policy, err := c.Store.RepositoryPolicy(ctx, repository)
	if err != nil {
		return err
	}
	if !policy.Enabled || policy.Paused {
		delete(c.creationIdlePolls, repository)
		return nil
	}
	now := time.Now().Unix()
	if poll, ok := c.creationIdlePolls[repository]; ok {
		if poll.position == after && poll.policyRevision == policy.Revision && poll.notBefore > now && poll.notBefore <= now+idleCreationPollSeconds {
			return nil
		}
		delete(c.creationIdlePolls, repository)
	}
	if !readinessBudgetFrom(ctx).reserveNativeRPC(1) {
		return ErrReadinessPassPending
	}
	page, next, err := c.readCreationPage(ctx, repository, after)
	if err != nil {
		return err
	}
	works, err := foldCreationObservations(page, repository, after, next)
	if err != nil {
		return err
	}
	// Unsupported or redacted tasks must not disappear behind the cursor.
	// Keep the known observations durable, but retain this bounded page until
	// its original provenance can be corroborated. ReachedRetainedEnd names the
	// retained scan end and does not change this provenance requirement.
	checkpoint := next
	if !page.ProvenanceComplete {
		checkpoint = after
	}
	if err := c.Store.EnqueueReadinessCreationPage(ctx, repository, after, checkpoint, works, policy.Revision); err != nil {
		return err
	}
	if !page.ProvenanceComplete {
		return ErrCreationProvenanceIncomplete
	}
	// Only a successful, complete idle scan waits. Backlog pages, unresolved
	// provenance and failed I/O keep their existing bounded recovery behavior.
	if page.ReachedRetainedEnd && len(page.Events) == 0 {
		if c.creationIdlePolls == nil {
			c.creationIdlePolls = make(map[int64]creationIdlePoll)
		}
		c.creationIdlePolls[repository] = creationIdlePoll{position: checkpoint, policyRevision: policy.Revision, notBefore: time.Now().Unix() + idleCreationPollSeconds}
	}
	return nil
}

// nextCreationRepository advances the creation recovery cursor across
// repositories, wrapping to the start when the cursor runs out.
func (c *Coordinator) nextCreationRepository(ctx context.Context) (int64, error) {
	repositories, err := c.Store.CreationRecoveryRepositories(ctx, c.creationRepositoryCursor, 1)
	if err != nil {
		return 0, err
	}
	if len(repositories) == 0 {
		c.creationRepositoryCursor = 0
		repositories, err = c.Store.CreationRecoveryRepositories(ctx, 0, 1)
		if err != nil || len(repositories) == 0 {
			return 0, err
		}
	}
	c.creationRepositoryCursor = repositories[0]
	return repositories[0], nil
}

// readCreationPage reads and bounds one creation recovery page behind
// the cursor.
func (c *Coordinator) readCreationPage(ctx context.Context, repository, after int64) (factory.CreationRecoveryBatch, int64, error) {
	var empty factory.CreationRecoveryBatch
	page, err := c.Creations.ReadCreations(ctx, repository, after, creationRecoveryPageSize)
	if err != nil {
		return empty, 0, err
	}
	if page.Repository != repository || len(page.Events) > creationRecoveryPageSize || !page.RetentionLimited {
		return empty, 0, errors.New("invalid native creation recovery page")
	}
	if page.NextPosition < after || page.OldestRetainedPosition < 0 {
		return empty, 0, errors.New("invalid native creation recovery cursor")
	}
	return page, page.NextPosition, nil
}

// foldCreationObservations validates one page's observations in task
// order into readiness work.
func foldCreationObservations(page factory.CreationRecoveryBatch, repository, after, next int64) ([]store.ReadinessWork, error) {
	works := make([]store.ReadinessWork, 0, len(page.Events))
	last := after
	for _, observation := range page.Events {
		work, err := foldCreationObservation(observation, repository, &last, next)
		if err != nil {
			return nil, err
		}
		works = append(works, work)
	}
	return works, nil
}

// foldCreationObservation validates one creation observation against
// the scan window into readiness work.
func foldCreationObservation(observation factory.OriginalCreationEvent, repository int64, last *int64, next int64) (store.ReadinessWork, error) {
	var empty store.ReadinessWork
	task := observation.Position
	if task <= *last || task > next || observation.Repository != repository {
		return empty, errors.New("invalid native creation task identity")
	}
	*last = task
	if observation.Issue <= 0 || observation.OriginalIssueID <= 0 || observation.Creator <= 0 {
		return empty, errors.New("invalid native creation identity")
	}
	hint := IntakeHint{
		Delivery: observation.Delivery, Repository: repository, Issue: observation.Issue,
		Created: true, Creator: observation.Creator, CreatorWrite: observation.CreatorWrite, CreationVerified: observation.Verified,
		NativeIssueID: observation.OriginalIssueID, CreatedUnix: observation.CreatedUnix, TitleDigest: observation.TitleDigest, ContentDigest: observation.ContentDigest,
	}
	if err := hint.Validate(); err != nil {
		return empty, err
	}
	return readinessCreationWork(hint), nil
}
