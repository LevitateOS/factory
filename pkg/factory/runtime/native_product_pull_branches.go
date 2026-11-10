package runtime

import "github.com/levitateos/factory/pkg/collaboration"

// Native facts retain unknown metadata. They grant no product capability;
// only their branch pins enter the immutable creation preparation.
type nativePullBranches struct {
	Base nativePullBranchSide `json:"base"`
	Head nativePullBranchSide `json:"head"`
}
type nativePullRepositoryIdentity struct {
	ExternalID string `json:"external_id"`
	NodeID     string `json:"node_id"`
}
type nativePullBranchSide struct {
	Repository struct {
		ExternalID string `json:"external_id"`
		NodeID     string `json:"node_id"`
		Name       string `json:"name"`
		Owner      struct {
			ExternalID string `json:"external_id"`
			NodeID     string `json:"node_id"`
			Login      string `json:"login"`
			Type       string `json:"type"` // Empty when the provider does not expose the owner category; never grants write authority.
		} `json:"owner"`
		Private  *bool                         `json:"private"`
		Archived *bool                         `json:"archived"`
		Disabled *bool                         `json:"disabled"`
		Fork     *bool                         `json:"fork"`
		Push     *bool                         `json:"push"`
		Parent   *nativePullRepositoryIdentity `json:"parent_repository"`
		Source   *nativePullRepositoryIdentity `json:"source_repository"`
	} `json:"repository"`
	Branch struct {
		Name   string `json:"name"`
		NodeID string `json:"node_id"`
		Commit string `json:"commit"`
	} `json:"branch"`
}

func (p nativePullBranchPin) valid() bool {
	return nativeReviewNode(p.RepositoryNodeID, 1024) && nativeReviewNode(p.OwnerNodeID, 1024) && nativeReviewNode(p.ReferenceNodeID, 1024) && collaboration.ValidCommitID(collaboration.CommitID(p.Commit))
}
func (s nativePullBranchSide) pin() nativePullBranchPin {
	return nativePullBranchPin{RepositoryNodeID: s.Repository.NodeID, OwnerNodeID: s.Repository.Owner.NodeID, ReferenceNodeID: s.Branch.NodeID, Commit: s.Branch.Commit}
}
func (s nativePullBranchSide) valid(repository, branch string) bool {
	if !nativeRemoteID(repository) || s.Repository.ExternalID != repository || s.Branch.Name != branch || !s.pin().valid() || !nativeObservedText(s.Repository.Name, 512, false) || !nativeRemoteID(s.Repository.Owner.ExternalID) || !nativeObservedText(s.Repository.Owner.Login, 256, false) || s.Repository.Owner.Type != "" && s.Repository.Owner.Type != "User" && s.Repository.Owner.Type != "Organization" {
		return false
	}
	for _, related := range []*nativePullRepositoryIdentity{s.Repository.Parent, s.Repository.Source} {
		if related != nil && (!nativeRemoteID(related.ExternalID) || !nativeReviewNode(related.NodeID, 1024)) {
			return false
		}
	}
	return true
}
