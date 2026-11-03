package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/levitateos/sodaos/internal/factory"
	"github.com/levitateos/sodaos/internal/project"
)

// Feed record enrichment: one assignment row is the canonical feed
// record for every loop phase (decompose, investigate, implement,
// review, correct, reassess, repair, verify), so plans, reviews,
// corrections and verifications already reach the feed as records.
// This read fills their server-derived serving fields, recorded
// content and typed component payloads from the canonical owners.
// Confirmed merges, verified completions and bound Jev judgments
// are their own milestone entries (FeedMilestoneDetails below):
// each receives its own global feed order when recorded.
// The worker record's status still names its bound integration
// stages as recorded context; the milestone posts carry the
// Factory/Jev authorship. Reads spend and dispatch nothing.

// FeedRecordDetail carries one stored assignment record's serving fields.
type FeedRecordDetail struct {
	ID      string
	Actor   string
	Author  string
	At      int64
	Status  string
	Issue   int64
	PR      int64
	Ref     string
	Summary string
	Payload *factory.FeedPayload
}

// feedRecordStatusLimit mirrors the wire bound on one entry status.
const feedRecordStatusLimit = 64

// FeedSummaryLimit bounds one record's served summary preview in
// runes; the full recorded content stays with its canonical owner.
const FeedSummaryLimit = 512

