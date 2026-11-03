package control

import (
	"context"
	"errors"
	"strconv"
	"strings"

	"github.com/levitateos/sodaos/internal/factory"
	"github.com/levitateos/sodaos/internal/store"
)

// MaxCycleNodes bounds one dependency-cycle walk. The walk stops exceeded
// past the bound instead of timing out an assessment.
const MaxCycleNodes = 1024

// findPrereqCycle walks recorded head acceptances from one issue and
// reports the cycle path when the issue reaches itself through declared
// dependencies of any length. Missing endpoint acceptances are leaves;
// the walk budget fails closed to exceeded.
func (c *Coordinator) findPrereqCycle(ctx context.Context, repository, issue int64) (string, bool, error) {
	start := factory.DependenceRef{Repository: repository, Issue: issue}
	visited := map[factory.DependenceRef]bool{start: true}
	type frame struct {
		endpoints []factory.DependenceRef
		next      int
	}
	decision, err := c.headDecision(ctx, repository, issue)
	if err != nil || decision == nil {
		return "", false, err
	}
	stack := []frame{{endpoints: prereqEndpoints(*decision)}}
	path := []factory.DependenceRef{start}
	nodes := 1
	for len(stack) != 0 {
		top := &stack[len(stack)-1]
		if top.next >= len(top.endpoints) {
			stack = stack[:len(stack)-1]
			path = path[:len(path)-1]
			continue
		}
		endpoint := top.endpoints[top.next]
		top.next++
		if endpoint == start {
			return cyclePath(append(path, start)), false, nil
		}
		if visited[endpoint] {
			continue
		}
		visited[endpoint] = true
		nodes++
		if nodes > MaxCycleNodes {
			return "", true, nil
		}
		decision, err := c.headDecision(ctx, endpoint.Repository, endpoint.Issue)
		if err != nil || decision == nil {
			if err != nil {
				return "", false, err
			}
			continue
		}
		stack = append(stack, frame{endpoints: prereqEndpoints(*decision)})
		path = append(path, endpoint)
	}
	return "", false, nil
}

// headDecision returns the head acceptance decision for one issue, or nil
// when no decision was ever recorded.
func (c *Coordinator) headDecision(ctx context.Context, repository, issue int64) (*factory.Acceptance, error) {
	head, err := c.Store.AcceptanceHead(ctx, repository, issue)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, nil
		}
		return nil, err
	}
	decision, err := c.Store.AcceptanceDecision(ctx, head)
	if err != nil {
		return nil, err
	}
	return &decision, nil
}

func prereqEndpoints(decision factory.Acceptance) []factory.DependenceRef {
	var endpoints []factory.DependenceRef
	for _, prereq := range decision.Prerequisites {
		endpoints = append(endpoints, factory.DependenceRef{Repository: prereq.EndpointRepo, Issue: prereq.EndpointIssue})
	}
	return endpoints
}

func cyclePath(path []factory.DependenceRef) string {
	parts := make([]string, 0, len(path))
	for _, node := range path {
		parts = append(parts, strconv.FormatInt(node.Repository, 10)+"/"+strconv.FormatInt(node.Issue, 10))
	}
	joined := strings.Join(parts, " -> ")
	if len(joined) > 240 {
		return joined[:240] + "..."
	}
	return joined
}
