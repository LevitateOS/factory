package runtime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"

	"github.com/levitateos/factory/pkg/collaboration"
	"github.com/levitateos/factory/pkg/factory"
	factoryservice "github.com/levitateos/factory/pkg/factory/service"
)

// Only metadata enters the existing command capture; candidate bytes are
// re-exported from the retained native run and checked against this digest.
type nativeBranchTransfer struct {
	Base             string `json:"base"`
	ComparisonBranch string `json:"comparison_branch"`
	ComparisonOID    string `json:"comparison_oid"`
	BundleSHA256     string `json:"bundle_sha256"`
}

func (t nativeBranchTransfer) valid(q factory.HumanBranchCreateCommand) bool {
	comparison := q
	comparison.Name, comparison.Commit = t.ComparisonBranch, collaboration.CommitID(t.Base)
	return comparison.Validate() == nil && t.ComparisonBranch != q.Name && factory.ValidCommit(t.ComparisonOID) && factory.ValidDigest(t.BundleSHA256)
}

type nativeBranchPublishWrite struct {
	Repository  string `json:"repository"`
	Branch      string `json:"branch"`
	ExpectedOld string `json:"expected_old"`
	Candidate   string `json:"candidate"`
	nativeBranchTransfer
	Bundle []byte `json:"bundle"`
}
type nativeBranchPublishEffect struct {
	Kind      string                   `json:"kind"`
	CommandID string                   `json:"command_id"`
	Digest    string                   `json:"digest"`
	NotAfter  int64                    `json:"not_after"`
	Branch    nativeBranchPublishWrite `json:"branch"`
}
type nativeBranchPublishReceipt struct {
	Account                collaboration.ObservedActor `json:"account"`
	Repository             string                      `json:"repository"`
	Name                   string                      `json:"name"`
	RepositoryCondition    string                      `json:"repository_condition"`
	Branch                 string                      `json:"branch"`
	Old                    string                      `json:"old"`
	New                    string                      `json:"new"`
	Comparison             string                      `json:"comparison"`
	ComparisonBranch       string                      `json:"comparison_branch"`
	ComparisonCondition    string                      `json:"comparison_condition"`
	RefCondition           string                      `json:"ref_condition"`
	BundleSHA256           string                      `json:"bundle_sha256"`
	ProviderResponseSHA256 string                      `json:"provider_response_sha256"`
}

func validateNativeBranchTransferReceipt(c factory.HumanBranchCreateCapture, p nativeBranchCreatePreparation, payload json.RawMessage) (nativeBranchPublishReceipt, error) {
	var r nativeBranchPublishReceipt
	if p.Transfer == nil || nativeDecode(payload, &r) != nil || !nativeActor(r.Account) || r.Account.ExternalID != p.Account || r.Repository != p.Branch.Repository || r.RepositoryCondition != "bounded_observation" || r.Branch != "refs/heads/"+c.Request.Name || r.Old != "" || r.New != string(c.Request.Commit) || r.Comparison != p.Transfer.ComparisonOID || r.ComparisonBranch != "refs/heads/"+p.Transfer.ComparisonBranch || r.ComparisonCondition != "bounded_observation" || r.RefCondition != "exact_old_oid" || r.BundleSHA256 != p.Transfer.BundleSHA256 || !factory.ValidDigest(r.ProviderResponseSHA256) {
		return r, factoryservice.ErrHumanBranchCreateUnavailable
	}
	return r, nil
}
func (s nativeHumanBranchCreates) submitTransfer(ctx context.Context, c factory.HumanBranchCreateCapture, p nativeBranchCreatePreparation, bundle []byte) (collaboration.OperationRecord, error) {
	var empty collaboration.OperationRecord
	hash := sha256.Sum256(bundle)
	if len(bundle) == 0 || hex.EncodeToString(hash[:]) != p.Transfer.BundleSHA256 {
		return empty, factoryservice.ErrHumanBranchCreateConflict
	}
	won, err := s.store.ReserveHumanBranchCreateDispatch(ctx, c)
	if err != nil {
		return empty, err
	}
	if !won {
		prior, err := s.LookupHumanBranchCreate(ctx, c)
		if err != nil {
			return empty, err
		}
		if prior.Record != nil {
			return *prior.Record, nil
		}
		return nativeBranchCreateRecord(c), nil
	}
	cmd, err := c.Command()
	if err != nil {
		return empty, err
	}
	write := nativeBranchPublishWrite{Repository: p.Branch.Repository, Branch: p.Branch.Name, Candidate: p.Branch.Commit, nativeBranchTransfer: *p.Transfer, Bundle: bundle}
	outcome, err := s.callHumanEffect(ctx, nativeBranchPublishEffect{Kind: "publish_branch", CommandID: c.OperationID, Digest: cmd.Digest, NotAfter: p.NotAfter, Branch: write}, cmd, p.Branch.Repository, factoryservice.ErrHumanBranchCreateConflict)
	if err != nil {
		return empty, err
	}
	record := nativeBranchCreateRecord(c)
	if outcome.State == "applied" {
		if outcome.Failure != nil {
			return empty, factoryservice.ErrHumanBranchCreateUnavailable
		}
		if _, err = validateNativeBranchTransferReceipt(c, p, outcome.Receipt); err != nil {
			return empty, err
		}
		record.Effect, record.Completion, record.Reason = collaboration.EffectCommitted, collaboration.CompletionComplete, "native_branch_published"
		record.Receipt = &collaboration.ProviderReceipt{Authority: c.Authority, Reference: c.OperationID, IntentDigest: nativeCreationSHA(c.ProviderIntent), Payload: outcome.Receipt}
	} else if !outcome.applyFailure(&record) {
		return empty, factoryservice.ErrHumanBranchCreateUnavailable
	}
	return record, record.Validate()
}
