package runtime

import (
	"context"

	"github.com/levitateos/factory/pkg/factory"
	"github.com/levitateos/factory/pkg/factory/control"
)

var _ control.IssueGraphPrefix = NativeGraphs{}

func (s NativeGraphs) ReadGraphPrefix(ctx context.Context, g factory.IntentGeneration, repository int64) (factory.GraphPrefixFacts, error) {
	var empty factory.GraphPrefixFacts
	p, err := s.registered(ctx, g, repository)
	if err != nil {
		return empty, err
	}
	progress, err := s.scan(ctx, g, p)
	if err != nil {
		return empty, err
	}
	facts := factory.GraphPrefixFacts{Repository: repository, Actor: g.ActorID, Effects: []factory.GraphIssueEffect{}}
	for index, id := range progress.commands {
		var effect factory.GraphIssueEffect
		switch {
		case index < len(p.Graph.Creates):
			desired := p.Graph.Creates[index]
			effect.Issue = progress.creates[desired.Key]
			effect.Created = true
			effect.Text = &factory.GraphIssueText{Title: desired.Title, Body: desired.Body}
		case index < len(p.Graph.Creates)+len(p.Graph.Edits):
			desired := p.Graph.Edits[index-len(p.Graph.Creates)]
			effect.Issue = desired.Issue
			effect.Text = &factory.GraphIssueText{Title: desired.Title, Body: desired.Body}
			effect.Expected = &factory.GraphIssueText{Title: desired.Expected.Title, Body: desired.Expected.Body}
		default:
			cmd, e := s.Store.FactoryCommand(ctx, id)
			if e != nil {
				return empty, e
			}
			captured, e := factory.DecodeIssueDependencyCapture(cmd)
			if e != nil {
				return empty, e
			}
			effect.Issue = captured.Request.Issue
			effect.Dependency = &factory.GraphDependencyEffect{ID: captured.Request.Blocking.ExternalID, Remove: captured.Request.Remove, Expected: append([]string{}, captured.Request.Expected...)}
		}
		facts.Effects = append(facts.Effects, effect)
	}
	return facts, nil
}
