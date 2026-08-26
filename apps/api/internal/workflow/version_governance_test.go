package workflow

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/yyl1212/agent-studio/apps/api/internal/domain"
	"github.com/yyl1212/agent-studio/apps/api/internal/engine"
	"github.com/yyl1212/agent-studio/internal/nodepackage"
	"github.com/yyl1212/agent-studio/sdk/go/agentnode"
)

const versionGovernanceWorkflowID = "11111111-1111-4111-8111-111111111111"

func TestVersionGovernanceListPaginatesWithoutSkipping(t *testing.T) {
	checkpoint := &domain.RollbackCheckpointSummary{SourceRevision: 7, RestoredRevision: 8, RestoredFromVersion: 1}
	store := &versionGovernanceFixtureStore{
		workflow: domain.Workflow{ID: versionGovernanceWorkflowID, DraftRevision: 8},
		versions: []domain.WorkflowVersionSummary{
			{ID: "v3", Version: 3, Current: true, CreatedAt: time.Unix(3, 0).UTC()},
			{ID: "v2", Version: 2, CreatedAt: time.Unix(2, 0).UTC()},
			{ID: "v1", Version: 1, CreatedAt: time.Unix(1, 0).UTC()},
		},
		checkpoint: checkpoint,
	}
	service := NewVersionGovernanceService(store, nil, emptyVersionCatalog{})

	first, err := service.List(context.Background(), versionGovernanceWorkflowID, WorkflowVersionListRequest{Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Items) != 2 || first.Items[0].Version != 3 || first.Items[1].Version != 2 || first.NextCursor == nil || first.RollbackCheckpoint != checkpoint {
		t.Fatalf("first=%+v", first)
	}
	if store.beforeVersion != 0 || store.limit != 3 {
		t.Fatalf("store before=%d limit=%d", store.beforeVersion, store.limit)
	}
	before, err := decodeVersionCursor(*first.NextCursor, versionGovernanceWorkflowID)
	if err != nil || before != 2 {
		t.Fatalf("cursor before=%d err=%v", before, err)
	}

	second, err := service.List(context.Background(), versionGovernanceWorkflowID, WorkflowVersionListRequest{Limit: 2, Cursor: *first.NextCursor})
	if err != nil || len(second.Items) != 1 || second.Items[0].Version != 1 || second.NextCursor != nil {
		t.Fatalf("second=%+v err=%v", second, err)
	}
}

func TestVersionGovernanceListDefaultsAndRejectsInvalidRequestsBeforeStore(t *testing.T) {
	store := &versionGovernanceFixtureStore{workflow: domain.Workflow{ID: versionGovernanceWorkflowID}}
	service := NewVersionGovernanceService(store, nil, emptyVersionCatalog{})

	page, err := service.List(context.Background(), versionGovernanceWorkflowID, WorkflowVersionListRequest{})
	if err != nil || page.Items == nil || len(page.Items) != 0 || store.limit != 21 {
		t.Fatalf("page=%+v limit=%d err=%v", page, store.limit, err)
	}
	for _, request := range []WorkflowVersionListRequest{
		{Limit: -1},
		{Limit: 101},
		{Limit: 20, Cursor: "invalid"},
	} {
		calls := store.listCalls
		_, err := service.List(context.Background(), versionGovernanceWorkflowID, request)
		if !errors.Is(err, ErrInvalidWorkflowInput) && !errors.Is(err, ErrCursorInvalid) {
			t.Fatalf("request=%+v err=%v", request, err)
		}
		if store.listCalls != calls {
			t.Fatalf("invalid request reached store: before=%d after=%d", calls, store.listCalls)
		}
	}
}

func TestVersionGovernanceRollbackCompilesTargetBeforeStoreMutation(t *testing.T) {
	store := snapshotFixtureStore(snapshotGraph("text", "null"), snapshotTextInputSchema())
	compiler := &versionGovernanceFixtureCompiler{issues: []domain.ValidationIssue{{Code: "NODE_TYPE_NOT_FOUND", NodeID: "old"}}}
	service := NewVersionGovernanceService(store, compiler, emptyVersionCatalog{})

	_, err := service.Rollback(context.Background(), store.workflow.ID, WorkflowRollbackInput{
		TargetVersion: 1, ExpectedDraftRevision: store.workflow.DraftRevision,
	})
	if !errors.Is(err, domain.ErrWorkflowSnapshotUnsupported) || store.rollbackCalls != 0 || compiler.calls != 1 {
		t.Fatalf("err=%v rollbackCalls=%d compilerCalls=%d", err, store.rollbackCalls, compiler.calls)
	}
}

func TestVersionGovernanceRollbackUsesVersionIDAndReturnsCheckpoint(t *testing.T) {
	store := snapshotFixtureStore(snapshotGraph("text", "null"), snapshotTextInputSchema())
	checkpoint := domain.RollbackCheckpointSummary{SourceRevision: 8, RestoredRevision: 9, RestoredFromVersion: 1}
	store.rollbackWorkflow = domain.Workflow{ID: store.workflow.ID, DraftRevision: 9}
	store.rollbackCheckpoint = checkpoint
	compiler := &versionGovernanceFixtureCompiler{}

	result, err := NewVersionGovernanceService(store, compiler, emptyVersionCatalog{}).Rollback(
		context.Background(), store.workflow.ID,
		WorkflowRollbackInput{TargetVersion: 1, ExpectedDraftRevision: store.workflow.DraftRevision},
	)
	if err != nil || result.Workflow.DraftRevision != 9 || result.RollbackCheckpoint != checkpoint {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	if store.rollbackVersionID != store.version.ID || store.rollbackRevision != store.workflow.DraftRevision || compiler.calls != 1 {
		t.Fatalf("versionID=%q revision=%d compilerCalls=%d", store.rollbackVersionID, store.rollbackRevision, compiler.calls)
	}
}

func TestVersionGovernanceRollbackAndUndoValidateAndPreserveStoreErrors(t *testing.T) {
	store := snapshotFixtureStore(snapshotGraph("text", "null"), snapshotTextInputSchema())
	service := NewVersionGovernanceService(store, &versionGovernanceFixtureCompiler{}, emptyVersionCatalog{})
	for _, input := range []WorkflowRollbackInput{
		{TargetVersion: 0, ExpectedDraftRevision: store.workflow.DraftRevision},
		{TargetVersion: 1, ExpectedDraftRevision: 0},
	} {
		getCalls, rollbackCalls := store.getVersionCalls, store.rollbackCalls
		if _, err := service.Rollback(context.Background(), store.workflow.ID, input); !errors.Is(err, ErrInvalidWorkflowInput) {
			t.Fatalf("input=%+v err=%v", input, err)
		}
		if store.getVersionCalls != getCalls || store.rollbackCalls != rollbackCalls {
			t.Fatalf("invalid input reached store: %+v", input)
		}
	}
	if _, err := service.Rollback(context.Background(), store.workflow.ID, WorkflowRollbackInput{
		TargetVersion: 2, ExpectedDraftRevision: store.workflow.DraftRevision,
	}); !errors.Is(err, domain.ErrWorkflowVersionNotFound) {
		t.Fatalf("missing version err=%v", err)
	}

	store.rollbackErr = domain.ErrWorkflowArchived
	if _, err := service.Rollback(context.Background(), store.workflow.ID, WorkflowRollbackInput{
		TargetVersion: 1, ExpectedDraftRevision: store.workflow.DraftRevision,
	}); !errors.Is(err, domain.ErrWorkflowArchived) {
		t.Fatalf("archived rollback err=%v", err)
	}
	store.undoErr = domain.ErrRollbackUndoUnavailable
	if _, err := service.Undo(context.Background(), store.workflow.ID, store.workflow.DraftRevision); !errors.Is(err, domain.ErrRollbackUndoUnavailable) {
		t.Fatalf("undo err=%v", err)
	}
	if store.undoRevision != store.workflow.DraftRevision {
		t.Fatalf("undo revision=%d", store.undoRevision)
	}
	undoCalls := store.undoCalls
	if _, err := service.Undo(context.Background(), store.workflow.ID, 0); !errors.Is(err, ErrInvalidWorkflowInput) || store.undoCalls != undoCalls {
		t.Fatalf("invalid undo err=%v calls=%d", err, store.undoCalls)
	}
}

type versionGovernanceFixtureStore struct {
	workflow           domain.Workflow
	version            domain.WorkflowVersion
	versions           []domain.WorkflowVersionSummary
	checkpoint         *domain.RollbackCheckpointSummary
	beforeVersion      int
	limit              int
	listCalls          int
	getVersionCalls    int
	rollbackCalls      int
	rollbackVersionID  string
	rollbackRevision   int64
	rollbackWorkflow   domain.Workflow
	rollbackCheckpoint domain.RollbackCheckpointSummary
	rollbackErr        error
	undoCalls          int
	undoRevision       int64
	undoWorkflow       domain.Workflow
	undoErr            error
}

func (store *versionGovernanceFixtureStore) GetWorkflow(context.Context, string) (domain.Workflow, error) {
	return store.workflow, nil
}

func (store *versionGovernanceFixtureStore) GetWorkflowVersionByNumber(_ context.Context, workflowID string, version int) (domain.WorkflowVersion, error) {
	store.getVersionCalls++
	if store.version.WorkflowID != workflowID || store.version.Version != version {
		return domain.WorkflowVersion{}, domain.ErrWorkflowVersionNotFound
	}
	return store.version, nil
}

func (store *versionGovernanceFixtureStore) ListWorkflowVersions(_ context.Context, _ string, beforeVersion, limit int) (VersionListRows, error) {
	store.beforeVersion = beforeVersion
	store.limit = limit
	store.listCalls++
	items := make([]domain.WorkflowVersionSummary, 0, limit)
	for _, item := range store.versions {
		if beforeVersion != 0 && item.Version >= beforeVersion {
			continue
		}
		if len(items) == limit {
			break
		}
		items = append(items, item)
	}
	return VersionListRows{Items: items, Checkpoint: store.checkpoint}, nil
}

func (store *versionGovernanceFixtureStore) RollbackWorkflowDraft(_ context.Context, _ string, versionID string, revision int64) (domain.Workflow, domain.RollbackCheckpointSummary, error) {
	store.rollbackCalls++
	store.rollbackVersionID = versionID
	store.rollbackRevision = revision
	return store.rollbackWorkflow, store.rollbackCheckpoint, store.rollbackErr
}

func (store *versionGovernanceFixtureStore) UndoWorkflowDraftRollback(_ context.Context, _ string, revision int64) (domain.Workflow, error) {
	store.undoCalls++
	store.undoRevision = revision
	return store.undoWorkflow, store.undoErr
}

type versionGovernanceFixtureCompiler struct {
	calls  int
	graph  domain.Graph
	issues []domain.ValidationIssue
}

func (compiler *versionGovernanceFixtureCompiler) Compile(graph domain.Graph) (*engine.Plan, []domain.ValidationIssue) {
	compiler.calls++
	compiler.graph = graph
	return nil, compiler.issues
}

type emptyVersionCatalog struct{}

func (emptyVersionCatalog) Definitions() []agentnode.Definition {
	return nil
}

func (emptyVersionCatalog) PackageFor(string, string) (nodepackage.Summary, bool) {
	return nodepackage.Summary{}, false
}
