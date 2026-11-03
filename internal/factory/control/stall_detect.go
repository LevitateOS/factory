package control

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"

	"github.com/levitateos/sodaos/internal/factory"
	"github.com/levitateos/sodaos/internal/store"
)

var errStallStoreMissing = errors.New("stall detection needs its store")

// D15 complete-inventory/wait/stall detector. Detection runs inside the
// existing progress pass after observation and reconciliation; this file
// owns the classification while the pass owns the timing, so there is no
// new timer, polling model or scheduler. Components and their causal
// blockers are classified per repository, never as one repository-wide
// boolean, so a legitimate external wait in one component cannot hide a
// repairable cycle or missing producer in another. Ordinary waits never
// call an investigator; a bound stall episode admits bounded diagnosis
// only through the explicit repair-grant admission the store owns.

// Stall component decision kinds.
const (
	StallComponentReady     = "ready"
	StallComponentWait      = "wait"
	StallComponentStall     = "stall"
	StallComponentAttention = "attention"
)

// Stall repository result kinds: ready work resumes selection, zero
// waits hold without an investigator, one bound episode admits
// diagnosis, attention needs evidence, and a fully delivered set
// enters verification.
const (
	StallRepositoryReady     = "ready"
	StallRepositoryZeroWait  = "zero_wait"
	StallRepositoryStall     = "stall"
	StallRepositoryAttention = "attention"
	StallRepositoryVerify    = "verify"
)

// StallInventoryIssue is one sealed member's observed signals: its
// readiness verdict, this pass's dispatch wait reason when it waited,
// the repository fence state, live execution and allowance closure.
type StallInventoryIssue struct {
	Repository        int64
	Issue             int64
	Control           factory.IssueControl
	DispatchWait      string
	Fenced            bool
	ActiveRun         bool
	AllowanceClosed   bool
	CIPending         bool
	Paused            bool
	ProductChoiceOpen bool
}

// StallInventory is the sealed complete input for one detection round.
// Complete=false means partial, hidden or stale inventory, which gives
// observation attention, never deadlock.
type StallInventory struct {
	Generation        int64
	Fingerprint       string
	Digest            string
	SourceCommit      string
	PreparationDigest string
	Complete          bool
	Issues            []StallInventoryIssue
	// Cycles carries established dependency-cycle evidence per
	// "repository/issue" key. Producers marks issues with a viable
	// outcome producer; a pending prerequisite without one is a
	// missing producer, not an ordinary wait.
	Cycles    map[string]string
	Producers map[string]bool
	// PriorEvents carries standing stall event IDs with the inventory
	// digest they bound, scoped by repository: new ready work stales
	// only its own repository's obsolete investigation. A global map
	// would let one repository retire another's events.
	PriorEvents map[int64]map[string]string
}

// StallComponentDecision is one classified component.
type StallComponentDecision struct {
	Repository  int64  `json:"repository,string"`
	Issue       int64  `json:"issue,string"`
	Kind        string `json:"kind"`
	WaitKind    string `json:"wait_kind,omitempty"`
	Named       bool   `json:"named,omitempty"`
	WaitOwner   string `json:"wait_owner,omitempty"`
	WaitEvent   string `json:"wait_event,omitempty"`
	Reason      string `json:"reason,omitempty"`
	Investigate bool   `json:"investigate,omitempty"`
}

// StallRepositoryResult is one repository's classification. Event is
// set exactly when Kind is stall: one bound episode per repository
// and progress generation.
type StallRepositoryResult struct {
	Repository  int64                    `json:"repository"`
	Kind        string                   `json:"kind"`
	Decisions   []StallComponentDecision `json:"decisions"`
	Event       *factory.StallEvent      `json:"event,omitempty"`
	StalePrior  []string                 `json:"stale_prior,omitempty"`
	Investigate bool                     `json:"investigate,omitempty"`
}

// StallClassification is one detection round over every repository.
type StallClassification struct {
	Generation   int64                   `json:"generation"`
	Repositories []StallRepositoryResult `json:"repositories"`
}

// stallIssueKey names one component for cycle/producer evidence.
func stallIssueKey(repository, issue int64) string {
	return fmt.Sprintf("%d/%d", repository, issue)
}

