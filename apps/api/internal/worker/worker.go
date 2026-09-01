package worker

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"sync"
	"time"

	"github.com/yyl1212/agent-studio/apps/api/internal/domain"
	"github.com/yyl1212/agent-studio/apps/api/internal/engine"
	"github.com/yyl1212/agent-studio/apps/api/internal/observability"
	"github.com/yyl1212/agent-studio/apps/api/internal/runpayload"
	"github.com/yyl1212/agent-studio/apps/api/internal/workflow"
)

type Config struct {
	OwnerID             string
	MaxActiveRuns       int
	LeaseDuration       time.Duration
	HeartbeatInterval   time.Duration
	ClaimInterval       time.Duration
	QueueSampleInterval time.Duration
	ShutdownTimeout     time.Duration
}

type executionRehydrator interface {
	Load(context.Context, workflow.ClaimedRun) (PreparedExecution, error)
}

type leasedExecutor interface {
	ExecuteLeased(context.Context, *workflow.PreparedRun, engine.Checkpoint, domain.RunLease, *runpayload.Cipher, engine.Observer) (engine.RunResult, error)
}

type queueStatsSource interface {
	RunQueueStats(context.Context) (int64, time.Duration, error)
}

type Worker struct {
	config     Config
	store      workflow.DurableRunStore
	rehydrator executionRehydrator
	executor   leasedExecutor
	cipher     *runpayload.Cipher
	claimer    *claimer
	telemetry  *Telemetry
	logger     *slog.Logger

	mu      sync.Mutex
	cancels map[string]context.CancelCauseFunc
}

type Option func(*Worker)

func WithTelemetry(providers observability.Providers) Option {
	return func(worker *Worker) { worker.telemetry = newTelemetry(providers) }
}

func WithLogger(logger *slog.Logger) Option {
	return func(worker *Worker) {
		if logger != nil {
			worker.logger = logger
		}
	}
}

// WithClaimRandom is intended for deterministic tests of claim jitter.
func WithClaimRandom(random func() float64) Option {
	return func(worker *Worker) {
		if random != nil {
			worker.claimer.random = random
		}
	}
}

