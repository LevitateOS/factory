package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/levitateos/factory/pkg/collaboration"
	"github.com/levitateos/factory/pkg/factory"
)

// Project summary bounds: rows per read, roles per row, waiting items per row.
const (
	ProjectSummaryLimit = 50
	ProjectWorkingLimit = 8
	ProjectNeedsYouCap  = 99
)

// ProjectSummary is one repository a profile connection has opened, with
// retained Factory facts as of the read. It is never access evidence.
// Dispatch is open or closed from the recorded gate, or none when no gate
// was ever recorded. NeedsYou counts feedNeedsYouSQL up to its cap.
type ProjectSummary struct {
	Repository collaboration.RepositoryID `json:"repository,string"`
	Name       string                     `json:"name"`
	Connection collaboration.ConnectionID `json:"connection"`
	Dispatch   string                     `json:"dispatch"`
	Working    []ProjectWork              `json:"working"`
	NeedsYou   int                        `json:"needs_you"`
	LatestPost *ProjectPost               `json:"latest_post"`
}

// ProjectWork is one assigned role with its issue, zero for issue-less work.
type ProjectWork struct {
	Role  factory.MCPProfile `json:"role"`
	Issue int64              `json:"issue"`
}

// ProjectPost carries the feed's post fields: Author is the recorded display
// name, Actor the role or author key, At Unix seconds.
type ProjectPost struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	Author string `json:"author,omitempty"`
	Actor  string `json:"actor"`
	At     int64  `json:"at,omitempty"`
}

// ProjectSummaries reads, in one statement, the repositories this profile's
// connections have opened, most recently posted first. It performs no
// provider I/O and grants no authority; the caller admits the profile.
func (s *Store) ProjectSummaries(ctx context.Context, profile collaboration.ProfileID, limit int) ([]ProjectSummary, time.Time, error) {
	if !collaboration.ValidID(string(profile)) || limit < 1 || limit > ProjectSummaryLimit {
		return nil, time.Time{}, errors.New("invalid project summary scope")
	}
	rows, err := s.db.QueryContext(ctx, `WITH clock AS (SELECT now() AS as_of), projects AS (
 SELECT r.id,r.name,rc.connection,
  CASE WHEN d.repository IS NULL THEN 'none' WHEN d.open THEN 'open' ELSE 'closed' END AS dispatch,
  COALESCE(w.items,'[]'::jsonb) AS working,
  (SELECT count(*) FROM (`+feedNeedsYouSQL("r.id", "$3")+` LIMIT $5) waiting) AS needs_you,
  lp.id AS post,lp.seq,lp.title,lp.author,lp.actor,lp.at
 FROM collaboration_connections c
 JOIN collaboration_repository_connections rc ON rc.connection=c.id AND rc.host=c.host
 JOIN collaboration_repositories r ON r.id=rc.repository AND r.host=rc.host
 LEFT JOIN factory_dispatch d ON d.repository=r.id
 LEFT JOIN LATERAL (SELECT jsonb_agg(jsonb_build_object('phase',a.phase,'issue_maker',a.issue_maker,'issue',a.issue) ORDER BY a.seq) AS items
  FROM (SELECT seq,COALESCE(data->>'phase','') AS phase,COALESCE(data->'planning_context'->'IssueMaker','null'::jsonb)<>'null'::jsonb AS issue_maker,issue
   FROM factory_assignments WHERE repository=r.id AND stage=$4 ORDER BY seq LIMIT $6) a) w ON true
 LEFT JOIN LATERAL (SELECT p.id,p.seq,COALESCE(NULLIF(p.data->'communication'->>'title',''),p.body) AS title,
  COALESCE(p.data->>'author_name','') AS author,COALESCE(NULLIF(p.data->>'actor',''),p.author) AS actor,COALESCE((p.data->>'at')::bigint,0) AS at
  FROM factory_feed_posts p WHERE p.repository=r.id
  AND NOT COALESCE((p.data->>'deleted')::boolean,false) AND NOT COALESCE((p.data->>'revoked')::boolean,false)
  ORDER BY p.seq DESC LIMIT 1) lp ON true
 WHERE c.profile=$1
 ORDER BY lp.seq DESC NULLS LAST,r.name,r.id,rc.connection LIMIT $2)
SELECT clock.as_of,p.id,p.name,p.connection,p.dispatch,p.working,p.needs_you,p.post,p.title,p.author,p.actor,p.at
FROM clock LEFT JOIN projects p ON true ORDER BY p.seq DESC NULLS LAST,p.name,p.id,p.connection`,
		profile, limit, deliveryCarriedOn, factory.AssignmentAssigned, ProjectNeedsYouCap, ProjectWorkingLimit)
	if err != nil {
		return nil, time.Time{}, err
	}
	defer func() { _ = rows.Close() }()
	var asOf time.Time
	summaries := []ProjectSummary{}
	for rows.Next() {
		var repository sql.NullInt64
		var name, connection, dispatch, post, title, author, actor sql.NullString
		var working []byte
		var needs sql.NullInt64
		var at sql.NullInt64
		if err := rows.Scan(&asOf, &repository, &name, &connection, &dispatch, &working, &needs, &post, &title, &author, &actor, &at); err != nil {
			return nil, time.Time{}, err
		}
		if !repository.Valid {
			continue
		}
		var items []struct {
			Phase      string `json:"phase"`
			IssueMaker bool   `json:"issue_maker"`
			Issue      int64  `json:"issue"`
		}
		if err := json.Unmarshal(working, &items); err != nil {
			return nil, time.Time{}, err
		}
		summary := ProjectSummary{Repository: collaboration.RepositoryID(repository.Int64), Name: name.String,
			Connection: collaboration.ConnectionID(connection.String), Dispatch: dispatch.String, NeedsYou: int(needs.Int64), Working: []ProjectWork{}}
		for _, item := range items {
			assignment := factory.Assignment{Phase: item.Phase}
			if item.IssueMaker {
				assignment.PlanningContext = &factory.SemanticPlannerInput{IssueMaker: &factory.IssueMakerScope{}}
			}
			summary.Working = append(summary.Working, ProjectWork{Role: factory.MCPProfileOfAssignment(assignment), Issue: item.Issue})
		}
		if post.Valid {
			summary.LatestPost = &ProjectPost{ID: post.String, Title: TruncateFeedSummary(title.String), Author: author.String, Actor: actor.String, At: at.Int64}
		}
		summaries = append(summaries, summary)
	}
	if err := rows.Err(); err != nil {
		return nil, time.Time{}, err
	}
	return summaries, asOf, nil
}