// classifyStallIssue applies the D15 ordered evaluation to one member:
// sealed ready work first, then named waits with owner and next event,
// then dependency components and missing viable producers. A real
// prerequisite is never removed merely because it blocks selection.
func classifyStallIssue(in StallInventoryIssue, cycles map[string]string, producers map[string]bool) StallComponentDecision {
	decision := StallComponentDecision{Repository: in.Repository, Issue: in.Issue}
	if classifyStallSignal(in, &decision) {
		return decision
	}
	if classifyStallDispatchWait(in, &decision) {
		return decision
	}
	if classifyStallBlocker(in, &decision) {
		return decision
	}
	classifyStallDependency(in, stallIssueKey(in.Repository, in.Issue), cycles, producers, &decision)
	return decision
}

// classifyStallSignal classifies explicit per-issue signals first:
// live execution, CI, pause and open product choice are ordinary
// observed waits even when the recorded verdict still reads queued.
// Pending completion gates behind the repository fence.
func classifyStallSignal(in StallInventoryIssue, decision *StallComponentDecision) bool {
	// Explicit per-issue signals first: live execution, CI, pause and
	// open product choice are ordinary observed waits even when the
	// recorded verdict still reads queued.
	switch {
	case in.ActiveRun || in.Control.Readiness == factory.ReadinessActive:
		decision.Kind, decision.WaitKind, decision.Named = StallComponentWait, factory.StallWaitActiveWork, true
		decision.WaitOwner, decision.WaitEvent = "executor", "run settlement"
		return true
	case in.CIPending:
		decision.Kind, decision.WaitKind, decision.Named = StallComponentWait, factory.StallWaitCIChecks, true
		decision.WaitOwner, decision.WaitEvent = "checks", "required check completion"
		return true
	case in.Paused:
		decision.Kind, decision.WaitKind, decision.Named = StallComponentWait, factory.StallWaitExplicitPause, true
		decision.WaitOwner, decision.WaitEvent = "maintainer", "explicit resume"
		return true
	case in.ProductChoiceOpen:
		decision.Kind, decision.WaitKind, decision.Named = StallComponentWait, factory.StallWaitProductChoice, true
		decision.WaitOwner, decision.WaitEvent = "product", "accepted product direction"
		return true
	}
	if in.Fenced {
		decision.Kind, decision.WaitKind, decision.Named = StallComponentWait, factory.StallWaitPendingCompletion, true
		decision.WaitOwner, decision.WaitEvent = "completion", "reassessment release"
		return true
	}
	return false
}

// classifyStallDispatchWait maps dispatch wait reasons to the named
// taxonomy; unlisted reasons stay ordinary observed waits that equally
// never call an investigator.
func classifyStallDispatchWait(in StallInventoryIssue, decision *StallComponentDecision) bool {
	if in.DispatchWait == "" {
		// Eligible queued work with no wait resumes normal selection.
		if in.Control.Readiness == factory.ReadinessQueued {
			decision.Kind = StallComponentReady
			return true
		}
		return false
	}
	decision.Kind = StallComponentWait
	switch in.DispatchWait {
	case WaitCapacity, WaitRepository:
		decision.WaitKind, decision.Named = factory.StallWaitCapacity, true
		decision.WaitOwner, decision.WaitEvent = "capacity", "execution slot"
	case WaitSponsorship, WaitAllowance, WaitSponsorshipRole:
		decision.WaitKind, decision.Named = factory.StallWaitQuota, true
		decision.WaitOwner, decision.WaitEvent = "sponsor", "allowance grant"
	case WaitPreparation:
		decision.WaitKind, decision.Named = factory.StallWaitPreparation, true
		decision.WaitOwner, decision.WaitEvent = "preparation", "approved preparation"
	case WaitAuthority:
		decision.WaitKind, decision.Named = factory.StallWaitPermissions, true
		decision.WaitOwner, decision.WaitEvent = "authority", "restored authorization"
	case WaitDispatchClosed:
		decision.WaitKind, decision.Named = factory.StallWaitExplicitPause, true
		decision.WaitOwner, decision.WaitEvent = "maintainer", "dispatch reopen"
	case WaitReassessmentPending:
		decision.WaitKind, decision.Named = factory.StallWaitPendingCompletion, true
		decision.WaitOwner, decision.WaitEvent = "completion", "reassessment release"
	case WaitAssigned, WaitInputsBusy:
		decision.WaitKind, decision.Named = factory.StallWaitActiveWork, true
		decision.WaitOwner, decision.WaitEvent = "executor", "run settlement"
	default:
		decision.WaitKind = in.DispatchWait
		decision.WaitOwner, decision.WaitEvent = "dispatch", "later pass"
	}
	return true
}

