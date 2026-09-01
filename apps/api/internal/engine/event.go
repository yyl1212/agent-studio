package engine

import (
	"time"

	"github.com/yyl1212/agent-studio/apps/api/internal/domain"
)

type Event struct {
	Sequence            int64               `json:"sequence"`
	Type                string              `json:"type"`
	RunID               string              `json:"runId"`
	NodeID              string              `json:"nodeId,omitempty"`
	NodeAttempt         int                 `json:"nodeAttempt,omitempty"`
	Status              domain.NodeStatus   `json:"status,omitempty"`
	Input               any                 `json:"input,omitempty"`
	Output              any                 `json:"output,omitempty"`
	ActivePorts         []string            `json:"activePorts"`
	InputRedactedPaths  []string            `json:"inputRedactedPaths"`
	OutputRedactedPaths []string            `json:"outputRedactedPaths"`
	Error               *domain.PublicError `json:"error,omitempty"`
	Timestamp           time.Time           `json:"timestamp"`
}

type RunResult struct {
	RunID        string
	Output       any
	NodeStatuses map[string]domain.NodeStatus
	StartedAt    time.Time
	EndedAt      time.Time
}
