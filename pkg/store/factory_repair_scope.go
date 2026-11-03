package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strconv"
	"time"
	"unicode/utf8"

	"github.com/levitateos/factory/pkg/factory"
	"github.com/levitateos/factory/pkg/project"
)

// repairObservationScopeTx binds post reads and writes to the active
// investigator and its admitted cause. It buys no work.
func repairObservationScopeTx(ctx context.Context, tx *sql.Tx, cap factory.MCPCapability) (factory.Assignment, factory.CurrentContext, error) {
	var a factory.Assignment
	var current factory.CurrentContext
	if cap.Validate() != nil || cap.Profile != factory.MCPProfileStrategist || cap.State != factory.MCPCapabilityActive {
		return a, current, errors.New("repair scope requires an active bound Strategist")
	}
	var recorded factory.MCPCapability
	var state string
	var raw []byte
	if err := tx.QueryRowContext(ctx, `SELECT state,data FROM factory_mcp_capabilities WHERE id=$1 FOR SHARE`, cap.ID).Scan(&state, &raw); err != nil {
		return a, current, err
	}
	if json.Unmarshal(raw, &recorded) != nil || state != factory.MCPCapabilityActive || recorded.ScopeDigest() != cap.ScopeDigest() || recorded.Expired(time.Now()) {
		return a, current, errors.New("repair scope capability is no longer active")
	}
	if err := contextJSONRow(ctx, tx, &a, `SELECT data FROM factory_assignments WHERE id=$1 AND stage='assigned' FOR SHARE`, cap.AssignmentID); err != nil {
		return a, current, err
	}
	phaseMatches := a.Subject.Completion != nil && a.Phase == project.ExecutionReassess || (a.Subject.Stall != nil || a.Subject.DeliveryRepair != nil) && a.Phase == project.ExecutionRepair
	if a.Validate() != nil || a.ID != cap.AssignmentID || a.Run != cap.RunID || a.Repository != cap.Repository || a.ProjectID != cap.Project || a.SourceCommit != cap.SourceDigest || strconv.FormatInt(a.NativeRev, 10) != cap.AcceptedRevision || a.Subject.SubjectKey(a.Repository) != cap.Subject || !phaseMatches || a.Role != project.RoleCoder || a.Issue != 0 {
		return a, current, errors.New("repair scope differs from its admitted investigator")
	}
	var locked string
	if err := tx.QueryRowContext(ctx, `SELECT id FROM factory_runs WHERE id=$1 AND active=TRUE AND settled=FALSE FOR SHARE`, a.Run).Scan(&locked); err != nil {
		return a, current, err
	}
	if err := tx.QueryRowContext(ctx, `SELECT root_assignment FROM factory_attempt_allowances WHERE repository=$1 AND root_assignment=$2 FOR SHARE`, a.Repository, a.AttemptRoot).Scan(&locked); err != nil {
		return a, current, err
	}
	current, err := factoryCurrentContextTx(ctx, tx, cap, time.Now())
	if err != nil {
		return a, current, err
	}
	if current.Run == nil || !current.Run.Active || current.Run.Settled || current.Run.Reconciled || current.Allowance == nil || current.Allowance.Closed || current.Allowance.DeadlineRemainingSeconds <= 0 || !factory.ValidDigest(factory.RepairInventoryDigest(current)) {
		return a, current, errors.New("repair scope lacks an active run, allowance or complete admitted cause")
	}
	return a, current, nil
}

// ReassessmentScope reads only this assignment's attributed intermediate
// report references. Full contents remain in the existing inbox/feed owner.
func (s *Store) ReassessmentScope(ctx context.Context, cap factory.MCPCapability, after int64) (factory.RepairScopeView, error) {
	var out factory.RepairScopeView
	if after < 0 {
		return out, errors.New("invalid repair observation cursor")
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	a, current, err := repairObservationScopeTx(ctx, tx, cap)
	if err != nil {
		return out, err
	}
	out = factory.RepairScopeView{Assignment: a.ID, Run: a.Run, InputDigest: cap.ScopeDigest(), Subject: cap.Subject, SourceCommit: a.SourceCommit, InventoryDigest: factory.RepairInventoryDigest(current), Records: []factory.RepairScopeRecord{}, Limit: factory.RepairObservationPageSize, MaximumPerTool: factory.MaxRepairObservations}
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM factory_feed_posts WHERE repository=$1 AND data->>'assignment'=$2 AND data->>'run'=$3 AND data->'communication'->>'kind' IN ('diagnosis','repair_draft')`, a.Repository, a.ID, a.Run).Scan(&out.Total); err != nil {
		return out, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT id,CASE data->'communication'->>'kind' WHEN 'diagnosis' THEN $4 ELSE $5 END,seq FROM factory_feed_posts WHERE repository=$1 AND data->>'assignment'=$2 AND data->>'run'=$3 AND data->'communication'->>'kind' IN ('diagnosis','repair_draft') AND seq>$6 ORDER BY seq LIMIT $7`, a.Repository, a.ID, a.Run, string(factory.MCPToolRecordDiagnosis), string(factory.MCPToolDraftRepair), after, out.Limit+1)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var record factory.RepairScopeRecord
		if err := rows.Scan(&record.ID, &record.Tool, &record.Sequence); err != nil {
			rows.Close()
			return out, err
		}
		if len(out.Records) == out.Limit {
			out.NextAfter = out.Records[len(out.Records)-1].Sequence
			break
		}
		out.Records = append(out.Records, record)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	out.Complete = out.NextAfter == 0
	return out, tx.Commit()
}

// ReadRepairObservation reads only this active investigator's diagnosis or
// draft post. A reference grants no broader read.
func (s *Store) ReadRepairObservation(ctx context.Context, cap factory.MCPCapability, id string, offset int) (factory.RepairObservationPage, error) {
	var out factory.RepairObservationPage
	if !factory.ValidID(id) || offset < 0 {
		return out, errors.New("invalid repair observation reference or offset")
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	a, _, err := repairObservationScopeTx(ctx, tx, cap)
	if err != nil {
		return out, err
	}
	var raw []byte
	if err := tx.QueryRowContext(ctx, `SELECT CASE data->'communication'->>'kind' WHEN 'diagnosis' THEN $5 ELSE $6 END,CASE data->'communication'->>'kind' WHEN 'diagnosis' THEN (data->'diagnosis')::text ELSE data->>'repair_draft' END FROM factory_feed_posts WHERE id=$1 AND repository=$2 AND data->>'assignment'=$3 AND data->>'run'=$4 AND data->'communication'->>'kind' IN ('diagnosis','repair_draft') FOR SHARE`, id, a.Repository, a.ID, a.Run, string(factory.MCPToolRecordDiagnosis), string(factory.MCPToolDraftRepair)).Scan(&out.Tool, &raw); err != nil {
		return out, err
	}
	if offset >= len(raw) || !utf8.RuneStart(raw[offset]) {
		return factory.RepairObservationPage{}, errors.New("repair observation offset is outside its UTF-8 content")
	}
	end := min(len(raw), offset+factory.RepairObservationFragmentBytes)
	for end < len(raw) && !utf8.RuneStart(raw[end]) {
		end--
	}
	out.ID, out.Offset, out.TotalBytes, out.Encoding = id, offset, len(raw), "utf8-json-fragment"
	out.Content, out.Complete = string(raw[offset:end]), end == len(raw)
	if !out.Complete {
		out.NextOffset = end
	}
	return out, tx.Commit()
}
