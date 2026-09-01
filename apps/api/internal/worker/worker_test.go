package worker

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/yyl1212/agent-studio/apps/api/internal/domain"
	"github.com/yyl1212/agent-studio/apps/api/internal/engine"
	"github.com/yyl1212/agent-studio/apps/api/internal/runpayload"
	"github.com/yyl1212/agent-studio/apps/api/internal/workflow"
)

func TestWorkerDefaultsToOneActiveRun(t *testing.T) {
	store := &workerStore{claims: []workflow.ClaimedRun{claimedRun("run-1", 1), claimedRun("run-2", 1)}}
	rehydrator := &workerRehydrator{results: map[string]PreparedExecution{
		"run-1": preparedWorkerExecution("run-1"), "run-2": preparedWorkerExecution("run-2"),
	}}
	executor := &workerExecutor{started: make(chan string, 2), release: make(chan struct{})}
	worker := New(Config{OwnerID: "worker", LeaseDuration: time.Second, HeartbeatInterval: 100 * time.Millisecond, ClaimInterval: time.Millisecond, ShutdownTimeout: time.Second}, store, rehydrator, executor, nil)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- worker.Run(ctx) }()
	<-executor.started
	select {
	case second := <-executor.started:
		t.Fatalf("second run started before capacity released: %s", second)
	case <-time.After(20 * time.Millisecond):
	}
	executor.release <- struct{}{}
	<-executor.started
	executor.release <- struct{}{}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if executor.maximum != 1 {
		t.Fatalf("max active=%d", executor.maximum)
	}
}

func TestWorkerRetriesClaimFailureWithJitter(t *testing.T) {
	store := &workerStore{claimErrors: []error{errors.New("temporary claim failure")}, claims: []workflow.ClaimedRun{claimedRun("retry-claim", 1)}}
	rehydrator := &workerRehydrator{results: map[string]PreparedExecution{"retry-claim": preparedWorkerExecution("retry-claim")}}
	executor := &workerExecutor{started: make(chan string, 1), release: make(chan struct{})}
	worker := New(Config{OwnerID: "worker", HeartbeatInterval: time.Second, ClaimInterval: time.Millisecond, ShutdownTimeout: time.Second}, store, rehydrator, executor, nil, WithClaimRandom(func() float64 { return 0.5 }))
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- worker.Run(ctx) }()
	if runID := <-executor.started; runID != "retry-claim" {
		t.Fatalf("started=%s", runID)
	}
	executor.release <- struct{}{}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestWorkerLogsContainOnlyStableOperationalFields(t *testing.T) {
	const private = "postgres://user:password@db/private encryption-key ciphertext raw-input node-config"
	store := &workerStore{claimErrors: []error{errors.New(private)}, claimErrored: make(chan struct{}, 1)}
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, nil))
	worker := New(Config{OwnerID: "worker-safe", ClaimInterval: time.Millisecond}, store,
		&workerRehydrator{results: map[string]PreparedExecution{}},
		&workerExecutor{started: make(chan string, 1), release: make(chan struct{})}, nil,
		WithLogger(logger), WithClaimRandom(func() float64 { return 0.5 }))
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- worker.Run(ctx) }()
	<-store.claimErrored
	time.Sleep(5 * time.Millisecond)
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if strings.Contains(logs.String(), private) || strings.Contains(logs.String(), "password") || strings.Contains(logs.String(), "raw-input") {
		t.Fatalf("private data leaked to worker log: %s", logs.String())
	}
	if !strings.Contains(logs.String(), "worker-safe") || !strings.Contains(logs.String(), "claim_failed") {
		t.Fatalf("stable fields missing from worker log: %s", logs.String())
	}
}

func TestWorkerPersistsRecoveryDecisionWithoutExecuting(t *testing.T) {
	store := &workerStore{claims: []workflow.ClaimedRun{claimedRun("recover", 1)}, recovered: make(chan domain.RunRecoveryReason, 1)}
	rehydrator := &workerRehydrator{results: map[string]PreparedExecution{"recover": {Recovery: RecoveryDecision{Required: true, Reason: domain.RecoveryUncertainEffect, Sequence: 3}}}}
	executor := &workerExecutor{started: make(chan string, 1), release: make(chan struct{})}
	worker := New(Config{OwnerID: "worker", LeaseDuration: time.Second, HeartbeatInterval: 100 * time.Millisecond, ClaimInterval: time.Millisecond, ShutdownTimeout: time.Second}, store, rehydrator, executor, nil)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- worker.Run(ctx) }()
	if reason := <-store.recovered; reason != domain.RecoveryUncertainEffect {
		t.Fatalf("reason=%s", reason)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	select {
	case runID := <-executor.started:
		t.Fatalf("executed recovery run %s", runID)
	default:
	}
}

