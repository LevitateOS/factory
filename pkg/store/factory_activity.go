package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
	"time"

	"github.com/levitateos/factory/pkg/factory"
)

// boundActivitySection cuts one kind's appended items to the feed bound
// and folds its truncation into the feed flag. Queries read one row past
// the bound, so a full page means older entries may exist even when some
// rows fail validation.
func boundActivitySection(items []factory.ActivityItem, before, scanned int, truncated bool) ([]factory.ActivityItem, bool) {
	if len(items)-before > factory.ActivityItemBound {
		items = items[:before+factory.ActivityItemBound]
		truncated = true
	}
	return items, truncated || scanned > factory.ActivityItemBound
}

// boundActivityFeed sorts the merged feed newest-first with stable ID
// order and cuts it to the feed bound, reporting whether entries were
// dropped.
func boundActivityFeed(items []factory.ActivityItem) ([]factory.ActivityItem, bool) {
	sort.Slice(items, func(i, j int) bool {
		if items[i].CreatedUnix != items[j].CreatedUnix {
			return items[i].CreatedUnix > items[j].CreatedUnix
		}
		return items[i].ID < items[j].ID
	})
	if len(items) > factory.ActivityItemBound {
		return items[:factory.ActivityItemBound], true
	}
	return items, false
}

func (s *Store) ActivitySettings(ctx context.Context, repository int64) (factory.ActivitySettings, int64, error) {
	settings := factory.DefaultActivitySettings()
	var revision int64
	var raw []byte
	err := s.db.QueryRowContext(ctx, `SELECT revision,data FROM factory_activity_settings WHERE repository=$1`, repository).Scan(&revision, &raw)
	if errors.Is(err, ErrNotFound) {
		return settings, 0, nil
	}
	if err != nil {
		return settings, 0, err
	}
	err = json.Unmarshal(raw, &settings)
	return settings, revision, err
}

func (s *Store) SetActivitySettings(ctx context.Context, repository, actor, expected int64, settings factory.ActivitySettings) error {
	if repository <= 0 || actor <= 0 || expected < 0 || expected == math.MaxInt64 {
		return errors.New("invalid activity settings revision")
	}
	raw, err := json.Marshal(settings)
	if err != nil {
		return err
	}
	query := `UPDATE factory_activity_settings SET revision=revision+1,actor_id=$2,data=$4 WHERE repository=$1 AND revision=$3`
	if expected == 0 {
		query = `INSERT INTO factory_activity_settings(repository,actor_id,revision,data) SELECT $1,$2,1,$4::jsonb WHERE $3::bigint=0 ON CONFLICT DO NOTHING`
	}
	result, err := s.db.ExecContext(ctx, query, repository, actor, expected, string(raw))
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrStaleRevision
	}
	return nil
}

// RepositoryActivity derives bounded local facts. Pending native effects and
// committed merges still awaiting original issue closure never become events.
// Navigation is projected by the admitted host, not the durable facts reader.
func (s *Store) RepositoryActivity(ctx context.Context, repository int64) (factory.RepositoryActivity, error) {
	settings, revision, err := s.ActivitySettings(ctx, repository)
	out := factory.RepositoryActivity{Repository: strconv.FormatInt(repository, 10), Revision: revision, Settings: settings, Items: []factory.ActivityItem{}, ObservedUnix: time.Now().Unix()}
	if err != nil {
		return out, err
	}
	add := func(id, kind string, issue, pr, t int64, summary string) {
		if issue <= 0 || t < 0 {
			return
		}
		p := ""
		if pr > 0 {
			p = strconv.FormatInt(pr, 10)
		}
		if kind == "pr_created" {
			if pr <= 0 {
				return
			}
		}
		out.Items = append(out.Items, factory.ActivityItem{ID: id, Kind: kind, Issue: strconv.FormatInt(issue, 10), PR: p, CreatedUnix: t, Summary: summary})
	}
	if settings.IssueCreated {
		if err := s.scanCreatedIssues(ctx, repository, &out, add); err != nil {
			return out, err
		}
	}
	if settings.PRCreated {
		if err := s.scanCreatedPRs(ctx, repository, &out, add); err != nil {
			return out, err
		}
	}
	if settings.VerifiedMerge {
		if err := s.scanVerifiedMerges(ctx, repository, &out, add); err != nil {
			return out, err
		}
	}
	bounded, dropped := boundActivityFeed(out.Items)
	out.Items, out.Truncated = bounded, out.Truncated || dropped
	return out, nil
}

// scanCreatedIssues appends completed accepted-intent issues to one
// activity feed.
func (s *Store) scanCreatedIssues(ctx context.Context, repository int64, out *factory.RepositoryActivity, add func(id, kind string, issue, pr, t int64, summary string)) error {
	before := len(out.Items)
	rows, e := s.db.QueryContext(ctx, `SELECT data FROM (SELECT DISTINCT ON (t.data->>'issue_id') t.data FROM factory_intent_tasks t JOIN factory_intent_revisions r ON r.id=t.revision JOIN factory_intent_generations g ON g.revision=t.revision WHERE r.data->>'repository'=$1 AND g.data->>'state'='completed' ORDER BY t.data->>'issue_id',t.revision) facts ORDER BY COALESCE((data->>'created_unix')::bigint,0) DESC,data->>'issue_id' LIMIT 61`, strconv.FormatInt(repository, 10))
	if e != nil {
		return e
	}
	scanned := 0
	for rows.Next() {
		scanned++
		var raw []byte
		if e = rows.Scan(&raw); e != nil {
			break
		}
		var b factory.IntentTaskBinding
		if e = json.Unmarshal(raw, &b); e != nil {
			break
		}
		if b.IssueID > 0 && b.AcceptanceID != "" {
			add(fmt.Sprintf("issue:%d", b.IssueID), "issue_created", b.IssueIndex, 0, b.CreatedUnix, fmt.Sprintf("Issue #%d created from accepted product intent.", b.IssueIndex))
		}
	}
	if e = errors.Join(e, rows.Err(), rows.Close()); e != nil {
		return e
	}
	out.Items, out.Truncated = boundActivitySection(out.Items, before, scanned, out.Truncated)
	return nil
}

