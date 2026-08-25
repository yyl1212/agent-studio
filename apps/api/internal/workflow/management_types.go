package workflow

import "github.com/yyl1212/agent-studio/apps/api/internal/domain"

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