func TestWorkerConvergesPreviouslyRequestedCancellationWithoutRehydrating(t *testing.T) {
	requestedAt := time.Now().UTC()
	claim := claimedRun("cancel-before-claim", 1)
	claim.Run.CancelRequestedAt = &requestedAt
	claim.Run.Status = domain.RunCancelling
	claim.Run.GraphSnapshot = []byte(`{"schemaVersion":1,"nodes":[],"edges":[]}`)
	store := &workerStore{
		claims:       []workflow.ClaimedRun{claim},
		loadedEvents: []domain.RunEvent{{RunID: claim.Run.ID, Sequence: 1, Type: "run.queued"}},
		finalized:    make(chan domain.RunStatus, 1),
	}
	rehydrator := &unexpectedWorkerRehydrator{called: make(chan struct{}, 1)}
	executor := &workerExecutor{started: make(chan string, 1), release: make(chan struct{})}
	worker := New(Config{OwnerID: "worker", HeartbeatInterval: time.Second, ClaimInterval: time.Millisecond, ShutdownTimeout: time.Second}, store, rehydrator, executor, nil)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- worker.Run(ctx) }()
	if status := <-store.finalized; status != domain.RunCancelled {
		t.Fatalf("status=%s", status)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	select {
	case <-rehydrator.called:
		t.Fatal("rehydrator called for previously requested cancellation")
	default:
	}
	select {
	case runID := <-executor.started:
		t.Fatalf("executed previously cancelled run %s", runID)
	default:
	}
}

func TestWorkerLeaseLossCancelsExecution(t *testing.T) {
	store := &workerStore{claims: []workflow.ClaimedRun{claimedRun("lost", 1)}, renewErr: domain.ErrRunLeaseLost, renewed: make(chan struct{}, 1)}
	rehydrator := &workerRehydrator{results: map[string]PreparedExecution{"lost": preparedWorkerExecution("lost")}}
	executor := &workerExecutor{started: make(chan string, 1), cancelled: make(chan error, 1)}
	worker := New(Config{OwnerID: "worker", LeaseDuration: 30 * time.Millisecond, HeartbeatInterval: 5 * time.Millisecond, ClaimInterval: time.Millisecond, ShutdownTimeout: time.Second}, store, rehydrator, executor, nil)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- worker.Run(ctx) }()
	<-executor.started
	<-store.renewed
	if err := <-executor.cancelled; !errors.Is(err, context.Canceled) {
		t.Fatalf("execution error=%v", err)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestWorkerHeartbeatCancellationCancelsExecution(t *testing.T) {
	claim := claimedRun("cancelled", 1)
	store := &workerStore{
		claims:    []workflow.ClaimedRun{claim},
		heartbeat: workflow.LeaseHeartbeat{Lease: claim.Lease, CancelRequested: true},
		renewed:   make(chan struct{}, 1),
	}
	rehydrator := &workerRehydrator{results: map[string]PreparedExecution{"cancelled": preparedWorkerExecution("cancelled")}}
	executor := &workerExecutor{started: make(chan string, 1), cancelled: make(chan error, 1)}
	worker := New(Config{OwnerID: "worker", LeaseDuration: time.Second, HeartbeatInterval: 5 * time.Millisecond, ClaimInterval: time.Millisecond, ShutdownTimeout: time.Second}, store, rehydrator, executor, nil)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- worker.Run(ctx) }()
	<-executor.started
	<-store.renewed
	if err := <-executor.cancelled; !errors.Is(err, context.Canceled) {
		t.Fatalf("execution error=%v", err)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestWorkerShutdownWaitsForActiveRunWithinGracePeriod(t *testing.T) {
	store := &workerStore{claims: []workflow.ClaimedRun{claimedRun("graceful", 1)}}
	rehydrator := &workerRehydrator{results: map[string]PreparedExecution{"graceful": preparedWorkerExecution("graceful")}}
	executor := &workerExecutor{started: make(chan string, 1), release: make(chan struct{}), cancelled: make(chan error, 1)}
	worker := New(Config{OwnerID: "worker", HeartbeatInterval: time.Second, ClaimInterval: time.Millisecond, ShutdownTimeout: 200 * time.Millisecond}, store, rehydrator, executor, nil)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- worker.Run(ctx) }()
	<-executor.started
	cancel()
	select {
	case err := <-done:
		t.Fatalf("worker stopped before active run completed: %v", err)
	case <-time.After(10 * time.Millisecond):
	}
	executor.release <- struct{}{}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if err := <-executor.cancelled; err != nil {
		t.Fatalf("graceful execution was cancelled: %v", err)
	}
}

func TestWorkerShutdownTimeoutCancelsActiveRun(t *testing.T) {
	store := &workerStore{claims: []workflow.ClaimedRun{claimedRun("timeout", 1)}}
	rehydrator := &workerRehydrator{results: map[string]PreparedExecution{"timeout": preparedWorkerExecution("timeout")}}
	executor := &workerExecutor{started: make(chan string, 1), cancelled: make(chan error, 1)}
	worker := New(Config{OwnerID: "worker", HeartbeatInterval: time.Second, ClaimInterval: time.Millisecond, ShutdownTimeout: 20 * time.Millisecond}, store, rehydrator, executor, nil)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- worker.Run(ctx) }()
	<-executor.started
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if err := <-executor.cancelled; !errors.Is(err, context.Canceled) {
		t.Fatalf("execution error=%v", err)
	}
}

