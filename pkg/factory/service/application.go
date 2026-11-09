package service

import (
	"context"
	"errors"

	"github.com/levitateos/factory/pkg/factory"
	"github.com/levitateos/factory/pkg/factory/control"
	"github.com/levitateos/factory/pkg/store"
)

// ApplicationPorts supplies host-admitted runtime and collaboration operations.
// Nil optional ports retain the coordinator's existing unavailable/wait behavior.
// Judgment ports receive no credentials here; the host owns provider setup.
type ApplicationPorts struct {
	Store                  *store.Store
	Host                   control.HostFactory
	Broker                 control.BrokerExecution
	Graph                  control.IssueGraphExecutor
	GraphActorID           int64
	AcceptanceReads        control.AcceptanceSource
	Readiness              control.ReadinessObservation
	Creations              control.CreationRecoverySource
	DispatchReads          control.DispatchReads
	Publication            control.PublicationExecutor
	Reviews                control.ReviewExecutor
	Resolutions            control.ResolutionExecutor
	Checks                 control.CheckObserver
	Merges                 control.MergeExecutor
	CompletionEnumerator   control.CompletionIssueEnumerator
	CompletionIssues       func(context.Context, int64) ([]control.NativeInventoryIssue, error)
	CompletionLocalCapture func(context.Context, int64) (factory.CompletionLocal, error)
	Priority               control.PriorityChooser
	Planning               control.ProductJudgment
	ContextChooser         control.ProductJudgment
	CurrentChooser         func() control.ProductJudgment
	IssueEffects           IssueMakerEffects
	PostJudge              PostJudge
}

// Application composes canonical workflows over one store and coordinator.
// Host adapters may also form a view over that same coordinator for commands
// and collaborator readback. A view owns no second ledger or progress loop.
type Application struct {
	Store        *store.Store
	Coordinator  *control.Coordinator
	Chooser      func() control.ProductJudgment
	IssueEffects IssueMakerEffects
	PostJudge    PostJudge
}

// NewApplication constructs the coordinator and its background workflow stages.
// It performs no I/O, admission, reconciliation, provider call or dispatch. The
// host must explicitly call Start under its existing ownership lock.
func NewApplication(ports ApplicationPorts) Application {
	coordinator := control.NewCoordinator(ports.Store, ports.Host, ports.Broker)
	coordinator.Graph, coordinator.GraphActorID = ports.Graph, ports.GraphActorID
	coordinator.AcceptanceReads, coordinator.Readiness = ports.AcceptanceReads, ports.Readiness
	coordinator.Creations, coordinator.DispatchReads = ports.Creations, ports.DispatchReads
	coordinator.Publication, coordinator.Reviews = ports.Publication, ports.Reviews
	coordinator.Resolutions, coordinator.Checks, coordinator.Merges = ports.Resolutions, ports.Checks, ports.Merges
	coordinator.CompletionEnumerator, coordinator.CompletionLocalCapture = ports.CompletionEnumerator, ports.CompletionLocalCapture
	coordinator.CompletionIssues = ports.CompletionIssues
	coordinator.Priority = &control.PriorityJudgments{Journal: ports.Store, Chooser: ports.Priority, MaxCalls: control.PriorityDefaultMaxCalls}
	coordinator.ContextChooser = ports.ContextChooser
	if ports.Planning != nil {
		coordinator.Planning = &control.PlanningJudgments{Chooser: ports.Planning}
	}
	application := Application{Store: ports.Store, Coordinator: coordinator, Chooser: ports.CurrentChooser, IssueEffects: ports.IssueEffects, PostJudge: ports.PostJudge}
	coordinator.CommentDrive = application.Background().Drive
	return application
}

// Start takes the coordinator's existing ledger lock and starts reconciliation
// and background progress. Hosts must supply admitted runtime ports and bind
// worker channels before calling it; construction and HTTP reads never call it.
func (a Application) Start(ctx context.Context, lockPath string) error {
	if a.Coordinator == nil {
		return errors.New("factory application coordinator required")
	}
	if err := a.Recover(ctx); err != nil {
		return err
	}
	return a.Coordinator.Start(ctx, lockPath)
}

// Recover reconciles store-owned assessments and capability projections from
// existing sealed native receipts before host readiness.
// It only settles proved released producer ownership; it neither replays model
// calls nor inspects/retires executions, advances workflows or grants authority.
// Hosts without admitted runtime ports can run this recovery independently of
// Start, preserving all retained execution facts until those ports are available.
func (a Application) Recover(ctx context.Context) error {
	if a.Store == nil {
		return errors.New("factory application store required")
	}
	if err := a.Store.RecoverNativeCapabilityRetirement(ctx); err != nil {
		return err
	}
	var after int64
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		page, err := a.Store.RecoverContributionReviews(ctx, after)
		if err != nil {
			return err
		}
		if page.Complete {
			return nil
		}
		after = page.Next
	}
}

// StopAdmission refuses new coordinator-gated requests and cancels background
// progress. Hosts call it before retiring their listeners and upgraded peers.
func (a Application) StopAdmission() {
	if a.Coordinator != nil {
		a.Coordinator.StopAdmission()
	}
}

// Close joins background progress and every coordinator-admitted request before
// releasing ledger ownership. The host must close the store only after this
// returns, including when forced HTTP closure leaves an admitted handler alive.
// Close also drains an application whose background runtime was never started.
func (a Application) Close() error {
	if a.Coordinator == nil {
		return nil
	}
	return a.Coordinator.Close()
}

// IssueMaker shares the original request's effect and funded planning owners.
func (a Application) IssueMaker() *IssueMaker {
	maker := &IssueMaker{Store: a.Store, Effects: a.IssueEffects}
	if a.Coordinator != nil {
		maker.Transport = a.Coordinator
	}
	return maker
}

// Background composes continuation and readback from the same application.
// Current provider availability is resolved by Background at each pass; this
// method never calls the chooser or starts work.
func (a Application) Background() Background {
	runner := Background{Store: a.Store, Chooser: a.Chooser}
	runner.Stages.Judge = a.PostJudge
	if a.PostJudge != nil {
		runner.Stages.Prepare = (PostContext{Posts: Posts{Store: a.Store}}).Prepare
	}
	runner.Stages.IssueMaker = a.IssueMaker().Drive
	if a.Coordinator != nil && a.Store != nil {
		runner.Stages.Drafter = &IntentPostDrafter{Store: a.Store, Transport: a.Coordinator}
		if a.Coordinator.Graph != nil && a.Coordinator.GraphActorID > 0 {
			runner.Stages.Creator = &PostIssueEffects{Store: a.Store, Graph: a.Coordinator.Graph, Actor: a.Coordinator.GraphActorID}
		}
	}
	return runner
}
