package postgres

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/yyl1212/agent-studio/apps/api/internal/domain"
	workflowservice "github.com/yyl1212/agent-studio/apps/api/internal/workflow"
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
	if count != 3 {
		t.Fatalf("migration count=%d", count)
	}
}

func TestListWorkflowsHidesArchivedButGetPreservesState(t *testing.T) {
	store := migratedTestStore(t)
	active := createWorkflowFixture(t, store, "active-list")
	archived := createWorkflowFixture(t, store, "archived-list")
	archivedAt := time.Date(2026, 8, 25, 4, 0, 0, 0, time.UTC)
	if _, err := store.pool.Exec(context.Background(),
		"UPDATE workflows SET archived_at=$2 WHERE id=$1",
		archived.ID,
		archivedAt,
	); err != nil {
		t.Fatal(err)
	}

	workflows, err := store.ListWorkflows(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(workflows) != 1 || workflows[0].ID != active.ID {
		t.Fatalf("active workflows=%+v", workflows)
	}
	loaded, err := store.GetWorkflow(context.Background(), archived.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.ArchivedAt == nil || !loaded.ArchivedAt.Equal(archivedAt) {
		t.Fatalf("archived workflow=%+v", loaded)
	}
}

func TestWorkflowSummaryFiltersStateAndLiteralSearch(t *testing.T) {
	store := migratedTestStore(t)
	active := createWorkflowFixture(t, store, "summary-active")
	activeVersion := publishFixture(t, store, active)
	archived := createWorkflowFixture(t, store, "summary-archived")
	literal := createWorkflowFixture(t, store, "summary-literal")
	literalName := `Literal %_\ Agent`
	archivedAt := time.Date(2026, 8, 25, 4, 0, 0, 0, time.UTC)
	updatedAt := time.Date(2026, 8, 25, 5, 0, 0, 0, time.UTC)
	if _, err := store.pool.Exec(context.Background(),
		`UPDATE workflows SET archived_at=CASE WHEN id=$1 THEN $4::timestamptz ELSE NULL::timestamptz END,
		 name=CASE WHEN id=$2 THEN $3 ELSE name END,updated_at=$5 WHERE id=ANY($6::uuid[])`,
		archived.ID,
		literal.ID,
		literalName,
		archivedAt,
		updatedAt,
		[]string{active.ID, archived.ID, literal.ID},
	); err != nil {
		t.Fatal(err)
	}

	activeRows, err := store.ListWorkflowSummaries(context.Background(), workflowservice.WorkflowSummaryStoreQuery{
		State: workflowservice.WorkflowStateActive, Limit: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(activeRows) != 2 || activeRows[0].ID != literal.ID || activeRows[1].ID != active.ID {
		t.Fatalf("active rows=%+v", activeRows)
	}
	if activeRows[1].PublishedVersionID == nil || *activeRows[1].PublishedVersionID != activeVersion.ID || activeRows[1].PublishedVersion == nil || *activeRows[1].PublishedVersion != 1 {
		t.Fatalf("active published summary=%+v", activeRows[1])
	}
	if activeRows[1].CreatedAt.Location() != time.UTC || activeRows[1].UpdatedAt.Location() != time.UTC {
		t.Fatalf("active summary timestamps must be UTC: %+v", activeRows[1])
	}
	archivedRows, err := store.ListWorkflowSummaries(context.Background(), workflowservice.WorkflowSummaryStoreQuery{
		State: workflowservice.WorkflowStateArchived, Limit: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(archivedRows) != 1 || archivedRows[0].ID != archived.ID || archivedRows[0].ArchivedAt == nil || !archivedRows[0].ArchivedAt.Equal(archivedAt) {
		t.Fatalf("archived rows=%+v", archivedRows)
	}
	if archivedRows[0].ArchivedAt.Location() != time.UTC {
		t.Fatalf("archived timestamp must be UTC: %+v", archivedRows[0])
	}
	allRows, err := store.ListWorkflowSummaries(context.Background(), workflowservice.WorkflowSummaryStoreQuery{
		State: workflowservice.WorkflowStateAll, Limit: 10,
	})
	if err != nil || len(allRows) != 3 {
		t.Fatalf("all rows=%+v error=%v", allRows, err)
	}
	literalRows, err := store.ListWorkflowSummaries(context.Background(), workflowservice.WorkflowSummaryStoreQuery{
		Text: `%_\`, State: workflowservice.WorkflowStateAll, Limit: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(literalRows) != 1 || literalRows[0].ID != literal.ID || literalRows[0].Name != literalName {
		t.Fatalf("literal rows=%+v", literalRows)
	}
	slugRows, err := store.ListWorkflowSummaries(context.Background(), workflowservice.WorkflowSummaryStoreQuery{
		Text: strings.ToUpper(active.Slug), State: workflowservice.WorkflowStateAll, Limit: 10,
	})
	if err != nil || len(slugRows) != 1 || slugRows[0].ID != active.ID {
		t.Fatalf("slug rows=%+v error=%v", slugRows, err)
	}
}

func TestWorkflowSummaryUsesStableDescendingCursor(t *testing.T) {
	store := migratedTestStore(t)
	first := createWorkflowFixture(t, store, "summary-page-first")
	second := createWorkflowFixture(t, store, "summary-page-second")
	third := createWorkflowFixture(t, store, "summary-page-third")
	sameTime := time.Date(2026, 8, 25, 6, 0, 0, 0, time.UTC)
	if _, err := store.pool.Exec(context.Background(),
		"UPDATE workflows SET updated_at=$1 WHERE id=ANY($2::uuid[])",
		sameTime,
		[]string{first.ID, second.ID, third.ID},
	); err != nil {
		t.Fatal(err)
	}

	firstPage, err := store.ListWorkflowSummaries(context.Background(), workflowservice.WorkflowSummaryStoreQuery{
		State: workflowservice.WorkflowStateAll, Limit: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(firstPage) != 2 || firstPage[0].ID != third.ID || firstPage[1].ID != second.ID {
		t.Fatalf("first page=%+v", firstPage)
	}
	secondPage, err := store.ListWorkflowSummaries(context.Background(), workflowservice.WorkflowSummaryStoreQuery{
		State:        workflowservice.WorkflowStateAll,
		AfterUpdated: &sameTime,
		AfterID:      second.ID,
		Limit:        2,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(secondPage) != 1 || secondPage[0].ID != first.ID {
		t.Fatalf("second page=%+v", secondPage)
	}
}

func TestWorkflowManagementLifecyclePersistsAndIsIdempotent(t *testing.T) {
	store := migratedTestStore(t)
	workflow := createWorkflowFixture(t, store, "lifecycle")
	updated, err := store.UpdateWorkflowMetadata(context.Background(), workflow.ID, "新名称", "新说明")
	if err != nil {
		t.Fatal(err)
	}
	if updated.Name != "新名称" || updated.Description != "新说明" || updated.ArchivedAt != nil {
		t.Fatalf("updated=%+v", updated)
	}

	firstArchive, err := store.ArchiveWorkflow(context.Background(), workflow.ID)
	if err != nil {
		t.Fatal(err)
	}
	secondArchive, err := store.ArchiveWorkflow(context.Background(), workflow.ID)
	if err != nil {
		t.Fatal(err)
	}
	if firstArchive.ArchivedAt == nil || secondArchive.ArchivedAt == nil || !firstArchive.ArchivedAt.Equal(*secondArchive.ArchivedAt) || !firstArchive.UpdatedAt.Equal(secondArchive.UpdatedAt) {
		t.Fatalf("archives=%+v %+v", firstArchive, secondArchive)
	}
	if _, err := store.UpdateWorkflowMetadata(context.Background(), workflow.ID, "禁止修改", "禁止修改"); !errors.Is(err, domain.ErrWorkflowArchived) {
		t.Fatalf("archived update error=%v", err)
	}
	if _, err := store.CreateWorkflow(context.Background(), domain.Workflow{
		ID: fixtureUUID(), Name: "重复 slug", Slug: workflow.Slug,
		DraftGraph: json.RawMessage(`{"schemaVersion":1,"nodes":[],"edges":[]}`), DraftRevision: 1,
	}); !errors.Is(err, domain.ErrSlugConflict) {
		t.Fatalf("archived slug error=%v", err)
	}

	firstRestore, err := store.RestoreWorkflow(context.Background(), workflow.ID)
	if err != nil {
		t.Fatal(err)
	}
	secondRestore, err := store.RestoreWorkflow(context.Background(), workflow.ID)
	if err != nil {
		t.Fatal(err)
	}
	if firstRestore.ArchivedAt != nil || secondRestore.ArchivedAt != nil || !firstRestore.UpdatedAt.Equal(secondRestore.UpdatedAt) {
		t.Fatalf("restores=%+v %+v", firstRestore, secondRestore)
	}
	loaded, err := store.GetWorkflow(context.Background(), workflow.ID)
	if err != nil || loaded.Name != "新名称" || loaded.Description != "新说明" {
		t.Fatalf("loaded=%+v error=%v", loaded, err)
	}
}

func TestArchivedWorkflowStoreRejectsDraftPublishAndRun(t *testing.T) {
	store := migratedTestStore(t)
	workflow := createWorkflowFixture(t, store, "archived-writes")
	if _, err := store.ArchiveWorkflow(context.Background(), workflow.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpdateDraft(context.Background(), workflow.ID, workflow.DraftRevision, workflow.DraftGraph); !errors.Is(err, domain.ErrWorkflowArchived) {
		t.Fatalf("update error=%v", err)
	}
	if _, err := store.Publish(context.Background(), workflow.ID, workflow.DraftRevision, workflow.DraftGraph, json.RawMessage(`{"type":"object"}`)); !errors.Is(err, domain.ErrWorkflowArchived) {
		t.Fatalf("publish error=%v", err)
	}
	run := newTestRun(workflow.ID, workflow.DraftRevision, workflow.DraftGraph)
	if err := store.CreateRun(context.Background(), run); !errors.Is(err, domain.ErrWorkflowArchived) {
		t.Fatalf("create run error=%v", err)
	}
	var versions, runs int
	if err := store.pool.QueryRow(context.Background(), "SELECT count(*) FROM workflow_versions WHERE workflow_id=$1", workflow.ID).Scan(&versions); err != nil {
		t.Fatal(err)
	}
	if err := store.pool.QueryRow(context.Background(), "SELECT count(*) FROM runs WHERE workflow_id=$1", workflow.ID).Scan(&runs); err != nil {
		t.Fatal(err)
	}
	if versions != 0 || runs != 0 {
		t.Fatalf("versions=%d runs=%d", versions, runs)
	}
}

func TestCreateRunWaitsForConcurrentArchiveDecision(t *testing.T) {
	store := migratedTestStore(t)
	workflow := createWorkflowFixture(t, store, "archive-run-race")
	transaction, err := store.pool.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer transaction.Rollback(context.Background())
	if _, err := transaction.Exec(context.Background(), "UPDATE workflows SET archived_at=now() WHERE id=$1", workflow.ID); err != nil {
		t.Fatal(err)
	}

	result := make(chan error, 1)
	started := make(chan struct{})
	go func() {
		close(started)
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		result <- store.CreateRun(ctx, newTestRun(workflow.ID, workflow.DraftRevision, workflow.DraftGraph))
	}()
	<-started
	select {
	case err := <-result:
		t.Fatalf("create run returned before archive committed: %v", err)
	case <-time.After(250 * time.Millisecond):
	}
	if err := transaction.Commit(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-result:
		if !errors.Is(err, domain.ErrWorkflowArchived) {
			t.Fatalf("create run error=%v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("create run did not finish after archive commit")
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
