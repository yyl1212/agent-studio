package postgres

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/yyl1212/agent-studio/apps/api/internal/domain"
)

var (
	databaseTestMutex sync.Mutex
	fixtureSequence   atomic.Int64
)

func TestMigrateIsIdempotent(t *testing.T) {
	store := migratedTestStore(t)
	if err := store.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := store.pool.QueryRow(context.Background(), "SELECT count(*) FROM schema_migrations").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("migration count=%d", count)
	}
}

func TestPublishPreservesVersionAndTestSnapshot(t *testing.T) {
	store := migratedTestStore(t)
	workflow := createWorkflowFixture(t, store, "publish")
	version, err := store.Publish(context.Background(), workflow.ID, workflow.DraftRevision, workflow.DraftGraph, json.RawMessage(`{"type":"object"}`))
	if err != nil {
		t.Fatal(err)
	}
	if version.Version != 1 || version.WorkflowID != workflow.ID {
		t.Fatalf("version=%+v", version)
	}
	loadedWorkflow, loadedVersion, err := store.GetCurrentAgentVersion(context.Background(), workflow.Slug)
	if err != nil {
		t.Fatal(err)
	}
	if loadedWorkflow.ID != workflow.ID || loadedVersion.ID != version.ID {
		t.Fatalf("workflow=%+v version=%+v", loadedWorkflow, loadedVersion)
	}

	run := newTestRun(workflow.ID, workflow.DraftRevision, workflow.DraftGraph)
	if err := store.CreateRun(context.Background(), run); err != nil {
		t.Fatal(err)
	}
	loaded, _, err := store.GetRun(context.Background(), run.ID)
	if err != nil || !bytes.Equal(loaded.GraphSnapshot, workflow.DraftGraph) {
		t.Fatalf("loaded=%+v error=%v", loaded, err)
	}
}

func TestPublishedRunRejectsVersionFromAnotherWorkflow(t *testing.T) {
	store := migratedTestStore(t)
	first := createWorkflowFixture(t, store, "first")
	second := createWorkflowFixture(t, store, "second")
	firstVersion := publishFixture(t, store, first)
	secondVersion := publishFixture(t, store, second)
	_ = firstVersion
	run := newPublishedRun(first.ID, secondVersion.ID)
	if err := store.CreateRun(context.Background(), run); err == nil {
		t.Fatal("expected composite foreign key violation")
	}
}

func TestWorkflowLookupKeepsPublishedVersionsImmutable(t *testing.T) {
	store := migratedTestStore(t)
	workflow := createWorkflowFixture(t, store, "immutable")
	versionOne := publishFixture(t, store, workflow)
	versionTwoGraph := json.RawMessage(`{"schemaVersion":1,"nodes":[],"edges":[],"label":"v2"}`)
	updated, err := store.UpdateDraft(context.Background(), workflow.ID, workflow.DraftRevision, versionTwoGraph)
	if err != nil {
		t.Fatal(err)
	}
	versionTwo, err := store.Publish(context.Background(), workflow.ID, updated.DraftRevision, updated.DraftGraph, json.RawMessage(`{"type":"object"}`))
	if err != nil {
		t.Fatal(err)
	}
	if versionTwo.Version != 2 {
		t.Fatalf("version two=%+v", versionTwo)
	}
	loaded, err := store.GetWorkflow(context.Background(), workflow.ID)
	if err != nil || loaded.PublishedVersion == nil || *loaded.PublishedVersion != 2 {
		t.Fatalf("workflow=%+v error=%v", loaded, err)
	}
	workflows, err := store.ListWorkflows(context.Background())
	if err != nil || len(workflows) != 1 {
		t.Fatalf("workflows=%+v error=%v", workflows, err)
	}
	_, oldVersion, err := store.GetAgentVersion(context.Background(), workflow.Slug, versionOne.ID)
	if err != nil {
		t.Fatal(err)
	}
	if oldVersion.Version != 1 || !jsonEqual(oldVersion.Graph, versionOne.Graph) {
		t.Fatalf("old version changed: %+v", oldVersion)
	}
	_, currentVersion, err := store.GetCurrentAgentVersion(context.Background(), workflow.Slug)
	if err != nil || currentVersion.ID != versionTwo.ID {
		t.Fatalf("current version=%+v error=%v", currentVersion, err)
	}
}

