package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/levitateos/sodaos/internal/factory"
)

// Discovery and role selection must apply the same bounded, admitted scope
// before their limits. Otherwise recent sources without an eligible role can
// hide an older source indefinitely. Raw worker prompts are not scope evidence.
const contributionRoleScopeSQL = `LEFT(BTRIM(COALESCE(a.data->'accepted_context'->>'title','')||E'\n'||
 COALESCE(a.data->'accepted_context'->>'body',a.data->'planning_context'->'head'->>'objective',
 (SELECT i.data->>'objective' FROM factory_intent_revisions i WHERE i.id=a.data->'subject'->'delivery'->>'accepted_revision'),''),E' \t\r\n'),4096)`

func contributionReplyProfiles() []string {
	profiles := []string{}
	for _, profile := range factory.MCPProfiles {
		if factory.MCPToolAdmitted(profile, factory.MCPToolReply) {
			profiles = append(profiles, string(profile))
		}
	}
	return profiles
}

// ContributionSources is a bounded read for the existing service continuation.
// System receipts, consultation comments and worker replies cannot recursively
// trigger invitations. Assignment records with a recorded result are eligible
// alongside human and worker posts. Submitted work posts stay in their
// assignment's discussion; completed results are invited through the record leg.
// A record whose reassessment cycle was superseded by an accepted successor
// retry is replaced history, never a source; that currentness predicate, the
// role-scope predicate and the source-profile exclusion (the record's own
// serving profile can never be its invited role, mirroring the post leg)
// all apply before the per-leg limits, and the merged window keeps each
// non-empty leg's newest so neither leg starves.
// Reading the feed itself never invokes this driver.
func (s *Store) ContributionSources(ctx context.Context) ([]string, error) {
	type candidate struct {
		id     string
		ts     int64
		record bool
	}
	candidates := []candidate{}
	now := time.Now()
	rows, err := s.db.QueryContext(ctx, `SELECT p.id,COALESCE((p.data->>'at')::bigint,0) FROM factory_feed_posts p
 WHERE NOT COALESCE((p.data->>'deleted')::boolean,false) AND NOT COALESCE((p.data->>'revoked')::boolean,false)
 AND NOT p.data ? 'consultation' AND NOT p.data ? 'contribution'
 AND (COALESCE(p.data->>'actor','')<>'factory' AND p.author<>'factory' OR p.data->'communication'->>'component_id'='F11')
 AND (p.author NOT LIKE 'worker:%' OR COALESCE(p.data->>'parent_id','')='')
 AND COALESCE((p.data->>'at')::bigint,0)>$1
 AND EXISTS(SELECT 1 FROM factory_mcp_capabilities c JOIN factory_assignments a ON a.id=c.assignment AND a.run=c.run
 JOIN factory_runs r ON r.id=c.run WHERE c.repository=p.repository AND c.state='active' AND c.deadline>$2
 AND c.profile<>COALESCE(p.data->>'actor','') AND c.profile=ANY($3)
 AND `+contributionRoleScopeSQL+`<>'' AND a.stage='assigned' AND r.active AND NOT r.settled)
 AND NOT EXISTS(SELECT 1 FROM factory_feed_posts r WHERE r.author='factory' AND r.repository=p.repository
 AND r.idempotency='invite:'||p.id||':'||p.revision::text)
 ORDER BY p.seq DESC LIMIT 8`, now.Add(-24*time.Hour).Unix(), now.Unix(), contributionReplyProfiles())
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var c candidate
		if err := rows.Scan(&c.id, &c.ts); err != nil {
			rows.Close()
			return nil, err
		}
		candidates = append(candidates, c)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	recordScopeSQL := `LEFT(BTRIM(COALESCE(e.data->'accepted_context'->>'title','')||E'\n'||
 COALESCE(e.data->'accepted_context'->>'body',e.data->'planning_context'->'head'->>'objective',
 (SELECT i.data->>'objective' FROM factory_intent_revisions i WHERE i.id=e.data->'subject'->'delivery'->>'accepted_revision'),''),E' \t\r\n'),4096)`
	recordRows, err := s.db.QueryContext(ctx, `SELECT a.id,COALESCE((a.data->>'finished_unix')::bigint,(a.data->>'created_unix')::bigint,0) FROM factory_assignments a
 WHERE a.stage='finished' AND a.data ? 'result' AND a.data->'result' IS NOT NULL
 AND COALESCE((a.data->>'finished_unix')::bigint,(a.data->>'created_unix')::bigint,0)>$1
 AND EXISTS(SELECT 1 FROM factory_mcp_capabilities c JOIN factory_assignments e ON e.id=c.assignment AND e.run=c.run
 JOIN factory_runs r ON r.id=c.run WHERE c.repository=a.repository AND c.state='active' AND c.deadline>$2
 AND c.profile=ANY($3) AND c.profile<>CASE a.data->>'phase' WHEN 'decompose' THEN 'product_lead' WHEN 'investigate' THEN 'architect' WHEN 'implement' THEN 'builder' WHEN 'review' THEN 'inspector' WHEN 'correct' THEN 'fixer' WHEN 'verify' THEN 'test_pilot' WHEN 'reassess' THEN 'strategist' WHEN 'repair' THEN 'strategist' ELSE '' END AND `+recordScopeSQL+`<>'' AND e.stage='assigned' AND r.active AND NOT r.settled)
 AND NOT EXISTS(SELECT 1 FROM factory_reassessments rs WHERE rs.cycle=a.data->'result'->'phase_result'->'output'->'reassessment'->>'cycle_id' AND rs.state='superseded')
 AND NOT EXISTS(SELECT 1 FROM factory_feed_posts r WHERE r.author='factory' AND r.repository=a.repository
 AND r.idempotency='invite:'||a.id||':'||a.revision::text)
 ORDER BY a.seq DESC LIMIT 8`, now.Add(-24*time.Hour).Unix(), now.Unix(), contributionReplyProfiles())
	if err != nil {
		return nil, err
	}
	defer recordRows.Close()
	for recordRows.Next() {
		var c candidate
		if err := recordRows.Scan(&c.id, &c.ts); err != nil {
			return nil, err
		}
		c.record = true
		candidates = append(candidates, c)
	}
	if err := recordRows.Err(); err != nil {
		return nil, err
	}
	for i := 1; i < len(candidates); i++ {
		for j := i; j > 0 && candidates[j].ts > candidates[j-1].ts; j-- {
			candidates[j], candidates[j-1] = candidates[j-1], candidates[j]
		}
	}
	ids := make([]string, 0, 8)
	for _, c := range candidates {
		if len(ids) == 8 {
			break
		}
		ids = append(ids, c.id)
	}
	// Newest-first order must not let one leg starve the other: eight
	// newer eligible posts would otherwise hide an older eligible
	// record from every pass indefinitely. When both legs hold
	// candidates but the window holds only one leg, the missing leg's
	// newest candidate takes the oldest slot. Windows holding both
	// legs, or eight or fewer candidates, keep their newest-first
	// order untouched.
	for _, leg := range []bool{true, false} {
		newest := ""
		for _, c := range candidates {
			if c.record == leg {
				newest = c.id
				break
			}
		}
		if newest == "" {
			continue
		}
		represented := false
		for _, id := range ids {
			if id == newest {
				represented = true
				break
			}
			for _, c := range candidates {
				if c.id == id && c.record == leg {
					represented = true
					break
				}
			}
			if represented {
				break
			}
		}
		if !represented && len(ids) > 0 {
			ids[len(ids)-1] = newest
		}
	}
	return ids, nil
}

