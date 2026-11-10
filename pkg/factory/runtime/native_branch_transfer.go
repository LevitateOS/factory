package runtime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strconv"
	"strings"

	"github.com/levitateos/factory/pkg/collaboration"
	"github.com/levitateos/factory/pkg/factory"
	factoryservice "github.com/levitateos/factory/pkg/factory/service"
)

// Only metadata enters the existing command capture; candidate bytes are
// re-exported from the retained native run and checked against this digest.
type nativeBranchTransfer struct {
	ControllerGrantID string                     `json:"controller_grant_id,omitempty"`
	SourceConnection  collaboration.ConnectionID `json:"source_connection,omitempty"`
	Base              string                     `json:"base"`
	ComparisonBranch  string                     `json:"comparison_branch"`
	ComparisonOID     string                     `json:"comparison_oid"`
	BundleSHA256      string                     `json:"bundle_sha256"`
	ExpectedOld       string                     `json:"expected_old,omitempty"`
}

func (t *nativeBranchTransfer) captureController(s *nativeProductAccount, deadline *int64) error {
	if s.binding.Host.Provider != collaboration.Fountain {
		return nil
	}
	if s.controller == nil {
		return errors.New("native publication controller is not selected")
	}
	t.ControllerGrantID, t.SourceConnection = s.controller.GrantID, s.binding.Connection.ID
	*deadline = min(*deadline, s.controller.NotAfter)
	return nil
}

func (t nativeBranchTransfer) controllerMatches(s *nativeProductAccount) bool {
	if s.binding.Host.Provider == collaboration.Fountain {
		return factory.ValidID(t.ControllerGrantID) && t.SourceConnection == s.binding.Connection.ID
	}
	return t.ControllerGrantID == "" && t.SourceConnection == ""
}

func (t nativeBranchTransfer) admitController(ctx context.Context, s *nativeProductAccount) error {
	if !t.controllerMatches(s) {
		return errNativeReadScope
	}
	if s.binding.Host.Provider == collaboration.Fountain {
		if s.controller == nil || s.controller.GrantID != t.ControllerGrantID {
			return errNativeReadScope
		}
		_, err := s.controllerGrant(ctx, s.repository.ID, t.ControllerGrantID, factory.OpRefPublish)
		return err
	}
	return nil
}

func (t nativeBranchTransfer) valid(q factory.HumanBranchCreateCommand) bool {
	comparison := q
	comparison.Name, comparison.Commit = t.ComparisonBranch, collaboration.CommitID(t.Base)
	return comparison.Validate() == nil && t.ComparisonBranch != q.Name && factory.ValidCommit(t.ComparisonOID) && factory.ValidDigest(t.BundleSHA256) && (t.ExpectedOld == "" || factory.ValidCommit(t.ExpectedOld) && t.ExpectedOld != string(q.Commit))
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
	HostOperation *struct {
		GrantID        string `json:"grant_id"`
		InstallationID string `json:"installation_id"`
		OperationID    string `json:"operation_id"`
		IntentDigest   string `json:"intent_digest"`
		Receipt        struct {
			Repository    int64  `json:"repository_id"`
			Actor         int64  `json:"actor_id"`
			Ref           string `json:"ref"`
			Old           string `json:"old_oid"`
			New           string `json:"new_oid"`
			ComparisonRef string `json:"comparison_ref"`
			Comparison    string `json:"comparison_oid"`
		} `json:"receipt"`
	} `json:"host_operation,omitempty"`
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

func validateNativeBranchTransferReceipt(q factory.HumanBranchCreateCommand, p nativeBranchCreatePreparation, payload json.RawMessage) (nativeBranchPublishReceipt, error) {
	var r nativeBranchPublishReceipt
	if p.Transfer == nil || nativeDecode(payload, &r) != nil || !nativeActor(r.Account) || r.Account.ExternalID != p.Account || r.Repository != p.Branch.Repository || r.Branch != "refs/heads/"+q.Name || r.Old != p.Transfer.ExpectedOld || r.New != string(q.Commit) || r.Comparison != p.Transfer.ComparisonOID || r.ComparisonBranch != "refs/heads/"+p.Transfer.ComparisonBranch || r.RefCondition != "exact_old_oid" || r.BundleSHA256 != p.Transfer.BundleSHA256 || !factory.ValidDigest(r.ProviderResponseSHA256) {
		return r, factoryservice.ErrHumanBranchCreateUnavailable
	}
	if p.Transfer.ControllerGrantID == "" {
		if r.HostOperation != nil || r.RepositoryCondition != "bounded_observation" || r.ComparisonCondition != "bounded_observation" {
			return r, errNativeRead
		}
	} else {
		h := r.HostOperation
		if h == nil || h.GrantID != p.Transfer.ControllerGrantID || !nativeObservedText(h.InstallationID, 128, false) || h.OperationID != q.ID || !factory.ValidDigest(h.IntentDigest) || r.RepositoryCondition != "current_host_permission" || r.ComparisonCondition != "atomic_comparison_oid" {
			return r, errNativeRead
		}
		v := h.Receipt
		expectedOld := r.Old
		if expectedOld == "" {
			expectedOld = strings.Repeat("0", 40)
		}
		if strconv.FormatInt(v.Repository, 10) != r.Repository || strconv.FormatInt(v.Actor, 10) != p.Account || v.Ref != r.Branch || v.Old != expectedOld || v.New != r.New || v.ComparisonRef != r.ComparisonBranch || v.Comparison != r.Comparison {
			return r, errNativeRead
		}
	}
	return r, nil
}
func (s nativeHumanBranchCreates) submitTransfer(ctx context.Context, c factory.HumanBranchCreateCapture, p nativeBranchCreatePreparation, bundle []byte) (collaboration.OperationRecord, error) {
	var empty collaboration.OperationRecord
	if err := p.Transfer.admitController(ctx, s.nativeProductAccount); err != nil {
		return empty, err
	}
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
	return nativeBranchTransferOutcome(c, p, outcome)
}
func nativeBranchTransferOutcome(c factory.HumanBranchCreateCapture, p nativeBranchCreatePreparation, outcome nativeCreationReply) (collaboration.OperationRecord, error) {
	var empty collaboration.OperationRecord
	record := nativeBranchCreateRecord(c)
	if outcome.State == "applied" {
		if outcome.Failure != nil {
			return empty, factoryservice.ErrHumanBranchCreateUnavailable
		}
		if _, err := validateNativeBranchTransferReceipt(c.Request, p, outcome.Receipt); err != nil {
			return empty, err
		}
		record.Effect, record.Completion, record.Reason = collaboration.EffectCommitted, collaboration.CompletionComplete, "native_branch_published"
		record.Receipt = &collaboration.ProviderReceipt{Authority: c.Authority, Reference: c.OperationID, IntentDigest: nativeCreationSHA(c.ProviderIntent), Payload: outcome.Receipt}
	} else if !outcome.applyFailure(&record) {
		return empty, factoryservice.ErrHumanBranchCreateUnavailable
	}
	return record, record.Validate()
}
