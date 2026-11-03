package control

import (
	"context"
	"errors"
	"slices"
	"sort"

	"github.com/levitateos/sodaos/internal/factory"
	"github.com/levitateos/sodaos/internal/store"
)

// reassessmentInputs reconstructs proposal authority from the exact
// accepted records in the sealed material. Mutable work must still have
// its recorded acceptance and unedited native body; delivered, running,
// PR-linked and manually changed work remains protected.
func (c *Coordinator) reassessmentInputs(ctx context.Context, record store.ReassessmentRecord, report factory.ReassessmentReport) (map[string]ReassessmentPrior, ReassessmentAuthority, []int64, error) {
	if len(record.Cycle.Ledger.CoderRuns) != 1 {
		return nil, ReassessmentAuthority{}, nil, errors.New("reassessment lacks its sole coder")
	}
	return c.graphRepairInputs(ctx, record.Local, record.Evidence, record.Cycle.Ledger.CoderRuns[0], record.Completion.AcceptanceID, report.IntentChanges)
}

// graphRepairInputs shares the protected accepted graph authority for genuine
// completion and stall causes; neither caller can invent native issue authority.
func (c *Coordinator) graphRepairInputs(ctx context.Context, local factory.CompletionLocal, evidenceBodies []factory.CompletionIssueEvidence, runID, delivered string, changes []factory.ReassessmentIntentChange) (map[string]ReassessmentPrior, ReassessmentAuthority, []int64, error) {
	priors := map[string]ReassessmentPrior{}
	authority := ReassessmentAuthority{EffectiveIdeas: map[string]map[string]bool{}, DeliveredVerified: map[string]bool{}}
	material := local.Material
	if material == nil {
		return nil, authority, nil, errors.New("reassessment lacks its sealed material")
	}
	coder, err := c.Store.FactoryRun(ctx, runID)
	if err != nil {
		return nil, authority, nil, err
	}
	for _, merge := range material.Merges {
		if merge.Stage == factory.MergeMerged && merge.MergedCommit != "" && merge.OriginalClosingOccurrence != "" {
			authority.DeliveredVerified[merge.Acceptance] = true
		}
	}
	if delivered != "" {
		authority.DeliveredVerified[delivered] = true
	}
	evidence := map[int64]factory.CompletionIssueEvidence{}
	currentAcceptances := map[string]bool{}
	for _, acceptance := range material.Acceptances {
		currentAcceptances[acceptance.ID] = true
	}
	for _, body := range evidenceBodies {
		evidence[body.Index] = body
	}
	needed := map[string]bool{}
	for _, change := range changes {
		needed[change.IntentID] = true
	}
	for _, scope := range material.Intents {
		if !needed[scope.IntentID] || !scope.LatestAccepted {
			continue
		}
		revision, err := c.Store.CompletionIntentRevision(ctx, scope)
		if err != nil {
			return nil, authority, nil, err
		}
		if err := c.currentAcceptedIntent(ctx, revision); err != nil {
			return nil, authority, nil, err
		}
		prior := ReassessmentPrior{Revision: revision, SubmittedTasks: scope.SubmittedTasks, SuccessorCreatedUnix: coder.Started.Unix(), Bindings: map[string]factory.IntentTaskBinding{}, Protected: map[string]bool{}, PrereqAcceptance: map[string]map[string]string{}}
		if scope.GenerationState != "completed" || len(prior.SubmittedTasks) == 0 {
			return nil, authority, nil, errors.New("reassessment intent lacks a completed native task graph")
		}
		for _, binding := range scope.Bindings {
			prior.Bindings[binding.TaskKey] = binding
		}
		ideas := map[string]bool{}
		for _, idea := range scope.Ideas {
			ideas[idea.ID] = true
		}
		authority.EffectiveIdeas[scope.IntentID] = ideas
		for _, task := range prior.SubmittedTasks {
			binding, ok := prior.Bindings[task.Key]
			if !ok {
				return nil, authority, nil, errors.New("reassessment task lacks its generated native binding")
			}
			decision, err := c.Store.AcceptanceDecision(ctx, binding.AcceptanceID)
			if err != nil {
				return nil, authority, nil, err
			}
			body, present := evidence[binding.IssueIndex]
			protected := !present || !currentAcceptances[binding.AcceptanceID] || body.TitleDigest != decision.TitleDigest || body.ContentDigest != decision.ContentDigest || authority.DeliveredVerified[binding.AcceptanceID]
			dependencies := make([]string, 0, len(decision.Prerequisites))
			for _, prerequisite := range decision.Prerequisites {
				dependencies = append(dependencies, prerequisite.DependsOn)
			}
			sort.Slice(dependencies, func(i, j int) bool {
				return len(dependencies[i]) < len(dependencies[j]) || len(dependencies[i]) == len(dependencies[j]) && dependencies[i] < dependencies[j]
			})
			if !slices.Equal(dependencies, body.Dependencies) {
				protected = true
			}
			head, err := c.Store.AcceptanceHead(ctx, revision.Repository, binding.IssueIndex)
			if err != nil || head != binding.AcceptanceID {
				protected = true
			}
			if _, delivered := scope.Delivered[task.Key]; delivered {
				protected = true
			}
			for _, assignment := range material.Assignments {
				if assignment.Issue == binding.IssueIndex && assignment.Stage == factory.AssignmentAssigned {
					protected = true
				}
			}
			for _, publication := range material.Publications {
				if publication.Issue == binding.IssueIndex && publication.PRID > 0 {
					protected = true
				}
			}
			prior.Protected[task.Key] = protected
			routes := map[string]string{}
			for _, dep := range task.DependsOn {
				bound, ok := prior.Bindings[dep]
				if !ok {
					return nil, authority, nil, errors.New("reassessment prerequisite lacks its native binding")
				}
				for _, prerequisite := range decision.Prerequisites {
					if prerequisite.EndpointRepo == revision.Repository && prerequisite.EndpointIssue == bound.IssueIndex && prerequisite.PrereqAcceptance == bound.AcceptanceID {
						routes[dep] = bound.AcceptanceID
					}
				}
			}
			prior.PrereqAcceptance[task.Key] = routes
		}
		priors[scope.IntentID] = prior
	}
	changedIndexes := map[int64]bool{}
	for _, change := range changes {
		prior, ok := priors[change.IntentID]
		if !ok {
			return nil, authority, nil, errors.New("reassessment changes an unknown accepted intent")
		}
		previous, proposed := map[string]factory.IntentTask{}, map[string]factory.IntentTask{}
		for _, task := range prior.SubmittedTasks {
			previous[task.Key] = task
		}
		for _, task := range change.Tasks {
			proposed[task.Key] = task
		}
		changed, retired := diffReassessmentTasks(previous, proposed)
		affected, err := transitivelyAffectedTasks(proposed, changed, prior)
		if err != nil {
			return nil, authority, nil, err
		}
		for _, key := range retired {
			affected[key] = true
		}
		for key := range affected {
			if binding, exists := prior.Bindings[key]; exists {
				changedIndexes[binding.IssueIndex] = true
			}
		}
	}
	changed := make([]int64, 0, len(changedIndexes))
	for index := range changedIndexes {
		changed = append(changed, index)
	}
	sort.Slice(changed, func(i, j int) bool { return changed[i] < changed[j] })
	return priors, authority, changed, nil
}
