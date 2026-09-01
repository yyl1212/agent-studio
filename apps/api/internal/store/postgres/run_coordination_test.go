package postgres

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/yyl1212/agent-studio/apps/api/internal/domain"
	workflowservice "github.com/yyl1212/agent-studio/apps/api/internal/workflow"
)

func TestRequestRunCancelIsIdempotentAndRejectsTerminalRuns(t *testing.T) {
	store := migratedTestStore(t)
	workflow := createWorkflowFixture(t, store, "request-cancel")
	running := newTestRun(workflow.ID, workflow.DraftRevision, workflow.DraftGraph)
	if err := store.CreateRun(context.Background(), running); err != nil {
		t.Fatal(err)
	}
	first, err := store.RequestRunCancel(context.Background(), running.ID)
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.RequestRunCancel(context.Background(), running.ID)
	if err != nil {
		t.Fatal(err)
	}
	if first.Status != domain.RunCancelling || first.CancelRequestedAt == nil || second.CancelRequestedAt == nil || !second.CancelRequestedAt.Equal(*first.CancelRequestedAt) {
		t.Fatalf("first=%+v second=%+v", first, second)
	}
	for _, status := range []domain.RunStatus{domain.RunCompleted, domain.RunFailed, domain.RunCancelled} {
		run := newTestRun(workflow.ID, workflow.DraftRevision, workflow.DraftGraph)
		if err := store.CreateRun(context.Background(), run); err != nil {
			t.Fatal(err)
		}
		if _, err := store.pool.Exec(context.Background(), "UPDATE runs SET status=$2,ended_at=clock_timestamp() WHERE id=$1", run.ID, status); err != nil {
			t.Fatal(err)
		}
		if _, err := store.RequestRunCancel(context.Background(), run.ID); !errors.Is(err, workflowservice.ErrRunNotCancellable) {
			t.Fatalf("status=%s error=%v", status, err)
		}
	}
}

func TestRequestRunCancelFinalizesQueuedDurableRun(t *testing.T) {
	store := migratedTestStore(t)
	workflow := createWorkflowFixture(t, store, "cancel-queued")
	submission := durableSubmissionFixture(workflow)
	if err := store.SubmitRun(context.Background(), submission); err != nil {
		t.Fatal(err)
	}
	summary, err := store.RequestRunCancel(context.Background(), submission.Run.ID)
	if err != nil {
		t.Fatal(err)
	}
	events, eventErr := store.ListRunEvents(context.Background(), submission.Run.ID, 0, 10)
	if summary.Status != domain.RunCancelled || summary.EndedAt == nil || summary.CancelRequestedAt == nil || eventErr != nil || len(events) != 2 || events[1].Type != "run.cancelled" {
		t.Fatalf("summary=%+v events=%+v error=%v", summary, events, eventErr)
	}
	if _, claimed, err := store.ClaimRun(context.Background(), "worker", time.Minute); err != nil || claimed {
		t.Fatalf("cancelled queued run claimed=%v error=%v", claimed, err)
	}
}

func TestRequestRunCancelFinalizesRecoveryRequiredRun(t *testing.T) {
	store := migratedTestStore(t)
	workflowRecord := createWorkflowFixture(t, store, "cancel-recovery")
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
		domain.RecoveryUncertainEffect, recoveryAt, domain.RunEventBudget{MaxEvents: 16, MaxTotalDataBytes: 1 << 20}); err != nil {
		t.Fatal(err)
	}
	summary, err := store.RequestRunCancel(context.Background(), submission.Run.ID)
	if err != nil || summary.Status != domain.RunCancelled || summary.EndedAt == nil {
		t.Fatalf("summary=%+v error=%v", summary, err)
	}
	events, err := store.ListRunEvents(context.Background(), submission.Run.ID, 0, 10)
	if err != nil || len(events) != 3 || events[2].Type != "run.cancelled" {
		t.Fatalf("events=%+v error=%v", events, err)
	}
}

func TestRequestRunCancelRacesLinearlyWithFinalize(t *testing.T) {
	store := migratedTestStore(t)
	workflow := createWorkflowFixture(t, store, "cancel-race")
	for iteration := 0; iteration < 10; iteration++ {
		run := newTestRun(workflow.ID, workflow.DraftRevision, workflow.DraftGraph)
		if err := store.CreateRun(context.Background(), run); err != nil {
			t.Fatal(err)
		}
		start := make(chan struct{})
		var wait sync.WaitGroup
		wait.Add(2)
		var cancelErr, finalizeErr error
		go func() {
			defer wait.Done()
			<-start
			_, cancelErr = store.RequestRunCancel(context.Background(), run.ID)
		}()
		go func() {
			defer wait.Done()
			<-start
			now := time.Now().UTC()
			_, finalizeErr = store.FinalizeRun(context.Background(), workflowservice.RunFinalization{
				RunID: run.ID, Status: domain.RunCompleted, EndedAt: now,
				TerminalEvent: domain.RunEvent{RunID: run.ID, Sequence: 1, Type: "run.completed", Timestamp: now},
				Budget:        domain.RunEventBudget{MaxEvents: 2, MaxTotalDataBytes: 16 << 20},
			})
		}()
		close(start)
		wait.Wait()
		if finalizeErr != nil || (cancelErr != nil && !errors.Is(cancelErr, workflowservice.ErrRunNotCancellable)) {
			t.Fatalf("cancel=%v finalize=%v", cancelErr, finalizeErr)
		}
		loaded, _, err := store.GetRun(context.Background(), run.ID)
		if err != nil || (loaded.Status != domain.RunCompleted && loaded.Status != domain.RunCancelled) {
			t.Fatalf("run=%+v error=%v", loaded, err)
		}
	}
}