type workerRehydrator struct{ results map[string]PreparedExecution }

func (rehydrator *workerRehydrator) Load(_ context.Context, claimed workflow.ClaimedRun) (PreparedExecution, error) {
	return rehydrator.results[claimed.Run.ID], nil
}

type unexpectedWorkerRehydrator struct{ called chan struct{} }

func (rehydrator *unexpectedWorkerRehydrator) Load(context.Context, workflow.ClaimedRun) (PreparedExecution, error) {
	rehydrator.called <- struct{}{}
	return PreparedExecution{}, errors.New("unexpected rehydration")
}

type workerExecutor struct {
	mu              sync.Mutex
	active, maximum int
	started         chan string
	release         chan struct{}
	cancelled       chan error
}

func (executor *workerExecutor) ExecuteLeased(ctx context.Context, prepared *workflow.PreparedRun, _ engine.Checkpoint, _ domain.RunLease, _ *runpayload.Cipher, _ engine.Observer) (engine.RunResult, error) {
	executor.mu.Lock()
	executor.active++
	if executor.active > executor.maximum {
		executor.maximum = executor.active
	}
	executor.mu.Unlock()
	executor.started <- prepared.RunID
	if executor.release != nil {
		select {
		case <-executor.release:
		case <-ctx.Done():
		}
	} else {
		<-ctx.Done()
	}
	err := ctx.Err()
	executor.mu.Lock()
	executor.active--
	executor.mu.Unlock()
	if executor.cancelled != nil {
		executor.cancelled <- err
	}
	return engine.RunResult{RunID: prepared.RunID}, err
}

type workerStore struct {
	mu           sync.Mutex
	claims       []workflow.ClaimedRun
	claimErrors  []error
	claimErrored chan struct{}
	renewErr     error
	heartbeat    workflow.LeaseHeartbeat
	loadedEvents []domain.RunEvent
	recovered    chan domain.RunRecoveryReason
	renewed      chan struct{}
	finalized    chan domain.RunStatus
}

func (store *workerStore) SubmitRun(context.Context, workflow.RunSubmission) error { return nil }
func (store *workerStore) ClaimRun(context.Context, string, time.Duration) (workflow.ClaimedRun, bool, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	if len(store.claimErrors) > 0 {
		err := store.claimErrors[0]
		store.claimErrors = store.claimErrors[1:]
		if store.claimErrored != nil {
			store.claimErrored <- struct{}{}
		}
		return workflow.ClaimedRun{}, false, err
	}
	if len(store.claims) == 0 {
		return workflow.ClaimedRun{}, false, nil
	}
	claim := store.claims[0]
	store.claims = store.claims[1:]
	return claim, true, nil
}
func (store *workerStore) RenewRunLease(context.Context, domain.RunLease, time.Duration) (workflow.LeaseHeartbeat, error) {
	if store.renewed != nil {
		select {
		case store.renewed <- struct{}{}:
		default:
		}
	}
	return store.heartbeat, store.renewErr
}
func (store *workerStore) LoadRunExecution(context.Context, string) (domain.Run, []domain.RunEvent, []domain.RunPayload, error) {
	return domain.Run{}, store.loadedEvents, nil, nil
}
func (*workerStore) PersistLeasedRunEvent(context.Context, domain.RunLease, domain.RunEvent, *domain.NodeRun, []domain.RunPayload, domain.RunEventBudget) error {
	return nil
}
func (store *workerStore) RequireRunRecovery(_ context.Context, _ domain.RunLease, _ domain.RunEvent, reason domain.RunRecoveryReason, _ time.Time, _ domain.RunEventBudget) error {
	if store.recovered != nil {
		store.recovered <- reason
	}
	return nil
}
func (store *workerStore) FinalizeLeasedRun(_ context.Context, _ domain.RunLease, finalization workflow.RunFinalization, _ []domain.RunPayload) (domain.RunEvent, error) {
	if store.finalized != nil {
		store.finalized <- finalization.Status
	}
	return finalization.TerminalEvent, nil
}

func claimedRun(id string, token int64) workflow.ClaimedRun {
	return workflow.ClaimedRun{Run: domain.Run{ID: id, Status: domain.RunRunning}, Lease: domain.RunLease{RunID: id, Owner: "worker", Token: token, ExpiresAt: time.Now().Add(time.Second)}}
}
func preparedWorkerExecution(id string) PreparedExecution {
	return PreparedExecution{Prepared: &workflow.PreparedRun{RunID: id, Plan: &engine.Plan{Nodes: map[string]engine.CompiledNode{}}}, Checkpoint: engine.Checkpoint{LastSequence: 1}}
}
