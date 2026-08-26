package workflow

import (
	"context"
	"encoding/json"

	"github.com/yyl1212/agent-studio/apps/api/internal/domain"
	"github.com/yyl1212/agent-studio/apps/api/internal/engine"
)

type Store interface {
	ListWorkflows(context.Context) ([]domain.Workflow, error)
	CreateWorkflow(context.Context, domain.Workflow) (domain.Workflow, error)
	GetWorkflow(context.Context, string) (domain.Workflow, error)
	UpdateDraft(context.Context, string, int64, json.RawMessage) (domain.Workflow, error)
	UpdateAgentPresentation(context.Context, string, int64, domain.AgentPresentation) (domain.Workflow, error)
	Publish(context.Context, string, int64, json.RawMessage, json.RawMessage, domain.AgentPresentation) (domain.WorkflowVersion, error)
	GetCurrentAgentVersion(context.Context, string) (domain.Workflow, domain.WorkflowVersion, error)
	GetAgentVersion(context.Context, string, string) (domain.Workflow, domain.WorkflowVersion, error)
	CreateRun(context.Context, domain.Run) error
	PersistRunEvent(context.Context, domain.RunEvent, *domain.NodeRun, domain.RunEventBudget) error
	ListRunEvents(context.Context, string, int64, int) ([]domain.RunEvent, error)
	FinalizeRun(context.Context, RunFinalization) (domain.RunEvent, error)
	GetRun(context.Context, string) (domain.Run, []domain.NodeRun, error)
	ListRuns(context.Context, string, int) ([]domain.Run, error)
}

type Compiler interface {
	Compile(domain.Graph) (*engine.Plan, []domain.ValidationIssue)
}

type Engine interface {
	Run(context.Context, string, *engine.Plan, map[string]any, engine.Observer) (engine.RunResult, error)
	RunWithScope(context.Context, string, *engine.Plan, map[string]any, engine.Observer, engine.ExecutionScope) (engine.RunResult, error)
}
