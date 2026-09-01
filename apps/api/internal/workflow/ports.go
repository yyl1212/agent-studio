package workflow

import (
	"context"
	"encoding/json"
	"time"

	"github.com/yyl1212/agent-studio/apps/api/internal/domain"
	"github.com/yyl1212/agent-studio/apps/api/internal/engine"
)

type RunSubmission struct {
	Run          domain.Run
	QueuedEvent  domain.RunEvent
	InputPayload domain.RunPayload
}

type ClaimedRun struct {
	Run   domain.Run
	Lease domain.RunLease
}

type LeaseHeartbeat struct {
	Lease           domain.RunLease
	CancelRequested bool
}

type DurableRunStore interface {
	SubmitRun(context.Context, RunSubmission) error
	ClaimRun(context.Context, string, time.Duration) (ClaimedRun, bool, error)
	RenewRunLease(context.Context, domain.RunLease, time.Duration) (LeaseHeartbeat, error)
	LoadRunExecution(context.Context, string) (domain.Run, []domain.RunEvent, []domain.RunPayload, error)
	PersistLeasedRunEvent(context.Context, domain.RunLease, domain.RunEvent, *domain.NodeRun, []domain.RunPayload, domain.RunEventBudget) error
	RequireRunRecovery(context.Context, domain.RunLease, domain.RunEvent, domain.RunRecoveryReason, time.Time, domain.RunEventBudget) error
	FinalizeLeasedRun(context.Context, domain.RunLease, RunFinalization, []domain.RunPayload) (domain.RunEvent, error)
}

type Store interface {
	AgentRunStore
	DurableRunStore
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
