package control

import (
	"context"
	"errors"

	"github.com/levitateos/sodaos/internal/factory"
)

// MaxCorrectionReportBytes bounds the complete fenced correction report block
// the parser attempts to decode. A correction report carries the same bounded
// finding set as a review report plus one small disposition per key, so it
// shares the review-report ceiling: worst valid encodings stay below 2 MiB.
const MaxCorrectionReportBytes = 2 << 20

// CorrectionAdmission is one corrector run's admitted report: the cycle and
// run it answered, the finding-set digest covered, and the disposition keys
// by outcome. Admitted reports proceed to judgment; judgment (FW21) decides
// review_again or ready_to_merge, and native resolution (FW22) follows only
// an acceptance. Admission never resolves threads itself.
type CorrectionAdmission struct {
	CycleID          string   `json:"cycle_id"`
	RunID            string   `json:"run_id"`
	SetDigest        string   `json:"set_digest"`
	NewCandidate     string   `json:"new_candidate"`
	Fixed            []string `json:"fixed,omitempty"`
	Unresolved       []string `json:"unresolved,omitempty"`
	DeclinedOptional []string `json:"declined_optional,omitempty"`
}

// CorrectionBlock reports one correction that needs maintainer intervention
// instead of a retry: disputed findings use existing intervention, and only
// a human decision unblocks them. Refusals (plain errors) carry a corrected
// report; blocks do not.
type CorrectionBlock struct {
	Reason string
}

func (b *CorrectionBlock) Error() string { return "correction needs intervention: " + b.Reason }

// AdmitCorrectionReport validates one corrector run's report against the
// review cycle it answered. Every finding is accounted for exactly once
// (FW18 report validation plus the cycle's exact finding-set digest);
// missing, duplicate, unknown or stale dispositions refuse. Disputed findings
// block with intervention and never reach judgment. Actionable findings
// declined as optional refuse; optional-only reports admit without forcing
// failure. Fixed code findings require a changed candidate plus relevant
// changed paths/commits on the exact new tree; verification-only findings
// may retain the same head when new passing checks on that exact head prove
// the required outcome. Unchanged code cannot claim a code fix.
func AdmitCorrectionReport(cycle factory.ReviewCycle, runID, newCandidate string, report factory.CorrectionReport, evidence []factory.CorrectionFixEvidence, checks *factory.CheckAssessment) (CorrectionAdmission, error) {
	var empty CorrectionAdmission
	if err := admitCorrectionIdentity(cycle, runID, newCandidate, report); err != nil {
		return empty, err
	}
	byKey := make(map[string]factory.Finding, len(report.Findings))
	for _, finding := range report.Findings {
		byKey[finding.Key] = finding
	}
	evidenceByKey, err := indexCorrectionEvidence(evidence)
	if err != nil {
		return empty, err
	}
	admission := CorrectionAdmission{
		CycleID: cycle.ID, RunID: runID,
		SetDigest: report.SetDigest, NewCandidate: newCandidate,
	}
	if err := foldCorrectionDispositions(cycle, newCandidate, report, byKey, evidenceByKey, checks, &admission); err != nil {
		return empty, err
	}
	if err := checkCorrectionEvidenceCoverage(report, byKey, evidenceByKey); err != nil {
		return empty, err
	}
	if err := checkCorrectionChecks(checks, newCandidate, cycle); err != nil {
		return empty, err
	}
	return admission, nil
}

// admitCorrectionIdentity binds one correction report to its cycle,
// run, candidate and finding set.
func admitCorrectionIdentity(cycle factory.ReviewCycle, runID, newCandidate string, report factory.CorrectionReport) error {
	if err := cycle.Validate(); err != nil {
		return err
	}
	if !factory.ValidID(runID) {
		return errors.New("invalid correction run")
	}
	if !factory.ValidCommit(newCandidate) {
		return errors.New("correction lacks its exact candidate")
	}
	if err := report.Validate(); err != nil {
		return err
	}
	// The retained candidate binds the report's own nomination: the
	// settlement-mapped candidate and the corrector's nominated tree must
	// be the same commit, so a mismatched wiring refuses instead of
	// advancing a tree the report never accounted.
	if report.NewCandidate != newCandidate {
		return errors.New("correction nomination differs from its recorded candidate")
	}
	if report.SetDigest != cycle.FindingDigest {
		return errors.New("correction dispositions answer a changed finding set")
	}
	return nil
}