func TestCreateWorkflowMapsDuplicateSlug(t *testing.T) {
	store := migratedTestStore(t)
	first := createWorkflowFixture(t, store, "duplicate")
	_, err := store.CreateWorkflow(context.Background(), domain.Workflow{
		ID:            fixtureUUID(),
		Name:          "重复 slug",
		Slug:          first.Slug,
		DraftGraph:    json.RawMessage(`{"schemaVersion":1,"nodes":[],"edges":[]}`),
		DraftRevision: 1,
	})
	if !errors.Is(err, domain.ErrSlugConflict) {
		t.Fatalf("error=%v", err)
	}
}

func TestUpdateDraftUsesRevisionAndPublishRollsBackOnConflict(t *testing.T) {
	store := migratedTestStore(t)
	workflow := createWorkflowFixture(t, store, "revision")
	newGraph := json.RawMessage(`{"schemaVersion":1,"nodes":[],"edges":[],"revision":2}`)
	updated, err := store.UpdateDraft(context.Background(), workflow.ID, workflow.DraftRevision, newGraph)
	if err != nil {
		t.Fatal(err)
	}
	if updated.DraftRevision != workflow.DraftRevision+1 || !jsonEqual(updated.DraftGraph, newGraph) {
		t.Fatalf("updated=%+v", updated)
	}
	if _, err := store.UpdateDraft(context.Background(), workflow.ID, workflow.DraftRevision, workflow.DraftGraph); !errors.Is(err, ErrRevisionConflict) {
		t.Fatalf("revision error=%v", err)
	}
	if _, err := store.Publish(context.Background(), workflow.ID, workflow.DraftRevision, workflow.DraftGraph, json.RawMessage(`{"type":"object"}`)); !errors.Is(err, ErrRevisionConflict) {
		t.Fatalf("publish error=%v", err)
	}
	var count int
	if err := store.pool.QueryRow(context.Background(), "SELECT count(*) FROM workflow_versions WHERE workflow_id=$1", workflow.ID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("conflicting publish inserted %d versions", count)
	}
}

