package workflow

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/yyl1212/agent-studio/apps/api/internal/domain"
)

const defaultManagementPageLimit = 50
const maxManagementPageLimit = 100
const maxWorkflowSearchBytes = 100

type workflowSummaryFilter struct {
	Query string        `json:"q"`
	State WorkflowState `json:"state"`
}

type WorkflowManagementService struct {
	store WorkflowManagementStore
}

func NewWorkflowManagementService(store WorkflowManagementStore) *WorkflowManagementService {
	return &WorkflowManagementService{store: store}
}

func (service *WorkflowManagementService) List(ctx context.Context, request WorkflowSummaryRequest) (WorkflowSummaryPage, error) {
	if len([]byte(request.Text)) > maxWorkflowSearchBytes {
		return WorkflowSummaryPage{}, ErrInvalidWorkflowInput
	}
	queryText := strings.TrimSpace(request.Text)
	if len([]byte(queryText)) > maxWorkflowSearchBytes {
		return WorkflowSummaryPage{}, ErrInvalidWorkflowInput
	}
	state := request.State
	if state == "" {
		state = WorkflowStateActive
	}
	if state != WorkflowStateActive && state != WorkflowStateArchived && state != WorkflowStateAll {
		return WorkflowSummaryPage{}, ErrInvalidWorkflowInput
	}
	limit := request.Limit
	if limit == 0 {
		limit = defaultManagementPageLimit
	}
	if limit < 1 || limit > maxManagementPageLimit {
		return WorkflowSummaryPage{}, ErrInvalidWorkflowInput
	}

	fingerprint := filterFingerprint(workflowSummaryFilter{Query: queryText, State: state})
	storeQuery := WorkflowSummaryStoreQuery{Text: queryText, State: state, Limit: limit + 1}
	if request.Cursor != "" {
		afterUpdated, afterID, err := decodePageCursor(request.Cursor, fingerprint)
		if err != nil {
			return WorkflowSummaryPage{}, err
		}
		storeQuery.AfterUpdated = &afterUpdated
		storeQuery.AfterID = afterID
	}

	rows, err := service.store.ListWorkflowSummaries(ctx, storeQuery)
	if err != nil {
		return WorkflowSummaryPage{}, fmt.Errorf("list workflow summaries: %w", err)
	}
	visible := len(rows)
	if visible > limit {
		visible = limit
	}
	items := make([]domain.WorkflowSummary, visible)
	for index := range visible {
		items[index] = cloneWorkflowSummary(rows[index])
	}
	page := WorkflowSummaryPage{Items: items}
	if len(rows) > limit {
		last := items[len(items)-1]
		nextCursor, err := encodePageCursor(last.UpdatedAt, last.ID, fingerprint)
		if err != nil {
			return WorkflowSummaryPage{}, err
		}
		page.NextCursor = &nextCursor
	}
	return page, nil
}

func (service *WorkflowManagementService) Update(ctx context.Context, id string, input UpdateWorkflowInput) (domain.Workflow, error) {
	name, description, err := normalizeWorkflowMetadata(input.Name, input.Description)
	if err != nil {
		return domain.Workflow{}, err
	}
	loaded, err := service.store.GetWorkflow(ctx, id)
	if err != nil {
		return domain.Workflow{}, err
	}
	if err := ensureWorkflowActive(loaded); err != nil {
		return domain.Workflow{}, err
	}
	return service.store.UpdateWorkflowMetadata(ctx, id, name, description)
}

func (service *WorkflowManagementService) Copy(ctx context.Context, id string, input CopyWorkflowInput) (domain.Workflow, error) {
	source, err := service.store.GetWorkflow(ctx, id)
	if err != nil {
		return domain.Workflow{}, err
	}
	identity, err := normalizeWorkflowIdentity(CreateWorkflowInput{
		Name: input.Name, Slug: input.Slug, Description: source.Description,
	})
	if err != nil {
		return domain.Workflow{}, err
	}
	return service.store.CreateWorkflow(ctx, domain.Workflow{
		ID:                uuid.NewString(),
		Name:              identity.Name,
		Slug:              identity.Slug,
		Description:       identity.Description,
		AgentPresentation: source.AgentPresentation,
		DraftGraph:        append([]byte(nil), source.DraftGraph...),
		DraftRevision:     1,
	})
}

func (service *WorkflowManagementService) Archive(ctx context.Context, id string) (domain.Workflow, error) {
	return service.store.ArchiveWorkflow(ctx, id)
}

func (service *WorkflowManagementService) Restore(ctx context.Context, id string) (domain.Workflow, error) {
	return service.store.RestoreWorkflow(ctx, id)
}

func cloneWorkflowSummary(value domain.WorkflowSummary) domain.WorkflowSummary {
	cloned := value
	if value.PublishedVersionID != nil {
		publishedVersionID := *value.PublishedVersionID
		cloned.PublishedVersionID = &publishedVersionID
	}
	if value.PublishedVersion != nil {
		publishedVersion := *value.PublishedVersion
		cloned.PublishedVersion = &publishedVersion
	}
	if value.ArchivedAt != nil {
		archivedAt := *value.ArchivedAt
		cloned.ArchivedAt = &archivedAt
	}
	return cloned
}