// indexCorrectionEvidence indexes fix evidence by finding key,
// refusing invalid or duplicate entries.
func indexCorrectionEvidence(evidence []factory.CorrectionFixEvidence) (map[string]factory.CorrectionFixEvidence, error) {
	evidenceByKey := make(map[string]factory.CorrectionFixEvidence, len(evidence))
	for _, entry := range evidence {
		if err := entry.Validate(); err != nil {
			return nil, err
		}
		if _, dup := evidenceByKey[entry.Key]; dup {
			return nil, errors.New("correction fix evidence repeats its key")
		}
		evidenceByKey[entry.Key] = entry
	}
	return evidenceByKey, nil
}

// foldCorrectionDispositions folds one report's dispositions into its admission.
func foldCorrectionDispositions(cycle factory.ReviewCycle, newCandidate string, report factory.CorrectionReport, byKey map[string]factory.Finding, evidenceByKey map[string]factory.CorrectionFixEvidence, checks *factory.CheckAssessment, admission *CorrectionAdmission) error {
	for _, disposition := range report.Dispositions {
		finding := byKey[disposition.Key]
		entry, hasEvidence := evidenceByKey[disposition.Key]
		switch disposition.Disposition {
		case factory.DispositionDisputed:
			return &CorrectionBlock{Reason: "disputed finding " + disposition.Key}
		case factory.DispositionFixed:
			if err := checkFixedDisposition(cycle, newCandidate, finding, entry, hasEvidence, checks); err != nil {
				return err
			}
			admission.Fixed = append(admission.Fixed, disposition.Key)
		case factory.DispositionUnresolved:
			if hasEvidence {
				return errors.New("unresolved finding carries fix evidence")
			}
			admission.Unresolved = append(admission.Unresolved, disposition.Key)
		case factory.DispositionDeclinedOptional:
			if hasEvidence {
				return errors.New("declined optional finding carries fix evidence")
			}
			admission.DeclinedOptional = append(admission.DeclinedOptional, disposition.Key)
		default:
			return errors.New("invalid correction disposition")
		}
	}
	return nil
}

// checkFixedDisposition requires changed paths and commits behind one
// fixed finding, with new passing checks on a same-head fix.
func checkFixedDisposition(cycle factory.ReviewCycle, newCandidate string, finding factory.Finding, entry factory.CorrectionFixEvidence, hasEvidence bool, checks *factory.CheckAssessment) error {
	if finding.FixKind == factory.FindingFixCode && newCandidate == cycle.Head {
		return errors.New("unchanged code cannot fix a code defect")
	}
	if !hasEvidence {
		if finding.FixKind == factory.FindingFixCode || newCandidate != cycle.Head {
			return errors.New("fixed finding lacks its changed paths and commits")
		}
	} else if len(entry.ChangedPaths) == 0 && len(entry.Commits) == 0 {
		return errors.New("fixed finding lacks its changed paths and commits")
	}
	if newCandidate == cycle.Head {
		if checks == nil || checks.Verdict != factory.CheckPass || checks.HeadOID != newCandidate {
			return errors.New("same-head fix lacks new passing checks on its exact head")
		}
	}
	return nil
}

// checkCorrectionEvidenceCoverage requires every evidence entry to
// answer a known fixed finding.
func checkCorrectionEvidenceCoverage(report factory.CorrectionReport, byKey map[string]factory.Finding, evidenceByKey map[string]factory.CorrectionFixEvidence) error {
	for key := range evidenceByKey {
		if _, ok := byKey[key]; !ok {
			return errors.New("correction fix evidence answers an unknown finding")
		}
		if !evidenceFixesFinding(report, key) {
			return errors.New("correction fix evidence answers an unfixed finding")
		}
	}
	return nil
}

// evidenceFixesFinding reports whether one evidence key answers a
// fixed disposition.
func evidenceFixesFinding(report factory.CorrectionReport, key string) bool {
	for _, disposition := range report.Dispositions {
		if disposition.Key == key && disposition.Disposition == factory.DispositionFixed {
			return true
		}
	}
	return false
}