func (s *Store) ContributionContext(ctx context.Context, id string) (factory.ContributionReview, error) {
	var review factory.ContributionReview
	if source, err := s.FeedPost(ctx, id); err == nil {
		isDecision := source.Payload != nil && source.Payload.Communication != nil && source.Payload.Communication.ComponentID == "F11"
		if source.Record.Deleted || source.Record.Revoked || source.Consultation != nil || (source.Actor == "factory" && !isDecision) {
			return review, ErrNotFound
		}
		review = factory.ContributionReview{
			Source: id, Revision: source.Record.Revision, Repository: source.Record.Repository,
			Body: source.Body, SourceActor: source.Actor, State: "pending", Replies: []factory.ContributionReply{},
			PolicyVersion: "single-role-discussion-v1", ProbabilityThreshold: factory.ContributionProbability, ConfidenceThreshold: factory.ContributionConfidence,
		}
		if source.Source != nil {
			review.Context = TruncateFeedSummary(source.Source.Summary)
		}
		if source.Record.ParentID != "" {
			if parent, err := s.FeedPost(ctx, source.Record.ParentID); err == nil && parent.Record.Repository == review.Repository && !parent.Record.Deleted && !parent.Record.Revoked {
				review.Context = parent.Body
			}
		}
		return s.finishContributionContext(ctx, review, factory.PostSourcePost)
	}
	return s.contributionRecordContext(ctx, id)
}

