package engine

import (
	"time"

	"agentstudio.local/api/internal/domain"
)

type Event struct {
	Sequence  int64               `json:"sequence"`
	Type      string              `json:"type"`
	RunID     string              `json:"runId"`
	NodeID    string              `json:"nodeId,omitempty"`
	Status    domain.NodeStatus   `json:"status,omitempty"`
	Input     any                 `json:"input,omitempty"`
	Output    any                 `json:"output,omitempty"`
	Error     *domain.PublicError `json:"error,omitempty"`
	Timestamp time.Time           `json:"timestamp"`
}

type RunResult struct {
	RunID        string
	Output       any
	NodeStatuses map[string]domain.NodeStatus
	StartedAt    time.Time
	EndedAt      time.Time
}
