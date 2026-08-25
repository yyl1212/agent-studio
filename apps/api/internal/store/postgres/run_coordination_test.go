package postgres

import (
	"context"
	"encoding/json"
	"sort"
	"testing"
	"time"

	"github.com/yyl1212/agent-studio/apps/api/internal/domain"
)

func TestHeartbeatRunsUpdatesOnlyActiveRunsAndReturnsCancellationRequests(t *testing.T) {
	store := migratedTestStore(t)
	workflow := createWorkflowFixture(t, store, "heartbeat")
	running := newTestRun(workflow.ID, workflow.DraftRevision, workflow.DraftGraph)
	cancelling := newTestRun(workflow.ID, workflow.DraftRevision, workflow.DraftGraph)
	completed := newTestRun(workflow.ID, workflow.DraftRevision, workflow.DraftGraph)
	for _, run := range []domain.Run{running, cancelling, completed} {
		if err := store.CreateRun(context.Background(), run); err != nil {
			t.Fatal(err)
		}
	}
	marker := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	if _, err := store.pool.Exec(context.Background(), `UPDATE runs SET heartbeat_at=$1`, marker); err != nil {
		t.Fatal(err)
	}
	if _, err := store.pool.Exec(context.Background(), `UPDATE runs SET status='cancelling',cancel_requested_at=clock_timestamp() WHERE id=$1`, cancelling.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.pool.Exec(context.Background(), `UPDATE runs SET status='completed',ended_at=clock_timestamp() WHERE id=$1`, completed.ID); err != nil {
		t.Fatal(err)
	}
	cancelled, err := store.HeartbeatRuns(context.Background(), []string{completed.ID, running.ID, cancelling.ID})
	if err != nil {
		t.Fatal(err)
	}
	if len(cancelled) != 1 || cancelled[0] != cancelling.ID {
		t.Fatalf("cancelled IDs=%v", cancelled)
	}
	for _, test := range []struct {
		id      string
		updated bool
	}{{running.ID, true}, {cancelling.ID, true}, {completed.ID, false}} {
		var heartbeat time.Time
		if err := store.pool.QueryRow(context.Background(), "SELECT heartbeat_at FROM runs WHERE id=$1", test.id).Scan(&heartbeat); err != nil {
			t.Fatal(err)
		}
		if got := heartbeat.After(marker); got != test.updated {
			t.Fatalf("run %s heartbeat updated=%v, want %v", test.id, got, test.updated)
		}
	}
}

func TestFinalizeInterruptedRunsUsesDatabaseAgeAndAtomicFinalization(t *testing.T) {
	store := migratedTestStore(t)
	workflow := createWorkflowFixture(t, store, "interrupted")
	stale := newTestRun(workflow.ID, workflow.DraftRevision, workflow.DraftGraph)
	fresh := newTestRun(workflow.ID, workflow.DraftRevision, workflow.DraftGraph)
	heartbeating := newTestRun(workflow.ID, workflow.DraftRevision, workflow.DraftGraph)
	for _, run := range []domain.Run{stale, fresh, heartbeating} {
		if err := store.CreateRun(context.Background(), run); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := store.pool.Exec(context.Background(), `UPDATE runs
		SET started_at=clock_timestamp()-interval '30 seconds'
		WHERE id IN ($1,$2)`, stale.ID, heartbeating.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.pool.Exec(context.Background(), `UPDATE runs
		SET status='cancelling',cancel_requested_at=clock_timestamp() WHERE id=$1`, stale.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.pool.Exec(context.Background(), `UPDATE runs SET heartbeat_at=clock_timestamp() WHERE id=$1`, heartbeating.ID); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	for _, nodeRun := range []domain.NodeRun{
		{ID: fixtureUUID(), RunID: stale.ID, NodeID: "running", NodeType: "fixture", Status: domain.NodeRunning, StartedAt: &now},
		{ID: fixtureUUID(), RunID: stale.ID, NodeID: "completed", NodeType: "fixture", Status: domain.NodeCompleted, StartedAt: &now, EndedAt: &now},
	} {
		if err := store.UpsertNodeRun(context.Background(), nodeRun); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.PersistRunEvent(context.Background(), domain.RunEvent{
		RunID: stale.ID, Sequence: 1, Type: "run.started", ActivePorts: []string{},
		InputRedactedPaths: []string{}, OutputRedactedPaths: []string{}, Timestamp: now,
	}, nil, domain.RunEventBudget{MaxEvents: 2, MaxTotalDataBytes: 16 << 20}); err != nil {
		t.Fatal(err)
	}
	count, err := store.FinalizeInterruptedRuns(context.Background(), 15, 500)
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("finalized count=%d", count)
	}
	loaded, nodeRuns, err := store.GetRun(context.Background(), stale.ID)
	if err != nil || loaded.Status != domain.RunCancelled || loaded.Error == nil || loaded.Error.Code != "RUN_INTERRUPTED" {
		t.Fatalf("stale run=%+v err=%v", loaded, err)
	}
	statuses := map[string]domain.NodeStatus{}
	for _, nodeRun := range nodeRuns {
		statuses[nodeRun.NodeID] = nodeRun.Status
	}
	if statuses["running"] != domain.NodeCancelled || statuses["completed"] != domain.NodeCompleted {
		t.Fatalf("node statuses=%v", statuses)
	}
	events, err := store.ListRunEvents(context.Background(), stale.ID, 0, 10)
	if err != nil || len(events) != 2 || events[1].Type != "run.cancelled" || events[1].Error == nil || events[1].Error.Code != "RUN_INTERRUPTED" {
		t.Fatalf("events=%+v err=%v", events, err)
	}
	for _, id := range []string{fresh.ID, heartbeating.ID} {
		run, _, getErr := store.GetRun(context.Background(), id)
		if getErr != nil || run.Status != domain.RunRunning {
			t.Fatalf("healthy run %s=%+v err=%v", id, run, getErr)
		}
	}
}

func TestFinalizeInterruptedRunsLimitsAndStablyOrdersBatch(t *testing.T) {
	store := migratedTestStore(t)
	workflow := createWorkflowFixture(t, store, "interrupted-batch")
	ids := make([]string, 0, 501)
	transaction, err := store.pool.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer transaction.Rollback(context.Background())
	for index := 0; index < 501; index++ {
		id := fixtureUUID()
		ids = append(ids, id)
		if _, err := transaction.Exec(context.Background(), `INSERT INTO runs(
			id,workflow_id,draft_revision,graph_snapshot,mode,status,input,started_at
		) VALUES($1,$2,1,$3,'test','running','{}'::jsonb,'2020-01-01T00:00:00Z'::timestamptz)`, id, workflow.ID, workflow.DraftGraph); err != nil {
			t.Fatal(err)
		}
	}
	if err := transaction.Commit(context.Background()); err != nil {
		t.Fatal(err)
	}
	count, err := store.FinalizeInterruptedRuns(context.Background(), 15, 500)
	if err != nil || count != 500 {
		t.Fatalf("first sweep count=%d err=%v", count, err)
	}
	var remaining []string
	rows, err := store.pool.Query(context.Background(), "SELECT id::text FROM runs WHERE workflow_id=$1 AND status='running'", workflow.ID)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		remaining = append(remaining, id)
	}
	rows.Close()
	sort.Strings(ids)
	if len(remaining) != 1 || remaining[0] != ids[len(ids)-1] {
		t.Fatalf("remaining=%v, want highest ID %s", remaining, ids[len(ids)-1])
	}
	count, err = store.FinalizeInterruptedRuns(context.Background(), 15, 500)
	if err != nil || count != 1 {
		t.Fatalf("second sweep count=%d err=%v", count, err)
	}
}

func TestFinalizeInterruptedRunsRejectsInvalidGraphWithoutPartialFinalization(t *testing.T) {
	store := migratedTestStore(t)
	workflow := createWorkflowFixture(t, store, "interrupted-invalid")
	run := newTestRun(workflow.ID, workflow.DraftRevision, json.RawMessage(`{"schemaVersion":1,"nodes":{},"edges":[]}`))
	if err := store.CreateRun(context.Background(), run); err != nil {
		t.Fatal(err)
	}
	if _, err := store.pool.Exec(context.Background(), "UPDATE runs SET started_at=clock_timestamp()-interval '30 seconds' WHERE id=$1", run.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.FinalizeInterruptedRuns(context.Background(), 15, 500); err == nil {
		t.Fatal("invalid graph nodes should fail closed")
	}
	loaded, _, err := store.GetRun(context.Background(), run.ID)
	if err != nil || loaded.Status != domain.RunRunning {
		t.Fatalf("invalid graph run partially finalized=%+v err=%v", loaded, err)
	}
	events, err := store.ListRunEvents(context.Background(), run.ID, 0, 10)
	if err != nil || len(events) != 0 {
		t.Fatalf("invalid graph events=%+v err=%v", events, err)
	}
}
