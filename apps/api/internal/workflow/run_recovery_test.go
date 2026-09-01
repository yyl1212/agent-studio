package workflow

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/yyl1212/agent-studio/apps/api/internal/domain"
	"github.com/yyl1212/agent-studio/apps/api/internal/engine"
	"github.com/yyl1212/agent-studio/sdk/go/agentnode"
)

func TestRunRecoveryViewDerivesUnresolvedNodeWithoutPrivatePayload(t *testing.T) {
	store := recoveryStoreFixture(domain.RecoveryUncertainEffect, 1)
	service := NewRunRecoveryService(store, recoveryCompiler{plan: recoveryPlan(agentnode.ExecutionSafetySideEffect)})
	view, err := service.Get(context.Background(), testRunID)
	if err != nil {
		t.Fatal(err)
	}
	if view.RunID != testRunID || view.Sequence != 4 || view.Reason != domain.RecoveryUncertainEffect || len(view.Nodes) != 1 {
		t.Fatalf("view=%+v", view)
	}
	node := view.Nodes[0]
	if node.NodeID != "work" || node.NodeAttempt != 1 || node.Safety != agentnode.ExecutionSafetySideEffect || !node.RetryAllowed || node.StartedAt.IsZero() || node.RiskMessage == "" {
		t.Fatalf("node=%+v", node)
	}
}

func TestRunRecoveryConfirmValidatesSequenceAttemptAndAvailability(t *testing.T) {
	tests := []struct {
		name       string
		reason     domain.RunRecoveryReason
		attempt    int
		request    ConfirmNodeRetryRequest
		wantErr    error
		storeCalls int
	}{
		{name: "confirmed", reason: domain.RecoveryUncertainReadOnly, attempt: 1, request: ConfirmNodeRetryRequest{NodeAttempt: 1, ExpectedSequence: 4}, storeCalls: 1},
		{name: "sequence conflict", reason: domain.RecoveryUncertainReadOnly, attempt: 1, request: ConfirmNodeRetryRequest{NodeAttempt: 1, ExpectedSequence: 3}, wantErr: ErrRunRecoveryConflict},
		{name: "attempt mismatch", reason: domain.RecoveryUncertainReadOnly, attempt: 1, request: ConfirmNodeRetryRequest{NodeAttempt: 2, ExpectedSequence: 4}, wantErr: ErrRunRecoveryNodeNotFound},
		{name: "attempt exhausted", reason: domain.RecoveryAttemptLimit, attempt: 3, request: ConfirmNodeRetryRequest{NodeAttempt: 3, ExpectedSequence: 4}, wantErr: ErrRunRecoveryRetryExhausted},
		{name: "payload unavailable", reason: domain.RecoveryPayloadUnavailable, attempt: 1, request: ConfirmNodeRetryRequest{NodeAttempt: 1, ExpectedSequence: 4}, wantErr: ErrRunRecoveryPayloadUnavailable},
		{name: "legacy unavailable", reason: domain.RecoveryLegacyActive, attempt: 1, request: ConfirmNodeRetryRequest{NodeAttempt: 1, ExpectedSequence: 4}, wantErr: ErrRunRecoveryRetryUnavailable},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := recoveryStoreFixture(test.reason, test.attempt)
			service := NewRunRecoveryService(store, recoveryCompiler{plan: recoveryPlan(agentnode.ExecutionSafetyReadOnly)})
			_, err := service.ConfirmNodeRetry(context.Background(), testRunID, "work", test.request)
			if !errors.Is(err, test.wantErr) || store.confirmCalls != test.storeCalls {
				t.Fatalf("error=%v calls=%d", err, store.confirmCalls)
			}
		})
	}
}

func TestRunRecoveryViewRejectsNonRecoveryRun(t *testing.T) {
	store := recoveryStoreFixture(domain.RecoveryUncertainReadOnly, 1)
	store.run.Status = domain.RunRunning
	service := NewRunRecoveryService(store, recoveryCompiler{plan: recoveryPlan(agentnode.ExecutionSafetyReadOnly)})
	if _, err := service.Get(context.Background(), testRunID); !errors.Is(err, ErrRunRecoveryNotRequired) {
		t.Fatalf("error=%v", err)
	}
}

func TestRunRecoveryTerminateDelegatesExpectedSequence(t *testing.T) {
	store := recoveryStoreFixture(domain.RecoveryHistoryInvalid, 0)
	service := NewRunRecoveryService(store, recoveryCompiler{plan: recoveryPlan(agentnode.ExecutionSafetyPure)})
	summary, err := service.Terminate(context.Background(), testRunID, TerminateRecoveryRequest{ExpectedSequence: 4})
	if err != nil || summary.Status != domain.RunCancelled || store.terminateSequence != 4 {
		t.Fatalf("summary=%+v sequence=%d error=%v", summary, store.terminateSequence, err)
	}
}

func TestRunRecoveryViewAcceptsPriorNodeConfirmationAndExposesOnlyUnresolvedNodes(t *testing.T) {
	store := recoveryStoreFixture(domain.RecoveryUncertainEffect, 1)
	now := time.Now().UTC()
	secondAttempt := 1
	store.events = []domain.RunEvent{
		{RunID: testRunID, Sequence: 1, Type: "run.queued", Timestamp: now},
		{RunID: testRunID, Sequence: 2, Type: "node.started", NodeID: "first", NodeAttempt: &secondAttempt, Timestamp: now},
		{RunID: testRunID, Sequence: 3, Type: "node.started", NodeID: "work", NodeAttempt: &secondAttempt, Timestamp: now},
		{RunID: testRunID, Sequence: 4, Type: "run.recovery_required", Timestamp: now},
		{RunID: testRunID, Sequence: 5, Type: "node.retry_confirmed", NodeID: "first", NodeAttempt: &secondAttempt, Timestamp: now},
	}
	service := NewRunRecoveryService(store, recoveryCompiler{plan: recoveryPlan(agentnode.ExecutionSafetySideEffect)})
	view, err := service.Get(context.Background(), testRunID)
	if err != nil || view.Sequence != 5 || len(view.Nodes) != 1 || view.Nodes[0].NodeID != "work" {
		t.Fatalf("view=%+v error=%v", view, err)
	}
}

