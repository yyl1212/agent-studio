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

type UpdateWorkflowInput struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

type CopyWorkflowInput struct {
	Name string `json:"name"`
	Slug string `json:"slug"`
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
	GetWorkflow(context.Context, string) (domain.Workflow, error)
	CreateWorkflow(context.Context, domain.Workflow) (domain.Workflow, error)
	UpdateWorkflowMetadata(context.Context, string, string, string) (domain.Workflow, error)
	ArchiveWorkflow(context.Context, string) (domain.Workflow, error)
	RestoreWorkflow(context.Context, string) (domain.Workflow, error)
}

type RunSummaryRequest struct {
	WorkflowID    string
	Statuses      []domain.RunStatus
	Modes         []domain.RunMode
	StartedAfter  *time.Time
	StartedBefore *time.Time
	RunID         string
	Cursor        string
	Limit         int
}

type RunSummaryStoreQuery struct {
	WorkflowID    string
	Statuses      []domain.RunStatus
	Modes         []domain.RunMode
	StartedAfter  *time.Time
	StartedBefore *time.Time
	RunID         string
	AfterStarted  *time.Time
	AfterID       string
	Limit         int
}

type RunSummaryPage struct {
	Items      []domain.RunSummary `json:"items"`
	NextCursor *string             `json:"nextCursor"`
}

type RunFinalization struct {
	RunID         string
	Status        domain.RunStatus
	Output        any
	Error         *domain.PublicError
	EndedAt       time.Time
	TerminalEvent domain.RunEvent
	Budget        domain.RunEventBudget
}

type RunManagementStore interface {
	ListRunSummaries(context.Context, RunSummaryStoreQuery) ([]domain.RunSummary, error)
}
