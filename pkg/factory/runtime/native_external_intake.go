package runtime

import (
	"context"
	"strconv"

	"github.com/levitateos/factory/pkg/collaboration"
	"github.com/levitateos/factory/pkg/factory"
	"github.com/levitateos/factory/pkg/factory/control"
	"github.com/levitateos/factory/pkg/project"
	"github.com/levitateos/factory/pkg/store"
)

type nativeExternalPull struct {
	Number         int64                        `json:"number"`
	ExternalID     string                       `json:"external_id"`
	NodeID         string                       `json:"node_id"`
	Title          string                       `json:"title"`
	Repository     string                       `json:"repository"`
	HeadRepository string                       `json:"head_repository"`
	BaseRepository string                       `json:"base_repository"`
	Head           string                       `json:"head_tip"`
	Base           string                       `json:"base_tip"`
	HeadRef        string                       `json:"head_ref"`
	BaseRef        string                       `json:"base_ref"`
	State          string                       `json:"state"`
	Draft          bool                         `json:"draft"`
	Author         *collaboration.ObservedActor `json:"author"`
	AuthorWriter   bool                         `json:"author_writer"`
	ClosingIssues  []struct {
		Repository string `json:"repository"`
		ExternalID string `json:"external_id"`
		NodeID     string `json:"node_id"`
		Number     int64  `json:"number"`
	} `json:"closing_issues"`
	ClosingComplete bool `json:"closing_issues_complete"`
}

func (p nativeExternalPull) eligible(repository, target string) bool {
	return nativeIndex(p.Number) && nativeRemoteID(p.ExternalID) && p.Repository == repository && p.HeadRepository == repository && p.BaseRepository == repository &&
		factory.ValidCommit(p.Head) && factory.ValidCommit(p.Base) && p.Head != p.Base && factory.ValidTargetBranch(p.HeadRef) && p.BaseRef == target && p.HeadRef != p.BaseRef &&
		p.State == "open" && !p.Draft && p.Author != nil && nativeActor(*p.Author) && p.AuthorWriter && p.ClosingComplete && len(p.ClosingIssues) == 1 &&
		p.ClosingIssues[0].Repository == repository && nativeRemoteID(p.ClosingIssues[0].ExternalID) && nativeIndex(p.ClosingIssues[0].Number)
}

func (p NativeReviews) automaticExternalPulls(ctx context.Context, repository, number int64, cursor string) ([]nativeExternalPull, string, bool, factory.RepositoryPolicy, error) {
	var empty factory.RepositoryPolicy
	policy, err := p.Store.RepositoryPolicy(ctx, repository)
	if err != nil {
		return nil, "", false, empty, err
	}
	if !policy.Enabled || policy.Paused || !policy.AutomaticExternalPulls || policy.Validate() != nil {
		return nil, "", false, empty, store.ErrAdmissionChanged
	}
	account, err := p.account(ctx, strconv.FormatInt(repository, 10), factory.OpReviewSubmit)
	if err != nil {
		return nil, "", false, empty, err
	}
	if account.binding.Host.Provider != collaboration.GitHub {
		return nil, "", false, empty, errNativeReadScope
	}
	var raw struct {
		Repository       string                      `json:"repository"`
		RepositoryNodeID string                      `json:"repository_node_id"`
		Name             string                      `json:"name"`
		Actor            collaboration.ObservedActor `json:"actor"`
		ActorNodeID      string                      `json:"actor_node_id"`
		Writer           bool                        `json:"writer"`
		Items            []nativeExternalPull        `json:"items"`
		Next             string                      `json:"next_cursor"`
		Complete         bool                        `json:"complete"`
	}
	if err = account.read(ctx, nativeRead{Kind: "external_pull_candidates", Repository: account.repository.ExternalID, Number: uint32(number), Cursor: cursor}, &raw); err != nil {
		return nil, "", false, empty, err
	}
	if raw.Repository != account.repository.ExternalID || raw.Actor.ExternalID != account.binding.Actor.ExternalID || !raw.Writer || len(raw.Items) > 16 || len(raw.Next) > 1024 || raw.Complete && raw.Next != "" || !raw.Complete && (raw.Next == "" || raw.Next == cursor) || number > 0 && (len(raw.Items) != 1 || raw.Items[0].Number != number || !raw.Complete) {
		return nil, "", false, empty, errNativeRead
	}
	seen := map[int64]bool{}
	for _, item := range raw.Items {
		if item.Repository != raw.Repository || !nativeIndex(item.Number) || seen[item.Number] {
			return nil, "", false, empty, errNativeRead
		}
		seen[item.Number] = true
	}
	current, err := p.Store.RepositoryPolicy(ctx, repository)
	if err != nil {
		return nil, "", false, empty, err
	}
	if current.Revision != policy.Revision || !current.Enabled || current.Paused || !current.AutomaticExternalPulls {
		return nil, "", false, empty, store.ErrAdmissionChanged
	}
	if err = account.currentBackground(ctx, factory.OpReviewSubmit); err != nil {
		return nil, "", false, empty, err
	}
	return raw.Items, raw.Next, raw.Complete, policy, nil
}