// classifyStallBlocker classifies readiness blockers without a
// dispatch wait.
func classifyStallBlocker(in StallInventoryIssue, decision *StallComponentDecision) bool {
	for _, blocker := range in.Control.Blockers {
		decision.Kind = StallComponentWait
		switch blocker.Code {
		case factory.BlockerAuthorityMissing:
			decision.WaitKind, decision.Named = factory.StallWaitPermissions, true
			decision.WaitOwner, decision.WaitEvent = "authority", "restored authorization"
			return true
		case factory.BlockerAcceptanceMissing, factory.BlockerAcceptanceInvalid:
			decision.WaitKind, decision.Named = factory.StallWaitProductChoice, true
			decision.WaitOwner, decision.WaitEvent = "product", "adopted requirements"
			return true
		case factory.BlockerIssueInaccessible, factory.BlockerEndpointHidden:
			decision.Kind, decision.WaitKind = StallComponentAttention, ""
			decision.WaitOwner, decision.WaitEvent = "visibility", "readable evidence"
			return true
		case factory.BlockerEvidenceIncomplete:
			decision.Kind = StallComponentAttention
			decision.WaitOwner, decision.WaitEvent = "scope", "narrower evidence"
			return true
		}
	}
	return false
}

// classifyStallDependency classifies dependency components: an
// established cycle, a pending prerequisite with no viable producer,
// or unexplained no-ready.
func classifyStallDependency(in StallInventoryIssue, key string, cycles map[string]string, producers map[string]bool, decision *StallComponentDecision) {
	if _, ok := cycles[key]; ok || hasCycleBlocker(in.Control) {
		decision.Kind, decision.Reason, decision.Investigate = StallComponentStall, factory.StallReasonCycle, true
		return
	}
	if prereq, pending := pendingPrereq(in.Control); pending {
		if !producers[key] {
			decision.Kind, decision.Reason, decision.Investigate = StallComponentStall, factory.StallReasonMissingProducer, true
			decision.WaitOwner, decision.WaitEvent = "dependency", "viable outcome producer"
			_ = prereq
			return
		}
		decision.Kind, decision.WaitKind, decision.Named = StallComponentWait, factory.StallWaitActiveWork, true
		decision.WaitOwner, decision.WaitEvent = "prerequisite", "endpoint delivery"
		return
	}
	if in.Control.Readiness == factory.ReadinessBlocked || in.Control.Readiness == factory.ReadinessNotAuthorized {
		decision.Kind, decision.Reason, decision.Investigate = StallComponentStall, factory.StallReasonUnexplainedNoReady, true
		return
	}
	if in.Control.Readiness == factory.ReadinessCompleted {
		decision.Kind = StallComponentReady
		return
	}
	if in.AllowanceClosed {
		decision.Kind, decision.WaitKind = StallComponentWait, factory.StallWaitQuota
		decision.Named = true
		decision.WaitOwner, decision.WaitEvent = "maintainer", "explicit retry"
		return
	}
	decision.Kind = StallComponentAttention
}

// hasCycleBlocker reports an assessment-established dependency cycle.
// A depth-exceeded bound is a failed-closed walk, not an established
// cycle: it falls through to unexplained no-ready diagnosis.
func hasCycleBlocker(control factory.IssueControl) bool {
	for _, blocker := range control.Blockers {
		if blocker.Code == factory.BlockerCycle {
			return true
		}
	}
	return false
}

func pendingPrereq(control factory.IssueControl) (factory.Blocker, bool) {
	for _, blocker := range control.Blockers {
		if blocker.Code == factory.BlockerCodePending || blocker.Code == factory.BlockerResultPending {
			return blocker, true
		}
	}
	return factory.Blocker{}, false
}

