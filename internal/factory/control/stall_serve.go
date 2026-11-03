package control

import (
	"context"
	"time"
)

// stallEpisodeActiveMinutes bounds one admitted cycle-1 investigator
// run: D15 allows at most 30 active minutes per automatic cycle, with
// the tighter remaining grant limits prevailing inside admission.
const stallEpisodeActiveMinutes = 30

// ServeStallRound runs D15 detection inside the existing progress
// pass after a dispatch pass selected no work. A pass that launched
// anything skips detection entirely: ready work resumes selection
// and ordinary waits never call an investigator. Detection spends
// nothing itself; every bound episode persists as a durable
// observation, and only an investigate result admits one bounded
// cycle-1 charge through the explicit repair-grant joint. Any store
// failure fails the round closed instead of spending blind.
func (c *Coordinator) ServeStallRound(ctx context.Context, report DispatchReport) (StallClassification, error) {
	if c.Store == nil {
		return StallClassification{}, nil
	}
	if len(report.Launched) > 0 {
		ready := map[int64]bool{}
		for _, launch := range report.Launched {
			if launch.Issue > 0 {
				ready[launch.Repository] = true
			}
		}
		c.retireReadyStallRepairs(ctx, ready)
		return StallClassification{}, nil
	}
	generation, err := c.Store.StallRoundGeneration(ctx)
	if err != nil {
		return StallClassification{}, err
	}
	standing, err := c.Store.StandingStallEvents(ctx)
	if err != nil {
		return StallClassification{}, err
	}
	prior := make(map[int64]map[string]string, len(standing))
	for _, entry := range standing {
		events, ok := prior[entry.Repository]
		if !ok {
			events = map[string]string{}
			prior[entry.Repository] = events
		}
		events[entry.EventID] = entry.Digest
	}
	classified, err := c.DetectRepairStalls(ctx, StallDetectDeps{
		Store: c.Store, Generation: generation,
		DispatchWaits: report.Waits, PriorEvents: prior,
	})
	if err != nil {
		return StallClassification{}, err
	}
	now := time.Now()
	ready := map[int64]bool{}
	stalled := map[int64]bool{}
	for _, result := range classified.Repositories {
		if result.Kind == StallRepositoryReady {
			ready[result.Repository] = true
		}
		if result.Kind == StallRepositoryStall {
			stalled[result.Repository] = true
		}
		if result.Event == nil {
			continue
		}
		if _, err := c.Store.SaveStallEvent(ctx, *result.Event); err != nil {
			return StallClassification{}, err
		}
		if !result.Investigate {
			continue
		}
		if _, err := AdmitStallEpisodeRun(ctx, c.Store, result, generation, stallEpisodeActiveMinutes, now); err != nil {
			return StallClassification{}, err
		}
		if err := c.stageStallRepair(ctx, *result.Event); err != nil {
			return StallClassification{}, err
		}
	}
	c.retireReadyStallRepairs(ctx, ready)
	c.driveStallRepairs(ctx, stalled)
	return classified, nil
}
