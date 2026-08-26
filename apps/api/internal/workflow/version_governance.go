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
