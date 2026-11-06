package control

import (
	"context"
	"errors"

	"github.com/levitateos/factory/pkg/factory"
	"github.com/levitateos/factory/pkg/store"
)

func (c *Coordinator) driveDeliveryRepairAdoption(ctx context.Context, record store.DeliveryRepairRecord) {
	c.driveRepairAdoption(ctx, repairAdoptionCause{delivery: &record, cycle: record.Cycle})
}

func (c *Coordinator) validateDeliveryRepairGeneration(ctx context.Context, generation factory.IntentGeneration) (*DispatchWait, error) {
	if generation.ReassessmentCycle != "" || generation.PlanAssignment != "" || !factory.ValidID(generation.RepairCycle) {
		return nil, errors.New("ambiguous delivery repair graph owner")
	}
	record, cycle, err := c.Store.DeliveryRepairCycle(ctx, generation.RepairCycle)
	if err != nil {
		return nil, err
	}
	return c.validateRepairAdoptionGeneration(ctx, generation, repairAdoptionCause{delivery: &record, cycle: cycle})
}

func loadDeliveryRepairAdoption(codec factory.GraphCodec, record store.DeliveryRepairRecord) (reassessmentAdoptionProgress, error) {
	p, err := loadGraphRepairAdoption(codec, record.Cycle.AdoptionProgress, record.Cause.Repository, "", "", record.Cycle.ID, record.Cycle.InventoryDigest)
	if err != nil {
		return p, err
	}
	if p.Journal.DeliveryID != record.Subject.DeliveryID || p.Journal.FailedVerificationRunID != record.Subject.FailedVerificationRunID {
		return p, errors.New("delivery repair journal answers another failure")
	}
	return p, nil
}

func (c *Coordinator) holdDeliveryRepair(ctx context.Context, record store.DeliveryRepairRecord, cycle factory.StallRepairCycle, reason string) {
	record.Cycle = cycle
	c.saveDeliveryRepair(ctx, record, cycle.State, reason)
}

func (c *Coordinator) saveDeliveryRepairCycle(ctx context.Context, record store.DeliveryRepairRecord, cycle factory.StallRepairCycle) error {
	record.Cycle = cycle
	record.Revision++
	return c.Store.SaveDeliveryRepairRecord(ctx, record, record.Revision-1)
}