// FeedRecordDetails reads the serving fields behind a set of record
// entries: the phase's canonical MCP profile as actor, its persona
// name as author, the recorded creation/finish time, a status
// composed from recorded phase/stage/outcome plus review verdict,
// verification outcome, finding/failure counts and any bound
// publication, merge and verified completion stages, the bound
// issue and PR, the produced candidate ref, and the recorded result
// summary (phase summary, plan approach, review summary or result
// summary, in that order). Fields the record does not supply stay
// empty; nothing is inferred beyond the recorded rows.
func (s *Store) FeedRecordDetails(ctx context.Context, ids []string) (map[string]FeedRecordDetail, error) {
	out := make(map[string]FeedRecordDetail, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := s.db.QueryContext(ctx, `SELECT a.id,a.stage,a.issue,a.data->'result',COALESCE(a.data->>'attempt_root',''),
		COALESCE(a.data->>'phase',''),COALESCE(a.data->>'role',''),COALESCE(a.data->>'outcome',''),
		CASE WHEN a.data->>'created_unix' ~ '^[0-9]+$' THEN (a.data->>'created_unix')::bigint ELSE 0 END,
		CASE WHEN a.data->>'finished_unix' ~ '^[0-9]+$' THEN (a.data->>'finished_unix')::bigint ELSE 0 END,
		COALESCE(p.stage,''),COALESCE(m.stage,''),(c.publication IS NOT NULL),COALESCE(m.pr,0),
		COALESCE(NULLIF(a.data->'result'->'phase_result'->>'summary',''),
			NULLIF(a.data->'result'->'phase_result'->'output'->'plan'->>'approach',''),
			NULLIF(a.data->'result'->'review'->>'summary',''),
			NULLIF(a.data->'result'->>'summary',''),''),
		COALESCE(NULLIF(a.data->'result'->'review'->>'verdict',''),
			NULLIF(a.data->'result'->'phase_result'->'output'->'verification'->>'outcome',''),''),
		COALESCE(NULLIF(a.data->'result'->>'candidate',''),
			NULLIF(a.data->'result'->'phase_result'->'output'->'correction'->>'new_candidate',''),''),
		GREATEST(
			CASE WHEN jsonb_typeof(a.data->'result'->'findings')='array' THEN jsonb_array_length(a.data->'result'->'findings') ELSE 0 END,
			CASE WHEN jsonb_typeof(a.data->'result'->'phase_result'->'output'->'correction'->'findings')='array' THEN jsonb_array_length(a.data->'result'->'phase_result'->'output'->'correction'->'findings') ELSE 0 END),
		CASE WHEN jsonb_typeof(a.data->'result'->'phase_result'->'output'->'verification'->'check_receipts')='array' THEN jsonb_array_length(a.data->'result'->'phase_result'->'output'->'verification'->'check_receipts') ELSE 0 END,
		CASE WHEN jsonb_typeof(a.data->'result'->'phase_result'->'output'->'verification'->'failures')='array' THEN jsonb_array_length(a.data->'result'->'phase_result'->'output'->'verification'->'failures') ELSE 0 END,
		(a.data->'planning_context'->'IssueMaker' IS NOT NULL AND a.data->'planning_context'->'IssueMaker'<>'null'::jsonb)
		FROM factory_assignments a
		LEFT JOIN factory_publications p ON p.assignment=a.id
		LEFT JOIN factory_merges m ON m.publication=(p.data->>'id')
		LEFT JOIN factory_completions c ON c.publication=(p.data->>'id') AND c.repository=a.repository
		WHERE a.id = ANY($1)`, ids)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	taskRoots := make(map[string][]string)
	for rows.Next() {
		var detail FeedRecordDetail
		var phase, role, stage, outcome, pubStage, mergeStage, verdict, attemptRoot string
		var rawResult []byte
		var created, finished int64
		var findings, checks, failures int
		var completed bool
		var issueMaker sql.NullBool
		if err := rows.Scan(&detail.ID, &stage, &detail.Issue, &rawResult, &attemptRoot, &phase, &role, &outcome,
			&created, &finished, &pubStage, &mergeStage, &completed, &detail.PR,
			&detail.Summary, &verdict, &detail.Ref, &findings, &checks, &failures, &issueMaker); err != nil {
			return nil, err
		}
		detail.Payload = feedResultPayload(rawResult)
		if attemptRoot != "" && (phase == project.ExecutionImplement || phase == project.ExecutionCorrect) {
			taskRoots[attemptRoot] = append(taskRoots[attemptRoot], detail.ID)
		}
		detail.Actor, detail.Author = RecordAuthor(phase, role)
		if issueMaker.Valid && issueMaker.Bool {
			detail.Actor, detail.Author = "issue_maker", "Issue Maker"
		}
		detail.At = created
		if stage == factory.AssignmentFinished && finished > 0 {
			detail.At = finished
		}
		detail.Summary = TruncateFeedSummary(detail.Summary)
		var extra []string
		if findings > 0 {
			extra = append(extra, fmt.Sprintf("findings:%d", findings))
		}
		if checks > 0 {
			extra = append(extra, fmt.Sprintf("checks:%d", checks))
		}
		if failures > 0 {
			extra = append(extra, fmt.Sprintf("failures:%d", failures))
		}
		if pubStage != "" {
			extra = append(extra, pubStage)
		}
		if mergeStage != "" {
			extra = append(extra, mergeStage)
		}
		if completed {
			extra = append(extra, "completed")
		}
		detail.Status = RecordStatus(phase, stage, outcome, verdict, extra)
		out[detail.ID] = detail
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := s.feedTaskStates(ctx, taskRoots, out); err != nil {
		return nil, err
	}
	return out, nil
}

// feedTaskStates fills the observed plan-task lanes behind the
// window's implementation/correction records from the canonical
// plan task owner at the attempt root's current plan head. States
// are the currently observed ones, not a finish-time snapshot.
func (s *Store) feedTaskStates(ctx context.Context, roots map[string][]string, out map[string]FeedRecordDetail) error {
	if len(roots) == 0 {
		return nil
	}
	keys := make([]string, 0, len(roots))
	for root := range roots {
		keys = append(keys, root)
	}
	rows, err := s.db.QueryContext(ctx, `SELECT t.attempt_root,t.task_id,t.state
		FROM factory_plan_tasks t
		JOIN factory_plan_heads h ON h.attempt_root=t.attempt_root AND h.plan_id=t.plan_id
		WHERE t.attempt_root = ANY($1) ORDER BY t.attempt_root,t.task_id`, keys)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	byRoot := make(map[string]*factory.FeedTaskStates)
	for rows.Next() {
		var root, id, state string
		if err := rows.Scan(&root, &id, &state); err != nil {
			return err
		}
		lanes := byRoot[root]
		if lanes == nil {
			lanes = &factory.FeedTaskStates{}
			byRoot[root] = lanes
		}
		lanes.Total++
		if len(lanes.Tasks) < feedPayloadCap {
			lanes.Tasks = append(lanes.Tasks, factory.FeedTaskState{ID: id, State: state})
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for root, ids := range roots {
		lanes := byRoot[root]
		if lanes == nil {
			continue
		}
		for _, id := range ids {
			detail := out[id]
			if detail.Payload == nil {
				detail.Payload = &factory.FeedPayload{}
			}
			detail.Payload.Tasks = lanes
			out[id] = detail
		}
	}
	return nil
}

// TruncateFeedSummary cuts one served summary preview to its rune
// bound. The untruncated recorded text stays with its owner.
func TruncateFeedSummary(summary string) string {
	runes := []rune(summary)
	if len(runes) <= FeedSummaryLimit {
		return summary
	}
	return string(runes[:FeedSummaryLimit])
}

// RecordAuthor derives one record's actor/author pair from its
// recorded phase through the canonical MCP profiles: actor is the
// profile that serves the phase (the Strategist serves both reassess
// and repair), author its persona name. An unknown-phase record falls
// back to its recorded execution role as actor and leaves the author
// empty instead of inventing a persona.
func RecordAuthor(phase, role string) (actor, author string) {
	profile := recordProfile(phase)
	if profile == "" {
		return role, ""
	}
	return string(profile), recordPersona(profile)
}

func recordProfile(phase string) factory.MCPProfile {
	if phase == project.ExecutionRepair {
		return factory.MCPProfileStrategist
	}
	for _, profile := range factory.MCPProfiles {
		if factory.MCPPhaseOf(profile) == phase {
			return profile
		}
	}
	return ""
}

// recordPersona names one canonical profile the way the feed design
// presents its role authors.
func recordPersona(profile factory.MCPProfile) string {
	switch profile {
	case factory.MCPProfileIssueMaker:
		return "Issue Maker"
	case factory.MCPProfileProductLead:
		return "Product Lead"
	case factory.MCPProfileArchitect:
		return "Architect"
	case factory.MCPProfileBuilder:
		return "Builder"
	case factory.MCPProfileInspector:
		return "Inspector"
	case factory.MCPProfileFixer:
		return "Fixer"
	case factory.MCPProfileStrategist:
		return "Strategist"
	case factory.MCPProfileTestPilot:
		return "Test Pilot"
	default:
		return ""
	}
}

// RecordStatus derives one record's feed status from its recorded
// phase, stage, outcome and verdict segment (review verdict or
// verification outcome) plus its extra recorded segments, e.g.
// "review:finished:succeeded:request-changes>findings:3" or
// "implement:finished:succeeded>published>merged>completed". Every
// segment is a recorded value; a missing phase, outcome or verdict
// simply drops its segment. The result stays within the wire status
// bound: extra segments drop first, then verdict, then outcome.
func RecordStatus(phase, stage, outcome, verdict string, extra []string) string {
	base := stage
	if phase != "" {
		base = phase + ":" + stage
	}
	withOutcome := base
	if outcome != "" {
		withOutcome = base + ":" + outcome
	}
	withVerdict := withOutcome
	if verdict != "" {
		withVerdict = withOutcome + ":" + verdict
	}
	full := withVerdict
	if len(extra) > 0 {
		full = withVerdict + ">" + strings.Join(extra, ">")
	}
	for _, candidate := range []string{full, withVerdict, withOutcome, base} {
		if len(candidate) <= feedRecordStatusLimit {
			return candidate
		}
	}
	return stage
}

// feedPayloadCap bounds one previewed list inside a typed payload;
// the honest total travels beside every capped list.
const feedPayloadCap = 24

// feedText bounds one previewed recorded string to the shared
// summary rune limit.
func feedText(text string) string { return TruncateFeedSummary(text) }

func feedStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	out := make([]string, 0, len(values))
	for _, value := range values {
		out = append(out, feedText(value))
	}
	return out
}

// feedResultPayload projects one stored assignment result into its
// typed component payload. A missing or undecodable result yields
// no payload instead of a guessed one.
func feedResultPayload(raw []byte) *factory.FeedPayload {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var result factory.AssignmentResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil
	}
	payload := &factory.FeedPayload{}
	if result.Review != nil {
		review := &factory.FeedReview{Verdict: result.Review.Verdict, Summary: feedText(result.Review.Summary)}
		review.Findings, review.FindingsTotal = feedPreviewStrings(result.Review.Findings)
		payload.Review = review
	}
	if len(result.Findings) > 0 && payload.Review == nil {
		review := &factory.FeedReview{}
		review.Findings, review.FindingsTotal = feedPreviewStrings(result.Findings)
		payload.Review = review
	} else if len(result.Findings) > 0 && payload.Review.FindingsTotal == 0 {
		payload.Review.Findings, payload.Review.FindingsTotal = feedPreviewStrings(result.Findings)
	}
	if result.PhaseResult == nil || result.PhaseResult.Output == nil {
		return emptyFeedPayload(payload)
	}
	out := result.PhaseResult.Output
	switch {
	case out.Plan != nil:
		payload.Plan = feedPlanPayload(out.Plan)
	case out.Implementation != nil:
		payload.Implementation = &factory.FeedImplementation{
			Candidate: out.Implementation.Candidate, DiagnosticRefs: feedStrings(out.Implementation.DiagnosticRefs),
		}
	case out.Review != nil || out.Clear != nil:
		feedReviewCyclePayload(payload, out)
	case out.Correction != nil:
		payload.Correction = feedCorrectionPayload(out.Correction)
	case out.Reassessment != nil:
		r := out.Reassessment
		payload.Reassessment = &factory.FeedReassessment{
			Summary: feedText(r.Summary), RetainedIssues: r.RetainedIssueIndexes,
			InspectedTotal: len(r.Inspected),
		}
		payload.Reassessment.Changes, payload.Reassessment.ChangesTotal = feedIntentChanges(r.IntentChanges)
		payload.Reassessment.Attention = feedAttention(r.Attention)
		payload.Reassessment.Coverage, payload.Reassessment.CoverageTotal = feedCoverage(r.Coverage)
	case out.Repair != nil:
		r := out.Repair
		payload.Repair = &factory.FeedRepair{
			Outcome: r.Outcome, Causes: feedCheckFailures(r.Causes),
			RetainedIssues: r.RetainedIssueIndexes, InspectedTotal: len(r.Inspected),
		}
		payload.Repair.Changes, payload.Repair.ChangesTotal = feedIntentChanges(r.IntentChanges)
		payload.Repair.Attention = feedAttention(r.Attention)
		payload.Repair.Coverage, payload.Repair.CoverageTotal = feedCoverage(r.Coverage)
	case out.DeliveryRepair != nil:
		r := out.DeliveryRepair
		payload.Repair = &factory.FeedRepair{
			Outcome: r.Outcome, Causes: feedCheckFailures(r.Causes),
			RetainedIssues: r.RetainedIssueIndexes, InspectedTotal: len(r.Inspected),
		}
		payload.Repair.Changes, payload.Repair.ChangesTotal = feedIntentChanges(r.IntentChanges)
		payload.Repair.Attention = feedAttention(r.Attention)
		payload.Repair.Coverage, payload.Repair.CoverageTotal = feedCoverage(r.Coverage)
	case out.Verification != nil:
		payload.Verification = feedVerificationPayload(out.Verification)
	case out.Decomposition != nil:
		payload.Decomposition = feedDecompositionPayload(out.Decomposition)
	}
	return emptyFeedPayload(payload)
}

func emptyFeedPayload(payload *factory.FeedPayload) *factory.FeedPayload {
	if payload.Plan == nil && payload.Tasks == nil && payload.Implementation == nil &&
		payload.Review == nil && payload.Correction == nil && payload.Reassessment == nil &&
		payload.Repair == nil && payload.Verification == nil && payload.Decomposition == nil &&
		payload.Judgment == nil && payload.Merge == nil && payload.Completion == nil {
		return nil
	}
	return payload
}

func feedPreviewStrings(values []string) ([]string, int) {
	total := len(values)
	if total > feedPayloadCap {
		values = values[:feedPayloadCap]
	}
	return feedStrings(values), total
}

func feedPlanPayload(plan *factory.InvestigationPlan) *factory.FeedPlan {
	out := &factory.FeedPlan{
		Approach: feedText(plan.Approach), SourceCommit: plan.SourceCommit,
		Generation: plan.Generation, Unresolved: feedStrings(plan.Unresolved),
		AcceptanceCoverage: feedStrings(plan.AcceptanceCoverage),
		FindingsTotal:      len(plan.Findings), InspectedTotal: len(plan.Inspected),
		TasksTotal: len(plan.Tasks), ChecksTotal: len(plan.Checks),
	}
	out.Findings, _ = feedPreviewStrings(plan.Findings)
	for i, rejected := range plan.Rejected {
		if i >= feedPayloadCap {
			break
		}
		out.Rejected = append(out.Rejected, factory.FeedRejected{
			Approach: feedText(rejected.Approach), Reason: feedText(rejected.Reason),
		})
	}
	for i, inspected := range plan.Inspected {
		if i >= feedPayloadCap {
			break
		}
		out.Inspected = append(out.Inspected, factory.FeedInspected{
			Path: inspected.Path, Commit: inspected.Commit, Evidence: feedText(inspected.Evidence),
		})
	}
	for i, task := range plan.Tasks {
		if i >= feedPayloadCap {
			break
		}
		out.Tasks = append(out.Tasks, factory.FeedPlanTask{
			ID: task.ID, Objective: feedText(task.Objective), Lane: task.Lane,
			DependsOn: task.DependsOn, Outputs: task.Outputs, Checks: task.CheckIDs,
		})
	}
	for i, lane := range plan.Lanes {
		if i >= feedPayloadCap {
			break
		}
		out.Lanes = append(out.Lanes, factory.FeedPlanLane{ID: lane.ID, Tasks: lane.TaskIDs})
	}
	for i, check := range plan.Checks {
		if i >= feedPayloadCap {
			break
		}
		out.Checks = append(out.Checks, factory.FeedPlanCheck{
			ID: check.ID, Argv: check.Argv, Scope: check.Scope, Required: check.Required,
		})
	}
	return out
}

func feedFindingPayload(finding factory.Finding) factory.FeedFinding {
	out := factory.FeedFinding{
		Key: finding.Key, Severity: finding.Severity, Defect: feedText(finding.Defect),
		Evidence: feedText(finding.Evidence), RequiredOutcome: feedText(finding.RequiredOutcome),
		FixKind: finding.FixKind, Actionable: finding.Actionable,
	}
	if finding.Location != nil {
		out.Location = &factory.FeedFindingLocation{
			Path: finding.Location.Path, Side: finding.Location.Side, Line: finding.Location.Line,
		}
	}
	return out
}

func feedFindings(findings []factory.Finding) ([]factory.FeedFinding, int) {
	total := len(findings)
	if total > feedPayloadCap {
		findings = findings[:feedPayloadCap]
	}
	out := make([]factory.FeedFinding, 0, len(findings))
	for _, finding := range findings {
		out = append(out, feedFindingPayload(finding))
	}
	if len(out) == 0 {
		return nil, total
	}
	return out, total
}

func feedAssessments(assessments []factory.FindingAssessment) ([]factory.FeedAssessment, int) {
	total := len(assessments)
	if total > feedPayloadCap {
		assessments = assessments[:feedPayloadCap]
	}
	out := make([]factory.FeedAssessment, 0, len(assessments))
	for _, assessment := range assessments {
		out = append(out, factory.FeedAssessment{
			Key: assessment.Key, Status: assessment.Status, Evidence: feedText(assessment.Evidence),
		})
	}
	if len(out) == 0 {
		return nil, total
	}
	return out, total
}

func feedReviewCyclePayload(payload *factory.FeedPayload, out *factory.PhaseOutput) {
	review := payload.Review
	if review == nil {
		review = &factory.FeedReview{}
		payload.Review = review
	}
	if out.Review != nil {
		review.CycleID = out.Review.CycleID
		review.NewFindings, review.NewFindingsTotal = feedFindings(out.Review.NewFindings)
		review.Assessments, review.AssessmentsTotal = feedAssessments(out.Review.Assessments)
	}
	if out.Clear != nil {
		clear := &factory.FeedClearReview{
			Head: out.Clear.Head, Base: out.Clear.BaseOID,
			CoverageDigest: out.Clear.CoverageDigest,
		}
		clear.Assessments, clear.AssessmentsTotal = feedAssessments(out.Clear.Assessments)
		review.CycleID = out.Clear.CycleID
		review.Clear = clear
	}
}

func feedCorrectionPayload(report *factory.CorrectionReport) *factory.FeedCorrection {
	out := &factory.FeedCorrection{NewCandidate: report.NewCandidate, SetDigest: report.SetDigest}
	out.Findings, out.FindingsTotal = feedFindings(report.Findings)
	evidence := make(map[string]factory.CorrectionFixEvidence, len(report.Evidence))
	for _, entry := range report.Evidence {
		evidence[entry.Key] = entry
	}
	out.DispositionsTotal = len(report.Dispositions)
	for i, disposition := range report.Dispositions {
		if i >= feedPayloadCap {
			break
		}
		entry := factory.FeedDisposition{Key: disposition.Key, Disposition: disposition.Disposition}
		if fix, ok := evidence[disposition.Key]; ok {
			entry.ChangedPaths = fix.ChangedPaths
			entry.Commits = fix.Commits
		}
		out.Dispositions = append(out.Dispositions, entry)
	}
	return out
}

func feedIntentTasks(tasks []factory.IntentTask) ([]factory.FeedIntentTask, int) {
	total := len(tasks)
	if total > feedPayloadCap {
		tasks = tasks[:feedPayloadCap]
	}
	out := make([]factory.FeedIntentTask, 0, len(tasks))
	for _, task := range tasks {
		out = append(out, factory.FeedIntentTask{
			Key: task.Key, Objective: feedText(task.Objective),
			DependsOn: task.DependsOn, Outcomes: feedStrings(task.Outcomes),
		})
	}
	if len(out) == 0 {
		return nil, total
	}
	return out, total
}

func feedIntentChanges(changes []factory.ReassessmentIntentChange) ([]factory.FeedIntentChange, int) {
	total := len(changes)
	if total > feedPayloadCap {
		changes = changes[:feedPayloadCap]
	}
	out := make([]factory.FeedIntentChange, 0, len(changes))
	for _, change := range changes {
		entry := factory.FeedIntentChange{IntentID: change.IntentID, Reason: feedText(change.Reason)}
		entry.Tasks, entry.TasksTotal = feedIntentTasks(change.Tasks)
		out = append(out, entry)
	}
	if len(out) == 0 {
		return nil, total
	}
	return out, total
}

func feedAttention(entries []factory.ReassessmentAttentionEntry) []factory.FeedAttention {
	out := make([]factory.FeedAttention, 0, len(entries))
	for i, entry := range entries {
		if i >= feedPayloadCap {
			break
		}
		out = append(out, factory.FeedAttention{Issue: entry.IssueIndex, Reason: feedText(entry.Reason)})
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func feedCoverage(coverage []factory.ReassessmentCoverage) ([]factory.FeedCoverage, int) {
	total := len(coverage)
	if total > feedPayloadCap {
		coverage = coverage[:feedPayloadCap]
	}
	out := make([]factory.FeedCoverage, 0, len(coverage))
	for _, entry := range coverage {
		out = append(out, factory.FeedCoverage{
			IntentID: entry.IntentID, OutcomeID: entry.OutcomeID,
			TaskKeys: entry.TaskKeys, DeliveredAcceptanceIDs: entry.DeliveredAcceptanceIDs,
		})
	}
	if len(out) == 0 {
		return nil, total
	}
	return out, total
}

func feedCheckFailures(failures []factory.CheckFailure) []factory.FeedCheckFailure {
	out := make([]factory.FeedCheckFailure, 0, len(failures))
	for i, failure := range failures {
		if i >= feedPayloadCap {
			break
		}
		out = append(out, factory.FeedCheckFailure{Check: failure.CheckRef, Detail: feedText(failure.Detail)})
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func feedVerificationPayload(report *factory.AppVerificationReport) *factory.FeedVerification {
	out := &factory.FeedVerification{
		Outcome: report.Outcome, DeliveryID: report.DeliveryID,
		ChecksTotal: len(report.CheckReceipts), Failures: feedCheckFailures(report.Failures),
		ArtifactsTotal: len(report.ArtifactRefs), EvidenceTotal: len(report.EvidenceRefs),
	}
	for i, receipt := range report.CheckReceipts {
		if i >= feedPayloadCap {
			break
		}
		out.Checks = append(out.Checks, factory.FeedCheckReceipt{
			ID: receipt.CheckID, Outcome: receipt.Outcome, ArtifactDigest: receipt.ArtifactDigest,
		})
	}
	out.Coverage, out.CoverageTotal = feedCoverage(report.Coverage)
	return out
}

func feedDecompositionPayload(output *factory.SemanticPlannerOutput) *factory.FeedDecomposition {
	out := &factory.FeedDecomposition{
		Operation: output.Operation, Attention: feedText(output.Attention),
		CandidatesTotal: len(output.Candidates),
	}
	for i, candidate := range output.Candidates {
		if i >= feedPayloadCap {
			break
		}
		entry := factory.FeedDecompCandidate{Key: candidate.Key, Rationale: feedText(candidate.Rationale)}
		entry.Tasks, entry.TasksTotal = feedIntentTasks(candidate.Tasks)
		out.Candidates = append(out.Candidates, entry)
	}
	return out
}

// FeedMilestoneDetails reads the serving fields behind a window's
// milestone entries from their canonical owners: confirmed merges
// and verified completions authored by Factory, recorded Jev
// judgments authored by Jev. Entries whose milestone record cannot
// be read stay absent from the result rather than guessed.
func (s *Store) FeedMilestoneDetails(ctx context.Context, entries []factory.FeedEntry) (map[string]FeedRecordDetail, error) {
	out := make(map[string]FeedRecordDetail)
	var merges, completions, judgments []string
	for _, entry := range entries {
		switch entry.Activity {
		case factory.FeedActivityMerged:
			merges = append(merges, entry.ID)
		case factory.FeedActivityCompleted:
			completions = append(completions, entry.ID)
		case factory.FeedActivityJudgment:
			judgments = append(judgments, entry.ID)
		}
	}
	if err := s.feedMergeDetails(ctx, merges, out); err != nil {
		return nil, err
	}
	if err := s.feedCompletionDetails(ctx, completions, out); err != nil {
		return nil, err
	}
	if err := s.feedJudgmentDetails(ctx, judgments, out); err != nil {
		return nil, err
	}
	return out, nil
}

func (s *Store) feedMergeDetails(ctx context.Context, ids []string, out map[string]FeedRecordDetail) error {
	if len(ids) == 0 {
		return nil
	}
	rows, err := s.db.QueryContext(ctx, `SELECT publication,issue,pr,stage,data
		FROM factory_merges WHERE publication = ANY($1)`, ids)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var detail FeedRecordDetail
		var stage string
		var raw []byte
		if err := rows.Scan(&detail.ID, &detail.Issue, &detail.PR, &stage, &raw); err != nil {
			return err
		}
		detail.Actor, detail.Author = "factory", "Factory"
		detail.Status = "merged"
		merge := &factory.FeedMerge{Stage: stage, Publication: detail.ID}
		var record factory.Merge
		if json.Unmarshal(raw, &record) == nil {
			detail.Ref = record.MergedCommit
			detail.At = record.MergedUnix
			if detail.At == 0 {
				detail.At = record.FinishedUnix
			}
			merge.MergedCommit = record.MergedCommit
			merge.MergedUnix = record.MergedUnix
			merge.OriginalClosedUnix = record.OriginalClosedUnix
			if record.OriginalClosedUnix > 0 {
				detail.Status = "merged>closed"
			}
		}
		detail.Payload = &factory.FeedPayload{Merge: merge}
		out[detail.ID] = detail
	}
	return rows.Err()
}

func (s *Store) feedCompletionDetails(ctx context.Context, ids []string, out map[string]FeedRecordDetail) error {
	if len(ids) == 0 {
		return nil
	}
	rows, err := s.db.QueryContext(ctx, `SELECT event,generation,publication,occurrence,data
		FROM factory_completions WHERE event = ANY($1)`, ids)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var detail FeedRecordDetail
		var completion factory.FeedCompletion
		var raw []byte
		if err := rows.Scan(&detail.ID, &completion.Generation, &completion.Publication, &completion.Occurrence, &raw); err != nil {
			return err
		}
		completion.Event = detail.ID
		detail.Actor, detail.Author = "factory", "Factory"
		detail.Status = "issue:completed"
		var event factory.CompletionEvent
		if json.Unmarshal(raw, &event) == nil {
			detail.Issue = event.Issue
			detail.Ref = event.MergedCommit
			completion.MergedCommit = event.MergedCommit
		}
		detail.Payload = &factory.FeedPayload{Completion: &completion}
		out[detail.ID] = detail
	}
	return rows.Err()
}

func (s *Store) feedJudgmentDetails(ctx context.Context, ids []string, out map[string]FeedRecordDetail) error {
	if len(ids) == 0 {
		return nil
	}
	rows, err := s.db.QueryContext(ctx, `SELECT j.snapshot,j.data,COALESCE(p.issue,0)
		FROM factory_priority_judgments j
		LEFT JOIN factory_publications p ON (p.data->>'id')=(j.data->'input'->>'publication_id')
		WHERE j.snapshot = ANY($1) AND j.data IS NOT NULL`, ids)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var detail FeedRecordDetail
		var raw []byte
		if err := rows.Scan(&detail.ID, &raw, &detail.Issue); err != nil {
			return err
		}
		judgment, at, status, issue, ok := feedJudgmentPayload(raw)
		if !ok {
			continue
		}
		detail.Actor, detail.Author = "jev", "JEV"
		detail.At = at
		detail.Status = status
		if detail.Issue == 0 {
			detail.Issue = issue
		}
		detail.Payload = &factory.FeedPayload{Judgment: judgment}
		out[detail.ID] = detail
	}
	return rows.Err()
}

// feedJudgmentPayload projects one stored judgment journal row:
// a delivery receipt (kind delivery-v1) or a priority receipt.
// Rows that decode as neither report absent.
func feedJudgmentPayload(raw []byte) (judgment *factory.FeedJudgment, at int64, status string, issue int64, ok bool) {
	var probe struct {
		Kind string `json:"kind"`
	}
	if json.Unmarshal(raw, &probe) != nil {
		return nil, 0, "", 0, false
	}
	if probe.Kind == factory.DeliveryReceiptKind {
		var receipt factory.DeliveryReceipt
		if json.Unmarshal(raw, &receipt) != nil {
			return nil, 0, "", 0, false
		}
		answer := receipt.Decision.Answer
		judgment = &factory.FeedJudgment{
			Kind: "delivery", Selected: answer.Selected,
			Outcome: receipt.Outcome, Effective: receipt.Decision.Effective, Model: answer.Model,
			Probabilities: answer.Probabilities,
		}
		probability, confidence := answer.Probability, answer.Confidence
		judgment.Probability, judgment.Confidence = &probability, &confidence
		status = "judgment:delivery"
		if receipt.Decision.Effective != "" {
			status += ":" + receipt.Decision.Effective
		}
		return judgment, receipt.CreatedUnix, status, 0, true
	}
	var receipt factory.PriorityReceipt
	if json.Unmarshal(raw, &receipt) != nil || len(receipt.Candidates) == 0 {
		return nil, 0, "", 0, false
	}
	judgment = &factory.FeedJudgment{
		Kind: "priority", Selected: receipt.Selected,
		Outcome: receipt.Outcome, CandidatesTotal: len(receipt.Candidates),
	}
	for i, candidate := range receipt.Candidates {
		if candidate.ID == receipt.Selected {
			issue = candidate.Control.Issue
		}
		if i >= feedPayloadCap {
			continue
		}
		judgment.Candidates = append(judgment.Candidates, factory.FeedJudgmentCandidate{
			ID: candidate.ID, Repository: candidate.Control.Repository,
			Issue: candidate.Control.Issue, Objective: feedText(candidate.Objective),
		})
	}
	if len(receipt.Calls) > 0 {
		answer := receipt.Calls[len(receipt.Calls)-1].Answer
		judgment.Model = answer.Model
		judgment.Probabilities = answer.Probabilities
		confidence := answer.Confidence
		judgment.Confidence = &confidence
		if probability, found := answer.Probabilities[receipt.Selected]; found {
			judgment.Probability = &probability
		}
	}
	status = "judgment:priority"
	if receipt.Outcome != "" {
		status += ":" + receipt.Outcome
	}
	return judgment, receipt.CreatedUnix, status, issue, true
}
