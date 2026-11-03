// Package control supervises recorded factory runs: status reads recorded
// state, dispatch starts bounded recorded assignments, and reconcile settles
// outstanding runs against host and broker truth. Publication advances finished
// candidates through persisted conditional operations. The host owns native
// execution and the broker owns credential custody.
package control

import (
	"context"
	"errors"
	"net/http"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/sys/unix"

	"github.com/levitateos/sodaos/internal/factory"
	"github.com/levitateos/sodaos/internal/factory/execution"
	"github.com/levitateos/sodaos/internal/filelock"
	"github.com/levitateos/sodaos/internal/store"
)

// ErrCommandRunning reports a duplicate command whose first execution
// has not finished. The caller refreshes instead of executing twice.
var ErrCommandRunning = errors.New("factory command already running")

// HostFactory is the coordinator's entire host surface: launch starts one
// recorded run under supervision, stop retires one run, inspect observes
// one run without mutating it, takeover copies a retired run's retained
// work, export reads a settled run's exact candidate bundle, and tool inspection
// reports the runtime's proved executable/bundle identity.
type HostFactory interface {
	execution.RunLauncher
	execution.RunObserver
	execution.PreparationContextReader
	execution.WorkRecoverer
	execution.CandidateExporter
	execution.ToolObserver
	execution.PreparationLauncher
	execution.PreparationObserver
}

// BrokerExecution is the coordinator's entire broker surface: close seals
// one execution and get reads its nonsecret metadata. The coordinator never
// acquires, registers or handles credential delivery.
type BrokerExecution interface {
	execution.CustodyObserver
}

// Coordinator supervises the factory ledger in the dashboard store. One
// coordinator owns the database at a time; startup settles outstanding runs
// before serving operator commands. AcceptanceReads brackets native issue
// evidence for acceptance decisions; while no source is wired, admission
// and validity checks refuse as unavailable instead of guessing.
// DispatchReads brackets accepted-input content plus the target tip for
// dispatch; while no source is wired, dispatch waits instead of guessing.
// Publication executes conditional publication operations; while no
// executor is wired, finished candidates wait instead of publishing.
// Merges executes conditional merge operations; while no executor is
// wired, published candidates wait instead of merging.
// Checks observes native check evidence for published candidates; while
// no assessor is wired, checks report unavailable instead of guessing.
type Coordinator struct {
	MCPKey          []byte
	Store           *store.Store
	Host            HostFactory
	Broker          BrokerExecution
	Graph           IssueGraphExecutor
	GraphActorID    int64
	AcceptanceReads AcceptanceSource
	Readiness       ReadinessObservation
	Creations       CreationRecoverySource
	DispatchReads   DispatchReads
	Publication     PublicationExecutor
	Reviews         ReviewExecutor
	Resolutions     ResolutionExecutor
	Checks          CheckObserver
	Merges          MergeExecutor
	Priority        *PriorityJudgments
	Planning        *PlanningJudgments
	// CompletionEnumerator supplies native inventory pages for the D13
	// completion fence; CompletionLocalCapture captures the local half
	// of the sealed evidence. Both are FW31/FW32 handoff bindings: nil
	// holds collecting cycles honestly instead of sealing from partial
	// evidence. FW32 binds them to the FW29 snapshot family and the
	// local store capture.
	CompletionEnumerator   CompletionIssueEnumerator
	CompletionLocalCapture func(ctx context.Context, repository int64) (factory.CompletionLocal, error)
	// CommentDrive advances durable feed-comment pipelines at Start and
	// on each progress pass. The shared application service owns collaborators;
	// the coordinator owns only the lifetime that drives them,
	// so comment completion never depends on a browser request. Nil
	// holds pending comment requests in their stored state.
	CommentDrive        func(ctx context.Context)
	queue               DispatchQueueCursor
	lock                *os.File
	requestGate         sync.RWMutex
	cascadeMu           sync.Mutex
	closing             atomic.Bool
	ownerMu             sync.Mutex
	progressMu          sync.Mutex
	dispatchObservation atomic.Pointer[DispatchObservation]
	progressCancel      context.CancelFunc
	progressDone        chan struct{}
	// ownerMu also protects the bounded child-result recovery cursor.
	publicationChildCursor string
	// progressMu protects the bounded recovery and verification cursors below.
	creationRepositoryCursor int64
	intentGenerationCursor   string
	deliveryIntentCursor     string
	deliveryRepairCursor     string
	appVerificationCursor    int64
}

func NewCoordinator(db *store.Store, host HostFactory, broker BrokerExecution) *Coordinator {
	return &Coordinator{Store: db, Host: host, Broker: broker}
}

