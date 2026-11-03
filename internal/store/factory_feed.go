package store

import (
	"context"

	"github.com/levitateos/sodaos/internal/factory"
)

// FeedEntryByID addresses the same post identity from the timeline, parent link
// or reply action, including posts projected from canonical workflow records.
func (s *Store) FeedEntryByID(ctx context.Context, id string) (factory.FeedEntry, error) {
	var entry factory.FeedEntry
	err := s.db.QueryRowContext(ctx, `SELECT id,repository,seq,kind,activity,assignment FROM (
 SELECT id,repository,seq,'post' AS kind,'' AS activity,'' AS assignment FROM factory_feed_posts WHERE id=$1 AND NOT COALESCE((data->>'deleted')::boolean,false) AND NOT COALESCE((data->>'revoked')::boolean,false)
 UNION ALL SELECT id,repository,seq,'record','','' FROM factory_assignments WHERE id=$1
 UNION ALL SELECT c.event,a.repository,c.feed_seq,'record','completed',a.id FROM factory_completions c JOIN factory_publications p ON p.data->>'id'=c.publication JOIN factory_assignments a ON a.id=p.assignment WHERE c.event=$1
 UNION ALL SELECT m.publication,a.repository,m.feed_seq,'record','merged',a.id FROM factory_merges m JOIN factory_publications p ON p.data->>'id'=m.publication JOIN factory_assignments a ON a.id=p.assignment WHERE m.publication=$1 AND m.stage='merged'
 UNION ALL SELECT j.snapshot,a.repository,j.feed_seq,'record','judgment',a.id FROM factory_priority_judgments j JOIN factory_publications p ON p.data->>'id'=j.data->'input'->>'publication_id' JOIN factory_assignments a ON a.id=p.assignment WHERE j.snapshot=$1 AND j.data->>'kind'='delivery-v1'
 UNION ALL SELECT j.snapshot,(candidate->'control'->>'repository')::bigint,j.feed_seq,'record','judgment','' FROM factory_priority_judgments j CROSS JOIN LATERAL jsonb_array_elements(CASE WHEN jsonb_typeof(j.data->'candidates')='array' THEN j.data->'candidates' ELSE '[]'::jsonb END) candidate WHERE j.snapshot=$1 AND COALESCE(j.data->>'kind','')<>'delivery-v1' AND candidate->>'id'=j.data->>'selected' AND candidate->'control'->>'repository' ~ '^[1-9][0-9]*$'
) posts LIMIT 1`, id).Scan(&entry.ID, &entry.Repository, &entry.Seq, &entry.Kind, &entry.Activity, &entry.Assignment)
	return entry, err
}

