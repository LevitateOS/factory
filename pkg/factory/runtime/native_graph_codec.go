package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"

	"github.com/levitateos/factory/pkg/collaboration"
	"github.com/levitateos/factory/pkg/factory"
	"github.com/levitateos/factory/pkg/factory/control"
	factoryservice "github.com/levitateos/factory/pkg/factory/service"
)

// NativeGraphs composes fixed native commands in the existing Go generation
// journal. Its account revision is local policy authority, never a GitHub graph
// clock. Issue observations retain their independent native-input revisions.
type NativeGraphs struct{ NativeInputSource }

var _ control.IssueGraphExecutor = NativeGraphs{}
var _ control.ScopedIssueGraph = NativeGraphs{}
var _ control.ResumableIssueGraph = NativeGraphs{}

type nativeGraphPacket struct {
	Version    int                       `json:"version"`
	Repository int64                     `json:"repository,string"`
	Graph      collaboration.GraphIntent `json:"graph"`
}
type nativeGraphReceipt struct {
	Version    int            `json:"version"`
	Operation  string         `json:"operation"`
	Repository int64          `json:"repository,string"`
	Actor      int64          `json:"actor,string"`
	Commands   []string       `json:"commands"`
	Versions   map[string]int `json:"versions"`
}

func (s NativeGraphs) EncodeGraph(graph collaboration.GraphIntent, repository int64) (json.RawMessage, error) {
	if graph.Validate(collaboration.RepositoryID(repository)) != nil {
		return nil, collaboration.ErrInvalidGraph
	}
	return json.Marshal(nativeGraphPacket{Version: 1, Repository: repository, Graph: graph})
}
func nativeDecodeGraph(raw json.RawMessage) (nativeGraphPacket, error) {
	var p nativeGraphPacket
	if len(raw) > 64*1024 || nativeDecode(raw, &p) != nil || p.Version != 1 || p.Graph.Validate(collaboration.RepositoryID(p.Repository)) != nil {
		return p, collaboration.ErrInvalidGraph
	}
	return p, nil
}
func (s NativeGraphs) InspectGraph(raw json.RawMessage) (factory.GraphPacketFacts, error) {
	var facts factory.GraphPacketFacts
	p, err := nativeDecodeGraph(raw)
	if err != nil {
		return facts, err
	}
	facts.Canonical, err = json.Marshal(p)
	if err != nil {
		return facts, err
	}
	facts.Creates = p.Graph.Creates
	facts.Edits = map[int64]factory.GraphIssueText{}
	facts.DependencyChanges = len(p.Graph.Dependencies)
	for _, edit := range p.Graph.Edits {
		id, err := strconv.ParseInt(edit.Issue.ExternalID, 10, 64)
		if err != nil || id <= 0 {
			return facts, collaboration.ErrInvalidGraph
		}
		facts.Edits[id] = factory.GraphIssueText{Title: edit.Title, Body: edit.Body}
	}
	return facts, nil
}
func nativeGraphStepID(operation string, index int) string {
	return factory.MCPInputDigest([]byte("factory.native.graph\x00" + operation + "\x00" + strconv.Itoa(index)))[:32]
}
func (s NativeGraphs) GraphActor(ctx context.Context, repository, owner int64) (int64, error) {
	p, err := s.Store.RepositoryPolicy(ctx, repository)
	if err != nil {
		return 0, err
	}
	if owner <= 0 || p.Create.NativeProfile != s.Config.Profile || p.Create.Validate() != nil {
		return 0, errNativeReadScope
	}
	actor, err := s.Store.CollaborationActor(ctx, collaboration.ActorID(owner))
	if err != nil {
		return 0, err
	}
	repo, err := s.Store.CollaborationRepository(ctx, collaboration.RepositoryID(repository))
	if err != nil {
		return 0, err
	}
	if actor.Host != repo.Host {
		return 0, errNativeReadScope
	}
	return p.Create.ActorID, nil
}
func (s NativeGraphs) CheckIntentOwner(ctx context.Context, repository, owner, actor int64) (int64, error) {
	selected, err := s.GraphActor(ctx, repository, owner)
	if err != nil {
		return 0, err
	}
	if selected != actor {
		return 0, errNativeReadScope
	}
	p, err := s.Store.RepositoryPolicy(ctx, repository)
	if err != nil {
		return 0, err
	}
	account, err := s.account(ctx, strconv.FormatInt(repository, 10), factory.OpPRCreate)
	if err != nil {
		return 0, err
	}
	if int64(account.binding.Actor.ID) != actor || p.Create.NativeConnection != account.binding.Connection.ID {
		return 0, errNativeReadScope
	}
	caps, err := (nativeCollaborationReads{account}).ReadCapabilities(ctx, account.binding.Actor.ID, account.repository.ID, factoryservice.CapabilityQuery{Subject: "repository"})
	if err != nil {
		return 0, err
	}
	if caps.Actions[collaboration.CreateIssue].State != collaboration.Allowed {
		return 0, errNativeReadScope
	}
	after, err := s.Store.RepositoryPolicy(ctx, repository)
	if err != nil {
		return 0, err
	}
	if after.Revision != p.Revision {
		return 0, errNativeReadScope
	}
	return p.Revision, nil
}
func (s NativeGraphs) registered(ctx context.Context, g factory.IntentGeneration, repository int64) (nativeGraphPacket, error) {
	packet, err := nativeDecodeGraph(g.Payload)
	if err != nil {
		return packet, err
	}
	saved, err := s.Store.IntentGeneration(ctx, g.RevisionID)
	if err != nil {
		return packet, err
	}
	if repository != packet.Repository || saved.OperationID != g.OperationID || saved.ActorID != g.ActorID || saved.NativeRev != g.NativeRev || saved.AuthRevision != g.AuthRevision || saved.NotAfter != g.NotAfter || saved.IntentDigest != g.IntentDigest || !nativeGraphJSONEqual(saved.Payload, g.Payload) {
		return packet, errors.New("native graph differs from the registered generation")
	}
	return packet, nil
}
func (s NativeGraphs) ReadGeneratedIssue(ctx context.Context, repository, index, actor int64) (factory.GeneratedIssueObservation, int64, error) {
	var empty factory.GeneratedIssueObservation
	policy, err := s.Store.RepositoryPolicy(ctx, repository)
	if err != nil {
		return empty, 0, err
	}
	account, err := s.account(ctx, strconv.FormatInt(repository, 10), factory.OpPRCreate)
	if err != nil {
		return empty, 0, err
	}
	if int64(account.binding.Actor.ID) != actor || policy.Create.NativeConnection != account.binding.Connection.ID {
		return empty, 0, errNativeReadScope
	}
	raw, revision, mapped, err := (nativeIssueInputs{account}).readInputs(ctx, strconv.FormatInt(repository, 10), strconv.FormatInt(index, 10), nil, "")
	if err != nil {
		return empty, 0, err
	}
	i := raw.Issue
	creator, err := s.Store.ObserveCollaborationActor(ctx, account.binding.Host.ID, i.Author.ExternalID, i.Author.Login)
	if err != nil {
		return empty, 0, err
	}
	issue := &factory.GeneratedIssueFacts{ID: i.ID, Index: strconv.FormatInt(index, 10), Title: i.Title, Content: i.Body, TitleDigest: inputDigest([]byte(i.Title)), ContentDigest: inputDigest([]byte(i.Body)), ContentVersion: *i.Version, Creator: &creator.ID, CreatedUnix: i.CreatedAt.Unix(), CreationVerified: true, FirstCreated: *i.Version == 0 && i.TitleOccurrence == "original" && i.LifecycleOccurrence == "original", TitleOccurrence: occurrenceID(i.TitleOccurrence, mapped), IsClosed: *i.Closed, IsLocked: *i.Locked, Visible: true, Complete: true}
	deps := &factory.GeneratedDependencies{IssueID: i.ID, Items: []factory.GeneratedDependency{}, Complete: true}
	for _, edge := range raw.Dependencies {
		deps.Items = append(deps.Items, factory.GeneratedDependency{OccurrenceID: mapped[edge.Occurrence], DependencyID: edge.DependsOn, Visible: true, Complete: true})
	}
	after, err := s.Store.RepositoryPolicy(ctx, repository)
	if err != nil {
		return empty, 0, err
	}
	if after.Revision != policy.Revision {
		return empty, 0, errNativeReadScope
	}
	return factory.GeneratedIssueObservation{Issue: issue, Dependencies: deps, AuthorityRevision: policy.Revision}, revision, nil
}