// checkCorrectionChecks binds check evidence to the admitted or
// cycle head.
func checkCorrectionChecks(checks *factory.CheckAssessment, newCandidate string, cycle factory.ReviewCycle) error {
	if checks == nil {
		return nil
	}
	if err := checks.Validate(); err != nil {
		return errors.New("correction check evidence is malformed")
	}
	if checks.HeadOID != newCandidate && checks.HeadOID != cycle.Head {
		return errors.New("correction check evidence binds another head")
	}
	return nil
}

// admitFirstCorrectionReport binds a first correction report to the
// cycle's canonical carried findings before any journal/ref effect. The
// submitted findings must exactly equal the owning history's
// reconstruction (same keys, same content, same provenance), so a
// same-key rewritten defect, an omission or a changed origin refuses
// instead of journaling. The round key stays the finding-set digest;
// this content binding is the separate domain that keeps key equality
// from standing in for content freshness. Later reports for the same
// cycle answer the first journal's canonical set through
// AdmitCorrectionReportCanonical. PublishCorrectionWithReport's
// first-admission branch enters here.
func (c *Coordinator) admitFirstCorrectionReport(ctx context.Context, p factory.Publication, cycle factory.ReviewCycle, runID, newCandidate string, report factory.CorrectionReport, evidence []factory.CorrectionFixEvidence, checks *factory.CheckAssessment) (CorrectionAdmission, error) {
	canonical, err := c.reviewCarriedFindings(ctx, p, cycle)
	if err != nil {
		return CorrectionAdmission{}, err
	}
	return AdmitCorrectionReportCanonical(cycle, runID, newCandidate, canonical, report, evidence, checks)
}

// appendCorrectionJournal records one admitted report as the publication's
// next immutable correction journal entry: the cycle must belong to this
// publication, the run reports once, and the report answers the cycle's
// exact finding-set digest. The store write re-validates the whole journal
// (FW18 rules); this keeps refused reports from ever reaching it.
func appendCorrectionJournal(p *factory.Publication, cycle factory.ReviewCycle, runID string, report factory.CorrectionReport) error {
	if cycle.PublicationID != p.ID {
		return errors.New("correction consumes another publication's cycle")
	}
	for _, journal := range p.CorrectionJournals {
		if journal.RunID == runID {
			return errors.New("correction run reported twice")
		}
	}
	journal := factory.CorrectionJournal{CycleID: cycle.ID, RunID: runID, Report: report}
	if err := journal.Validate(); err != nil {
		return err
	}
	if journal.Report.SetDigest != cycle.FindingDigest {
		return errors.New("correction dispositions answer a changed finding set")
	}
	known := false
	for _, recorded := range p.ReviewCycles {
		if recorded.ID == cycle.ID {
			known = true
			break
		}
	}
	if !known {
		return errors.New("correction consumes an unrecorded cycle")
	}
	p.CorrectionJournals = append(p.CorrectionJournals, journal)
	return nil
}

// AdmitAndJournalCorrection admits one corrector run's report against its
// recorded cycle and appends the immutable journal entry. Refused or blocked
// reports change nothing; admitted reports persist exactly one journal entry
// per run over the cycle's exact finding-set digest. Journaling records the
// report for judgment; it never resolves native threads.
func (c *Coordinator) AdmitAndJournalCorrection(ctx context.Context, ownerAssignmentID, cycleID, runID, newCandidate string, report factory.CorrectionReport, evidence []factory.CorrectionFixEvidence, checks *factory.CheckAssessment) (CorrectionAdmission, error) {
	var empty CorrectionAdmission
	if c.Store == nil {
		return empty, errors.New("correction journal unavailable")
	}
	p, err := c.Store.PublicationByAssignment(ctx, ownerAssignmentID)
	if err != nil {
		return empty, errors.New("correction publication missing")
	}
	var cycle *factory.ReviewCycle
	for i := range p.ReviewCycles {
		if p.ReviewCycles[i].ID == cycleID {
			cycle = &p.ReviewCycles[i]
			break
		}
	}
	if cycle == nil {
		return empty, errors.New("correction consumes an unrecorded cycle")
	}
	admission, err := AdmitCorrectionReport(*cycle, runID, newCandidate, report, evidence, checks)
	if err != nil {
		return empty, err
	}
	if err := appendCorrectionJournal(&p, *cycle, runID, report); err != nil {
		return empty, err
	}
	p.Revision++
	if err := c.Store.UpdatePublication(ctx, p); err != nil {
		return empty, errors.New("correction journal unrecorded")
	}
	return admission, nil
}