func New(config Config, store workflow.DurableRunStore, rehydrator executionRehydrator, executor leasedExecutor, cipher *runpayload.Cipher, options ...Option) *Worker {
	if config.MaxActiveRuns <= 0 {
		config.MaxActiveRuns = 1
	}
	if config.LeaseDuration <= 0 {
		config.LeaseDuration = 30 * time.Second
	}
	if config.HeartbeatInterval <= 0 {
		config.HeartbeatInterval = 10 * time.Second
	}
	if config.ClaimInterval <= 0 {
		config.ClaimInterval = 500 * time.Millisecond
	}
	if config.ShutdownTimeout <= 0 {
		config.ShutdownTimeout = 30 * time.Second
	}
	worker := &Worker{
		config: config, store: store, rehydrator: rehydrator, executor: executor, cipher: cipher,
		cancels: make(map[string]context.CancelCauseFunc), telemetry: newTelemetry(observability.Providers{}),
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	worker.claimer = newClaimer(store, config.OwnerID, config.LeaseDuration, config.ClaimInterval, nil)
	for _, option := range options {
		option(worker)
	}
	return worker
}

func (worker *Worker) Run(ctx context.Context) error {
	if ctx == nil || worker.config.OwnerID == "" || worker.store == nil || worker.rehydrator == nil || worker.executor == nil {
		return errors.New("worker dependencies are incomplete")
	}
	completed := make(chan string, worker.config.MaxActiveRuns)
	active := 0
	for {
		if active < worker.config.MaxActiveRuns {
			started := time.Now()
			claimed, ok, err := worker.claimer.claim(ctx)
			if err != nil {
				worker.telemetry.claim(ctx, "error", time.Since(started))
				worker.logError(ctx, "run claim failed", "claim_failed", "", err)
				worker.sampleQueue(ctx)
				if ctx.Err() != nil {
					return worker.shutdown(active, completed)
				}
			} else if ok {
				worker.telemetry.claim(ctx, "claimed", time.Since(started))
				worker.sampleQueue(ctx)
				active++
				worker.launch(ctx, claimed, completed)
				continue
			} else {
				worker.telemetry.claim(ctx, "empty", time.Since(started))
				worker.sampleQueue(ctx)
			}
		}
		var delay <-chan time.Time
		if active < worker.config.MaxActiveRuns {
			delay = time.After(worker.claimer.retryDelay())
		}
		select {
		case runID := <-completed:
			active--
			worker.removeCancel(runID)
		case <-delay:
		case <-ctx.Done():
			return worker.shutdown(active, completed)
		}
	}
}

func (worker *Worker) sampleQueue(ctx context.Context) {
	source, ok := worker.store.(queueStatsSource)
	if !ok {
		return
	}
	depth, oldest, err := source.RunQueueStats(ctx)
	if err == nil {
		worker.telemetry.queue(ctx, depth, oldest)
	}
}

func (worker *Worker) launch(parent context.Context, claimed workflow.ClaimedRun, completed chan<- string) {
	executionContext, cancel := context.WithCancelCause(context.WithoutCancel(parent))
	worker.mu.Lock()
	worker.cancels[claimed.Run.ID] = cancel
	worker.mu.Unlock()
	worker.telemetry.leaseStarted(executionContext, claimed.Lease.Token > 1)
	go func() {
		defer func() {
			cancel(context.Canceled)
			worker.telemetry.leaseFinished(executionContext)
			completed <- claimed.Run.ID
		}()
		if err := worker.process(executionContext, cancel, claimed); err != nil {
			if errors.Is(err, domain.ErrRunLeaseLost) {
				worker.telemetry.fenced(executionContext)
			}
			worker.logError(executionContext, "run processing stopped", workerErrorCategory(err), claimed.Run.ID, err)
		}
	}()
}

func (worker *Worker) logError(ctx context.Context, message, category, runID string, err error) {
	if errors.Is(err, context.Canceled) || errors.Is(err, workflow.ErrRunExecutionInterrupted) {
		return
	}
	observability.Log(ctx, worker.logger, slog.LevelError, message, observability.IDs{RunID: runID},
		slog.String("worker_id", worker.config.OwnerID), slog.String("error_category", category))
}

func workerErrorCategory(err error) string {
	switch {
	case errors.Is(err, domain.ErrRunLeaseLost):
		return "lease_lost"
	case errors.Is(err, domain.ErrRunEventBudgetExceeded):
		return "event_budget"
	default:
		return "processing_failed"
	}
}

func (worker *Worker) process(ctx context.Context, cancel context.CancelCauseFunc, claimed workflow.ClaimedRun) error {
	stopHeartbeat := make(chan struct{})
	heartbeatDone := make(chan struct{})
	go func() {
		defer close(heartbeatDone)
		worker.heartbeat(ctx, cancel, claimed.Lease, stopHeartbeat)
	}()
	defer func() {
		close(stopHeartbeat)
		<-heartbeatDone
	}()
	if claimed.Run.CancelRequestedAt != nil {
		return worker.finalizeRequestedCancellation(ctx, claimed)
	}
	prepared, err := worker.rehydrator.Load(ctx, claimed)
	if err != nil || ctx.Err() != nil {
		return err
	}
	if prepared.Recovery.Required {
		worker.telemetry.recovery(ctx, prepared.Recovery)
		requestedAt := time.Now().UTC()
		event := domain.RunEvent{RunID: claimed.Run.ID, Sequence: prepared.Recovery.Sequence + 1, Type: "run.recovery_required", Timestamp: requestedAt, ActivePorts: []string{}, InputRedactedPaths: []string{}, OutputRedactedPaths: []string{}}
		return worker.store.RequireRunRecovery(ctx, claimed.Lease, event, prepared.Recovery.Reason, requestedAt, workerBudget(prepared.Prepared))
	}
	for _, attempt := range prepared.Checkpoint.NodeAttempts {
		if attempt > 1 {
			worker.telemetry.autoRecoveries.Add(ctx, 1)
		}
	}
	if prepared.TerminalStatus != "" {
		now := time.Now().UTC()
		eventType := "run.completed"
		if prepared.TerminalStatus == domain.RunFailed {
			eventType = "run.failed"
		} else if prepared.TerminalStatus == domain.RunCancelled {
			eventType = "run.cancelled"
		}
		terminal := domain.RunEvent{RunID: claimed.Run.ID, Sequence: prepared.Checkpoint.LastSequence + 1, Type: eventType, Output: marshalWorkerValue(prepared.TerminalOutput), Timestamp: now, ActivePorts: []string{}, InputRedactedPaths: []string{}, OutputRedactedPaths: []string{}}
		_, err := worker.store.FinalizeLeasedRun(ctx, claimed.Lease, workflow.RunFinalization{RunID: claimed.Run.ID, Status: prepared.TerminalStatus, Output: prepared.TerminalOutput, EndedAt: now, TerminalEvent: terminal, Budget: workerBudget(prepared.Prepared)}, nil)
		return err
	}
	_, err = worker.executor.ExecuteLeased(ctx, prepared.Prepared, prepared.Checkpoint, claimed.Lease, worker.cipher, nil)
	if errors.Is(context.Cause(ctx), domain.ErrRunLeaseLost) {
		return domain.ErrRunLeaseLost
	}
	return err
}

func (worker *Worker) finalizeRequestedCancellation(ctx context.Context, claimed workflow.ClaimedRun) error {
	_, events, _, err := worker.store.LoadRunExecution(ctx, claimed.Run.ID)
	if err != nil {
		return err
	}
	for index, event := range events {
		if event.RunID != claimed.Run.ID || event.Sequence != int64(index+1) || isWorkerTerminalEvent(event.Type) {
			return errors.New("cancelled run event history is invalid")
		}
	}
	var graph domain.Graph
	if err := json.Unmarshal(claimed.Run.GraphSnapshot, &graph); err != nil {
		return errors.New("cancelled run graph snapshot is invalid")
	}
	now := time.Now().UTC()
	terminal := domain.RunEvent{RunID: claimed.Run.ID, Sequence: int64(len(events) + 1), Type: "run.cancelled", Timestamp: now, ActivePorts: []string{}, InputRedactedPaths: []string{}, OutputRedactedPaths: []string{}}
	_, err = worker.store.FinalizeLeasedRun(ctx, claimed.Lease, workflow.RunFinalization{
		RunID: claimed.Run.ID, Status: domain.RunCancelled, EndedAt: now, TerminalEvent: terminal,
		Budget: domain.RunEventBudget{MaxEvents: 8*len(graph.Nodes) + 16, MaxTotalDataBytes: 32 << 20},
	}, nil)
	return err
}

func isWorkerTerminalEvent(eventType string) bool {
	switch eventType {
	case "run.completed", "run.failed", "run.cancelled":
		return true
	default:
		return false
	}
}

func (worker *Worker) heartbeat(ctx context.Context, cancel context.CancelCauseFunc, lease domain.RunLease, stop <-chan struct{}) {
	ticker := time.NewTicker(worker.config.HeartbeatInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			heartbeat, err := worker.store.RenewRunLease(ctx, lease, worker.config.LeaseDuration)
			if err != nil {
				if errors.Is(err, domain.ErrRunLeaseLost) {
					worker.telemetry.renewal(ctx, "lease_lost")
				} else {
					worker.telemetry.renewal(ctx, "error")
				}
				cancel(err)
				return
			}
			lease = heartbeat.Lease
			if heartbeat.CancelRequested {
				worker.telemetry.renewal(ctx, "cancel_requested")
				cancel(context.Canceled)
				return
			}
			worker.telemetry.renewal(ctx, "success")
		case <-stop:
			return
		case <-ctx.Done():
			return
		}
	}
}