// SubscribedFeed reads canonical assignments, comments and milestones in
// newest-first recording order. Each milestone receives its own key from
// the assignment/comment sequence when recorded, including JEV selections
// whose issue has not dispatched. The exclusive seq cursor is shared by
// all kinds. Reads do not allocate keys or dispatch work. Scope filtering
// happens here and access is checked again by the serving layer.
func (s *Store) SubscribedFeed(ctx context.Context, query factory.FeedQuery) ([]factory.FeedEntry, string, bool, error) {
	if err := query.Validate(); err != nil {
		return nil, "", false, err
	}
	wantsRecords, wantsPosts := false, false
	if len(query.Kinds) == 0 {
		wantsRecords, wantsPosts = true, true
	}
	for _, kind := range query.Kinds {
		if kind == factory.FeedKindRecord {
			wantsRecords = true
		}
		if kind == factory.FeedKindPost {
			wantsPosts = true
		}
	}
	if !wantsRecords && !wantsPosts {
		return nil, "", false, nil
	}
	bound, err := factory.ParseFeedCursor(query.Cursor)
	if err != nil {
		return nil, "", false, err
	}
	rows, err := s.db.QueryContext(ctx, `WITH members AS (
			SELECT seq,id,repository,'record' AS kind,'' AS activity,'' AS assignment
			FROM factory_assignments
			WHERE $1 AND repository = ANY($2) AND ($3 = 0 OR seq < $3)
			AND COALESCE(data->'planning_context'->'IssueMaker','null'::jsonb)='null'::jsonb
			UNION ALL
			SELECT seq,id,repository,'post','','' FROM factory_feed_posts
			WHERE (($4 AND NOT (data ? 'communication')) OR ($1 AND data ? 'communication'))
 AND repository = ANY($2) AND ($3 = 0 OR seq < $3)
			AND COALESCE((data->>'deleted')::boolean,false) = false
			AND COALESCE((data->>'revoked')::boolean,false) = false
			UNION ALL
			SELECT c.feed_seq,c.event,a.repository,'record','completed',a.id
			FROM factory_completions c
			JOIN factory_publications p ON (p.data->>'id')=c.publication
			JOIN factory_assignments a ON a.id=p.assignment
			WHERE $1 AND c.repository=a.repository AND a.repository = ANY($2)
			AND ($3 = 0 OR c.feed_seq < $3)
			UNION ALL
			SELECT m.feed_seq,m.publication,a.repository,'record','merged',a.id
			FROM factory_merges m
			JOIN factory_publications p ON (p.data->>'id')=m.publication
			JOIN factory_assignments a ON a.id=p.assignment
			WHERE $1 AND m.stage='merged' AND a.repository = ANY($2)
			AND ($3 = 0 OR m.feed_seq < $3)
			UNION ALL
			SELECT j.feed_seq,j.snapshot,a.repository,'record','judgment',a.id
			FROM factory_priority_judgments j
			JOIN factory_publications p ON (p.data->>'id')=(j.data->'input'->>'publication_id')
			JOIN factory_assignments a ON a.id=p.assignment
			WHERE $1 AND j.data->>'kind'='delivery-v1' AND a.repository = ANY($2)
			AND ($3 = 0 OR j.feed_seq < $3)
			UNION ALL
			SELECT j.feed_seq,j.snapshot,sel.repo,'record','judgment',COALESCE(a.id,'')
			FROM factory_priority_judgments j
			CROSS JOIN LATERAL (
				SELECT CASE WHEN c->'control'->>'repository' ~ '^[0-9]+$'
					THEN (c->'control'->>'repository')::bigint ELSE 0 END AS repo,
				CASE WHEN c->'control'->>'issue' ~ '^[0-9]+$'
					THEN (c->'control'->>'issue')::bigint ELSE 0 END AS issue
				FROM jsonb_array_elements(CASE WHEN jsonb_typeof(j.data->'candidates')='array' THEN j.data->'candidates' ELSE '[]'::jsonb END) c
				WHERE c->>'id'=j.data->>'selected' LIMIT 1
			) sel
			LEFT JOIN LATERAL (
				SELECT seq,id,repository FROM factory_assignments
				WHERE repository=sel.repo AND issue=sel.issue
				AND data->>'created_unix' ~ '^[0-9]+$' AND j.data->>'created_unix' ~ '^[0-9]+$'
				AND (data->>'created_unix')::bigint >= (j.data->>'created_unix')::bigint
				ORDER BY seq ASC LIMIT 1
			) a ON true
			WHERE $1 AND j.data IS NOT NULL AND COALESCE(j.data->>'kind','')<>'delivery-v1'
			AND sel.repo>0 AND sel.issue>0 AND sel.repo = ANY($2)
			AND ($3 = 0 OR j.feed_seq < $3)
		)
			SELECT m.seq,m.id,m.repository,m.kind,m.activity,m.assignment FROM members m
			LEFT JOIN factory_feed_posts c ON m.kind='post' AND c.id=m.id
			LEFT JOIN factory_assignments a ON m.kind='record' AND a.id=CASE WHEN m.assignment<>'' THEN m.assignment ELSE m.id END
			LEFT JOIN factory_library_items l ON l.id=c.data->'communication'->>'library_item_id'
			LEFT JOIN factory_reassessments rs ON rs.cycle=a.data->'result'->'phase_result'->'output'->'reassessment'->>'cycle_id'
			WHERE ($6 IN ('','all')
			 OR ($6='decisions' AND (c.data->'communication'->>'kind'='record_decision' OR c.data->'communication'->>'component_id' IN ('F03','F04','F11') OR l.kind='decision' OR m.activity='judgment' OR (m.activity='' AND a.data->'result'->'phase_result'->'output' ?| ARRAY['plan','decomposition','reassessment'])))
			 OR ($6='knowledge' AND (c.data->'communication'->>'kind' IN ('learned','record_decision') OR c.data->'communication'->>'component_id' IN ('F03','F04','F11') OR l.id IS NOT NULL OR m.activity='judgment' OR (m.activity='' AND a.data->'result'->'phase_result'->'output' ?| ARRAY['plan','decomposition','reassessment'])))
			 OR ($6='attention' AND ((c.data->'communication'->>'kind'='ask' AND NOT EXISTS(SELECT 1 FROM factory_feed_posts ans JOIN factory_feed_posts res ON res.repository=ans.repository AND res.data->'resolution'->>'post'=ans.id AND res.data->'resolution'->>'resolution'='incorporated' WHERE ans.data->>'parent_id'=c.id)) OR c.data->'communication'->>'kind' IN ('blocked','stuck','flag') OR a.data->>'outcome' IN ('needs-human','failed','blocked')))
			 OR ($6='evidence' AND (m.activity IN ('merged','completed') OR c.data->'communication' ?| ARRAY['evidence_refs','artifacts','source','commits'] OR (m.activity='' AND a.data->'result'->'phase_result'->'output' ?| ARRAY['review','correction','verification','research']))))
			AND ($7='' OR strpos(lower(COALESCE(c.body,'') || ' ' || COALESCE(c.data->'communication'->>'title','') || ' ' || COALESCE(c.data->'communication'->>'decision','') || ' ' || COALESCE(c.data->'communication'->>'rationale','') || ' ' || COALESCE(a.data->'result'->>'summary','') || ' ' || COALESCE(l.data->>'body','')),lower($7))>0)
			AND ($8='' OR ($8='human' AND c.author NOT LIKE 'worker:%' AND c.author<>'factory') OR COALESCE(c.data->>'actor',CASE a.data->>'phase' WHEN 'decompose' THEN 'product_lead' WHEN 'investigate' THEN 'architect' WHEN 'implement' THEN 'builder' WHEN 'review' THEN 'inspector' WHEN 'correct' THEN 'fixer' WHEN 'verify' THEN 'test_pilot' WHEN 'reassess' THEN 'strategist' END,'')=$8)
			AND (NOT $9 OR ((l.id IS NULL OR (NOT l.removed AND COALESCE(l.data->>'state','') NOT IN ('superseded','retracted') AND COALESCE((c.data->'communication'->>'library_revision')::bigint,l.revision)=l.revision)) AND (c.data->'communication'->'intent_source' IS NULL OR EXISTS(SELECT 1 FROM factory_intent_heads h WHERE h.id=c.data->'communication'->'intent_source'->>'intent' AND h.revision=c.data->'communication'->'intent_source'->>'revision'))))
			AND (NOT $9 OR c.data->'communication'->'plan_source' IS NULL OR CASE
			 WHEN EXISTS(SELECT 1 FROM factory_plan_heads h WHERE h.attempt_root=c.data->'communication'->'plan_source'->>'attempt_root')
			 THEN EXISTS(SELECT 1 FROM factory_plan_heads h WHERE h.attempt_root=c.data->'communication'->'plan_source'->>'attempt_root' AND h.plan_id=c.data->'communication'->'plan_source'->>'plan')
			 ELSE NOT EXISTS(SELECT 1 FROM factory_plan_revisions p WHERE p.attempt_root=c.data->'communication'->'plan_source'->>'attempt_root' AND p.generation>(c.data->'communication'->'plan_source'->>'generation')::integer) END)
			-- A reassessment decision record is current until its own cycle is
			-- superseded by an accepted successor retry: the canonical
			-- factory_reassessments lineage judges the record's own cycle, never
			-- the state of the completed predecessor attempt it cites, so a live
			-- successor stays readable beside its superseded predecessor.
			-- Released, live and other-output records stay, and a missing cycle
			-- row or NULL data never proves supersession, so those rows stay too;
			-- the All view without current-only keeps every history row either way.
			AND (NOT $9 OR rs.cycle IS NULL OR rs.state<>'superseded')
			-- A finished decompose/investigate planning record is current until a
			-- newer head supersedes it without adoption. A decompose record stays
			-- when its intent revision is the head, has no head row, or is the
			-- revision (in any state, including a completed draft) a live
			-- accepted head directly cites as its predecessor — adoption is
			-- judged by the live accepted successor's cite, never by the
			-- predecessor's own state; an investigate record stays when its plan is the head,
			-- has no head row, or a live assigned or accepted finished-succeeded
			-- successor attempt still cites the completed record as its parent.
			-- Other phases, unfinished work, missing bindings/outputs and posts
			-- (which never join an assignment) stay; only a proven-superseded
			-- finished planning record without an adopted successor leaves.
			AND (NOT $9 OR a.id IS NULL OR a.stage<>'finished'
			 OR COALESCE(a.data->>'phase','') NOT IN ('decompose','investigate')
			 OR (COALESCE(a.data->>'phase','')='decompose' AND (
			  a.data->'result'->'phase_result'->'output'->'decomposition'->>'intent' IS NULL
			  OR a.data->'result'->'phase_result'->'output'->'decomposition'->>'revision' IS NULL
			  OR NOT EXISTS(SELECT 1 FROM factory_intent_heads h WHERE h.id=a.data->'result'->'phase_result'->'output'->'decomposition'->>'intent')
			  OR EXISTS(SELECT 1 FROM factory_intent_heads h WHERE h.id=a.data->'result'->'phase_result'->'output'->'decomposition'->>'intent' AND h.revision=a.data->'result'->'phase_result'->'output'->'decomposition'->>'revision')
			  OR EXISTS(SELECT 1 FROM factory_intent_heads h JOIN factory_intent_revisions hr ON hr.id=h.revision AND hr.intent=h.id JOIN factory_intent_revisions r ON r.id=a.data->'result'->'phase_result'->'output'->'decomposition'->>'revision' AND r.intent=h.id WHERE h.id=a.data->'result'->'phase_result'->'output'->'decomposition'->>'intent' AND hr.data->>'state'='accepted' AND hr.data->>'predecessor'=r.id)))
			 OR (COALESCE(a.data->>'phase','')='investigate' AND (
			  a.data->'result'->'phase_result'->'output'->'plan'->>'attempt_root' IS NULL
			  OR a.data->'result'->'phase_result'->'output'->'plan'->>'id' IS NULL
			  OR NOT EXISTS(SELECT 1 FROM factory_plan_heads h WHERE h.attempt_root=a.data->'result'->'phase_result'->'output'->'plan'->>'attempt_root')
			  OR EXISTS(SELECT 1 FROM factory_plan_heads h WHERE h.attempt_root=a.data->'result'->'phase_result'->'output'->'plan'->>'attempt_root' AND h.plan_id=a.data->'result'->'phase_result'->'output'->'plan'->>'id')
			  OR EXISTS(SELECT 1 FROM factory_assignments s WHERE s.data->>'parent_assignment'=a.id AND (s.stage='assigned' OR (s.stage='finished' AND s.data->>'outcome'='succeeded'))))))
			ORDER BY m.seq DESC LIMIT $5`, wantsRecords, query.Repositories, bound, wantsPosts, query.Limit+1, query.View, query.Search, query.Role, query.Current)
	if err != nil {
		return nil, "", false, err
	}
	defer func() { _ = rows.Close() }()
	entries := make([]factory.FeedEntry, 0, query.Limit+1)
	for rows.Next() {
		var entry factory.FeedEntry
		if err = rows.Scan(&entry.Seq, &entry.ID, &entry.Repository, &entry.Kind, &entry.Activity, &entry.Assignment); err != nil {
			return nil, "", false, err
		}
		entries = append(entries, entry)
	}
	if err = rows.Err(); err != nil {
		return nil, "", false, err
	}
	page, next, truncated := factory.WindowFeedEntries(entries, query.Limit)
	return page, next, truncated, nil
}