// stallEpisodeID binds one episode to its repository and generation.
func stallEpisodeID(repository, generation int64) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("stall-episode\x00%d\x00%d", repository, generation)))
	return hex.EncodeToString(sum[:16])
}

// stallEventID binds one event to its repository, generation and
// sealed input fingerprint: the (repository, progress_generation,
// input_fingerprint) uniqueness.
func stallEventID(repository, generation int64, fingerprint string) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("stall-event\x00%d\x00%d\x00%s", repository, generation, fingerprint)))
	return hex.EncodeToString(sum[:16])
}

// ClassifyStallInventory evaluates one sealed round in D15 order per
// repository: reconcile first, then verification and ready work, then
// named waits, then dependency components. Partial inventory gives
// attention, never deadlock. Ready work anywhere in the repository
// resumes selection and stales obsolete investigations; all outcomes
// delivered enters verification.
func ClassifyStallInventory(in StallInventory) StallClassification {
	out := StallClassification{Generation: in.Generation}
	if !in.Complete {
		return out
	}
	byRepo := map[int64][]StallInventoryIssue{}
	var repos []int64
	for _, issue := range in.Issues {
		if _, ok := byRepo[issue.Repository]; !ok {
			repos = append(repos, issue.Repository)
		}
		byRepo[issue.Repository] = append(byRepo[issue.Repository], issue)
	}
	sort.Slice(repos, func(i, j int) bool { return repos[i] < repos[j] })
	for _, repo := range repos {
		result := StallRepositoryResult{Repository: repo, Kind: StallRepositoryReady}
		members := byRepo[repo]
		sort.Slice(members, func(i, j int) bool { return members[i].Issue < members[j].Issue })
		var stall *StallComponentDecision
		waits, readies, attentions, completed := 0, 0, 0, 0
		for _, issue := range members {
			decision := classifyStallIssue(issue, in.Cycles, in.Producers)
			result.Decisions = append(result.Decisions, decision)
			switch decision.Kind {
			case StallComponentReady:
				if issue.Control.Readiness == factory.ReadinessCompleted {
					completed++
				} else {
					readies++
				}
			case StallComponentWait:
				waits++
			case StallComponentStall:
				if stall == nil {
					kept := decision
					stall = &kept
				}
			case StallComponentAttention:
				attentions++
			}
		}
		switch {
		case readies > 0:
			result.Kind = StallRepositoryReady
			for id, digest := range in.PriorEvents[repo] {
				if digest != in.Digest {
					result.StalePrior = append(result.StalePrior, id)
				}
			}
			sort.Strings(result.StalePrior)
		case stall != nil:
			result.Kind = StallRepositoryStall
			result.Investigate = true
			event := factory.StallEvent{
				ID:         stallEventID(repo, in.Generation, in.Fingerprint),
				Repository: repo, ProgressGeneration: in.Generation,
				InputFingerprint: in.Fingerprint, InventoryDigest: in.Digest,
				SourceCommit: in.SourceCommit, PreparationDigest: in.PreparationDigest,
				Reason: stall.Reason, EpisodeID: stallEpisodeID(repo, in.Generation),
				State: factory.StallObserved,
			}
			result.Event = &event
		case attentions > 0:
			result.Kind = StallRepositoryAttention
		case completed > 0 && waits == 0:
			result.Kind = StallRepositoryVerify
		default:
			result.Kind = StallRepositoryZeroWait
		}
		out.Repositories = append(out.Repositories, result)
	}
	return out
}

// StallDetectDeps gathers one detection round's durable inputs. The
// progress pass supplies its dispatch waits and inventory seal; FW44
// owns the per-pass call site that feeds live waits.
type StallDetectDeps struct {
	Store             *store.Store
	Generation        int64
	SourceCommit      string
	PreparationDigest string
	DispatchWaits     []DispatchWait
	CIPending         map[string]bool
	Paused            map[string]bool
	ProductChoiceOpen map[string]bool
	// PriorEvents carries standing stall event IDs with the
	// inventory digest they bound, scoped by repository; the
	// serving round feeds back the store's latest observation
	// per repository.
	PriorEvents map[int64]map[string]string
}

