package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strconv"
	"time"

	"github.com/levitateos/factory/pkg/collaboration"
	"github.com/levitateos/factory/pkg/factory"
	factoryservice "github.com/levitateos/factory/pkg/factory/service"
	"github.com/levitateos/factory/pkg/store"
)

type nativeGraphProgress struct {
	next     int
	commands []string
	creates  map[string]collaboration.IssueRef
	edits    []collaboration.IssueRef
	blocked  *collaboration.OperationRecord
}

func nativeGraphCount(p nativeGraphPacket) int {
	return len(p.Graph.Creates) + len(p.Graph.Edits) + len(p.Graph.Dependencies)
}
func nativeGraphRef(ref collaboration.GraphRef, created map[string]collaboration.IssueRef) (collaboration.IssueRef, error) {
	if ref.Issue != nil && ref.Key == "" {
		return *ref.Issue, nil
	}
	if issue, ok := created[ref.Key]; ok && ref.Issue == nil {
		return issue, nil
	}
	return collaboration.IssueRef{}, errors.New("native graph issue reference is not committed")
}
func (s NativeGraphs) scan(ctx context.Context, g factory.IntentGeneration, p nativeGraphPacket) (nativeGraphProgress, error) {
	progress := nativeGraphProgress{next: nativeGraphCount(p), creates: map[string]collaboration.IssueRef{}}
	repo, err := s.Store.CollaborationRepository(ctx, collaboration.RepositoryID(p.Repository))
	if err != nil {
		return progress, err
	}
	actor, err := s.Store.CollaborationActor(ctx, collaboration.ActorID(g.ActorID))
	if err != nil {
		return progress, err
	}
	authority := "native-github:" + string(s.Config.Profile) + ":" + actor.ExternalID
	for index := 0; index < nativeGraphCount(p); index++ {
		id := nativeGraphStepID(g.OperationID, index)
		cmd, err := s.Store.FactoryCommand(ctx, id)
		if errors.Is(err, store.ErrNotFound) {
			if progress.next == nativeGraphCount(p) {
				progress.next = index
			}
			continue
		}
		if err != nil {
			return progress, err
		}
		if progress.next < index {
			return progress, errors.New("native graph has a non-prefix command history")
		}
		var issue collaboration.IssueRef
		switch {
		case index < len(p.Graph.Creates):
			c, e := factory.DecodeHumanIssueCreateCapture(cmd)
			if e != nil {
				return progress, e
			}
			desired := p.Graph.Creates[index]
			if c.Authority != authority || c.OperationID != id || c.Request.Actor != collaboration.ActorID(g.ActorID) || c.Request.Repository != repo.ID || c.Request.Host != repo.Host || c.Request.Title != desired.Title || c.Request.Body != desired.Body {
				return progress, errNativeReadScope
			}
		case index < len(p.Graph.Creates)+len(p.Graph.Edits):
			c, e := factory.DecodeHumanEditCapture(cmd)
			if e != nil {
				return progress, e
			}
			desired := p.Graph.Edits[index-len(p.Graph.Creates)]
			if c.Authority != authority || c.OperationID != id || c.Request.Actor != collaboration.ActorID(g.ActorID) || c.Request.Repository != repo.ID || c.Request.Host != repo.Host || c.Request.Subject != "issue" || c.Request.Number != desired.Issue.Number || c.Request.Title == nil || *c.Request.Title != desired.Title || c.Request.Body == nil || *c.Request.Body != desired.Body || c.Request.Expected == nil || *c.Request.Expected != (factory.HumanEditBase{ExternalID: desired.Issue.ExternalID, Title: desired.Expected.Title, BodyDigest: factory.MCPInputDigest([]byte(desired.Expected.Body))}) {
				return progress, errNativeReadScope
			}
			issue = desired.Issue
		default:
			c, e := factory.DecodeIssueDependencyCapture(cmd)
			if e != nil {
				return progress, e
			}
			desired := p.Graph.Dependencies[index-len(p.Graph.Creates)-len(p.Graph.Edits)]
			root, e := nativeGraphRef(desired.Issue, progress.creates)
			if e != nil {
				return progress, e
			}
			blocking, e := nativeGraphRef(desired.Prerequisite, progress.creates)
			if e != nil {
				return progress, e
			}
			if c.Authority != authority || c.OperationID != id || c.Request.Actor != collaboration.ActorID(g.ActorID) || c.Request.Host != repo.Host || c.Request.Issue != root || c.Request.Blocking != blocking || c.Request.Remove != desired.Remove {
				return progress, errNativeReadScope
			}
		}
		observed, err := s.Store.LookupHumanCommandDispatch(ctx, cmd)
		if err != nil {
			return progress, err
		}
		if observed.Record == nil {
			progress.next = index
			continue
		}
		record := observed.Record
		if record.Effect != collaboration.EffectCommitted || record.Completion != collaboration.CompletionComplete {
			progress.blocked = record
			progress.next = index
			return progress, nil
		}
		if record.Receipt == nil {
			return progress, errNativeRead
		}
		if index < len(p.Graph.Creates) {
			c, e := factory.DecodeHumanIssueCreateCapture(cmd)
			if e != nil {
				return progress, e
			}
			var prep nativeIssueCreatePreparation
			if nativeDecode(c.ProviderIntent, &prep) != nil || record.Receipt.IntentDigest != nativeCreationSHA(c.ProviderIntent) {
				return progress, errNativeRead
			}
			receipt, e := validateNativeIssueCreateReceipt(c, prep, record.Receipt.Payload)
			if e != nil {
				return progress, e
			}
			issue = collaboration.IssueRef{Kind: collaboration.SubjectIssue, Repository: repo.ID, ExternalID: receipt.Issue.ExternalID, Number: receipt.Issue.Number}
			progress.creates[p.Graph.Creates[index].Key] = issue
		} else if index < len(p.Graph.Creates)+len(p.Graph.Edits) {
			c, e := factory.DecodeHumanEditCapture(cmd)
			if e != nil {
				return progress, e
			}
			var receipt nativeEditReceipt
			if record.Receipt.IntentDigest != nativeCreationSHA(c.ProviderIntent) || nativeDecode(record.Receipt.Payload, &receipt) != nil || receipt.Subject != "issue" || receipt.SubjectID != issue.ExternalID || receipt.Number != uint64(issue.Number) || !receipt.EditedTitle || !receipt.EditedBody || receipt.TitleSHA256 != factory.MCPInputDigest([]byte(*c.Request.Title)) || receipt.BodySHA256 != factory.MCPInputDigest([]byte(*c.Request.Body)) {
				return progress, errNativeRead
			}
			progress.edits = append(progress.edits, issue)
		} else {
			c, e := factory.DecodeIssueDependencyCapture(cmd)
			if e != nil {
				return progress, e
			}
			var prep nativeDependencyPreparation
			if nativeDecode(c.ProviderIntent, &prep) != nil || record.Receipt.IntentDigest != nativeCreationSHA(c.ProviderIntent) || validateNativeDependencyReceipt(c, prep, record.Receipt.Payload) != nil {
				return progress, errNativeRead
			}
		}
		progress.commands = append(progress.commands, id)
	}
	return progress, nil
}
func (s NativeGraphs) guard(ctx context.Context, g factory.IntentGeneration, p nativeGraphPacket) (*nativeProductAccount, error) {
	saved, err := s.Store.IntentGeneration(ctx, g.RevisionID)
	if err != nil {
		return nil, err
	}
	if saved.State != "pending" || g.NotAfter <= time.Now().Unix() {
		return nil, errors.New("native graph is not active")
	}
	revision, err := s.Store.IntentGenerationHead(ctx, g.RevisionID)
	// The original registered generation supplies its owner through the current
	// head; a caller cannot select another account to resume it.
	if err != nil {
		return nil, err
	}
	if revision.ID != g.RevisionID || revision.State != "accepted" || revision.Digest() != g.IntentDigest || g.AuthRevision != revision.ID+":"+revision.Digest() {
		return nil, store.ErrStaleRevision
	}
	authority, err := s.CheckIntentOwner(ctx, p.Repository, revision.Owner, g.ActorID)
	if err != nil {
		return nil, err
	}
	if authority != g.NativeRev {
		return nil, errNativeReadScope
	}
	return s.account(ctx, strconv.FormatInt(p.Repository, 10), factory.OpPRCreate)
}
func (s NativeGraphs) LookupGraph(ctx context.Context, g factory.IntentGeneration, repository int64) (factory.GraphOperationLookup, error) {
	var empty factory.GraphOperationLookup
	p, err := s.registered(ctx, g, repository)
	if err != nil {
		return empty, err
	}
	saved, err := s.Store.IntentGeneration(ctx, g.RevisionID)
	if err != nil {
		return empty, err
	}
	if (saved.State == "committed" || saved.State == "completed") && len(saved.Receipt) > 0 {
		if _, err := s.DecodeGraphReceipt(ctx, saved.Receipt); err != nil {
			return empty, err
		}
		record := factory.GraphOperationOutcome{OperationID: g.OperationID, Effect: collaboration.EffectCommitted, Cancellation: collaboration.CancellationNone, Completion: collaboration.CompletionComplete, Receipt: saved.Receipt}
		return factory.GraphOperationLookup{State: collaboration.LookupFound, Record: &record}, nil
	}
	progress, err := s.scan(ctx, g, p)
	if err != nil {
		return empty, err
	}
	record := factory.GraphOperationOutcome{OperationID: g.OperationID, Effect: collaboration.EffectIndeterminate, Cancellation: collaboration.CancellationNone, Completion: collaboration.CompletionNeedsIntervention, Reason: "graph_steps_pending"}
	if progress.blocked != nil {
		record.Reason = "graph_step_" + progress.blocked.Reason
		if len(progress.commands) == 0 && progress.blocked.Effect == collaboration.EffectNotCommitted {
			record.Effect, record.Completion = collaboration.EffectNotCommitted, collaboration.CompletionComplete
		}
		return factory.GraphOperationLookup{State: collaboration.LookupFound, Record: &record}, nil
	}
	if progress.next < nativeGraphCount(p) {
		if len(progress.commands) == 0 {
			return factory.GraphOperationLookup{State: collaboration.LookupNotObserved}, nil
		}
		return factory.GraphOperationLookup{State: collaboration.LookupFound, Record: &record, Resumable: true}, nil
	}
	receipt, err := s.verifyGraph(ctx, g, p, progress)
	if err != nil {
		return empty, err
	}
	record.Effect, record.Completion, record.Reason, record.Receipt = collaboration.EffectCommitted, collaboration.CompletionComplete, "native_graph_steps_verified", receipt
	return factory.GraphOperationLookup{State: collaboration.LookupFound, Record: &record}, nil
}
func (s NativeGraphs) SubmitGraph(ctx context.Context, g factory.IntentGeneration, repository int64) (factory.GraphOperationOutcome, error) {
	return s.ResumeGraph(ctx, g, repository)
}
func (s NativeGraphs) ResumeGraph(ctx context.Context, g factory.IntentGeneration, repository int64) (factory.GraphOperationOutcome, error) {
	var empty factory.GraphOperationOutcome
	p, err := s.registered(ctx, g, repository)
	if err != nil {
		return empty, err
	}
	progress, err := s.scan(ctx, g, p)
	if err != nil {
		return empty, err
	}
	if progress.blocked != nil || progress.next == nativeGraphCount(p) {
		observed, err := s.LookupGraph(ctx, g, repository)
		if err != nil {
			return empty, err
		}
		if observed.Record != nil {
			return *observed.Record, nil
		}
		return empty, errNativeRead
	}
	account, err := s.guard(ctx, g, p)
	if err != nil {
		return empty, err
	}
	index := progress.next
	id := nativeGraphStepID(g.OperationID, index)
	switch {
	case index < len(p.Graph.Creates):
		desired := p.Graph.Creates[index]
		request := factory.HumanIssueCreateCommand{ID: id, Host: account.binding.Host.ID, Repository: account.repository.ID, Actor: account.binding.Actor.ID, Title: desired.Title, Body: desired.Body}
		writer := nativeHumanIssueCreates{account}
		_, err = s.Store.HumanIssueCreateCapture(ctx, request)
		if errors.Is(err, store.ErrNotFound) {
			capture, e := writer.PrepareHumanIssueCreate(ctx, request)
			if e != nil {
				return empty, e
			}
			var prep nativeIssueCreatePreparation
			if nativeDecode(capture.ProviderIntent, &prep) != nil {
				return empty, errNativeRead
			}
			prep.NotAfter = min(prep.NotAfter, g.NotAfter)
			capture.ProviderIntent, err = json.Marshal(prep)
			if err == nil {
				_, _, err = s.Store.CaptureHumanIssueCreate(ctx, capture)
			}
		}
		if err != nil {
			return empty, err
		}
		if _, err = s.guard(ctx, g, p); err != nil {
			return empty, err
		}
		_, err = (factoryservice.HumanIssueCreates{Store: s.Store, Writer: writer}).Submit(ctx, request)
	case index < len(p.Graph.Creates)+len(p.Graph.Edits):
		desired := p.Graph.Edits[index-len(p.Graph.Creates)]
		request := factory.HumanEditCommand{ID: id, Host: account.binding.Host.ID, Repository: account.repository.ID, Actor: account.binding.Actor.ID, Subject: "issue", Number: desired.Issue.Number, Title: &desired.Title, Body: &desired.Body, Expected: &factory.HumanEditBase{ExternalID: desired.Issue.ExternalID, Title: desired.Expected.Title, BodyDigest: factory.MCPInputDigest([]byte(desired.Expected.Body))}}
		writer := nativeHumanEdits{account}
		_, err = s.Store.HumanEditCapture(ctx, request)
		if errors.Is(err, store.ErrNotFound) {
			capture, e := writer.PrepareHumanEdit(ctx, request)
			if e != nil {
				return empty, e
			}
			var prep nativeEditPreparation
			if nativeDecode(capture.ProviderIntent, &prep) != nil {
				return empty, errNativeRead
			}
			prep.NotAfter = min(prep.NotAfter, g.NotAfter)
			capture.ProviderIntent, err = json.Marshal(prep)
			if err == nil {
				_, _, err = s.Store.CaptureHumanEdit(ctx, capture)
			}
		}
		if err != nil {
			return empty, err
		}
		if _, err = s.guard(ctx, g, p); err != nil {
			return empty, err
		}
		_, err = (factoryservice.HumanEdits{Store: s.Store, Writer: writer}).Submit(ctx, request)
	default:
		desired := p.Graph.Dependencies[index-len(p.Graph.Creates)-len(p.Graph.Edits)]
		root, e := nativeGraphRef(desired.Issue, progress.creates)
		if e != nil {
			return empty, e
		}
		blocking, e := nativeGraphRef(desired.Prerequisite, progress.creates)
		if e != nil {
			return empty, e
		}
		request := factory.IssueDependencyCommand{ID: id, Host: account.binding.Host.ID, Actor: account.binding.Actor.ID, Issue: root, Blocking: blocking, Remove: desired.Remove}
		capture, e := s.Store.FactoryCommand(ctx, id)
		var captured factory.IssueDependencyCapture
		if e == nil {
			captured, err = factory.DecodeIssueDependencyCapture(capture)
			request.Expected = captured.Request.Expected
		} else if errors.Is(e, store.ErrNotFound) {
			request.Expected, err = s.dependencyBase(ctx, g, p, progress, index, root)
			if err != nil {
				return empty, err
			}
			captured, err = (nativeIssueDependencies{account}).PrepareIssueDependency(ctx, request)
			if err == nil {
				var prep nativeDependencyPreparation
				if nativeDecode(captured.ProviderIntent, &prep) != nil {
					return empty, errNativeRead
				}
				prep.NotAfter = min(prep.NotAfter, g.NotAfter)
				captured.ProviderIntent, err = json.Marshal(prep)
				if err == nil {
					captured, _, err = s.Store.CaptureIssueDependency(ctx, captured)
				}
			}
		} else {
			err = e
		}
		if err != nil {
			return empty, err
		}
		if !captured.Request.SameRequest(request) {
			return empty, factory.ErrIssueDependencyObservation
		}
		cmd, e := captured.Command()
		if e != nil {
			return empty, e
		}
		observed, e := s.Store.LookupHumanCommandDispatch(ctx, cmd)
		if e != nil {
			return empty, e
		}
		if observed.Record == nil {
			if _, err = s.Store.ObserveIssueDependency(ctx, captured, observed); err != nil {
				return empty, err
			}
			if _, err = s.guard(ctx, g, p); err != nil {
				return empty, err
			}
			record, e := (nativeIssueDependencies{account}).SubmitIssueDependency(ctx, captured)
			if e != nil {
				return empty, e
			}
			observed = collaboration.OperationLookup{Host: request.Host, Authority: captured.Authority, OperationID: captured.OperationID, State: collaboration.LookupFound, Record: &record}
			_, err = s.Store.ObserveIssueDependency(ctx, captured, observed)
		}
	}
	if err != nil {
		return empty, err
	}
	observed, err := s.LookupGraph(ctx, g, repository)
	if err != nil {
		return empty, err
	}
	if observed.Record == nil {
		return empty, errNativeRead
	}
	return *observed.Record, nil
}

// Keep lookup comparisons stable after PostgreSQL JSONB normalization.
func nativeGraphJSONEqual(a, b []byte) bool {
	var left, right any
	return json.Unmarshal(a, &left) == nil && json.Unmarshal(b, &right) == nil && reflect.DeepEqual(left, right)
}