// contributionRecordContext builds the JEV review for a real assignment-record
// source through the canonical record owners: ResolvePostSource supplies
// the persisted summary/status, FeedRecordDetails supplies the serving actor,
// and the original record thread supplies existing replies. No separate
// authored post is created; the record itself is the source.
func (s *Store) contributionRecordContext(ctx context.Context, id string) (factory.ContributionReview, error) {
	var review factory.ContributionReview
	var repository, revision int64
	var stage string
	var data []byte
	if err := s.db.QueryRowContext(ctx, `SELECT repository,revision,stage,data FROM factory_assignments WHERE id=$1`, id).
		Scan(&repository, &revision, &stage, &data); err != nil {
		return review, err
	}
	if stage != factory.AssignmentFinished {
		return review, ErrNotFound
	}
	var assignment factory.Assignment
	if err := json.Unmarshal(data, &assignment); err != nil {
		return review, err
	}
	if assignment.Repository != repository || assignment.Result == nil {
		return review, ErrNotFound
	}
	source, err := s.ResolvePostSource(ctx, repository, factory.PostSourceRecord, id)
	if err != nil {
		return review, err
	}
	details, err := s.FeedRecordDetails(ctx, []string{id})
	if err != nil {
		return review, err
	}
	detail, ok := details[id]
	if !ok {
		return review, ErrNotFound
	}
	body := detail.Summary
	if body == "" && source.Summary != "" {
		body = source.Summary
	}
	if body == "" {
		return review, ErrNotFound
	}
	review = factory.ContributionReview{
		Source: id, SourceKind: factory.PostSourceRecord, Revision: revision, Repository: repository,
		Body: TruncateFeedSummary(body), SourceActor: detail.Actor, State: "pending", Replies: []factory.ContributionReply{},
		PolicyVersion: "single-role-discussion-v1", ProbabilityThreshold: factory.ContributionProbability, ConfidenceThreshold: factory.ContributionConfidence,
	}
	return s.finishContributionContext(ctx, review, factory.PostSourceRecord)
}

func (s *Store) finishContributionContext(ctx context.Context, review factory.ContributionReview, kind string) (factory.ContributionReview, error) {
	var err error
	review.RepliesVersion, err = contributionRepliesVersion(ctx, s.db, review)
	if err != nil {
		return review, err
	}
	ids, _, err := s.FeedReplyIDs(ctx, review.Repository, kind, review.Source, 0)
	if err != nil {
		return review, err
	}
	details, err := s.FeedPostDetails(ctx, ids)
	if err != nil {
		return review, err
	}
	for _, id := range ids {
		if reply, ok := details[id]; ok && reply.Actor != "factory" && reply.Consultation == nil {
			review.Replies = append(review.Replies, factory.ContributionReply{Actor: reply.Actor, Body: TruncateFeedSummary(reply.Body)})
		}
	}
	version, err := contributionRepliesVersion(ctx, s.db, review)
	if err != nil {
		return review, err
	}
	if version != review.RepliesVersion {
		return review, ErrStaleRevision
	}
	review.Roles, err = s.contributionRoles(ctx, review)
	if err != nil {
		return review, err
	}
	return review, nil
}