// scanCreatedPRs appends linked published pull requests to one
// activity feed, deduplicated by native PR identity.
func (s *Store) scanCreatedPRs(ctx context.Context, repository int64, out *factory.RepositoryActivity, add func(id, kind string, issue, pr, t int64, summary string)) error {
	before := len(out.Items)
	rows, e := s.db.QueryContext(ctx, `SELECT data FROM factory_publications WHERE repository=$1 AND stage='published' ORDER BY seq DESC LIMIT 61`, repository)
	if e != nil {
		return e
	}
	seen := map[int64]bool{}
	scanned := 0
	for rows.Next() {
		scanned++
		var raw []byte
		if e = rows.Scan(&raw); e != nil {
			break
		}
		var p factory.Publication
		if e = json.Unmarshal(raw, &p); e != nil {
			break
		}
		if !publishedPRAdmitted(p, repository) || seen[p.PRID] {
			continue
		}
		seen[p.PRID] = true
		add(fmt.Sprintf("pr:%d", p.PRID), "pr_created", p.Issue, p.PRNumber, p.FinishedUnix, fmt.Sprintf("Pull request #%d created for issue #%d.", p.PRNumber, p.Issue))
	}
	if e = errors.Join(e, rows.Err(), rows.Close()); e != nil {
		return e
	}
	out.Items, out.Truncated = boundActivitySection(out.Items, before, scanned, out.Truncated)
	return nil
}

// publishedPRAdmitted reports whether one publication row is a linked
// published pull request in scope.
func publishedPRAdmitted(p factory.Publication, repository int64) bool {
	return p.Outcome == factory.Succeeded && p.Reason == factory.PublishReasonLinked && p.FinishedUnix > 0 &&
		p.Publish.Effect == factory.OpEffectCommitted && p.Publish.Completion == factory.OpCompletionComplete &&
		p.Repository == repository && p.Stage == factory.PublicationPublished &&
		p.PRCreate.Effect == factory.OpEffectCommitted && p.PRCreate.Completion == factory.OpCompletionComplete &&
		p.PRCreate.PRNumber == p.PRNumber && p.PRCreate.PRID == p.PRID && p.PRID > 0 && p.PRNumber > 0
}

// scanVerifiedMerges appends verified merged pull requests to one
// activity feed.
func (s *Store) scanVerifiedMerges(ctx context.Context, repository int64, out *factory.RepositoryActivity, add func(id, kind string, issue, pr, t int64, summary string)) error {
	before := len(out.Items)
	rows, e := s.db.QueryContext(ctx, `SELECT data FROM factory_merges WHERE repository=$1 AND stage='merged' ORDER BY seq DESC LIMIT 61`, repository)
	if e != nil {
		return e
	}
	scanned := 0
	for rows.Next() {
		scanned++
		var raw []byte
		if e = rows.Scan(&raw); e != nil {
			break
		}
		var m factory.Merge
		if e = json.Unmarshal(raw, &m); e != nil {
			break
		}
		if !verifiedMergeAdmitted(m, repository) {
			continue
		}
		add("merge:"+m.ID, "verified_merge", m.Issue, m.PRNumber, m.FinishedUnix, fmt.Sprintf("Pull request #%d merged; original issue #%d closure confirmed.", m.PRNumber, m.Issue))
	}
	if e = errors.Join(e, rows.Err(), rows.Close()); e != nil {
		return e
	}
	out.Items, out.Truncated = boundActivitySection(out.Items, before, scanned, out.Truncated)
	return nil
}

// verifiedMergeAdmitted reports whether one merge row is a verified
// merged pull request in scope with confirmed original closure.
func verifiedMergeAdmitted(m factory.Merge, repository int64) bool {
	return mergeCompletionAdmitted(m) && mergeClosureAdmitted(m) && mergeLinkageAdmitted(m, repository)
}

// mergeCompletionAdmitted reports whether one merge row carries a
// successful merged completion.
func mergeCompletionAdmitted(m factory.Merge) bool {
	return m.ID != "" && m.Outcome == factory.Succeeded && m.Reason == factory.MergeReasonMerged && m.FinishedUnix > 0 && m.ClosedUnix > 0
}

// mergeClosureAdmitted reports whether one merge row carries a valid
// original closing occurrence.
func mergeClosureAdmitted(m factory.Merge) bool {
	occ, err := strconv.ParseInt(m.OriginalClosingOccurrence, 10, 64)
	return err == nil && occ > 0
}

// mergeLinkageAdmitted reports whether one merge row links its
// operation, repository, pull request and commits in scope.
func mergeLinkageAdmitted(m factory.Merge, repository int64) bool {
	return m.Operation.IssueID == m.IssueID && m.IssueID > 0 && m.Repository == repository && m.Stage == factory.MergeMerged &&
		m.Operation.Effect == factory.OpEffectCommitted && m.Operation.Completion == factory.OpCompletionComplete &&
		m.Operation.PRNumber == m.PRNumber && m.Operation.PRID == m.PRID && m.MergedCommit != "" && m.MergedCommit == m.HeadOID &&
		m.PRID > 0 && m.PRNumber > 0 && m.OriginalClosedUnix > 0 && m.MergedUnix > 0
}