func (p NativeReviews) ReadAutomaticExternalPulls(ctx context.Context, repository int64, cursor string) (control.AutomaticExternalPullPage, error) {
	var page control.AutomaticExternalPullPage
	items, next, complete, policy, err := p.automaticExternalPulls(ctx, repository, 0, cursor)
	if err != nil {
		return page, err
	}
	repo, err := p.Store.CollaborationRepository(ctx, collaboration.RepositoryID(repository))
	if err != nil {
		return page, err
	}
	for _, item := range items {
		if !item.eligible(repo.ExternalID, policy.TargetBranch) {
			continue
		}
		// Discovery never creates or replaces an acceptance for a PR link.
		if _, err = p.Store.AcceptanceHead(ctx, repository, item.ClosingIssues[0].Number); err == nil {
			id, err := strconv.ParseInt(item.ExternalID, 10, 64)
			if err != nil || id < 1 {
				return page, errNativeRead
			}
			page.Selections = append(page.Selections, control.AutomaticExternalPullSelection{ExternalPullSelection: factory.ExternalPullSelection{Number: item.Number, Issue: item.ClosingIssues[0].Number}, PRID: id, Head: item.Head})
		}
	}
	page.Next, page.Complete = next, complete
	return page, nil
}

// This exact read is used again immediately before native launch. A saved
// association never stands in for current author permission or provider links.
func (p NativeReviews) CheckAutomaticExternalPull(ctx context.Context, request factory.ExternalPullAdoption) error {
	if !request.Automatic || request.Validate() != nil {
		return store.ErrAdmissionChanged
	}
	items, _, _, policy, err := p.automaticExternalPulls(ctx, request.Repository, request.PRNumber, "")
	if err != nil {
		return err
	}
	repo, err := p.Store.CollaborationRepository(ctx, collaboration.RepositoryID(request.Repository))
	if err != nil {
		return err
	}
	item := items[0]
	if !item.eligible(repo.ExternalID, policy.TargetBranch) || item.ClosingIssues[0].Number != request.Issue || policy.Revision != request.Policy || policy.Roles[project.RoleCoder].Account != request.Connection {
		return store.ErrAdmissionChanged
	}
	author, err := p.Store.CollaborationActor(ctx, collaboration.ActorID(request.Target.AuthorID))
	if err != nil {
		return err
	}
	if author.Host != repo.Host || author.ExternalID != item.Author.ExternalID || item.ExternalID != strconv.FormatInt(request.PRID, 10) || item.Head != request.Target.HeadOID || item.Base != request.Target.BaseOID || item.HeadRef != request.Target.HeadRef || item.BaseRef != request.Target.BaseRef {
		return store.ErrAdmissionChanged
	}
	head, err := p.Store.AcceptanceHead(ctx, request.Repository, request.Issue)
	if err != nil || head != request.Acceptance {
		return store.ErrAdmissionChanged
	}
	return nil
}
