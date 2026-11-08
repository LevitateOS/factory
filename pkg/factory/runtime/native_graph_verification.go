package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strconv"

	"github.com/levitateos/factory/pkg/collaboration"
	"github.com/levitateos/factory/pkg/factory"
	"github.com/levitateos/factory/pkg/factory/control"
	"github.com/levitateos/factory/pkg/store"
)

func (s NativeGraphs) taskRefs(ctx context.Context, g factory.IntentGeneration, p nativeGraphPacket, progress nativeGraphProgress) (map[string]collaboration.IssueRef, error) {
	refs := map[string]collaboration.IssueRef{}
	for key, ref := range progress.creates {
		refs[key] = ref
	}
	existing := map[int64]collaboration.IssueRef{}
	observe := func(ref collaboration.IssueRef) error {
		id, err := strconv.ParseInt(ref.ExternalID, 10, 64)
		if err != nil || id <= 0 || ref.Repository != collaboration.RepositoryID(p.Repository) {
			return errNativeReadScope
		}
		if prior, ok := existing[id]; ok && prior != ref {
			return errNativeReadScope
		}
		existing[id] = ref
		return nil
	}
	for _, edit := range p.Graph.Edits {
		if err := observe(edit.Issue); err != nil {
			return nil, err
		}
	}
	for _, edge := range p.Graph.Dependencies {
		for _, ref := range []collaboration.GraphRef{edge.Issue, edge.Prerequisite} {
			if ref.Issue != nil {
				if err := observe(*ref.Issue); err != nil {
					return nil, err
				}
			}
		}
	}
	for key, identity := range g.Delivered {
		refs[key] = collaboration.IssueRef{Kind: collaboration.SubjectIssue, Repository: collaboration.RepositoryID(p.Repository), ExternalID: strconv.FormatInt(identity.IssueID, 10), Number: identity.IssueIndex}
	}
	for _, binding := range append(append([]factory.IntentTaskBinding{}, g.RetainedBindings...), g.CopiedBindings...) {
		refs[binding.TaskKey] = collaboration.IssueRef{Kind: collaboration.SubjectIssue, Repository: collaboration.RepositoryID(p.Repository), ExternalID: strconv.FormatInt(binding.IssueID, 10), Number: binding.IssueIndex}
	}
	previous, err := s.Store.PreviousGeneratedIntent(ctx, g.IntentID, g.RevisionID)
	if err == nil {
		bindings, e := s.Store.IntentTaskBindings(ctx, previous.ID)
		if e != nil {
			return nil, e
		}
		for _, binding := range bindings {
			existing[binding.IssueID] = collaboration.IssueRef{Kind: collaboration.SubjectIssue, Repository: collaboration.RepositoryID(p.Repository), ExternalID: strconv.FormatInt(binding.IssueID, 10), Number: binding.IssueIndex}
		}
	} else if !errors.Is(err, store.ErrNotFound) {
		return nil, err
	}
	for key, id := range g.TaskIssueIDs {
		if _, ok := refs[key]; ok {
			continue
		}
		ref, ok := existing[id]
		if !ok {
			return nil, errors.New("native graph retained task lacks its original issue binding")
		}
		refs[key] = ref
	}
	return refs, nil
}
func (s NativeGraphs) verifyGraph(ctx context.Context, g factory.IntentGeneration, p nativeGraphPacket, progress nativeGraphProgress) (json.RawMessage, error) {
	if progress.blocked != nil || progress.next != nativeGraphCount(p) || len(progress.commands) != nativeGraphCount(p) {
		return nil, errors.New("native graph verification requires every original committed step")
	}
	revision, err := s.Store.IntentGenerationHead(ctx, g.RevisionID)
	if err != nil {
		return nil, err
	}
	if revision.Intent != g.IntentID || revision.State != "accepted" || revision.Digest() != g.IntentDigest {
		return nil, store.ErrStaleRevision
	}
	// Fully committed writes may be verified after expiry or reconnect. This
	// current read authority never replaces the original generation's write
	// revision/deadline; pending and unknown steps still use the original guard.
	verification, err := s.CheckIntentOwner(ctx, p.Repository, revision.Owner, g.ActorID)
	if err != nil {
		return nil, err
	}
	scoped := revision
	scoped.Tasks = g.SubmittedTasks
	desired, err := control.IntentGraphPlan(scoped)
	if err != nil {
		return nil, err
	}
	refs, err := s.taskRefs(ctx, g, p, progress)
	if err != nil {
		return nil, err
	}
	versions := map[string]int{}
	for index, task := range g.SubmittedTasks {
		ref, ok := refs[task.Key]
		if !ok {
			return nil, errors.New("native graph is missing a complete task binding")
		}
		observed, _, err := s.ReadGeneratedIssue(ctx, p.Repository, ref.Number, g.ActorID)
		if err != nil {
			return nil, err
		}
		issue := observed.Issue
		if issue == nil || !issue.Visible || !issue.Complete || issue.IsPull || issue.IsLocked || issue.ID != ref.ExternalID || issue.Index != strconv.FormatInt(ref.Number, 10) || issue.Creator == nil || int64(*issue.Creator) != g.ActorID || observed.AuthorityRevision != verification {
			return nil, errNativeReadScope
		}
		if _, delivered := g.Delivered[task.Key]; !delivered && (issue.IsClosed || issue.Title != desired.Creates[index].Title || issue.Content != desired.Creates[index].Body) {
			return nil, errors.New("native graph task content changed")
		}
		if observed.Dependencies == nil || !observed.Dependencies.Complete || observed.Dependencies.IssueID != issue.ID || len(observed.Dependencies.Items) != len(task.DependsOn) {
			return nil, errors.New("native graph dependencies are incomplete")
		}
		expected := map[string]bool{}
		for _, key := range task.DependsOn {
			dependency, ok := refs[key]
			if !ok {
				return nil, errNativeReadScope
			}
			expected[dependency.ExternalID] = true
		}
		for _, edge := range observed.Dependencies.Items {
			if !edge.Visible || !edge.Complete || !expected[edge.DependencyID] {
				return nil, errors.New("native graph dependency differs from the intended graph")
			}
			delete(expected, edge.DependencyID)
		}
		if len(expected) != 0 {
			return nil, errNativeRead
		}
		versions[issue.ID] = issue.ContentVersion
	}
	authority, err := s.CheckIntentOwner(ctx, p.Repository, revision.Owner, g.ActorID)
	if err != nil {
		return nil, err
	}
	if authority != verification {
		return nil, errNativeReadScope
	}
	return json.Marshal(nativeGraphReceipt{Version: 1, Operation: g.OperationID, Repository: p.Repository, Actor: g.ActorID, Commands: progress.commands, Versions: versions, VerificationRevision: verification})
}
func (s NativeGraphs) DecodeGraphReceipt(ctx context.Context, raw json.RawMessage) (factory.GraphReceiptFacts, error) {
	var facts factory.GraphReceiptFacts
	var receipt nativeGraphReceipt
	if nativeDecode(raw, &receipt) != nil || receipt.Version != 1 || receipt.Operation == "" || receipt.Repository <= 0 || receipt.Actor <= 0 || receipt.VerificationRevision < 0 || len(receipt.Commands) > 64 || len(receipt.Versions) > 64 {
		return facts, errNativeRead
	}
	generation, err := s.Store.IntentGenerationOperation(ctx, receipt.Operation)
	if err != nil {
		return facts, err
	}
	p, err := s.registered(ctx, generation, receipt.Repository)
	if err != nil {
		return facts, err
	}
	if generation.ActorID != receipt.Actor {
		return facts, errNativeReadScope
	}
	progress, err := s.scan(ctx, generation, p)
	if err != nil {
		return facts, err
	}
	if progress.next != nativeGraphCount(p) || progress.blocked != nil || !reflect.DeepEqual(progress.commands, receipt.Commands) {
		return facts, errors.New("native graph receipt lacks complete original step outcomes")
	}
	facts = factory.GraphReceiptFacts{Original: append([]byte(nil), raw...), Repository: receipt.Repository, Actor: receipt.Actor, Creates: map[string]factory.GeneratedIssueIdentity{}}
	identity := func(ref collaboration.IssueRef) (factory.GeneratedIssueIdentity, error) {
		id, err := strconv.ParseInt(ref.ExternalID, 10, 64)
		version, ok := receipt.Versions[ref.ExternalID]
		if err != nil || id <= 0 || !ok || version < 0 {
			return factory.GeneratedIssueIdentity{}, errNativeRead
		}
		return factory.GeneratedIssueIdentity{IssueID: id, IssueIndex: ref.Number, ContentVersion: version}, nil
	}
	for key, ref := range progress.creates {
		value, err := identity(ref)
		if err != nil {
			return facts, err
		}
		facts.Creates[key] = value
	}
	for _, ref := range progress.edits {
		value, err := identity(ref)
		if err != nil {
			return facts, err
		}
		facts.Edits = append(facts.Edits, value)
	}
	return facts, nil
}
func (s NativeGraphs) CancelGraph(ctx context.Context, operation string) (factory.GraphOperationOutcome, error) {
	var empty factory.GraphOperationOutcome
	g, err := s.Store.IntentGenerationOperation(ctx, operation)
	if err != nil {
		return empty, err
	}
	p, err := nativeDecodeGraph(g.Payload)
	if err != nil {
		return empty, err
	}
	progress, err := s.scan(ctx, g, p)
	if err != nil {
		return empty, err
	}
	if len(progress.commands) == nativeGraphCount(p) {
		observed, err := s.LookupGraph(ctx, g, p.Repository)
		if err != nil {
			return empty, err
		}
		if observed.Record != nil {
			return *observed.Record, nil
		}
	}
	if progress.next < nativeGraphCount(p) {
		cmd, err := s.Store.FactoryCommand(ctx, nativeGraphStepID(operation, progress.next))
		if err == nil {
			switch cmd.Type {
			case factory.CommandHumanIssueCreate:
				c, e := factory.DecodeHumanIssueCreateCapture(cmd)
				if e != nil {
					return empty, e
				}
				_, err = s.Store.CancelHumanIssueCreateDispatch(ctx, c)
			case factory.CommandHumanEdit:
				c, e := factory.DecodeHumanEditCapture(cmd)
				if e != nil {
					return empty, e
				}
				_, err = s.Store.CancelHumanEditDispatch(ctx, c)
			case factory.CommandIssueDependency:
				c, e := factory.DecodeIssueDependencyCapture(cmd)
				if e != nil {
					return empty, e
				}
				_, err = s.Store.CancelIssueDependency(ctx, c)
			default:
				return empty, errNativeRead
			}
			if err != nil {
				return empty, err
			}
		} else if !errors.Is(err, store.ErrNotFound) {
			return empty, err
		}
	}
	if len(progress.commands) > 0 || progress.blocked != nil && progress.blocked.Effect != collaboration.EffectNotCommitted {
		return factory.GraphOperationOutcome{OperationID: operation, Effect: collaboration.EffectIndeterminate, Cancellation: collaboration.CancellationIndeterminate, Completion: collaboration.CompletionNeedsIntervention, Reason: "graph_partial_or_unknown_cancelled"}, nil
	}
	return factory.GraphOperationOutcome{OperationID: operation, Effect: collaboration.EffectNotCommitted, Cancellation: collaboration.CancellationCancelled, Completion: collaboration.CompletionComplete, Reason: "graph_cancelled_before_effect"}, nil
}