func TestRunRecoveryIgnoresAutomaticallyRetryablePureNodeWhenQueuingFinalConfirmation(t *testing.T) {
	store := recoveryStoreFixture(domain.RecoveryUncertainEffect, 1)
	now := time.Now().UTC()
	attempt := 1
	store.events = []domain.RunEvent{
		{RunID: testRunID, Sequence: 1, Type: "run.queued", Timestamp: now},
		{RunID: testRunID, Sequence: 2, Type: "run.started", Timestamp: now},
		{RunID: testRunID, Sequence: 3, Type: "node.started", NodeID: "pure", NodeAttempt: &attempt, Timestamp: now},
		{RunID: testRunID, Sequence: 4, Type: "node.started", NodeID: "work", NodeAttempt: &attempt, Timestamp: now},
		{RunID: testRunID, Sequence: 5, Type: "run.recovery_required", Timestamp: now},
	}
	plan := &engine.Plan{Nodes: map[string]engine.CompiledNode{
		"pure": {ExecutionSafety: agentnode.ExecutionSafetyPure},
		"work": {ExecutionSafety: agentnode.ExecutionSafetySideEffect},
	}}
	service := NewRunRecoveryService(store, recoveryCompiler{plan: plan})
	view, err := service.Get(context.Background(), testRunID)
	if err != nil || len(view.Nodes) != 1 || view.Nodes[0].NodeID != "work" {
		t.Fatalf("view=%+v error=%v", view, err)
	}
	if _, err := service.ConfirmNodeRetry(context.Background(), testRunID, "work", ConfirmNodeRetryRequest{NodeAttempt: 1, ExpectedSequence: 5}); err != nil || !store.confirmFinal {
		t.Fatalf("final=%v error=%v", store.confirmFinal, err)
	}
}

type recoveryStoreFake struct {
	run               domain.Run
	events            []domain.RunEvent
	confirmCalls      int
	confirmFinal      bool
	terminateSequence int64
}

func recoveryStoreFixture(reason domain.RunRecoveryReason, attempt int) *recoveryStoreFake {
	now := time.Now().UTC()
	events := []domain.RunEvent{
		{RunID: testRunID, Sequence: 1, Type: "run.queued", Timestamp: now},
		{RunID: testRunID, Sequence: 2, Type: "run.started", Timestamp: now},
	}
	if attempt > 0 {
		value := attempt
		events = append(events, domain.RunEvent{RunID: testRunID, Sequence: 3, Type: "node.started", NodeID: "work", NodeAttempt: &value, Status: domain.NodeRunning, Timestamp: now})
	}
	events = append(events, domain.RunEvent{RunID: testRunID, Sequence: int64(len(events) + 1), Type: "run.recovery_required", Timestamp: now})
	return &recoveryStoreFake{
		run:    domain.Run{ID: testRunID, WorkflowID: testWorkflowID, Mode: domain.RunModeTest, Status: domain.RunRecoveryRequired, RecoveryReason: reason, RecoveryRequestedAt: &now, GraphSnapshot: []byte(`{}`)},
		events: events,
	}
}

func (store *recoveryStoreFake) GetRun(context.Context, string) (domain.Run, []domain.NodeRun, error) {
	return store.run, nil, nil
}

func (store *recoveryStoreFake) ListRunEvents(_ context.Context, _ string, after int64, limit int) ([]domain.RunEvent, error) {
	result := make([]domain.RunEvent, 0, limit)
	for _, event := range store.events {
		if event.Sequence > after && len(result) < limit {
			result = append(result, event)
		}
	}
	return result, nil
}

func (store *recoveryStoreFake) GetWorkflow(context.Context, string) (domain.Workflow, error) {
	return domain.Workflow{}, nil
}

func (store *recoveryStoreFake) GetAgentVersion(context.Context, string, string) (domain.Workflow, domain.WorkflowVersion, error) {
	return domain.Workflow{}, domain.WorkflowVersion{}, nil
}

func (store *recoveryStoreFake) ConfirmRunNodeRetry(_ context.Context, _, _ string, _ int, _ int64, final bool) (domain.RunSummary, error) {
	store.confirmCalls++
	store.confirmFinal = final
	return domain.RunSummary{ID: testRunID, Status: domain.RunQueued}, nil
}

func (store *recoveryStoreFake) TerminateRunRecovery(_ context.Context, _ string, expectedSequence int64) (domain.RunSummary, error) {
	store.terminateSequence = expectedSequence
	return domain.RunSummary{ID: testRunID, Status: domain.RunCancelled}, nil
}

type recoveryCompiler struct{ plan *engine.Plan }

func (compiler recoveryCompiler) Compile(domain.Graph) (*engine.Plan, []domain.ValidationIssue) {
	return compiler.plan, nil
}

func recoveryPlan(safety agentnode.ExecutionSafety) *engine.Plan {
	return &engine.Plan{Nodes: map[string]engine.CompiledNode{"work": {ExecutionSafety: safety}}}
}