// Start takes exclusive coordinator ownership of the database and settles
// outstanding runs. A second coordinator fails fast instead of reconciling
// concurrently.
func (c *Coordinator) Start(ctx context.Context, lockPath string) error {
	lifetime := ctx
	ctx, unlock := c.progressPass(ctx)
	defer unlock()
	ctx, ownsPass, cancelPass := c.readinessPass(ctx)
	defer cancelPass()
	if c.Store == nil || c.Host == nil || c.Broker == nil {
		return errors.New("factory coordinator dependencies required")
	}
	c.requestGate.RLock()
	if c.closing.Load() {
		c.requestGate.RUnlock()
		return errors.New("factory coordinator is closing")
	}
	c.ownerMu.Lock()
	if c.lock != nil {
		c.ownerMu.Unlock()
		c.requestGate.RUnlock()
		return errors.New("factory coordinator already started")
	}
	file, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		c.ownerMu.Unlock()
		c.requestGate.RUnlock()
		return err
	}
	bounded, stop := context.WithTimeout(ctx, 10*time.Second)
	err = filelock.Acquire(bounded, file, unix.LOCK_EX)
	stop()
	if err != nil {
		c.ownerMu.Unlock()
		_ = file.Close()
		c.requestGate.RUnlock()
		return errors.New("another factory coordinator owns this database")
	}
	c.lock = file
	c.ownerMu.Unlock()
	_, err = c.reconcileRuns(ctx)
	if err != nil {
		c.requestGate.RUnlock()
		return errors.Join(err, c.Close())
	}
	if report := RecoverDispatch(ctx, c.dispatchDeps()); len(report.Errors) != 0 {
		c.requestGate.RUnlock()
		return errors.Join(errors.New("factory dispatch recovery failed"), c.Close())
	}
	// Apply queued terminal posts before publication.
	_ = c.settleMCPTerminals(ctx)
	// Stored comment requests resume service-side at startup: a
	// restart picks up the same durable rows instead of waiting for a
	// browser to re-drive them.
	if c.CommentDrive != nil {
		c.CommentDrive(ctx)
	}
	if c.Publication != nil {
		c.PublishPass(ctx)
	}
	if c.Checks != nil {
		c.CheckPass(ctx)
	}
	if c.Merges != nil {
		c.MergePass(ctx)
	}
	_ = c.RecoverIntentGraphs(ctx)
	_ = c.recoverCreations(ctx)
	if ownsPass {
		_, _ = c.drainReadinessWork(ctx, "")
	}
	c.startProgress(lifetime)
	c.requestGate.RUnlock()
	return nil
}

// AdmitHTTP keeps coordinator/Store ownership around the complete admitted
// handler, including synchronous host and broker work after request
// cancellation. All dashboard listener roots share this admission gate.
func (c *Coordinator) AdmitHTTP(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if c.closing.Load() {
			http.Error(w, "service shutting down", http.StatusServiceUnavailable)
			return
		}
		c.requestGate.RLock()
		defer c.requestGate.RUnlock()
		if c.closing.Load() {
			http.Error(w, "service shutting down", http.StatusServiceUnavailable)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// Admitting reports whether the coordinator still admits HTTP work.
// Readiness probes use it; StopAdmission flips it during shutdown.
func (c *Coordinator) Admitting() bool { return !c.closing.Load() }

// StopAdmission rejects new HTTP work while shutdown retires hijacked
// terminal peers and closes listeners. Existing handler readers remain held.
func (c *Coordinator) StopAdmission() {
	c.closing.Store(true)
	c.ownerMu.Lock()
	if c.progressCancel != nil {
		c.progressCancel()
	}
	c.ownerMu.Unlock()
}

// Close stops admission, waits for every admitted handler to finish, then
// releases coordinator ownership. Recorded runs keep their settled or
// fenced state; a later Start reconciles what remains.
func (c *Coordinator) Close() error {
	c.StopAdmission()
	c.ownerMu.Lock()
	done := c.progressDone
	c.ownerMu.Unlock()
	if done != nil {
		<-done
	}
	c.requestGate.Lock()
	defer c.requestGate.Unlock()
	c.ownerMu.Lock()
	defer c.ownerMu.Unlock()
	if c.lock == nil {
		return nil
	}
	err := c.lock.Close()
	c.lock = nil
	return err
}

// Status returns bounded recorded run state with credential-adjacent paths
// redacted. It never calls the host or broker; stop and reconcile refresh
// the recorded observations.
func (c *Coordinator) Status(ctx context.Context, target string) ([]factory.Run, error) {
	bounded, stop := context.WithTimeout(ctx, 30*time.Second)
	defer stop()
	if target != "" {
		if !factory.ValidID(target) {
			return nil, factory.ErrNotFound
		}
		r, err := c.Store.FactoryRun(bounded, target)
		if err != nil {
			return nil, factory.ErrNotFound
		}
		return []factory.Run{redactRun(r)}, nil
	}
	runs, err := c.Store.FactoryRuns(bounded, 100)
	if err != nil {
		return nil, err
	}
	for i := range runs {
		runs[i] = redactRun(runs[i])
	}
	return runs, nil
}

func redactRun(r factory.Run) factory.Run {
	if r.IdentityBinding == nil {
		return r
	}
	redacted := *r.IdentityBinding
	redacted.CredentialRoot = ""
	r.IdentityBinding = &redacted
	return r
}