// DetectRepairStalls runs the D15 detector over the sealed store
// inventory: every recorded issue control, the repository completion
// fences and live execution markers. It spends nothing and admits
// nothing; a bound episode admits diagnosis only through the explicit
// repair-grant admission.
func (c *Coordinator) DetectRepairStalls(ctx context.Context, deps StallDetectDeps) (StallClassification, error) {
	if deps.Store == nil {
		return StallClassification{}, errStallStoreMissing
	}
	controls, complete, err := deps.Store.AllIssueControls(ctx, 4096)
	if err != nil {
		return StallClassification{}, err
	}
	waits := map[string]string{}
	for _, wait := range deps.DispatchWaits {
		waits[stallIssueKey(wait.Repository, wait.Issue)] = wait.Reason
	}
	in := StallInventory{
		Generation: deps.Generation, Complete: complete,
		SourceCommit: deps.SourceCommit, PreparationDigest: deps.PreparationDigest,
		Cycles: map[string]string{}, Producers: map[string]bool{},
		PriorEvents: deps.PriorEvents,
	}
	fingerprint := sha256.New()
	seenRepo := map[int64]bool{}
	for _, control := range controls {
		key := stallIssueKey(control.Repository, control.Issue)
		fingerprint.Write([]byte(key + "\x00" + control.Fingerprint + "\x00"))
		seenRepo[control.Repository] = true
		if _, ok := in.Cycles[key]; !ok {
			cycle, exceeded, err := c.findPrereqCycle(ctx, control.Repository, control.Issue)
			if err != nil {
				return StallClassification{}, err
			}
			if exceeded {
				// The bounded walk did not finish: the cycle map is
				// incomplete, so the whole inventory fails closed to
				// incomplete rather than proving deadlock from a
				// partial walk.
				in.Complete = false
			} else if cycle != "" {
				in.Cycles[key] = cycle
			}
		}
		in.Producers[key] = true
	}
	in.Fingerprint = hex.EncodeToString(fingerprint.Sum(nil))
	digestSum := sha256.Sum256([]byte("stall-inventory\x00" + in.Fingerprint))
	in.Digest = hex.EncodeToString(digestSum[:])
	fenced := map[int64]bool{}
	for repo := range seenRepo {
		pending, err := deps.Store.CompletionPending(ctx, repo)
		if err != nil {
			return StallClassification{}, err
		}
		fenced[repo] = len(pending) > 0
	}
	for _, control := range controls {
		key := stallIssueKey(control.Repository, control.Issue)
		issue := StallInventoryIssue{
			Repository: control.Repository, Issue: control.Issue, Control: control,
			DispatchWait: waits[key], Fenced: fenced[control.Repository],
			ActiveRun: control.Readiness == factory.ReadinessActive,
			CIPending: deps.CIPending[key], Paused: deps.Paused[key],
			ProductChoiceOpen: deps.ProductChoiceOpen[key],
		}
		for _, blocker := range control.Blockers {
			if blocker.Code == factory.BlockerCodePending || blocker.Code == factory.BlockerResultPending {
				tracked, err := c.endpointProducerTracked(ctx, blocker)
				if err != nil {
					return StallClassification{}, err
				}
				// Downgrade only: one missing prerequisite
				// endpoint marks the issue missing-producer, and
				// a later viable blocker must not clear it.
				if !tracked {
					in.Producers[key] = false
				}
			}
		}
		in.Issues = append(in.Issues, issue)
	}
	return ClassifyStallInventory(in), nil
}

// endpointProducerTracked reports whether the pending prerequisite's
// specific endpoint still has a recorded head acceptance. Any nonempty
// head on an unrelated occurrence must not stand in for the endpoint
// this blocker actually waits on: only the endpoint's own recorded
// head proves a viable producer.
func (c *Coordinator) endpointProducerTracked(ctx context.Context, prereq factory.Blocker) (bool, error) {
	if prereq.EndpointRepo <= 0 || prereq.EndpointIssue <= 0 {
		return false, nil
	}
	_, err := c.Store.AcceptanceHead(ctx, prereq.EndpointRepo, prereq.EndpointIssue)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}
