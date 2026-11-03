package service

import (
	"context"

	"github.com/levitateos/factory/pkg/factory"
	"github.com/levitateos/factory/pkg/store"
)

// Parentage is causal context, independent of whether a post is shown in the
// main feed or in a conversation. Only recorded relationships supply a parent.
func EnrichParents(ctx context.Context, st *store.Store, entries []factory.FeedEntry) {
	if st == nil {
		return
	}
	for i := range entries {
		entry := &entries[i]
		if entry.Kind == factory.FeedKindRecord {
			if entry.Activity != "" && entry.Assignment != entry.ID {
				entry.ParentID = entry.Assignment
			} else if assignment, err := st.Assignment(ctx, entry.ID); err == nil {
				entry.ParentID = assignment.ParentAssignment
				if entry.ParentID == "" && assignment.PlanningContext != nil && assignment.PlanningContext.IssueMaker != nil {
					entry.ParentID = assignment.PlanningContext.IssueMaker.SourcePost
				}
			}
		}
		if entry.ParentID == "" {
			continue
		}
		if parent, err := st.FeedPost(ctx, entry.ParentID); err == nil && parent.Record.Repository == entry.Repository && !parent.Record.Deleted && !parent.Record.Revoked {
			entry.ParentAuthor = parent.AuthorName
			entry.ParentActor = parent.Actor
			if entry.ParentActor == "" {
				entry.ParentActor = parent.Record.Author
			}
			entry.ParentIssue = parent.Record.Issue
		} else if parent, err := st.FeedEntryByID(ctx, entry.ParentID); err == nil && parent.Repository == entry.Repository {
			parents := []factory.FeedEntry{parent}
			EnrichRecords(ctx, st, nil, parents)
			entry.ParentAuthor = parents[0].Author
			entry.ParentActor = parents[0].Actor
			entry.ParentIssue = parents[0].Issue
		}
	}
}

// EnrichRecords fills the server-derived record enrichment on one
// served window: assignment records from the canonical assignment
// owner (phase profile and persona, typed result payload) and
// Factory/Jev milestone entries from their merge, completion and
// judgment owners. Project fills from the visibility recheck even
// when detail lookup is unavailable; every other field stays empty
// rather than guessed.
func EnrichRecords(ctx context.Context, st *store.Store, projects map[int64]string, entries []factory.FeedEntry) {
	var ids []string
	var milestones []factory.FeedEntry
	for i, entry := range entries {
		if entry.Kind != factory.FeedKindRecord {
			continue
		}
		entries[i].Project = projects[entry.Repository]
		if entry.Activity == "" {
			ids = append(ids, entry.ID)
		} else {
			milestones = append(milestones, entry)
		}
	}
	if st == nil {
		return
	}
	details := make(map[string]store.FeedRecordDetail)
	if len(ids) > 0 {
		if found, err := st.FeedRecordDetails(ctx, ids); err == nil {
			for id, detail := range found {
				details[id] = detail
			}
		}
	}
	if len(milestones) > 0 {
		if found, err := st.FeedMilestoneDetails(ctx, milestones); err == nil {
			for id, detail := range found {
				details[id] = detail
			}
		}
	}
	for i, entry := range entries {
		if entry.Kind != factory.FeedKindRecord {
			continue
		}
		detail, ok := details[entry.ID]
		if !ok {
			continue
		}
		entries[i].Actor = detail.Actor
		entries[i].Author = detail.Author
		entries[i].At = detail.At
		entries[i].Status = detail.Status
		entries[i].Issue = detail.Issue
		entries[i].PR = detail.PR
		entries[i].Ref = detail.Ref
		entries[i].Summary = detail.Summary
		entries[i].Payload = detail.Payload
	}
}

// EnrichPosts fills the server-derived post enrichment on one
// served window from the canonical comment owners: author identity,
// display name, project, time and pipeline status. Records pass
// through for their own canonical producers (Lane A role/effect
// results). Closed sources never reach this point; the store already
// filters them.
func EnrichPosts(ctx context.Context, st *store.Store, projects map[int64]string, entries []factory.FeedEntry) {
	var ids []string
	for _, entry := range entries {
		if entry.Kind == factory.FeedKindPost {
			ids = append(ids, entry.ID)
		}
	}
	if len(ids) == 0 || st == nil {
		return
	}
	details, err := st.FeedPostDetails(ctx, ids)
	if err != nil {
		return
	}
	for i, entry := range entries {
		if entry.Kind != factory.FeedKindPost {
			continue
		}
		detail, ok := details[entry.ID]
		if !ok {
			continue
		}
		entries[i].Actor = detail.Record.Author
		entries[i].ParentID = detail.Record.ParentID
		if detail.Actor != "" {
			entries[i].Actor = detail.Actor
		}
		entries[i].Assignment = detail.Assignment
		entries[i].EventIdentity = detail.EventIdentity
		entries[i].Issue = detail.Record.Issue
		entries[i].Payload = detail.Payload
		if detail.Payload != nil && detail.Payload.Communication != nil {
			entries[i].Activity = detail.Payload.Communication.Kind
		}
		entries[i].Author = detail.AuthorName
		entries[i].Project = projects[entry.Repository]
		entries[i].At = detail.At
		entries[i].Status = detail.Status
		entries[i].Summary = store.TruncateFeedSummary(detail.Body)
	}
}
