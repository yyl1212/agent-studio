package postgres

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"
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
	if count != 7 {
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
	if _, err := store.Publish(context.Background(), workflow.ID, workflow.DraftRevision, workflow.DraftGraph, json.RawMessage(`{"type":"object"}`), workflow.AgentPresentation); !errors.Is(err, domain.ErrWorkflowArchived) {
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
	version, err := store.Publish(context.Background(), workflow.ID, workflow.DraftRevision, workflow.DraftGraph, json.RawMessage(`{"type":"object"}`), workflow.AgentPresentation)
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

func TestRunRecoveryFieldsRoundTripAndHideCoordinatorState(t *testing.T) {
	store := migratedTestStore(t)
	workflow := createWorkflowFixture(t, store, "run-recovery")
	source := newTestRun(workflow.ID, workflow.DraftRevision, workflow.DraftGraph)
	if err := store.CreateRun(context.Background(), source); err != nil {
		t.Fatal(err)
	}
	retryID := source.ID
	retryKey := fixtureUUID()
	cancelRequestedAt := time.Now().UTC().Truncate(time.Microsecond)
	heartbeatAt := cancelRequestedAt.Add(time.Second)
	retry := newTestRun(workflow.ID, workflow.DraftRevision, workflow.DraftGraph)
	retry.Status = domain.RunCancelling
	retry.RetryOfRunID = &retryID
	retry.RetryKey = &retryKey
	retry.InputRedactedPaths = []string{"/webhookToken"}
	retry.CancelRequestedAt = &cancelRequestedAt
	retry.HeartbeatAt = &heartbeatAt
	if err := store.CreateRun(context.Background(), retry); err != nil {
		t.Fatal(err)
	}
	loaded, _, err := store.GetRun(context.Background(), retry.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Status != domain.RunCancelling || loaded.RetryOfRunID == nil || *loaded.RetryOfRunID != source.ID ||
		loaded.RetryKey == nil || *loaded.RetryKey != retryKey || !reflect.DeepEqual(loaded.InputRedactedPaths, []string{"/webhookToken"}) ||
		loaded.CancelRequestedAt == nil || !loaded.CancelRequestedAt.Equal(cancelRequestedAt) ||
		loaded.HeartbeatAt == nil || !loaded.HeartbeatAt.Equal(heartbeatAt) {
		t.Fatalf("loaded recovery fields=%+v", loaded)
	}
	encoded, err := json.Marshal(loaded)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(encoded, []byte("retryKey")) || bytes.Contains(encoded, []byte("heartbeatAt")) {
		t.Fatalf("coordinator state leaked in public JSON: %s", encoded)
	}
	loadedSource, _, err := store.GetRun(context.Background(), source.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loadedSource.InputRedactedPaths == nil {
		t.Fatal("inputRedactedPaths must be a non-nil empty array")
	}
}

func TestCreateRetryRunIsConcurrentAndSourceScopedIdempotent(t *testing.T) {
	store := migratedTestStore(t)
	workflow := createWorkflowFixture(t, store, "retry-idempotency")
	source := newTestRun(workflow.ID, workflow.DraftRevision, workflow.DraftGraph)
	if err := store.CreateRun(context.Background(), source); err != nil {
		t.Fatal(err)
	}
	if _, err := store.pool.Exec(context.Background(), "UPDATE runs SET status='failed' WHERE id=$1", source.ID); err != nil {
		t.Fatal(err)
	}
	key := fixtureUUID()
	makeRetry := func() domain.Run {
		retry := newTestRun(workflow.ID, workflow.DraftRevision, workflow.DraftGraph)
		retryOf, retryKey := source.ID, key
		retry.RetryOfRunID, retry.RetryKey = &retryOf, &retryKey
		return retry
	}
	type result struct {
		id  string
		err error
	}
	start := make(chan struct{})
	results := make(chan result, 2)
	for range 2 {
		go func() {
			<-start
			id, err := store.CreateRetryRun(context.Background(), makeRetry())
			results <- result{id: id, err: err}
		}()
	}
	close(start)
	first, second := <-results, <-results
	var success result
	var duplicate *workflowservice.RunRetryAlreadyCreatedError
	if first.err == nil {
		success = first
		if !errors.As(second.err, &duplicate) {
			t.Fatalf("second error=%v", second.err)
		}
	} else {
		success = second
		if !errors.As(first.err, &duplicate) {
			t.Fatalf("first error=%v", first.err)
		}
	}
	if success.id == "" || duplicate == nil || duplicate.RunID != success.id {
		t.Fatalf("success=%+v duplicate=%+v", success, duplicate)
	}
	var count int
	if err := store.pool.QueryRow(context.Background(), "SELECT count(*) FROM runs WHERE retry_of_run_id=$1 AND retry_key=$2", source.ID, key).Scan(&count); err != nil || count != 1 {
		t.Fatalf("count=%d error=%v", count, err)
	}

	differentKey := fixtureUUID()
	retry := makeRetry()
	retry.RetryKey = &differentKey
	if _, err := store.CreateRetryRun(context.Background(), retry); err != nil {
		t.Fatalf("different key error=%v", err)
	}

	secondSource := newTestRun(workflow.ID, workflow.DraftRevision, workflow.DraftGraph)
	if err := store.CreateRun(context.Background(), secondSource); err != nil {
		t.Fatal(err)
	}
	if _, err := store.pool.Exec(context.Background(), "UPDATE runs SET status='cancelled',ended_at=now() WHERE id=$1", secondSource.ID); err != nil {
		t.Fatal(err)
	}
	otherSourceRetry := newTestRun(workflow.ID, workflow.DraftRevision, workflow.DraftGraph)
	otherSourceRetry.RetryOfRunID, otherSourceRetry.RetryKey = &secondSource.ID, &key
	if _, err := store.CreateRetryRun(context.Background(), otherSourceRetry); err != nil {
		t.Fatalf("same key for different source error=%v", err)
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
	versionTwo, err := store.Publish(context.Background(), workflow.ID, updated.DraftRevision, updated.DraftGraph, json.RawMessage(`{"type":"object"}`), updated.AgentPresentation)
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

func TestListWorkflowVersionsUsesDescendingVersionCursor(t *testing.T) {
	store := migratedTestStore(t)
	workflow := createWorkflowFixture(t, store, "version-list")
	versions := make([]domain.WorkflowVersion, 0, 3)
	for expected := 1; expected <= 3; expected++ {
		version := publishFixture(t, store, workflow)
		if version.Version != expected {
			t.Fatalf("published version=%d, want %d", version.Version, expected)
		}
		versions = append(versions, version)
	}
	other := createWorkflowFixture(t, store, "version-list-other")
	publishFixture(t, store, other)

	rows, err := store.ListWorkflowVersions(context.Background(), workflow.ID, 0, 2)
	if err != nil {
		t.Fatal(err)
	}
	if got := []int{rows.Items[0].Version, rows.Items[1].Version}; !reflect.DeepEqual(got, []int{3, 2}) {
		t.Fatalf("versions=%v", got)
	}
	if !rows.Items[0].Current || rows.Items[1].Current || rows.Checkpoint != nil {
		t.Fatalf("rows=%+v", rows)
	}

	next, err := store.ListWorkflowVersions(context.Background(), workflow.ID, 2, 2)
	if err != nil || len(next.Items) != 1 || next.Items[0].Version != 1 {
		t.Fatalf("next=%+v err=%v", next, err)
	}
	if _, err := store.ListWorkflowVersions(context.Background(), fixtureUUID(), 0, 2); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("missing workflow error=%v", err)
	}

	presentation, err := json.Marshal(workflow.AgentPresentation)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.pool.Exec(context.Background(), `UPDATE workflows SET draft_revision=draft_revision+1 WHERE id=$1`, workflow.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.pool.Exec(context.Background(), `INSERT INTO workflow_draft_checkpoints(
		workflow_id,source_revision,restored_revision,graph,agent_presentation,restored_from_version_id
	) VALUES($1,$2,$3,$4,$5,$6)`, workflow.ID, workflow.DraftRevision, workflow.DraftRevision+1, workflow.DraftGraph, presentation, versions[0].ID); err != nil {
		t.Fatal(err)
	}
	withCheckpoint, err := store.ListWorkflowVersions(context.Background(), workflow.ID, 0, 2)
	if err != nil || withCheckpoint.Checkpoint == nil || withCheckpoint.Checkpoint.SourceRevision != workflow.DraftRevision ||
		withCheckpoint.Checkpoint.RestoredRevision != workflow.DraftRevision+1 || withCheckpoint.Checkpoint.RestoredFromVersion != 1 {
		t.Fatalf("checkpoint=%+v err=%v", withCheckpoint.Checkpoint, err)
	}
}

func TestGetWorkflowVersionByNumberScopesLookupToWorkflow(t *testing.T) {
	store := migratedTestStore(t)
	workflow := createWorkflowFixture(t, store, "version-by-number")
	want := publishFixture(t, store, workflow)
	other := createWorkflowFixture(t, store, "version-by-number-other")
	publishFixture(t, store, other)
	publishFixture(t, store, other)

	got, err := store.GetWorkflowVersionByNumber(context.Background(), workflow.ID, 1)
	if err != nil || got.ID != want.ID || got.WorkflowID != workflow.ID || got.Version != 1 || !jsonEqual(got.Graph, want.Graph) {
		t.Fatalf("version=%+v err=%v", got, err)
	}
	if _, err := store.GetWorkflowVersionByNumber(context.Background(), workflow.ID, 2); !errors.Is(err, domain.ErrWorkflowVersionNotFound) {
		t.Fatalf("cross-workflow version error=%v", err)
	}
}

func TestWorkflowDraftRollbackAndUndoPreservePublishedVersion(t *testing.T) {
	store := migratedTestStore(t)
	workflow, versionOne, versionTwo := rollbackFixture(t, store, "rollback")
	originalGraph := append(json.RawMessage(nil), workflow.DraftGraph...)
	originalPresentation := workflow.AgentPresentation

	rolledBack, checkpoint, err := store.RollbackWorkflowDraft(context.Background(), workflow.ID, versionOne.ID, workflow.DraftRevision)
	if err != nil {
		t.Fatal(err)
	}
	if rolledBack.DraftRevision != workflow.DraftRevision+1 || checkpoint.SourceRevision != workflow.DraftRevision ||
		checkpoint.RestoredRevision != rolledBack.DraftRevision || checkpoint.RestoredFromVersion != versionOne.Version {
		t.Fatalf("workflow=%+v checkpoint=%+v", rolledBack, checkpoint)
	}
	if !jsonEqual(rolledBack.DraftGraph, versionOne.Graph) || rolledBack.AgentPresentation != versionOne.AgentPresentation {
		t.Fatalf("rollback did not restore version one: %+v", rolledBack)
	}
	if rolledBack.PublishedVersionID == nil || *rolledBack.PublishedVersionID != versionTwo.ID ||
		rolledBack.PublishedVersion == nil || *rolledBack.PublishedVersion != versionTwo.Version {
		t.Fatalf("published pointer changed: %+v", rolledBack)
	}

	undone, err := store.UndoWorkflowDraftRollback(context.Background(), workflow.ID, rolledBack.DraftRevision)
	if err != nil || undone.DraftRevision != rolledBack.DraftRevision+1 || !jsonEqual(undone.DraftGraph, originalGraph) ||
		undone.AgentPresentation != originalPresentation {
		t.Fatalf("undone=%+v err=%v", undone, err)
	}
	if _, err := store.UndoWorkflowDraftRollback(context.Background(), workflow.ID, undone.DraftRevision); !errors.Is(err, domain.ErrRollbackUndoUnavailable) {
		t.Fatalf("repeated undo error=%v", err)
	}

	loadedOne, err := store.GetWorkflowVersionByNumber(context.Background(), workflow.ID, versionOne.Version)
	if err != nil || !jsonEqual(loadedOne.Graph, versionOne.Graph) || loadedOne.AgentPresentation != versionOne.AgentPresentation {
		t.Fatalf("version one mutated: %+v err=%v", loadedOne, err)
	}
	loadedTwo, err := store.GetWorkflowVersionByNumber(context.Background(), workflow.ID, versionTwo.Version)
	if err != nil || !jsonEqual(loadedTwo.Graph, versionTwo.Graph) || loadedTwo.AgentPresentation != versionTwo.AgentPresentation {
		t.Fatalf("version two mutated: %+v err=%v", loadedTwo, err)
	}
}

func TestWorkflowDraftRollbackRejectsInvalidStateAndCrossWorkflowVersion(t *testing.T) {
	store := migratedTestStore(t)
	workflow, versionOne, _ := rollbackFixture(t, store, "rollback-errors")
	other := createWorkflowFixture(t, store, "rollback-errors-other")
	otherVersion := publishFixture(t, store, other)

	if _, _, err := store.RollbackWorkflowDraft(context.Background(), workflow.ID, versionOne.ID, workflow.DraftRevision-1); !errors.Is(err, domain.ErrRevisionConflict) {
		t.Fatalf("stale revision error=%v", err)
	}
	if _, _, err := store.RollbackWorkflowDraft(context.Background(), workflow.ID, otherVersion.ID, workflow.DraftRevision); !errors.Is(err, domain.ErrWorkflowVersionNotFound) {
		t.Fatalf("cross-workflow version error=%v", err)
	}
	if _, err := store.ArchiveWorkflow(context.Background(), workflow.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.RollbackWorkflowDraft(context.Background(), workflow.ID, versionOne.ID, workflow.DraftRevision); !errors.Is(err, domain.ErrWorkflowArchived) {
		t.Fatalf("archived rollback error=%v", err)
	}
	if _, err := store.UndoWorkflowDraftRollback(context.Background(), workflow.ID, workflow.DraftRevision); !errors.Is(err, domain.ErrWorkflowArchived) {
		t.Fatalf("archived undo error=%v", err)
	}
}

func TestWorkflowDraftRollbackOverwritesCheckpointAndEnforcesVersionForeignKey(t *testing.T) {
	store := migratedTestStore(t)
	workflow, versionOne, versionTwo := rollbackFixture(t, store, "rollback-checkpoint")

	first, _, err := store.RollbackWorkflowDraft(context.Background(), workflow.ID, versionOne.ID, workflow.DraftRevision)
	if err != nil {
		t.Fatal(err)
	}
	second, checkpoint, err := store.RollbackWorkflowDraft(context.Background(), workflow.ID, versionTwo.ID, first.DraftRevision)
	if err != nil || checkpoint.SourceRevision != first.DraftRevision || checkpoint.RestoredRevision != second.DraftRevision ||
		checkpoint.RestoredFromVersion != versionTwo.Version {
		t.Fatalf("second=%+v checkpoint=%+v err=%v", second, checkpoint, err)
	}

	presentation, err := json.Marshal(workflow.AgentPresentation)
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.pool.Exec(context.Background(), `INSERT INTO workflow_draft_checkpoints(
		workflow_id,source_revision,restored_revision,graph,agent_presentation,restored_from_version_id
	) VALUES($1,$2,$3,$4,$5,$6)
	ON CONFLICT(workflow_id) DO UPDATE SET restored_from_version_id=excluded.restored_from_version_id`,
		workflow.ID, second.DraftRevision, second.DraftRevision+1, workflow.DraftGraph, presentation, fixtureUUID())
	if err == nil {
		t.Fatal("checkpoint accepted a missing version")
	}
}

func TestWorkflowDraftRollbackConcurrentRevisionAllowsOneWinner(t *testing.T) {
	store := migratedTestStore(t)
	workflow, versionOne, _ := rollbackFixture(t, store, "rollback-concurrent")
	start := make(chan struct{})
	errorsByCall := make(chan error, 2)
	for range 2 {
		go func() {
			<-start
			_, _, err := store.RollbackWorkflowDraft(context.Background(), workflow.ID, versionOne.ID, workflow.DraftRevision)
			errorsByCall <- err
		}()
	}
	close(start)
	var succeeded, conflicted int
	for range 2 {
		err := <-errorsByCall
		switch {
		case err == nil:
			succeeded++
		case errors.Is(err, domain.ErrRevisionConflict):
			conflicted++
		default:
			t.Fatalf("unexpected rollback error=%v", err)
		}
	}
	if succeeded != 1 || conflicted != 1 {
		t.Fatalf("succeeded=%d conflicted=%d", succeeded, conflicted)
	}
}

func TestRollbackCheckpointInvalidatedByDraftAndPresentationUpdates(t *testing.T) {
	tests := []struct {
		name   string
		update func(*Store, domain.Workflow) (domain.Workflow, error)
	}{
		{name: "draft", update: func(store *Store, workflow domain.Workflow) (domain.Workflow, error) {
			return store.UpdateDraft(context.Background(), workflow.ID, workflow.DraftRevision, json.RawMessage(`{"schemaVersion":1,"nodes":[],"edges":[],"edited":true}`))
		}},
		{name: "presentation", update: func(store *Store, workflow domain.Workflow) (domain.Workflow, error) {
			presentation := workflow.AgentPresentation
			presentation.Title = "已编辑"
			return store.UpdateAgentPresentation(context.Background(), workflow.ID, workflow.DraftRevision, presentation)
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := migratedTestStore(t)
			workflow, versionOne, _ := rollbackFixture(t, store, "rollback-invalidate-"+test.name)
			rolledBack, _, err := store.RollbackWorkflowDraft(context.Background(), workflow.ID, versionOne.ID, workflow.DraftRevision)
			if err != nil {
				t.Fatal(err)
			}
			edited, err := test.update(store, rolledBack)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := store.UndoWorkflowDraftRollback(context.Background(), workflow.ID, edited.DraftRevision); !errors.Is(err, domain.ErrRollbackUndoUnavailable) {
				t.Fatalf("undo after edit error=%v", err)
			}
			var count int
			if err := store.pool.QueryRow(context.Background(), "SELECT count(*) FROM workflow_draft_checkpoints WHERE workflow_id=$1", workflow.ID).Scan(&count); err != nil || count != 0 {
				t.Fatalf("checkpoint count=%d err=%v", count, err)
			}
		})
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
	if _, err := store.Publish(context.Background(), workflow.ID, workflow.DraftRevision, workflow.DraftGraph, json.RawMessage(`{"type":"object"}`), workflow.AgentPresentation); !errors.Is(err, ErrRevisionConflict) {
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

func TestAgentPresentationSaveAndPublishSnapshot(t *testing.T) {
	store := migratedTestStore(t)
	workflow := createWorkflowFixture(t, store, "presentation")
	staleRevision := workflow.DraftRevision
	next := domain.AgentPresentation{
		Title: "公开助手", Description: "公开说明", Accent: domain.AgentAccentTeal,
		SubmitLabel: "开始", ResultMode: domain.AgentResultModeJSON,
	}
	updated, err := store.UpdateAgentPresentation(context.Background(), workflow.ID, workflow.DraftRevision, next)
	if err != nil || updated.DraftRevision != workflow.DraftRevision+1 || updated.AgentPresentation != next {
		t.Fatalf("updated=%+v error=%v", updated, err)
	}
	if _, err := store.Publish(context.Background(), updated.ID, staleRevision, updated.DraftGraph, json.RawMessage(`{"type":"object"}`), updated.AgentPresentation); !errors.Is(err, domain.ErrRevisionConflict) {
		t.Fatalf("stale publish error=%v", err)
	}
	var count int
	if err := store.pool.QueryRow(context.Background(), "SELECT count(*) FROM workflow_versions WHERE workflow_id=$1", workflow.ID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("version count=%d error=%v", count, err)
	}
	version, err := store.Publish(context.Background(), updated.ID, updated.DraftRevision, updated.DraftGraph, json.RawMessage(`{"type":"object"}`), updated.AgentPresentation)
	if err != nil || version.AgentPresentation != next {
		t.Fatalf("version=%+v error=%v", version, err)
	}
	loaded, loadedVersion, err := store.GetCurrentAgentVersion(context.Background(), updated.Slug)
	if err != nil || loaded.AgentPresentation != next || loadedVersion.AgentPresentation != next {
		t.Fatalf("loaded=%+v version=%+v error=%v", loaded, loadedVersion, err)
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
	if _, err := store.FinalizeRun(context.Background(), workflowservice.RunFinalization{
		RunID: run.ID, Status: domain.RunCompleted, Output: map[string]any{"answer": "ok"}, EndedAt: now,
		TerminalEvent: domain.RunEvent{RunID: run.ID, Sequence: 1, Type: "run.completed", Output: json.RawMessage(`{"answer":"ok"}`), Timestamp: now},
		Budget:        domain.RunEventBudget{MaxEvents: 10, MaxTotalDataBytes: 1 << 20},
	}); err != nil {
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

func TestFinalizeRunCancellationWinsAndKeepsSingleTerminal(t *testing.T) {
	store := migratedTestStore(t)
	workflow := createWorkflowFixture(t, store, "finalize-cancel")
	run := newTestRun(workflow.ID, workflow.DraftRevision, workflow.DraftGraph)
	if err := store.CreateRun(context.Background(), run); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	for _, nodeRun := range []domain.NodeRun{
		{ID: fixtureUUID(), RunID: run.ID, NodeID: "running", NodeType: "fixture", Status: domain.NodeRunning, StartedAt: &now},
		{ID: fixtureUUID(), RunID: run.ID, NodeID: "completed", NodeType: "fixture", Status: domain.NodeCompleted, StartedAt: &now, EndedAt: &now},
	} {
		if err := store.UpsertNodeRun(context.Background(), nodeRun); err != nil {
			t.Fatal(err)
		}
	}
	budget := domain.RunEventBudget{MaxEvents: 10, MaxTotalDataBytes: 1 << 20}
	if err := store.PersistRunEvent(context.Background(), domain.RunEvent{
		RunID: run.ID, Sequence: 1, Type: "run.started", ActivePorts: []string{},
		InputRedactedPaths: []string{}, OutputRedactedPaths: []string{}, Timestamp: now,
	}, nil, budget); err != nil {
		t.Fatal(err)
	}
	if _, err := store.pool.Exec(context.Background(), "UPDATE runs SET status='cancelling',cancel_requested_at=$2 WHERE id=$1", run.ID, now); err != nil {
		t.Fatal(err)
	}
	finalEvent, err := store.FinalizeRun(context.Background(), workflowservice.RunFinalization{
		RunID: run.ID, Status: domain.RunCompleted, Output: map[string]any{"answer": "should-disappear"}, EndedAt: now,
		TerminalEvent: domain.RunEvent{RunID: run.ID, Sequence: 2, Type: "run.completed", Output: json.RawMessage(`{"answer":"should-disappear"}`), ActivePorts: []string{}, InputRedactedPaths: []string{}, OutputRedactedPaths: []string{}, Timestamp: now},
		Budget:        budget,
	})
	if err != nil {
		t.Fatal(err)
	}
	if finalEvent.Type != "run.cancelled" || finalEvent.Error == nil || finalEvent.Error.Code != "RUN_CANCELLED" || len(finalEvent.Output) != 0 {
		t.Fatalf("final event=%+v", finalEvent)
	}
	loaded, nodeRuns, err := store.GetRun(context.Background(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Status != domain.RunCancelled || len(loaded.Output) != 0 || loaded.Error == nil || loaded.Error.Code != "RUN_CANCELLED" {
		t.Fatalf("loaded run=%+v", loaded)
	}
	statuses := map[string]domain.NodeStatus{}
	for _, nodeRun := range nodeRuns {
		statuses[nodeRun.NodeID] = nodeRun.Status
	}
	if statuses["running"] != domain.NodeCancelled || statuses["completed"] != domain.NodeCompleted {
		t.Fatalf("node statuses=%v", statuses)
	}
	events, err := store.ListRunEvents(context.Background(), run.ID, 0, 10)
	if err != nil || len(events) != 2 || events[1].Type != "run.cancelled" {
		t.Fatalf("events=%+v err=%v", events, err)
	}
}

func TestFinalizeRunCompletionPreventsLaterCancellationAndDuplicateTerminal(t *testing.T) {
	store := migratedTestStore(t)
	workflow := createWorkflowFixture(t, store, "finalize-complete")
	run := newTestRun(workflow.ID, workflow.DraftRevision, workflow.DraftGraph)
	if err := store.CreateRun(context.Background(), run); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	budget := domain.RunEventBudget{MaxEvents: 10, MaxTotalDataBytes: 1 << 20}
	finalization := workflowservice.RunFinalization{
		RunID: run.ID, Status: domain.RunCompleted, Output: map[string]any{"answer": "ok"}, EndedAt: now,
		TerminalEvent: domain.RunEvent{RunID: run.ID, Sequence: 1, Type: "run.completed", Output: json.RawMessage(`{"answer":"ok"}`), ActivePorts: []string{}, InputRedactedPaths: []string{}, OutputRedactedPaths: []string{}, Timestamp: now},
		Budget:        budget,
	}
	first, err := store.FinalizeRun(context.Background(), finalization)
	if err != nil {
		t.Fatal(err)
	}
	command, err := store.pool.Exec(context.Background(), "UPDATE runs SET status='cancelling',cancel_requested_at=$2 WHERE id=$1 AND status IN ('running','cancelling')", run.ID, now)
	if err != nil || command.RowsAffected() != 0 {
		t.Fatalf("late cancel rows=%d err=%v", command.RowsAffected(), err)
	}
	second, err := store.FinalizeRun(context.Background(), finalization)
	if err != nil {
		t.Fatal(err)
	}
	if first.Type != "run.completed" || second.Sequence != first.Sequence || second.Type != first.Type {
		t.Fatalf("first=%+v second=%+v", first, second)
	}
	events, err := store.ListRunEvents(context.Background(), run.ID, 0, 10)
	if err != nil || len(events) != 1 || events[0].Type != "run.completed" {
		t.Fatalf("events=%+v err=%v", events, err)
	}
}

func TestFinalizeRunRejectsTerminalOutsideEventBudgetWithoutPartialState(t *testing.T) {
	store := migratedTestStore(t)
	workflow := createWorkflowFixture(t, store, "finalize-budget")
	run := newTestRun(workflow.ID, workflow.DraftRevision, workflow.DraftGraph)
	if err := store.CreateRun(context.Background(), run); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	_, err := store.FinalizeRun(context.Background(), workflowservice.RunFinalization{
		RunID: run.ID, Status: domain.RunCompleted, EndedAt: now,
		TerminalEvent: domain.RunEvent{RunID: run.ID, Sequence: 1, Type: "run.completed", Timestamp: now},
		Budget:        domain.RunEventBudget{MaxEvents: 0, MaxTotalDataBytes: 1 << 20},
	})
	if !errors.Is(err, domain.ErrRunEventBudgetExceeded) {
		t.Fatalf("budget error=%v", err)
	}
	loaded, _, getErr := store.GetRun(context.Background(), run.ID)
	if getErr != nil || loaded.Status != domain.RunRunning || loaded.EndedAt != nil {
		t.Fatalf("partially finalized run=%+v err=%v", loaded, getErr)
	}
	events, listErr := store.ListRunEvents(context.Background(), run.ID, 0, 10)
	if listErr != nil || len(events) != 0 {
		t.Fatalf("partial terminal events=%+v err=%v", events, listErr)
	}
}

func TestRunSummaryFiltersModesStatusesTimeAndExactID(t *testing.T) {
	store := migratedTestStore(t)
	firstWorkflow := createWorkflowFixture(t, store, "run-summary-first")
	secondWorkflow := createWorkflowFixture(t, store, "run-summary-second")
	version := publishFixture(t, store, firstWorkflow)

	source := newTestRun(firstWorkflow.ID, firstWorkflow.DraftRevision, firstWorkflow.DraftGraph)
	if err := store.CreateRun(context.Background(), source); err != nil {
		t.Fatal(err)
	}
	published := newPublishedRun(firstWorkflow.ID, version.ID)
	if err := store.CreateRun(context.Background(), published); err != nil {
		t.Fatal(err)
	}
	sourceNodeID := "echo-1"
	debug := domain.Run{
		ID: fixtureUUID(), WorkflowID: firstWorkflow.ID, GraphSnapshot: firstWorkflow.DraftGraph,
		SourceRunID: &source.ID, SourceNodeID: &sourceNodeID, Mode: domain.RunModeDebug,
		Status: domain.RunRunning, Input: json.RawMessage(`{}`), StartedAt: time.Now().UTC(),
	}
	if err := store.CreateRun(context.Background(), debug); err != nil {
		t.Fatal(err)
	}
	second := newTestRun(secondWorkflow.ID, secondWorkflow.DraftRevision, secondWorkflow.DraftGraph)
	if err := store.CreateRun(context.Background(), second); err != nil {
		t.Fatal(err)
	}

	base := time.Date(2026, 8, 25, 0, 0, 0, 0, time.UTC)
	updates := []struct {
		runID  string
		status domain.RunStatus
		at     time.Time
	}{
		{runID: source.ID, status: domain.RunRunning, at: base.Add(time.Hour)},
		{runID: second.ID, status: domain.RunCancelled, at: base.Add(2 * time.Hour)},
		{runID: debug.ID, status: domain.RunFailed, at: base.Add(3 * time.Hour)},
		{runID: published.ID, status: domain.RunCompleted, at: base.Add(4 * time.Hour)},
	}
	for _, update := range updates {
		if _, err := store.pool.Exec(context.Background(), "UPDATE runs SET status=$2,started_at=$3 WHERE id=$1", update.runID, update.status, update.at); err != nil {
			t.Fatal(err)
		}
	}

	all, err := store.ListRunSummaries(context.Background(), workflowservice.RunSummaryStoreQuery{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 4 || all[0].ID != published.ID || all[1].ID != debug.ID || all[2].ID != second.ID || all[3].ID != source.ID {
		t.Fatalf("all=%+v", all)
	}
	if all[0].WorkflowVersionID == nil || *all[0].WorkflowVersionID != version.ID || all[0].WorkflowVersion == nil || *all[0].WorkflowVersion != 1 || all[0].StartedAt.Location() != time.UTC {
		t.Fatalf("published summary=%+v", all[0])
	}

	firstOnly, err := store.ListRunSummaries(context.Background(), workflowservice.RunSummaryStoreQuery{WorkflowID: firstWorkflow.ID, Limit: 10})
	if err != nil || len(firstOnly) != 3 {
		t.Fatalf("workflow rows=%+v error=%v", firstOnly, err)
	}
	statusRows, err := store.ListRunSummaries(context.Background(), workflowservice.RunSummaryStoreQuery{
		Statuses: []domain.RunStatus{domain.RunFailed, domain.RunCancelled}, Limit: 10,
	})
	if err != nil || len(statusRows) != 2 || statusRows[0].ID != debug.ID || statusRows[1].ID != second.ID {
		t.Fatalf("status rows=%+v error=%v", statusRows, err)
	}
	modeRows, err := store.ListRunSummaries(context.Background(), workflowservice.RunSummaryStoreQuery{
		Modes: []domain.RunMode{domain.RunModeTest}, Limit: 10,
	})
	if err != nil || len(modeRows) != 2 || modeRows[0].ID != second.ID || modeRows[1].ID != source.ID {
		t.Fatalf("mode rows=%+v error=%v", modeRows, err)
	}
	after, before := base.Add(2*time.Hour), base.Add(4*time.Hour)
	timeRows, err := store.ListRunSummaries(context.Background(), workflowservice.RunSummaryStoreQuery{
		StartedAfter: &after, StartedBefore: &before, Limit: 10,
	})
	if err != nil || len(timeRows) != 2 || timeRows[0].ID != debug.ID || timeRows[1].ID != second.ID {
		t.Fatalf("time rows=%+v error=%v", timeRows, err)
	}
	exactMismatch, err := store.ListRunSummaries(context.Background(), workflowservice.RunSummaryStoreQuery{
		WorkflowID: secondWorkflow.ID, RunID: published.ID, Limit: 10,
	})
	if err != nil || exactMismatch == nil || len(exactMismatch) != 0 {
		t.Fatalf("exact mismatch=%+v error=%v", exactMismatch, err)
	}
	afterStarted := base.Add(3 * time.Hour)
	next, err := store.ListRunSummaries(context.Background(), workflowservice.RunSummaryStoreQuery{
		AfterStarted: &afterStarted, AfterID: debug.ID, Limit: 10,
	})
	if err != nil || len(next) != 2 || next[0].ID != second.ID || next[1].ID != source.ID {
		t.Fatalf("next=%+v error=%v", next, err)
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

func TestCreateAgentRunIsIdempotentAndScoped(t *testing.T) {
	store := migratedTestStore(t)
	workflow := createWorkflowFixture(t, store, "agent-idempotent")
	version := publishFixture(t, store, workflow)
	key := fixtureUUID()
	run := newPublishedRun(workflow.ID, version.ID)
	run.AgentRequestKey = &key

	first, created, err := store.CreateAgentRun(context.Background(), run)
	if err != nil || !created || first.ID != run.ID {
		t.Fatalf("first=%+v created=%v error=%v", first, created, err)
	}
	duplicate := newPublishedRun(workflow.ID, version.ID)
	duplicate.AgentRequestKey = &key
	second, created, err := store.CreateAgentRun(context.Background(), duplicate)
	if err != nil || created || second.ID != run.ID {
		t.Fatalf("second=%+v created=%v error=%v", second, created, err)
	}
	record, err := store.FindAgentRunByRequestKey(context.Background(), workflow.Slug, key)
	if err != nil || record.Run.ID != run.ID || record.Version.ID != version.ID || record.Events == nil {
		t.Fatalf("record=%+v error=%v", record, err)
	}
}

func TestArchivedAgentRunIsNotPubliclyAccessible(t *testing.T) {
	store := migratedTestStore(t)
	workflow := createWorkflowFixture(t, store, "agent-archived-public")
	version := publishFixture(t, store, workflow)
	key := fixtureUUID()
	run := newPublishedRun(workflow.ID, version.ID)
	run.AgentRequestKey = &key
	if _, _, err := store.CreateAgentRun(context.Background(), run); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ArchiveWorkflow(context.Background(), workflow.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.FindAgentRunByRequestKey(context.Background(), workflow.Slug, key); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("find error=%v", err)
	}
	if _, err := store.GetAgentRun(context.Background(), workflow.Slug, run.ID, 0, 10); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("view error=%v", err)
	}
	if _, err := store.RequestAgentRunCancel(context.Background(), workflow.Slug, run.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("cancel error=%v", err)
	}
}

func TestCreateAgentRunConcurrentRequestsCreateExactlyOnce(t *testing.T) {
	store := migratedTestStore(t)
	workflow := createWorkflowFixture(t, store, "agent-concurrent")
	version := publishFixture(t, store, workflow)
	key := fixtureUUID()
	type result struct {
		run     domain.Run
		created bool
		err     error
	}
	start := make(chan struct{})
	results := make(chan result, 2)
	var wait sync.WaitGroup
	wait.Add(2)
	for range 2 {
		go func() {
			defer wait.Done()
			<-start
			run := newPublishedRun(workflow.ID, version.ID)
			run.AgentRequestKey = &key
			createdRun, created, err := store.CreateAgentRun(context.Background(), run)
			results <- result{run: createdRun, created: created, err: err}
		}()
	}
	close(start)
	wait.Wait()
	close(results)
	createdCount := 0
	var runID string
	for item := range results {
		if item.err != nil {
			t.Fatal(item.err)
		}
		if item.created {
			createdCount++
		}
		if runID == "" {
			runID = item.run.ID
		} else if item.run.ID != runID {
			t.Fatalf("different run IDs: %s and %s", runID, item.run.ID)
		}
	}
	if createdCount != 1 {
		t.Fatalf("created count=%d", createdCount)
	}
	var count int
	if err := store.pool.QueryRow(context.Background(), "SELECT count(*) FROM runs WHERE workflow_id=$1 AND agent_request_key=$2", workflow.ID, key).Scan(&count); err != nil || count != 1 {
		t.Fatalf("count=%d error=%v", count, err)
	}
}

func TestGetAgentRunUsesConsistentEventPage(t *testing.T) {
	store := migratedTestStore(t)
	workflow := createWorkflowFixture(t, store, "agent-snapshot")
	otherWorkflow := createWorkflowFixture(t, store, "agent-snapshot-other")
	version := publishFixture(t, store, workflow)
	key := fixtureUUID()
	run := newPublishedRun(workflow.ID, version.ID)
	run.AgentRequestKey = &key
	if _, created, err := store.CreateAgentRun(context.Background(), run); err != nil || !created {
		t.Fatalf("create run created=%v error=%v", created, err)
	}
	budget := domain.RunEventBudget{MaxEvents: 10, MaxTotalDataBytes: 1 << 20}
	for sequence := int64(1); sequence <= 3; sequence++ {
		event := domain.RunEvent{
			RunID: run.ID, Sequence: sequence, Type: "node.completed", NodeID: fmt.Sprintf("node-%d", sequence),
			Input: json.RawMessage(fmt.Sprintf(`{"input":%d}`, sequence)), Output: json.RawMessage(fmt.Sprintf(`{"output":%d}`, sequence)),
			Timestamp: time.Now().UTC(),
		}
		if err := store.PersistRunEvent(context.Background(), event, nil, budget); err != nil {
			t.Fatal(err)
		}
	}
	record, err := store.GetAgentRun(context.Background(), workflow.Slug, run.ID, 0, 2)
	if err != nil || len(record.Events) != 2 || record.Events[0].Sequence != 1 || record.Events[1].Sequence != 2 || !record.HasMore {
		t.Fatalf("record=%+v error=%v", record, err)
	}
	if _, err := store.GetAgentRun(context.Background(), otherWorkflow.Slug, run.ID, 0, 2); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("other slug error=%v", err)
	}

	for iteration := 0; iteration < 20; iteration++ {
		iterationKey := fixtureUUID()
		candidate := newPublishedRun(workflow.ID, version.ID)
		candidate.AgentRequestKey = &iterationKey
		if _, _, err := store.CreateAgentRun(context.Background(), candidate); err != nil {
			t.Fatal(err)
		}
		start := make(chan struct{})
		finalized := make(chan error, 1)
		go func() {
			<-start
			now := time.Now().UTC()
			_, err := store.FinalizeRun(context.Background(), workflowservice.RunFinalization{
				RunID: candidate.ID, Status: domain.RunCompleted, Output: json.RawMessage(`{"answer":"ok"}`), EndedAt: now,
				TerminalEvent: domain.RunEvent{RunID: candidate.ID, Sequence: 1, Type: "run.completed", Output: json.RawMessage(`{"answer":"ok"}`), Timestamp: now},
				Budget:        domain.RunEventBudget{MaxEvents: 2, MaxTotalDataBytes: 1 << 20},
			})
			finalized <- err
		}()
		close(start)
		snapshot, getErr := store.GetAgentRun(context.Background(), workflow.Slug, candidate.ID, 0, 10)
		if getErr != nil {
			t.Fatal(getErr)
		}
		switch snapshot.Run.Status {
		case domain.RunRunning:
			if len(snapshot.Events) != 0 {
				t.Fatalf("running snapshot contains terminal events: %+v", snapshot)
			}
		case domain.RunCompleted:
			if len(snapshot.Events) != 1 || snapshot.Events[0].Type != "run.completed" {
				t.Fatalf("terminal snapshot misses terminal event: %+v", snapshot)
			}
		default:
			t.Fatalf("unexpected snapshot status: %+v", snapshot)
		}
		if err := <-finalized; err != nil {
			t.Fatal(err)
		}
	}
}

func TestRequestAgentRunCancelIsIdempotent(t *testing.T) {
	store := migratedTestStore(t)
	workflow := createWorkflowFixture(t, store, "agent-cancel")
	version := publishFixture(t, store, workflow)
	key := fixtureUUID()
	run := newPublishedRun(workflow.ID, version.ID)
	run.AgentRequestKey = &key
	if _, _, err := store.CreateAgentRun(context.Background(), run); err != nil {
		t.Fatal(err)
	}
	first, err := store.RequestAgentRunCancel(context.Background(), workflow.Slug, run.ID)
	if err != nil || first.Run.Status != domain.RunCancelling || first.Run.CancelRequestedAt == nil {
		t.Fatalf("first=%+v error=%v", first, err)
	}
	second, err := store.RequestAgentRunCancel(context.Background(), workflow.Slug, run.ID)
	if err != nil || second.Run.Status != domain.RunCancelling {
		t.Fatalf("second=%+v error=%v", second, err)
	}
	now := time.Now().UTC()
	if _, err := store.FinalizeRun(context.Background(), workflowservice.RunFinalization{
		RunID: run.ID, Status: domain.RunCancelled, Error: domain.NewPublicRunError(context.Canceled), EndedAt: now,
		TerminalEvent: domain.RunEvent{RunID: run.ID, Sequence: 1, Type: "run.cancelled", Error: domain.NewPublicRunError(context.Canceled), Timestamp: now},
		Budget:        domain.RunEventBudget{MaxEvents: 2, MaxTotalDataBytes: 1 << 20},
	}); err != nil {
		t.Fatal(err)
	}
	terminal, err := store.RequestAgentRunCancel(context.Background(), workflow.Slug, run.ID)
	if err != nil || terminal.Run.Status != domain.RunCancelled || len(terminal.Events) != 1 {
		t.Fatalf("terminal=%+v error=%v", terminal, err)
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
		ID:                fixtureUUID(),
		Name:              "测试工作流 " + suffix,
		Slug:              fmt.Sprintf("workflow-%s-%d", suffix, sequence),
		Description:       "集成测试",
		AgentPresentation: workflowservice.DefaultAgentPresentation("测试工作流 "+suffix, "集成测试"),
		DraftGraph:        json.RawMessage(`{"schemaVersion":1,"nodes":[],"edges":[]}`),
		DraftRevision:     1,
	})
	if err != nil {
		t.Fatal(err)
	}
	return workflow
}

func publishFixture(t *testing.T, store *Store, workflow domain.Workflow) domain.WorkflowVersion {
	t.Helper()
	version, err := store.Publish(context.Background(), workflow.ID, workflow.DraftRevision, workflow.DraftGraph, json.RawMessage(`{"type":"object"}`), workflow.AgentPresentation)
	if err != nil {
		t.Fatal(err)
	}
	return version
}

func rollbackFixture(t *testing.T, store *Store, suffix string) (domain.Workflow, domain.WorkflowVersion, domain.WorkflowVersion) {
	t.Helper()
	workflow := createWorkflowFixture(t, store, suffix)
	versionOne := publishFixture(t, store, workflow)
	versionTwoGraph := json.RawMessage(`{"schemaVersion":1,"nodes":[],"edges":[],"version":2}`)
	updated, err := store.UpdateDraft(context.Background(), workflow.ID, workflow.DraftRevision, versionTwoGraph)
	if err != nil {
		t.Fatal(err)
	}
	versionTwoPresentation := updated.AgentPresentation
	versionTwoPresentation.Title = "版本二"
	updated, err = store.UpdateAgentPresentation(context.Background(), workflow.ID, updated.DraftRevision, versionTwoPresentation)
	if err != nil {
		t.Fatal(err)
	}
	versionTwo := publishFixture(t, store, updated)
	draftGraph := json.RawMessage(`{"schemaVersion":1,"nodes":[],"edges":[],"draft":3}`)
	current, err := store.UpdateDraft(context.Background(), workflow.ID, updated.DraftRevision, draftGraph)
	if err != nil {
		t.Fatal(err)
	}
	currentPresentation := current.AgentPresentation
	currentPresentation.Title = "当前草稿"
	current, err = store.UpdateAgentPresentation(context.Background(), workflow.ID, current.DraftRevision, currentPresentation)
	if err != nil {
		t.Fatal(err)
	}
	return current, versionOne, versionTwo
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