func (s *Store) contributionRoles(ctx context.Context, review factory.ContributionReview) ([]factory.ContributionRole, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT c.id,c.assignment,c.profile,c.subject,a.issue,`+contributionRoleScopeSQL+`
 FROM factory_mcp_capabilities c JOIN factory_assignments a ON a.id=c.assignment AND a.run=c.run
 JOIN factory_runs r ON r.id=c.run AND r.active AND NOT r.settled
 WHERE c.repository=$1 AND c.state='active' AND c.deadline>$2 AND a.stage='assigned'
 AND c.profile=ANY($3) AND c.profile<>$4 AND `+contributionRoleScopeSQL+`<>''
 ORDER BY a.seq,c.id LIMIT 64`, review.Repository, time.Now().Unix(), contributionReplyProfiles(), review.SourceActor)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	seen := map[factory.MCPProfile]bool{factory.MCPProfile(review.SourceActor): true}
	roles := []factory.ContributionRole{}
	for rows.Next() {
		var role factory.ContributionRole
		if err := rows.Scan(&role.Capability, &role.Assignment, &role.Profile, &role.Subject, &role.Issue, &role.Scope); err != nil {
			return nil, err
		}
		if seen[role.Profile] {
			continue
		}
		seen[role.Profile] = true
		roles = append(roles, role)
		if len(roles) == factory.ContributionMaxRoles {
			break
		}
	}
	return roles, rows.Err()
}

// ClaimContributionReview publishes the real pending assessment before calls.
// The source revision lock and existing comment idempotency key make a repeated
// service wake, model timeout or browser refresh unable to dispatch twice.
// Record sources retain the resolved source snapshot beside the assessment.
// Every assessment uses the normal parent_id thread link.
func (o *ContributionOwner) ClaimContributionReview(ctx context.Context, review factory.ContributionReview) (bool, error) {
	s := o.store
	if len(review.Roles) == 0 || review.State != "pending" {
		return false, errors.New("contribution requires a pending assessment with eligible roles")
	}
	var recordSource *factory.PostSource
	if review.SourceKind == factory.PostSourceRecord {
		source, err := s.ResolvePostSource(ctx, review.Repository, factory.PostSourceRecord, review.Source)
		if err != nil {
			return false, err
		}
		recordSource = source
	}
	tx, err := o.conn.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	if err := lockContributionSource(ctx, tx, review); err != nil {
		return false, err
	}
	fields := map[string]any{"actor": "factory", "author_name": "Factory", "at": time.Now().Unix(), "contribution": review, "contribution_owner": o.id, "status": "contribution:pending", "parent_id": review.Source}
	if recordSource != nil {
		fields["source"] = recordSource
	}
	raw, err := json.Marshal(fields)
	if err != nil {
		return false, err
	}
	result, err := tx.ExecContext(ctx, `INSERT INTO factory_feed_posts(id,repository,revision,author,body,idempotency,data)
 VALUES($1,$2,0,'factory',$3,$4,$5) ON CONFLICT DO NOTHING`, factory.ContributionReviewInvitationID(review), review.Repository,
		"JEV is checking whether an eligible running role can add a useful contribution.", "invite:"+review.Source+":"+strconv.FormatInt(review.Revision, 10), string(raw))
	if err != nil {
		return false, err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	return count == 1, tx.Commit()
}

func lockContributionSource(ctx context.Context, tx *sql.Tx, review factory.ContributionReview) error {
	if review.SourceKind == factory.PostSourceRecord {
		var revision int64
		var stage string
		var repository int64
		err := tx.QueryRowContext(ctx, `SELECT revision,stage,repository FROM factory_assignments WHERE id=$1 FOR SHARE`, review.Source).Scan(&revision, &stage, &repository)
		if err != nil {
			return err
		}
		if repository != review.Repository || stage != factory.AssignmentFinished || revision != review.Revision {
			return ErrStaleRevision
		}
		return nil
	}
	var revision int64
	err := tx.QueryRowContext(ctx, `SELECT revision FROM factory_feed_posts WHERE id=$1 AND repository=$2
 AND NOT COALESCE((data->>'deleted')::boolean,false) AND NOT COALESCE((data->>'revoked')::boolean,false) FOR SHARE`, review.Source, review.Repository).Scan(&revision)
	if err != nil {
		return err
	}
	if revision != review.Revision {
		return ErrStaleRevision
	}
	return nil
}

func (o *ContributionOwner) SettleContributionReview(ctx context.Context, review factory.ContributionReview) error {
	tx, err := o.conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := lockContributionSource(ctx, tx, review); err != nil {
		review.State, review.Assignment = "stale", ""
	}
	var owner string
	if err := tx.QueryRowContext(ctx, `SELECT data->>'contribution_owner' FROM factory_feed_posts WHERE id=$1 AND data->'contribution'->>'state'='pending' FOR UPDATE`, factory.ContributionReviewInvitationID(review)).Scan(&owner); err != nil {
		return err
	}
	if owner != o.id {
		return ErrStaleRevision
	}
	if review.State == "invited" {
		version, err := contributionRepliesVersion(ctx, tx, review)
		if err != nil {
			return err
		}
		if version != review.RepliesVersion {
			review.State, review.Assignment = "stale", ""
		}
	}
	if review.State == "invited" {
		capability := ""
		for _, role := range review.Roles {
			if role.Assignment == review.Assignment && role.Profile == review.Profile {
				capability = role.Capability
			}
		}
		var active bool
		err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM factory_mcp_capabilities c JOIN factory_assignments a ON a.id=c.assignment AND a.run=c.run
 JOIN factory_runs r ON r.id=c.run WHERE c.assignment=$1 AND c.repository=$2 AND c.state='active' AND c.deadline>$3
 AND c.id=$4 AND c.profile=$5 AND a.stage='assigned' AND r.active AND NOT r.settled)`, review.Assignment, review.Repository, time.Now().Unix(), capability, string(review.Profile)).Scan(&active)
		if err != nil {
			return err
		}
		if !active {
			review.State, review.Assignment = "role_unavailable", ""
		}
	}
	return settleContributionReviewTx(ctx, tx, review)
}

