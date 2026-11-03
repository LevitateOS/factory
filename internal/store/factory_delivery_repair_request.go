package store

import (
	"context"
	"errors"
	"time"

	"github.com/levitateos/sodaos/internal/factory"
)

// RemainingDeliveryRepairMinutes is a spend-free dispatch planning bound.
// Admission repeats the same actual origin-time join under the grant lock.
func (s *Store) RemainingDeliveryRepairMinutes(ctx context.Context, repository int64, failedRun string, now time.Time) (int, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	record, err := deliveryRepairRecordTx(ctx, tx, repository, failedRun, false)
	if err != nil {
		return 0, err
	}
	run := factory.RepairChargeID(factory.DeliveryRepairCauseID(failedRun), factory.RepairChargeRun, 1)
	seconds, err := deliveryRepairIntentRemainingTx(ctx, tx, record, run, now, false)
	if err != nil {
		return 0, err
	}
	return min(factory.DefaultRepairCycleActiveMinutes, int(seconds/60)), nil
}

// DeliveryRepairRequestAvailable is advisory, spend-free eligibility. The
// explicit owner request still repeats authority/native checks and records any
// missing default origin limit; charge/packet transactions remain authoritative.
func (s *Store) DeliveryRepairRequestAvailable(ctx context.Context, cause factory.DeliveryRepairCause, now time.Time) (bool, error) {
	if err := cause.Validate(); err != nil {
		return false, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	if _, err = deliveryRepairRecordTx(ctx, tx, cause.Repository, cause.FailedVerificationRunID, false); err == nil {
		return false, nil
	} else if !errors.Is(err, ErrNotFound) {
		return false, err
	}
	if err = validateDeliveryRepairCauseTx(ctx, tx, cause); err != nil {
		return false, err
	}
	grant, err := currentDeliveryRepairGrantTx(ctx, tx, cause.Repository)
	if errors.Is(err, ErrNotFound) || errors.Is(err, factory.ErrRepairGrantInactive) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	charges, err := repairCharges(ctx, tx, cause.Repository)
	if err != nil {
		return false, err
	}
	episode := factory.DeliveryRepairCauseID(cause.FailedVerificationRunID)
	charge := factory.RepairCharge{ID: factory.RepairChargeID(episode, factory.RepairChargeRun, 1), GrantID: grant.ID, Kind: factory.RepairChargeRun, EpisodeID: episode, Cycle: 1, EvidenceDigest: cause.FailureDigest, ActiveMinutes: 1, SpentUnix: now.Unix()}
	account, _, err := (factory.RepairAccount{Grant: grant, Charges: charges}).AdmitCharge(charge)
	if err != nil {
		return false, nil
	}
	charge.ID, charge.Kind, charge.ActiveMinutes = factory.RepairChargeID(episode, factory.RepairChargeJudgment, 1), factory.RepairChargeJudgment, 0
	if _, _, err = account.AdmitCharge(charge); err != nil {
		return false, nil
	}
	record := DeliveryRepairRecord{Cause: cause, Cycle: factory.StallRepairCycle{ID: factory.DeliveryRepairCycleID(cause.FailedVerificationRunID)}}
	if err = checkDeliveryRepairRunsTx(ctx, tx, record, factory.RepairChargeID(episode, factory.RepairChargeRun, 1)); errors.Is(err, factory.ErrRepairRunsExhausted) {
		return false, nil
	} else if err != nil {
		return false, err
	}
	err = deliveryRepairIntentTimeTx(ctx, tx, record, factory.RepairChargeID(episode, factory.RepairChargeRun, 1), 60, now, true)
	if errors.Is(err, factory.ErrIntentTimeExhausted) {
		return false, nil
	}
	return err == nil, err
}
