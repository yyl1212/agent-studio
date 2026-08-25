package workflow

import (
	"context"
	"fmt"
	"strings"

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