type contributionQuery interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func contributionRepliesVersion(ctx context.Context, query contributionQuery, review factory.ContributionReview) (string, error) {
	var version string
	err := query.QueryRowContext(ctx, `WITH RECURSIVE thread AS (
 SELECT id,seq,revision,data FROM factory_feed_posts WHERE repository=$1
 AND (data->>'parent_id'=$2 OR (data->'source'->>'kind'='post' AND data->'source'->>'id'=$2)
 OR (data->'source'->>'kind'='record' AND data->'source'->>'id'=$2))
 UNION SELECT c.id,c.seq,c.revision,c.data FROM factory_feed_posts c JOIN thread p
 ON c.data->>'parent_id'=p.id OR (c.data->'source'->>'kind'='post' AND c.data->'source'->>'id'=p.id)
 WHERE c.repository=$1
 ) SELECT COALESCE(string_agg(id||':'||revision::text,',' ORDER BY seq DESC),'') FROM
 (SELECT id,seq,revision FROM thread WHERE NOT data ? 'consultation' AND NOT data ? 'contribution'
 AND COALESCE(data->>'actor','')<>'factory' AND NOT COALESCE((data->>'deleted')::boolean,false)
 AND NOT COALESCE((data->>'revoked')::boolean,false) ORDER BY seq DESC LIMIT 21) current_replies`, review.Repository, review.Source).Scan(&version)
	if err != nil {
		return "", err
	}
	return factory.MCPInputDigest([]byte(version)), nil
}

func settleContributionReviewTx(ctx context.Context, tx *sql.Tx, review factory.ContributionReview) error {
	body := "No role was invited: the independent judgments did not clear the usefulness gate."
	switch review.State {
	case "invited":
		body = fmt.Sprintf("JEV invited %s to add one useful comment. Existing work and permissions stay in force. Awaiting its actual reply.", recordPersona(review.Profile))
	case "unavailable":
		body = "The role contribution assessment could not be confirmed. No agent was invited."
	case "stale":
		body = "The source changed during assessment. No agent was invited from this revision."
	case "role_unavailable":
		body = "The selected role is no longer running. No new execution was started."
	}
	raw, err := json.Marshal(review)
	if err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `UPDATE factory_feed_posts SET body=$2,data=jsonb_set(data,'{contribution}',$3::jsonb)||jsonb_build_object('status',$4::text)
 WHERE id=$1 AND data->'contribution'->>'state'='pending'`, factory.ContributionReviewInvitationID(review), body, string(raw), "contribution:"+review.State)
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
	return tx.Commit()
}

