package workflow

import (
	"context"

	"github.com/yyl1212/agent-studio/apps/api/internal/domain"
)

type AgentRunRecord struct {
	Run     domain.Run
	Version domain.WorkflowVersion
	Events  []domain.RunEvent
	HasMore bool
}

type AgentRunStore interface {
	FindAgentRunByRequestKey(context.Context, string, string) (AgentRunRecord, error)
	CreateAgentRun(context.Context, domain.Run) (domain.Run, bool, error)
	GetAgentRun(context.Context, string, string, int64, int) (AgentRunRecord, error)
	RequestAgentRunCancel(context.Context, string, string) (AgentRunRecord, error)
}