func TestRunAndNodeRunRoundTrip(t *testing.T) {
	store := migratedTestStore(t)
	workflow := createWorkflowFixture(t, store, "runs")
	run := newTestRun(workflow.ID, workflow.DraftRevision, workflow.DraftGraph)
	if err := store.CreateRun(context.Background(), run); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	nodeRun := domain.NodeRun{
		ID:        fixtureUUID(),
		RunID:     run.ID,
		NodeID:    "start",
		NodeType:  "start",
		Status:    domain.NodeCompleted,
		Input:     json.RawMessage(`{"topic":"Agent"}`),
		Output:    json.RawMessage(`{"topic":"Agent"}`),
		StartedAt: &now,
		EndedAt:   &now,
	}
	if err := store.UpsertNodeRun(context.Background(), nodeRun); err != nil {
		t.Fatal(err)
	}
	if err := store.FinishRun(context.Background(), run.ID, domain.RunCompleted, map[string]any{"answer": "ok"}, nil, now); err != nil {
		t.Fatal(err)
	}
	loaded, nodeRuns, err := store.GetRun(context.Background(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Status != domain.RunCompleted || len(nodeRuns) != 1 || nodeRuns[0].NodeID != "start" {
		t.Fatalf("run=%+v nodeRuns=%+v", loaded, nodeRuns)
	}
	runs, err := store.ListRuns(context.Background(), workflow.ID, 10)
	if err != nil || len(runs) != 1 {
		t.Fatalf("runs=%+v error=%v", runs, err)
	}
}

func TestDebugRunRequiresSameWorkflowSourceAndSnapshot(t *testing.T) {
	store := migratedTestStore(t)
	first := createWorkflowFixture(t, store, "debug-source")
	second := createWorkflowFixture(t, store, "debug-other")
	source := newTestRun(first.ID, first.DraftRevision, first.DraftGraph)
	if err := store.CreateRun(context.Background(), source); err != nil {
		t.Fatal(err)
	}

	sourceNodeID := "template-1"
	valid := domain.Run{
		ID: fixtureUUID(), WorkflowID: first.ID, GraphSnapshot: first.DraftGraph,
		Mode: domain.RunModeDebug, Status: domain.RunRunning, Input: json.RawMessage(`{}`),
		SourceRunID: &source.ID, SourceNodeID: &sourceNodeID, StartedAt: time.Now().UTC(),
	}
	if err := store.CreateRun(context.Background(), valid); err != nil {
		t.Fatal(err)
	}
	loaded, _, err := store.GetRun(context.Background(), valid.ID)
	if err != nil || loaded.SourceRunID == nil || *loaded.SourceRunID != source.ID || loaded.SourceNodeID == nil || *loaded.SourceNodeID != sourceNodeID {
		t.Fatalf("loaded=%+v error=%v", loaded, err)
	}

	tests := []struct {
		name string
		run  domain.Run
	}{
		{name: "cross workflow source", run: domain.Run{
			ID: fixtureUUID(), WorkflowID: second.ID, GraphSnapshot: second.DraftGraph,
			Mode: domain.RunModeDebug, Status: domain.RunRunning, Input: json.RawMessage(`{}`),
			SourceRunID: &source.ID, SourceNodeID: &sourceNodeID, StartedAt: time.Now().UTC(),
		}},
		{name: "debug without snapshot", run: domain.Run{
			ID: fixtureUUID(), WorkflowID: first.ID,
			Mode: domain.RunModeDebug, Status: domain.RunRunning, Input: json.RawMessage(`{}`),
			SourceRunID: &source.ID, SourceNodeID: &sourceNodeID, StartedAt: time.Now().UTC(),
		}},
		{name: "test with source", run: domain.Run{
			ID: fixtureUUID(), WorkflowID: first.ID, DraftRevision: &first.DraftRevision, GraphSnapshot: first.DraftGraph,
			Mode: domain.RunModeTest, Status: domain.RunRunning, Input: json.RawMessage(`{}`),
			SourceRunID: &source.ID, SourceNodeID: &sourceNodeID, StartedAt: time.Now().UTC(),
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := store.CreateRun(context.Background(), test.run); err == nil {
				t.Fatal("expected source constraint failure")
			}
		})
	}
}

func TestPersistRunEventIsAppendOnlyAndAtomicWithNodeSummary(t *testing.T) {
	store := migratedTestStore(t)
	workflow := createWorkflowFixture(t, store, "event-atomic")
	run := newTestRun(workflow.ID, workflow.DraftRevision, workflow.DraftGraph)
	if err := store.CreateRun(context.Background(), run); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	event := domain.RunEvent{
		RunID: run.ID, Sequence: 1, Type: "node.started", NodeID: "start", Status: domain.NodeRunning,
		ActivePorts: []string{}, InputRedactedPaths: []string{}, OutputRedactedPaths: []string{}, DataBytes: 2, Timestamp: now,
	}
	nodeRun := domain.NodeRun{
		ID: fixtureUUID(), RunID: fixtureUUID(), NodeID: "start", NodeType: "start", Status: domain.NodeRunning, StartedAt: &now,
	}
	budget := domain.RunEventBudget{MaxEvents: 8, MaxTotalDataBytes: 32}
	if err := store.PersistRunEvent(context.Background(), event, &nodeRun, budget); err == nil {
		t.Fatal("expected node summary foreign key failure")
	}
	var count int
	if err := store.pool.QueryRow(context.Background(), "SELECT count(*) FROM run_events WHERE run_id=$1", run.ID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("events after rollback=%d", count)
	}

	if err := store.PersistRunEvent(context.Background(), event, nil, budget); err != nil {
		t.Fatal(err)
	}
	if err := store.PersistRunEvent(context.Background(), event, nil, budget); !errors.Is(err, domain.ErrRunEventSequence) {
		t.Fatalf("duplicate error=%v", err)
	}
}

func TestPersistRunEventRejectsSequenceGapAndTotalBudget(t *testing.T) {
	store := migratedTestStore(t)
	workflow := createWorkflowFixture(t, store, "event-budget")
	run := newTestRun(workflow.ID, workflow.DraftRevision, workflow.DraftGraph)
	if err := store.CreateRun(context.Background(), run); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	budget := domain.RunEventBudget{MaxEvents: 2, MaxTotalDataBytes: 4}
	event := domain.RunEvent{
		RunID: run.ID, Sequence: 2, Type: "run.started", ActivePorts: []string{},
		InputRedactedPaths: []string{}, OutputRedactedPaths: []string{}, DataBytes: 2, Timestamp: now,
	}
	if err := store.PersistRunEvent(context.Background(), event, nil, budget); !errors.Is(err, domain.ErrRunEventSequence) {
		t.Fatalf("gap error=%v", err)
	}
	event.Sequence = 1
	if err := store.PersistRunEvent(context.Background(), event, nil, budget); err != nil {
		t.Fatal(err)
	}
	event.Sequence = 2
	event.Type = "run.completed"
	event.DataBytes = 3
	if err := store.PersistRunEvent(context.Background(), event, nil, budget); !errors.Is(err, domain.ErrRunEventBudgetExceeded) {
		t.Fatalf("budget error=%v", err)
	}
	events, err := store.ListRunEvents(context.Background(), run.ID, 0, 200)
	if err != nil || len(events) != 1 {
		t.Fatalf("events=%+v error=%v", events, err)
	}
}

func TestListRunEventsUsesExclusiveCursorAndStableOrder(t *testing.T) {
	store := migratedTestStore(t)
	workflow := createWorkflowFixture(t, store, "event-list")
	run := newTestRun(workflow.ID, workflow.DraftRevision, workflow.DraftGraph)
	if err := store.CreateRun(context.Background(), run); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	budget := domain.RunEventBudget{MaxEvents: 8, MaxTotalDataBytes: 32}
	for sequence, eventType := range []string{"run.started", "node.started", "node.completed", "run.completed"} {
		event := domain.RunEvent{
			RunID: run.ID, Sequence: int64(sequence + 1), Type: eventType, NodeID: "start",
			ActivePorts: []string{}, InputRedactedPaths: []string{}, OutputRedactedPaths: []string{}, Timestamp: now.Add(time.Duration(sequence) * time.Millisecond),
		}
		if eventType == "run.started" || eventType == "run.completed" {
			event.NodeID = ""
		}
		if err := store.PersistRunEvent(context.Background(), event, nil, budget); err != nil {
			t.Fatal(err)
		}
	}
	events, err := store.ListRunEvents(context.Background(), run.ID, 1, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 || events[0].Sequence != 2 || events[1].Sequence != 3 {
		t.Fatalf("events=%+v", events)
	}
	for _, event := range events {
		if event.ActivePorts == nil || event.InputRedactedPaths == nil || event.OutputRedactedPaths == nil {
			t.Fatalf("event arrays must be non-nil: %+v", event)
		}
	}
}

func migratedTestStore(t *testing.T) *Store {
	t.Helper()
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	databaseTestMutex.Lock()
	store, err := Open(context.Background(), databaseURL)
	if err != nil {
		databaseTestMutex.Unlock()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		store.Close()
		databaseTestMutex.Unlock()
	})
	if err := store.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := store.pool.Exec(context.Background(), "TRUNCATE node_runs, runs, workflow_versions, workflows CASCADE"); err != nil {
		t.Fatal(err)
	}
	return store
}

func createWorkflowFixture(t *testing.T, store *Store, suffix string) domain.Workflow {
	t.Helper()
	sequence := fixtureSequence.Add(1)
	workflow, err := store.CreateWorkflow(context.Background(), domain.Workflow{
		ID:            fixtureUUID(),
		Name:          "测试工作流 " + suffix,
		Slug:          fmt.Sprintf("workflow-%s-%d", suffix, sequence),
		Description:   "集成测试",
		DraftGraph:    json.RawMessage(`{"schemaVersion":1,"nodes":[],"edges":[]}`),
		DraftRevision: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	return workflow
}

func publishFixture(t *testing.T, store *Store, workflow domain.Workflow) domain.WorkflowVersion {
	t.Helper()
	version, err := store.Publish(context.Background(), workflow.ID, workflow.DraftRevision, workflow.DraftGraph, json.RawMessage(`{"type":"object"}`))
	if err != nil {
		t.Fatal(err)
	}
	return version
}

func newTestRun(workflowID string, revision int64, graph json.RawMessage) domain.Run {
	return domain.Run{
		ID:            fixtureUUID(),
		WorkflowID:    workflowID,
		DraftRevision: &revision,
		GraphSnapshot: graph,
		Mode:          domain.RunModeTest,
		Status:        domain.RunRunning,
		Input:         json.RawMessage(`{}`),
		StartedAt:     time.Now().UTC(),
	}
}

func newPublishedRun(workflowID, versionID string) domain.Run {
	return domain.Run{
		ID:                fixtureUUID(),
		WorkflowID:        workflowID,
		WorkflowVersionID: &versionID,
		Mode:              domain.RunModePublished,
		Status:            domain.RunRunning,
		Input:             json.RawMessage(`{}`),
		StartedAt:         time.Now().UTC(),
	}
}

func fixtureUUID() string {
	value := fixtureSequence.Add(1)
	return fmt.Sprintf("00000000-0000-4000-8000-%012d", value)
}

func jsonEqual(left, right []byte) bool {
	var leftValue, rightValue any
	if json.Unmarshal(left, &leftValue) != nil || json.Unmarshal(right, &rightValue) != nil {
		return false
	}
	leftJSON, _ := json.Marshal(leftValue)
	rightJSON, _ := json.Marshal(rightValue)
	return bytes.Equal(leftJSON, rightJSON)
}
