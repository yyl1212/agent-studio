package workflow

import (
	"context"

	"github.com/yyl1212/agent-studio/apps/api/internal/domain"
	"github.com/yyl1212/agent-studio/apps/api/internal/workflowtemplate"
	"github.com/yyl1212/agent-studio/sdk/go/agentnode"
)

type WorkflowVersionListRequest struct {
	Cursor string
	Limit  int
}

type WorkflowDiffRequest struct {
	Base    domain.WorkflowSnapshotRef `json:"base"`
	Compare domain.WorkflowSnapshotRef `json:"compare"`
}

type WorkflowRollbackInput struct {
	TargetVersion         int   `json:"targetVersion"`
	ExpectedDraftRevision int64 `json:"expectedDraftRevision"`
}

type WorkflowRollbackResult struct {
	Workflow           domain.Workflow                  `json:"workflow"`
	RollbackCheckpoint domain.RollbackCheckpointSummary `json:"rollbackCheckpoint"`
}

type WorkflowRollbackUndoInput struct {
	ExpectedDraftRevision int64 `json:"expectedDraftRevision"`
}

type VersionListRows struct {
	Items      []domain.WorkflowVersionSummary
	Checkpoint *domain.RollbackCheckpointSummary
}

type VersionGovernanceStore interface {
	GetWorkflow(context.Context, string) (domain.Workflow, error)
	GetWorkflowVersionByNumber(context.Context, string, int) (domain.WorkflowVersion, error)
	ListWorkflowVersions(context.Context, string, int, int) (VersionListRows, error)
	RollbackWorkflowDraft(context.Context, string, string, int64) (domain.Workflow, domain.RollbackCheckpointSummary, error)
	UndoWorkflowDraftRollback(context.Context, string, int64) (domain.Workflow, error)
}

type VersionGovernanceService struct {
	store       VersionGovernanceStore
	compiler    Compiler
	definitions []agentnode.Definition
}

func NewVersionGovernanceService(store VersionGovernanceStore, compiler Compiler, catalog workflowtemplate.NodeCatalog) *VersionGovernanceService {
	definitions := []agentnode.Definition{}
	if catalog != nil {
		definitions = append(definitions, catalog.Definitions()...)
	}
	return &VersionGovernanceService{store: store, compiler: compiler, definitions: definitions}
}

func (service *VersionGovernanceService) List(ctx context.Context, workflowID string, request WorkflowVersionListRequest) (domain.WorkflowVersionPage, error) {
	limit := request.Limit
	if limit == 0 {
		limit = 20
	}
	if limit < 1 || limit > 100 {
		return domain.WorkflowVersionPage{}, ErrInvalidWorkflowInput
	}
	beforeVersion := 0
	if request.Cursor != "" {
		decoded, err := decodeVersionCursor(request.Cursor, workflowID)
		if err != nil {
			return domain.WorkflowVersionPage{}, err
		}
		beforeVersion = decoded
	}
	rows, err := service.store.ListWorkflowVersions(ctx, workflowID, beforeVersion, limit+1)
	if err != nil {
		return domain.WorkflowVersionPage{}, err
	}
	items := append([]domain.WorkflowVersionSummary(nil), rows.Items...)
	if items == nil {
		items = []domain.WorkflowVersionSummary{}
	}
	var nextCursor *string
	if len(items) > limit {
		items = items[:limit]
		raw, err := encodeVersionCursor(workflowID, items[len(items)-1].Version)
		if err != nil {
			return domain.WorkflowVersionPage{}, err
		}
		nextCursor = &raw
	}
	return domain.WorkflowVersionPage{
		Items: items, NextCursor: nextCursor, RollbackCheckpoint: rows.Checkpoint,
	}, nil
}

func (service *VersionGovernanceService) Diff(ctx context.Context, workflowID string, request WorkflowDiffRequest) (domain.WorkflowDiff, error) {
	base, err := service.loadSnapshot(ctx, workflowID, request.Base)
	if err != nil {
		return domain.WorkflowDiff{}, err
	}
	compare, err := service.loadSnapshot(ctx, workflowID, request.Compare)
	if err != nil {
		return domain.WorkflowDiff{}, err
	}
	return newSemanticDiffEngine(service.definitions).Diff(base, compare)
}

func (service *VersionGovernanceService) Rollback(ctx context.Context, workflowID string, input WorkflowRollbackInput) (WorkflowRollbackResult, error) {
	if input.TargetVersion <= 0 || input.ExpectedDraftRevision <= 0 {
		return WorkflowRollbackResult{}, ErrInvalidWorkflowInput
	}
	targetVersion := input.TargetVersion
	snapshot, err := service.loadSnapshot(ctx, workflowID, domain.WorkflowSnapshotRef{
		Kind: domain.WorkflowSnapshotVersion, Version: &targetVersion,
	})
	if err != nil {
		return WorkflowRollbackResult{}, err
	}
	if service.compiler == nil {
		return WorkflowRollbackResult{}, domain.ErrWorkflowSnapshotUnsupported
	}
	if _, issues := service.compiler.Compile(snapshot.Graph); len(issues) > 0 {
		return WorkflowRollbackResult{}, domain.ErrWorkflowSnapshotUnsupported
	}
	if snapshot.Descriptor.VersionID == nil {
		return WorkflowRollbackResult{}, domain.ErrWorkflowSnapshotUnsupported
	}
	workflow, checkpoint, err := service.store.RollbackWorkflowDraft(
		ctx, workflowID, *snapshot.Descriptor.VersionID, input.ExpectedDraftRevision,
	)
	if err != nil {
		return WorkflowRollbackResult{}, err
	}
	return WorkflowRollbackResult{Workflow: workflow, RollbackCheckpoint: checkpoint}, nil
}

func (service *VersionGovernanceService) Undo(ctx context.Context, workflowID string, expectedRevision int64) (domain.Workflow, error) {
	if expectedRevision <= 0 {
		return domain.Workflow{}, ErrInvalidWorkflowInput
	}
	return service.store.UndoWorkflowDraftRollback(ctx, workflowID, expectedRevision)
}