// contributionReplyTx is called under ordinary current capability/submission
// admission. It consumes one exact discussion invitation, never an execution
// lease. Edited/revoked source revisions cannot borrow an older invitation.
// Record invitations are consumed by a direct reply to the invitation itself
// (the record is not a comment, so parent_id names the assessment); post
// invitations keep the existing exact-source-revision consumption. A direct
// reply to the invitation still binds the exact live original source
// kind/revision/repository plus nondeleted/nonrevoked state, so a stale edit
// or delete refuses with no consumption. A source edit landing between
// admission and consumption cannot commit a stale reply: consumption must
// affect exactly one invitation row unless the reply is the worker's own
// discussion (which needs no invitation), so a 0-row freshness UPDATE
// refuses instead of committing beside its comment and receipt.
func contributionReplyTx(ctx context.Context, tx *sql.Tx, cap factory.MCPCapability, post, reply string) error {
	result, err := tx.ExecContext(ctx, `UPDATE factory_feed_posts i SET data=jsonb_set(i.data,'{contribution}',i.data->'contribution'||jsonb_build_object('state','responded','reply',$4::text))
 FROM factory_feed_posts p WHERE p.id=$1 AND p.repository=$2 AND i.repository=p.repository
 AND i.data->'contribution'->>'assignment'=$3 AND i.data->'contribution'->>'state'='invited'
 AND ((i.data->'contribution'->>'source'=p.id AND (i.data->'contribution'->>'revision')::bigint=p.revision
 AND NOT COALESCE((p.data->>'deleted')::boolean,false) AND NOT COALESCE((p.data->>'revoked')::boolean,false))
 OR (i.id=p.id AND NOT COALESCE((p.data->>'deleted')::boolean,false) AND NOT COALESCE((p.data->>'revoked')::boolean,false)
 AND ((i.data->'source'->>'kind'='record' AND EXISTS(SELECT 1 FROM factory_assignments a
 WHERE a.id=i.data->'contribution'->>'source' AND a.repository=i.repository
 AND a.revision=(i.data->'contribution'->>'revision')::bigint AND a.stage='finished'))
 OR (COALESCE(i.data->'source'->>'kind','')<>'record' AND EXISTS(SELECT 1 FROM factory_feed_posts s
 WHERE s.id=i.data->'contribution'->>'source' AND s.repository=i.repository
 AND s.revision=(i.data->'contribution'->>'revision')::bigint
 AND NOT COALESCE((s.data->>'deleted')::boolean,false) AND NOT COALESCE((s.data->>'revoked')::boolean,false))))))`, post, cap.Repository, cap.AssignmentID, reply)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	// No invitation consumed. The worker's own discussion needs no
	// invitation, so it stays admitted; an invitation-admitted reply
	// whose freshness UPDATE matched nothing saw its source change
	// between admission and consumption and must refuse with the whole
	// submission transaction (comment, receipt and consumption).
	own, err := isOwnDiscussionReplyTx(ctx, tx, cap, post)
	if err != nil {
		return err
	}
	if own {
		return nil
	}
	return errors.New("invitation no longer admits this reply (source changed)")
}

// isOwnDiscussionReplyTx reports whether post sits in the worker's own
// discussion thread: one ancestor names this assignment's record source,
// or one ancestor is this assignment's own top-level post. It takes no
// row locks; callers hold the reply-target share lock already.
func isOwnDiscussionReplyTx(ctx context.Context, tx *sql.Tx, cap factory.MCPCapability, post string) (bool, error) {
	var own bool
	err := tx.QueryRowContext(ctx, `WITH RECURSIVE ancestors AS (
 SELECT id,data,0 AS depth FROM factory_feed_posts WHERE id=$2 AND repository=$1
 UNION ALL SELECT p.id,p.data,a.depth+1 FROM factory_feed_posts p JOIN ancestors a
 ON p.id=COALESCE(NULLIF(a.data->>'parent_id',''),CASE WHEN a.data->'source'->>'kind'='post' THEN a.data->'source'->>'id' END)
 WHERE p.repository=$1 AND a.depth<32
 ) SELECT EXISTS(SELECT 1 FROM ancestors WHERE
 (data->'source'->>'kind'='record' AND data->'source'->>'id'=$3)
 OR (data->>'assignment'=$3 AND COALESCE(data->>'parent_id','')='')
 OR EXISTS(SELECT 1 FROM factory_assignments a WHERE a.id=$3 AND a.data->'planning_context'->'IssueMaker'->>'post'=ancestors.id))`, cap.Repository, post, cap.AssignmentID).Scan(&own)
	if err != nil {
		return false, err
	}
	return own, nil
}

