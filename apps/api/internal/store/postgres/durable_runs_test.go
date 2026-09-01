package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/yyl1212/agent-studio/apps/api/internal/domain"
	workflowservice "github.com/yyl1212/agent-studio/apps/api/internal/workflow"
	"github.com/yyl1212/agent-studio/internal/database"
)

func TestDurableRunClaimStopsDuringExclusiveMaintenance(t *testing.T) {
	store := migratedTestStore(t)
	workflow := createWorkflowFixture(t, store, "durable-maintenance")
	submission := durableSubmissionFixture(workflow)
	if err := store.SubmitRun(context.Background(), submission); err != nil {
		t.Fatal(err)
	}
	lease, err := database.TryExclusive(context.Background(), store.pool)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Release(context.Background())
	if _, claimed, err := store.ClaimRun(context.Background(), "worker", time.Minute); !errors.Is(err, database.ErrMaintenanceBusy) || claimed {
		t.Fatalf("claimed=%v error=%v", claimed, err)
	}
	if err := lease.Release(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, claimed, err := store.ClaimRun(context.Background(), "worker", time.Minute); err != nil || !claimed {
		t.Fatalf("claim after maintenance release=%v error=%v", claimed, err)
	}
}

func TestDurableRunSubmissionIsAtomic(t *testing.T) {
	store := migratedTestStore(t)
	workflow := createWorkflowFixture(t, store, "durable-submit")
	submission := durableSubmissionFixture(workflow)
	if err := store.SubmitRun(context.Background(), submission); err != nil {
		t.Fatal(err)
	}

	var status string
	var protocol int16
	if err := store.pool.QueryRow(context.Background(), `SELECT status,execution_protocol FROM runs WHERE id=$1`, submission.Run.ID).Scan(&status, &protocol); err != nil {
		t.Fatal(err)
	}
	if status != "queued" || protocol != domain.CurrentExecutionProtocol {
		t.Fatalf("submitted run status=%q protocol=%d", status, protocol)
	}
	var eventType, payloadKind string
	if err := store.pool.QueryRow(context.Background(), `SELECT type FROM run_events WHERE run_id=$1 AND sequence=1`, submission.Run.ID).Scan(&eventType); err != nil {
		t.Fatal(err)
	}
	if err := store.pool.QueryRow(context.Background(), `SELECT kind FROM run_payloads WHERE run_id=$1 AND sequence=0`, submission.Run.ID).Scan(&payloadKind); err != nil {
		t.Fatal(err)
	}
	if eventType != "run.queued" || payloadKind != "run_input" {
		t.Fatalf("event=%q payload=%q", eventType, payloadKind)
	}

	invalid := durableSubmissionFixture(workflow)
	invalid.Run.ID = fixtureUUID()
	invalid.QueuedEvent.RunID = invalid.Run.ID
	invalid.InputPayload.RunID = invalid.Run.ID
	invalid.InputPayload.Ciphertext = nil
	if err := store.SubmitRun(context.Background(), invalid); err == nil {
		t.Fatal("SubmitRun() accepted empty encrypted payload")
	}
	var count int
	if err := store.pool.QueryRow(context.Background(), `SELECT count(*) FROM runs WHERE id=$1`, invalid.Run.ID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("failed submission left %d run rows", count)
	}

	oversized := durableSubmissionFixture(workflow)
	oversized.QueuedEvent.DataBytes = 32 << 20
	if err := store.SubmitRun(context.Background(), oversized); !errors.Is(err, domain.ErrRunEventBudgetExceeded) {
		t.Fatalf("oversized submission error=%v", err)
	}
}

func TestDurableRunSubmissionReturnsExistingIdempotentAgentRun(t *testing.T) {
	store := migratedTestStore(t)
	workflow := createWorkflowFixture(t, store, "durable-idempotent")
	version := publishFixture(t, store, workflow)
	key := fixtureUUID()
	first := durableSubmissionFixture(workflow)
	first.Run.Mode = domain.RunModePublished
	first.Run.DraftRevision = nil
	first.Run.GraphSnapshot = nil
	first.Run.WorkflowVersionID = &version.ID
	first.Run.AgentRequestKey = &key
	if err := store.SubmitRun(context.Background(), first); err != nil {
		t.Fatal(err)
	}
	duplicate := durableSubmissionFixture(workflow)
	duplicate.Run.Mode = domain.RunModePublished
	duplicate.Run.DraftRevision = nil
	duplicate.Run.GraphSnapshot = nil
	duplicate.Run.WorkflowVersionID = &version.ID
	duplicate.Run.AgentRequestKey = &key
	err := store.SubmitRun(context.Background(), duplicate)
	var existing *workflowservice.RunAlreadySubmittedError
	if !errors.As(err, &existing) || existing.Run.ID != first.Run.ID {
		t.Fatalf("duplicate error=%v existing=%+v", err, existing)
	}
	var runs, events, payloads int
	if err := store.pool.QueryRow(context.Background(), `SELECT count(*) FROM runs WHERE workflow_id=$1 AND agent_request_key=$2`, workflow.ID, key).Scan(&runs); err != nil {
		t.Fatal(err)
	}
	if err := store.pool.QueryRow(context.Background(), `SELECT count(*) FROM run_events WHERE run_id=$1`, first.Run.ID).Scan(&events); err != nil {
		t.Fatal(err)
	}
	if err := store.pool.QueryRow(context.Background(), `SELECT count(*) FROM run_payloads WHERE run_id=$1`, first.Run.ID).Scan(&payloads); err != nil {
		t.Fatal(err)
	}
	if runs != 1 || events != 1 || payloads != 1 {
		t.Fatalf("runs=%d events=%d payloads=%d", runs, events, payloads)
	}
}

func TestRunQueueStatsReportsDepthAndOldestAge(t *testing.T) {
	store := migratedTestStore(t)
	workflow := createWorkflowFixture(t, store, "queue-stats")
	submission := durableSubmissionFixture(workflow)
	submission.Run.StartedAt = time.Now().UTC().Add(-2 * time.Second)
	submission.QueuedEvent.Timestamp = submission.Run.StartedAt
	if err := store.SubmitRun(context.Background(), submission); err != nil {
		t.Fatal(err)
	}
	depth, oldest, err := store.RunQueueStats(context.Background())
	if err != nil || depth != 1 || oldest < time.Second {
		t.Fatalf("depth=%d oldest=%s error=%v", depth, oldest, err)
	}
	if _, claimed, err := store.ClaimRun(context.Background(), "worker", time.Minute); err != nil || !claimed {
		t.Fatalf("claimed=%v error=%v", claimed, err)
	}
	depth, oldest, err = store.RunQueueStats(context.Background())
	if err != nil || depth != 0 || oldest != 0 {
		t.Fatalf("after claim depth=%d oldest=%s error=%v", depth, oldest, err)
	}
}

func TestDurableRunLeaseFencesAllWrites(t *testing.T) {
	store := migratedTestStore(t)
	workflow := createWorkflowFixture(t, store, "durable-fencing")
	submission := durableSubmissionFixture(workflow)
	if err := store.SubmitRun(context.Background(), submission); err != nil {
		t.Fatal(err)
	}

	first, claimed, err := store.ClaimRun(context.Background(), "worker-1", time.Minute)
	if err != nil || !claimed {
		t.Fatalf("first claim=%+v claimed=%v error=%v", first, claimed, err)
	}
	if first.Run.Status != domain.RunRunning || first.Lease.Token < 1 {
		t.Fatalf("first claim=%+v", first)
	}
	if _, claimed, err := store.ClaimRun(context.Background(), "worker-2", time.Minute); err != nil || claimed {
		t.Fatalf("second live claim claimed=%v error=%v", claimed, err)
	}
	heartbeat, err := store.RenewRunLease(context.Background(), first.Lease, time.Minute)
	if err != nil || heartbeat.CancelRequested || heartbeat.Lease.Token != first.Lease.Token {
		t.Fatalf("heartbeat=%+v error=%v", heartbeat, err)
	}
	if _, err := store.pool.Exec(context.Background(), `UPDATE runs SET lease_expires_at=clock_timestamp()-interval '1 second' WHERE id=$1`, submission.Run.ID); err != nil {
		t.Fatal(err)
	}
	second, claimed, err := store.ClaimRun(context.Background(), "worker-2", time.Minute)
	if err != nil || !claimed || second.Lease.Token <= first.Lease.Token {
		t.Fatalf("takeover=%+v claimed=%v error=%v", second, claimed, err)
	}
	if _, err := store.RenewRunLease(context.Background(), first.Lease, time.Minute); !errors.Is(err, domain.ErrRunLeaseLost) {
		t.Fatalf("stale renewal error=%v; want ErrRunLeaseLost", err)
	}

	attempt := 1
	nodeEvent := domain.RunEvent{
		RunID: submission.Run.ID, Sequence: 2, Type: "node.started", NodeID: "node-1", NodeAttempt: &attempt,
		Status: domain.NodeRunning, Timestamp: time.Now().UTC(),
	}
	nodeRun := domain.NodeRun{
		ID: fixtureUUID(), RunID: submission.Run.ID, NodeID: "node-1", NodeType: "echo", Attempt: 1,
		Status: domain.NodeRunning,
	}
	nodePayload := domain.RunPayload{
		RunID: submission.Run.ID, Sequence: 2, Kind: domain.RunPayloadNodeInput, NodeID: "node-1", NodeAttempt: 1,
		ExecutionProtocol: domain.CurrentExecutionProtocol, CipherVersion: 1, Ciphertext: []byte{2},
	}
	budget := domain.RunEventBudget{MaxEvents: 20, MaxTotalDataBytes: 1 << 20}
	if err := store.PersistLeasedRunEvent(context.Background(), first.Lease, nodeEvent, &nodeRun, []domain.RunPayload{nodePayload}, budget); !errors.Is(err, domain.ErrRunLeaseLost) {
		t.Fatalf("stale event write error=%v; want ErrRunLeaseLost", err)
	}
	staleFinalization := workflowservice.RunFinalization{
		RunID: submission.Run.ID, Status: domain.RunCompleted, EndedAt: time.Now().UTC(),
		TerminalEvent: domain.RunEvent{RunID: submission.Run.ID, Sequence: 2, Type: "run.completed", Timestamp: time.Now().UTC()},
		Budget:        budget,
	}
	if _, err := store.FinalizeLeasedRun(context.Background(), first.Lease, staleFinalization, nil); !errors.Is(err, domain.ErrRunLeaseLost) {
		t.Fatalf("stale finalization error=%v; want ErrRunLeaseLost", err)
	}

	invalidPayload := nodePayload
	invalidPayload.Ciphertext = nil
	if err := store.PersistLeasedRunEvent(context.Background(), second.Lease, nodeEvent, &nodeRun, []domain.RunPayload{invalidPayload}, budget); err == nil {
		t.Fatal("leased event accepted invalid payload")
	}
	var eventCount, nodeCount int
	if err := store.pool.QueryRow(context.Background(), `SELECT count(*) FROM run_events WHERE run_id=$1 AND sequence=2`, submission.Run.ID).Scan(&eventCount); err != nil {
		t.Fatal(err)
	}
	if err := store.pool.QueryRow(context.Background(), `SELECT count(*) FROM node_runs WHERE run_id=$1`, submission.Run.ID).Scan(&nodeCount); err != nil {
		t.Fatal(err)
	}
	if eventCount != 0 || nodeCount != 0 {
		t.Fatalf("rolled back event count=%d node count=%d", eventCount, nodeCount)
	}

	if err := store.PersistLeasedRunEvent(context.Background(), second.Lease, nodeEvent, &nodeRun, []domain.RunPayload{nodePayload}, budget); err != nil {
		t.Fatal(err)
	}
	loadedRun, events, payloads, err := store.LoadRunExecution(context.Background(), submission.Run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loadedRun.LeaseToken != second.Lease.Token || len(events) != 2 || events[1].NodeAttempt == nil || *events[1].NodeAttempt != 1 || len(payloads) != 2 || payloads[0].Kind != domain.RunPayloadInput || payloads[1].Kind != domain.RunPayloadNodeInput {
		t.Fatalf("loaded run=%+v events=%+v payloads=%+v", loadedRun, events, payloads)
	}

	finalization := workflowservice.RunFinalization{
		RunID: submission.Run.ID, Status: domain.RunCompleted, Output: map[string]any{"ok": true}, EndedAt: time.Now().UTC(),
		TerminalEvent: domain.RunEvent{RunID: submission.Run.ID, Sequence: 3, Type: "run.completed", Output: json.RawMessage(`{"ok":true}`), Timestamp: time.Now().UTC()},
		Budget:        budget,
	}
	terminal, err := store.FinalizeLeasedRun(context.Background(), second.Lease, finalization, nil)
	if err != nil || terminal.Type != "run.completed" {
		t.Fatalf("terminal=%+v error=%v", terminal, err)
	}
	var finalStatus string
	var leaseOwner *string
	if err := store.pool.QueryRow(context.Background(), `SELECT status,lease_owner FROM runs WHERE id=$1`, submission.Run.ID).Scan(&finalStatus, &leaseOwner); err != nil {
		t.Fatal(err)
	}
	if finalStatus != "completed" || leaseOwner != nil {
		t.Fatalf("final status=%q lease owner=%v", finalStatus, leaseOwner)
	}
}

func TestDurableRunClaimRejectsUnknownProtocolButAllowsLegacyCancellation(t *testing.T) {
	store := migratedTestStore(t)
	workflow := createWorkflowFixture(t, store, "durable-protocol")
	unknown := durableSubmissionFixture(workflow)
	if err := store.SubmitRun(context.Background(), unknown); err != nil {
		t.Fatal(err)
	}
	if _, err := store.pool.Exec(context.Background(), `UPDATE runs SET execution_protocol=2 WHERE id=$1`, unknown.Run.ID); err != nil {
		t.Fatal(err)
	}
	if _, claimed, err := store.ClaimRun(context.Background(), "worker-1", time.Minute); err != nil || claimed {
		t.Fatalf("unknown protocol claimed=%v error=%v", claimed, err)
	}

	legacy := newTestRun(workflow.ID, workflow.DraftRevision, workflow.DraftGraph)
	legacy.Status = domain.RunCancelling
	if err := store.CreateRun(context.Background(), legacy); err != nil {
		t.Fatal(err)
	}
	claimedRun, claimed, err := store.ClaimRun(context.Background(), "worker-1", time.Minute)
	if err != nil || !claimed || claimedRun.Run.ID != legacy.ID || claimedRun.Run.Status != domain.RunCancelling {
		t.Fatalf("legacy cancellation claim=%+v claimed=%v error=%v", claimedRun, claimed, err)
	}
}

func TestDurableRunRecoveryRequiresCurrentLeaseAndReleasesIt(t *testing.T) {
	store := migratedTestStore(t)
	workflow := createWorkflowFixture(t, store, "durable-recovery")
	submission := durableSubmissionFixture(workflow)
	if err := store.SubmitRun(context.Background(), submission); err != nil {
		t.Fatal(err)
	}
	claimedRun, claimed, err := store.ClaimRun(context.Background(), "worker-1", time.Minute)
	if err != nil || !claimed {
		t.Fatalf("claim=%+v claimed=%v error=%v", claimedRun, claimed, err)
	}
	requestedAt := time.Now().UTC()
	event := domain.RunEvent{RunID: submission.Run.ID, Sequence: 2, Type: "run.recovery_required", Timestamp: requestedAt}
	budget := domain.RunEventBudget{MaxEvents: 20, MaxTotalDataBytes: 1 << 20}
	if err := store.RequireRunRecovery(context.Background(), claimedRun.Lease, event, domain.RecoveryUncertainEffect, requestedAt, budget); err != nil {
		t.Fatal(err)
	}
	loaded, events, _, err := store.LoadRunExecution(context.Background(), submission.Run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Status != domain.RunRecoveryRequired || loaded.RecoveryReason != domain.RecoveryUncertainEffect || loaded.LeaseOwner != "" || len(events) != 2 || events[1].Type != "run.recovery_required" {
		t.Fatalf("recovery run=%+v events=%+v", loaded, events)
	}
	if _, err := store.RenewRunLease(context.Background(), claimedRun.Lease, time.Minute); !errors.Is(err, domain.ErrRunLeaseLost) {
		t.Fatalf("released recovery lease renewal error=%v", err)
	}
}

func TestRunRecoveryConfirmationIsConcurrentAndQueuesOnlyAfterLastNode(t *testing.T) {
	store := migratedTestStore(t)
	workflowRecord := createWorkflowFixture(t, store, "recovery-confirm")
	submission := durableSubmissionFixture(workflowRecord)
	if err := store.SubmitRun(context.Background(), submission); err != nil {
		t.Fatal(err)
	}
	claimed, ok, err := store.ClaimRun(context.Background(), "worker", time.Minute)
	if err != nil || !ok {
		t.Fatalf("claimed=%v error=%v", ok, err)
	}
	budget := domain.RunEventBudget{MaxEvents: 32, MaxTotalDataBytes: 1 << 20}
	for sequence, nodeID := range []string{"node-a", "node-b"} {
		attempt := 1
		event := domain.RunEvent{RunID: submission.Run.ID, Sequence: int64(sequence + 2), Type: "node.started", NodeID: nodeID, NodeAttempt: &attempt, Status: domain.NodeRunning, Timestamp: time.Now().UTC()}
		nodeRun := domain.NodeRun{ID: fixtureUUID(), RunID: submission.Run.ID, NodeID: nodeID, NodeType: "fixture", Attempt: 1, Status: domain.NodeRunning}
		if err := store.PersistLeasedRunEvent(context.Background(), claimed.Lease, event, &nodeRun, nil, budget); err != nil {
			t.Fatal(err)
		}
	}
	recoveryAt := time.Now().UTC()
	if err := store.RequireRunRecovery(context.Background(), claimed.Lease,
		domain.RunEvent{RunID: submission.Run.ID, Sequence: 4, Type: "run.recovery_required", Timestamp: recoveryAt},
		domain.RecoveryUncertainEffect, recoveryAt, budget); err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	results := make(chan error, 2)
	for range 2 {
		go func() {
			<-start
			_, err := store.ConfirmRunNodeRetry(context.Background(), submission.Run.ID, "node-a", 1, 4, false)
			results <- err
		}()
	}
	close(start)
	firstErr, secondErr := <-results, <-results
	if (firstErr == nil) == (secondErr == nil) {
		t.Fatalf("confirmation errors=%v / %v", firstErr, secondErr)
	}
	conflict := firstErr
	if conflict == nil {
		conflict = secondErr
	}
	if !errors.Is(conflict, workflowservice.ErrRunRecoveryConflict) {
		t.Fatalf("conflict error=%v", conflict)
	}
	afterFirst, _, err := store.GetRun(context.Background(), submission.Run.ID)
	if err != nil || afterFirst.Status != domain.RunRecoveryRequired {
		t.Fatalf("after first=%+v error=%v", afterFirst, err)
	}
	afterLast, err := store.ConfirmRunNodeRetry(context.Background(), submission.Run.ID, "node-b", 1, 5, true)
	if err != nil || afterLast.Status != domain.RunQueued {
		t.Fatalf("after last=%+v error=%v", afterLast, err)
	}
	events, err := store.ListRunEvents(context.Background(), submission.Run.ID, 0, 10)
	if err != nil || len(events) != 7 || events[4].Type != "node.retry_confirmed" || events[5].Type != "node.retry_confirmed" || events[6].Type != "run.queued" {
		t.Fatalf("events=%+v error=%v", events, err)
	}
}

func TestRunRecoveryTerminateUsesExpectedSequenceAndStableError(t *testing.T) {
	store := migratedTestStore(t)
	workflowRecord := createWorkflowFixture(t, store, "recovery-terminate")
	submission := durableSubmissionFixture(workflowRecord)
	if err := store.SubmitRun(context.Background(), submission); err != nil {
		t.Fatal(err)
	}
	claimed, ok, err := store.ClaimRun(context.Background(), "worker", time.Minute)
	if err != nil || !ok {
		t.Fatalf("claimed=%v error=%v", ok, err)
	}
	recoveryAt := time.Now().UTC()
	if err := store.RequireRunRecovery(context.Background(), claimed.Lease,
		domain.RunEvent{RunID: submission.Run.ID, Sequence: 2, Type: "run.recovery_required", Timestamp: recoveryAt},
		domain.RecoveryPayloadUnavailable, recoveryAt, domain.RunEventBudget{MaxEvents: 16, MaxTotalDataBytes: 1 << 20}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.TerminateRunRecovery(context.Background(), submission.Run.ID, 1); !errors.Is(err, workflowservice.ErrRunRecoveryConflict) {
		t.Fatalf("stale terminate error=%v", err)
	}
	summary, err := store.TerminateRunRecovery(context.Background(), submission.Run.ID, 2)
	if err != nil || summary.Status != domain.RunCancelled || summary.EndedAt == nil {
		t.Fatalf("summary=%+v error=%v", summary, err)
	}
	run, _, err := store.GetRun(context.Background(), submission.Run.ID)
	if err != nil || run.Error == nil || run.Error.Code != "RUN_RECOVERY_TERMINATED" {
		t.Fatalf("run=%+v error=%v", run, err)
	}
	if _, err := store.TerminateRunRecovery(context.Background(), submission.Run.ID, 2); !errors.Is(err, workflowservice.ErrRunRecoveryConflict) {
		t.Fatalf("repeated stale terminate error=%v", err)
	}
	if _, err := store.TerminateRunRecovery(context.Background(), submission.Run.ID, 3); !errors.Is(err, workflowservice.ErrRunRecoveryNotRequired) {
		t.Fatalf("repeated current terminate error=%v", err)
	}
}

func TestDurableRunBudgetIncludesExistingAndNewPrivatePayloads(t *testing.T) {
	store, submission, lease := claimedDurableRunFixture(t, "combined-budget")
	// The submitted run already contains one byte of encrypted run input.
	attempt := 1
	event := domain.RunEvent{
		RunID: submission.Run.ID, Sequence: 2, Type: "node.started", NodeID: "node-1", NodeAttempt: &attempt,
		Status: domain.NodeRunning, DataBytes: 1, Timestamp: time.Now().UTC(),
	}
	payload := domain.RunPayload{
		RunID: submission.Run.ID, Sequence: 2, Kind: domain.RunPayloadNodeInput, NodeID: "node-1", NodeAttempt: 1,
		ExecutionProtocol: domain.CurrentExecutionProtocol, CipherVersion: 1, Ciphertext: []byte{2, 3},
	}
	budget := domain.RunEventBudget{MaxEvents: 10, MaxTotalDataBytes: 3}
	err := store.PersistLeasedRunEvent(context.Background(), lease, event, nil, []domain.RunPayload{payload}, budget)
	if !errors.Is(err, domain.ErrRunEventBudgetExceeded) {
		t.Fatalf("combined budget error=%v; want ErrRunEventBudgetExceeded", err)
	}
	var eventCount, payloadCount int
	if err := store.pool.QueryRow(context.Background(), `SELECT count(*) FROM run_events WHERE run_id=$1`, submission.Run.ID).Scan(&eventCount); err != nil {
		t.Fatal(err)
	}
	if err := store.pool.QueryRow(context.Background(), `SELECT count(*) FROM run_payloads WHERE run_id=$1`, submission.Run.ID).Scan(&payloadCount); err != nil {
		t.Fatal(err)
	}
	if eventCount != 1 || payloadCount != 1 {
		t.Fatalf("budget rejection was not atomic: events=%d payloads=%d", eventCount, payloadCount)
	}
}

func TestDurableRunRejectsUnfencedAndCrossRunWrites(t *testing.T) {
	t.Run("legacy event writer", func(t *testing.T) {
		store, submission, lease := claimedDurableRunFixture(t, "legacy-event")
		event := domain.RunEvent{RunID: submission.Run.ID, Sequence: 2, Type: "run.completed", Timestamp: time.Now().UTC()}
		err := store.PersistRunEvent(context.Background(), event, nil, domain.RunEventBudget{MaxEvents: 10, MaxTotalDataBytes: 1 << 20})
		if !errors.Is(err, domain.ErrRunLeaseLost) {
			t.Fatalf("unfenced PersistRunEvent() error=%v; want ErrRunLeaseLost (lease=%+v)", err, lease)
		}
	})

	t.Run("legacy finalizer", func(t *testing.T) {
		store, submission, _ := claimedDurableRunFixture(t, "legacy-finalize")
		now := time.Now().UTC()
		_, err := store.FinalizeRun(context.Background(), workflowservice.RunFinalization{
			RunID: submission.Run.ID, Status: domain.RunCompleted, EndedAt: now,
			TerminalEvent: domain.RunEvent{RunID: submission.Run.ID, Sequence: 2, Type: "run.completed", Timestamp: now},
			Budget:        domain.RunEventBudget{MaxEvents: 10, MaxTotalDataBytes: 1 << 20},
		})
		if !errors.Is(err, domain.ErrRunLeaseLost) {
			t.Fatalf("unfenced FinalizeRun() error=%v; want ErrRunLeaseLost", err)
		}
	})

	t.Run("cross run event", func(t *testing.T) {
		store, first, lease := claimedDurableRunFixture(t, "cross-event")
		workflow, err := store.GetWorkflow(context.Background(), first.Run.WorkflowID)
		if err != nil {
			t.Fatal(err)
		}
		second := durableSubmissionFixture(workflow)
		second.Run.StartedAt = first.Run.StartedAt.Add(time.Minute)
		second.QueuedEvent.Timestamp = second.Run.StartedAt
		if err := store.SubmitRun(context.Background(), second); err != nil {
			t.Fatal(err)
		}
		event := domain.RunEvent{RunID: second.Run.ID, Sequence: 2, Type: "run.completed", Timestamp: time.Now().UTC()}
		err = store.PersistLeasedRunEvent(context.Background(), lease, event, nil, nil, domain.RunEventBudget{MaxEvents: 10, MaxTotalDataBytes: 1 << 20})
		if !errors.Is(err, domain.ErrRunLeaseLost) {
			t.Fatalf("cross-run event error=%v; want ErrRunLeaseLost", err)
		}
	})

	t.Run("cross run payload", func(t *testing.T) {
		store, first, lease := claimedDurableRunFixture(t, "cross-payload")
		workflow, err := store.GetWorkflow(context.Background(), first.Run.WorkflowID)
		if err != nil {
			t.Fatal(err)
		}
		second := durableSubmissionFixture(workflow)
		second.Run.StartedAt = first.Run.StartedAt.Add(time.Minute)
		second.QueuedEvent.Timestamp = second.Run.StartedAt
		if err := store.SubmitRun(context.Background(), second); err != nil {
			t.Fatal(err)
		}
		attempt := 1
		event := domain.RunEvent{RunID: first.Run.ID, Sequence: 2, Type: "node.started", NodeID: "node-1", NodeAttempt: &attempt, Timestamp: time.Now().UTC()}
		payload := domain.RunPayload{
			RunID: second.Run.ID, Sequence: 2, Kind: domain.RunPayloadNodeInput, NodeID: "node-1", NodeAttempt: 1,
			ExecutionProtocol: domain.CurrentExecutionProtocol, CipherVersion: 1, Ciphertext: []byte{1},
		}
		err = store.PersistLeasedRunEvent(context.Background(), lease, event, nil, []domain.RunPayload{payload}, domain.RunEventBudget{MaxEvents: 10, MaxTotalDataBytes: 1 << 20})
		if !errors.Is(err, domain.ErrRunLeaseLost) {
			t.Fatalf("cross-run payload error=%v; want ErrRunLeaseLost", err)
		}
	})
}

func claimedDurableRunFixture(t *testing.T, suffix string) (*Store, workflowservice.RunSubmission, domain.RunLease) {
	t.Helper()
	store := migratedTestStore(t)
	workflow := createWorkflowFixture(t, store, suffix)
	submission := durableSubmissionFixture(workflow)
	if err := store.SubmitRun(context.Background(), submission); err != nil {
		t.Fatal(err)
	}
	claimedRun, claimed, err := store.ClaimRun(context.Background(), "worker-1", time.Minute)
	if err != nil || !claimed {
		t.Fatalf("claim=%+v claimed=%v error=%v", claimedRun, claimed, err)
	}
	return store, submission, claimedRun.Lease
}

func durableSubmissionFixture(workflow domain.Workflow) workflowservice.RunSubmission {
	runID := fixtureUUID()
	startedAt := time.Now().UTC()
	return workflowservice.RunSubmission{
		Run: domain.Run{
			ID: runID, WorkflowID: workflow.ID, DraftRevision: &workflow.DraftRevision, GraphSnapshot: workflow.DraftGraph,
			Mode: domain.RunModeTest, Status: domain.RunQueued, ExecutionProtocol: domain.CurrentExecutionProtocol,
			Input: json.RawMessage(`{"token":"[REDACTED]"}`), StartedAt: startedAt,
		},
		QueuedEvent: domain.RunEvent{RunID: runID, Sequence: 1, Type: "run.queued", Timestamp: startedAt},
		InputPayload: domain.RunPayload{
			RunID: runID, Sequence: 0, Kind: domain.RunPayloadInput, ExecutionProtocol: domain.CurrentExecutionProtocol,
			CipherVersion: 1, Ciphertext: []byte{1}, CreatedAt: startedAt,
		},
	}
}
