package workflow

import (
	"context"
	"encoding/json"
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

type RunRetryPreview struct {
	Source             domain.RunSummary `json:"source"`
	RetryOfRunID       string            `json:"retryOfRunId"`
	Input              map[string]any    `json:"input"`
	InputRedactedPaths []string          `json:"inputRedactedPaths"`
	InputSchema        json.RawMessage   `json:"inputSchema"`
}

type RunRetryRequest struct {
	SecretValues map[string]any `json:"secretValues"`
}

type RunRetryAlreadyCreatedError struct {
	RunID string
}

func (err *RunRetryAlreadyCreatedError) Error() string {
	return "run retry already created"
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

type RunCoordinationStore interface {
	HeartbeatRuns(context.Context, []string) ([]string, error)
	FinalizeInterruptedRuns(context.Context, int, int) (int, error)
}

type RunExecutionCoordinator interface {
	Register(context.Context, string) (context.Context, func())
}

type RunManagementStore interface {
	ListRunSummaries(context.Context, RunSummaryStoreQuery) ([]domain.RunSummary, error)
	RequestRunCancel(context.Context, string) (domain.RunSummary, error)
	GetRun(context.Context, string) (domain.Run, []domain.NodeRun, error)
	GetWorkflow(context.Context, string) (domain.Workflow, error)
	GetAgentVersion(context.Context, string, string) (domain.Workflow, domain.WorkflowVersion, error)
	CreateRetryRun(context.Context, domain.Run) (string, error)
}

type LocalRunCanceller interface {
	CancelLocal(string) bool
}