func admitWorkerDiscussionReplyTx(ctx context.Context, tx *sql.Tx, cap factory.MCPCapability, post string) error {
	// An invited thread is readable, but only its exact source admits one
	// cross-work reply. Descendants cannot recursively widen that invitation.
	// A direct reply to the invitation binds the exact live original source
	// kind/revision/repository plus nondeleted/nonrevoked state.
	//
	// The caller already holds the reply target FOR SHARE; the invitation
	// legs below additionally hold the exact original source row FOR SHARE
	// through the invited reply, in reply-target-then-source order. The
	// shares are compatible with the claim/settle source-first order, so
	// concurrent source edits queue on the held row instead of landing
	// between admission and consumption; only the final invitation UPDATE
	// takes an exclusive lock. Own-discussion replies need no source and
	// take no extra lock.
	own, err := isOwnDiscussionReplyTx(ctx, tx, cap, post)
	if err != nil {
		return err
	}
	if own {
		return nil
	}
	// Source-direct invitation: the reply target is the exact live source
	// revision itself. Re-assert it under the already-held share lock.
	var one int
	err = tx.QueryRowContext(ctx, `SELECT 1 FROM factory_feed_posts i JOIN factory_feed_posts p ON p.id=$2 AND p.repository=$1
 WHERE i.repository=$1 AND i.data->'contribution'->>'assignment'=$3 AND i.data->'contribution'->>'state'='invited'
 AND i.data->'contribution'->>'source'=p.id AND (i.data->'contribution'->>'revision')::bigint=p.revision
 AND NOT COALESCE((p.data->>'deleted')::boolean,false) AND NOT COALESCE((p.data->>'revoked')::boolean,false)
 FOR SHARE OF p`, cap.Repository, post, cap.AssignmentID).Scan(&one)
	if err == nil {
		return nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	// Invitation-direct post invitation: hold the exact live original post.
	err = tx.QueryRowContext(ctx, `SELECT 1 FROM factory_feed_posts i JOIN factory_feed_posts p ON p.id=$2 AND p.repository=i.repository
 JOIN factory_feed_posts s ON s.id=i.data->'contribution'->>'source' AND s.repository=i.repository
 WHERE i.repository=$1 AND i.id=p.id
 AND i.data->'contribution'->>'assignment'=$3 AND i.data->'contribution'->>'state'='invited'
 AND NOT COALESCE((p.data->>'deleted')::boolean,false) AND NOT COALESCE((p.data->>'revoked')::boolean,false)
 AND COALESCE(i.data->'source'->>'kind','')<>'record'
 AND s.revision=(i.data->'contribution'->>'revision')::bigint
 AND NOT COALESCE((s.data->>'deleted')::boolean,false) AND NOT COALESCE((s.data->>'revoked')::boolean,false)
 FOR SHARE OF s`, cap.Repository, post, cap.AssignmentID).Scan(&one)
	if err == nil {
		return nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	// Invitation-direct record invitation: hold the exact finished
	// assignment revision.
	err = tx.QueryRowContext(ctx, `SELECT 1 FROM factory_feed_posts i JOIN factory_feed_posts p ON p.id=$2 AND p.repository=i.repository
 JOIN factory_assignments a ON a.id=i.data->'contribution'->>'source'
 WHERE i.repository=$1 AND i.id=p.id
 AND i.data->'contribution'->>'assignment'=$3 AND i.data->'contribution'->>'state'='invited'
 AND NOT COALESCE((p.data->>'deleted')::boolean,false) AND NOT COALESCE((p.data->>'revoked')::boolean,false)
 AND i.data->'source'->>'kind'='record'
 AND a.repository=i.repository
 AND a.revision=(i.data->'contribution'->>'revision')::bigint AND a.stage='finished'
 FOR SHARE OF a`, cap.Repository, post, cap.AssignmentID).Scan(&one)
	if err == nil {
		return nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	return errors.New("reply needs the worker's own discussion or an unconsumed invitation to this exact source revision")
}
