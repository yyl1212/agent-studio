package workflow

import (
	"context"
	"time"

	"github.com/yyl1212/agent-studio/apps/api/internal/domain"
)

type WorkflowState string

const (
	WorkflowStateActive   WorkflowState = "active"
	WorkflowStateArchived WorkflowState = "archived"
	WorkflowStateAll      WorkflowState = "all"
)

type WorkflowSummaryRequest struct {
	Text   string
	State  WorkflowState
	Cursor string
	Limit  int
}

type WorkflowSummaryPage struct {
	Items      []domain.WorkflowSummary `json:"items"`
	NextCursor *string                  `json:"nextCursor"`
}

type WorkflowSummaryStoreQuery struct {
	Text         string
	State        WorkflowState
	AfterUpdated *time.Time
	AfterID      string
	Limit        int
}

type WorkflowManagementStore interface {
	ListWorkflowSummaries(context.Context, WorkflowSummaryStoreQuery) ([]domain.WorkflowSummary, error)
}
