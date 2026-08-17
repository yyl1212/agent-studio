package workflow

import (
	"context"
	"encoding/json"
	"time"

	"agentstudio.local/api/internal/domain"
	"agentstudio.local/api/internal/engine"
)

type Store interface {
	ListWorkflows(context.Context) ([]domain.Workflow, error)
	CreateWorkflow(context.Context, domain.Workflow) (domain.Workflow, error)
	GetWorkflow(context.Context, string) (domain.Workflow, error)
	UpdateDraft(context.Context, string, int64, json.RawMessage) (domain.Workflow, error)
	Publish(context.Context, string, int64, json.RawMessage, json.RawMessage) (domain.WorkflowVersion, error)
	GetCurrentAgentVersion(context.Context, string) (domain.Workflow, domain.WorkflowVersion, error)
	GetAgentVersion(context.Context, string, string) (domain.Workflow, domain.WorkflowVersion, error)
	CreateRun(context.Context, domain.Run) error
	UpsertNodeRun(context.Context, domain.NodeRun) error
	FinishRun(context.Context, string, domain.RunStatus, any, *domain.PublicError, time.Time) error
	GetRun(context.Context, string) (domain.Run, []domain.NodeRun, error)
	ListRuns(context.Context, string, int) ([]domain.Run, error)
}

type Compiler interface {
	Compile(domain.Graph) (*engine.Plan, []domain.ValidationIssue)
}

type Engine interface {
	Run(context.Context, string, *engine.Plan, map[string]any, engine.Observer) (engine.RunResult, error)
}