func (worker *Worker) shutdown(active int, completed <-chan string) error {
	if active == 0 {
		return nil
	}
	timer := time.NewTimer(worker.config.ShutdownTimeout)
	defer timer.Stop()
	for active > 0 {
		select {
		case runID := <-completed:
			active--
			worker.removeCancel(runID)
		case <-timer.C:
			worker.cancelAll(workflow.ErrRunExecutionInterrupted)
			return nil
		}
	}
	return nil
}

func (worker *Worker) removeCancel(runID string) {
	worker.mu.Lock()
	delete(worker.cancels, runID)
	worker.mu.Unlock()
}

func (worker *Worker) cancelAll(cause error) {
	worker.mu.Lock()
	defer worker.mu.Unlock()
	for _, cancel := range worker.cancels {
		cancel(cause)
	}
}

func workerBudget(prepared *workflow.PreparedRun) domain.RunEventBudget {
	nodes := 0
	if prepared != nil && prepared.Plan != nil {
		nodes = len(prepared.Plan.Nodes)
	}
	return domain.RunEventBudget{MaxEvents: 8*nodes + 16, MaxTotalDataBytes: 32 << 20}
}

func marshalWorkerValue(value any) []byte {
	if value == nil {
		return nil
	}
	body, _ := json.Marshal(value)
	return body
}
