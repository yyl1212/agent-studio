package workflow

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/yyl1212/agent-studio/apps/api/internal/domain"
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

type versionGovernanceFixtureStore struct {
	workflow      domain.Workflow
	versions      []domain.WorkflowVersionSummary
	checkpoint    *domain.RollbackCheckpointSummary
	beforeVersion int
	limit         int
	listCalls     int
}

func (store *versionGovernanceFixtureStore) GetWorkflow(context.Context, string) (domain.Workflow, error) {
	return store.workflow, nil
}

func (store *versionGovernanceFixtureStore) GetWorkflowVersionByNumber(context.Context, string, int) (domain.WorkflowVersion, error) {
	return domain.WorkflowVersion{}, domain.ErrWorkflowVersionNotFound
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

func (store *versionGovernanceFixtureStore) RollbackWorkflowDraft(context.Context, string, string, int64) (domain.Workflow, domain.RollbackCheckpointSummary, error) {
	return domain.Workflow{}, domain.RollbackCheckpointSummary{}, errors.New("not implemented")
}

func (store *versionGovernanceFixtureStore) UndoWorkflowDraftRollback(context.Context, string, int64) (domain.Workflow, error) {
	return domain.Workflow{}, errors.New("not implemented")
}

type emptyVersionCatalog struct{}

func (emptyVersionCatalog) Definitions() []agentnode.Definition {
	return nil
}

func (emptyVersionCatalog) PackageFor(string, string) (nodepackage.Summary, bool) {
	return nodepackage.Summary{}, false
}
