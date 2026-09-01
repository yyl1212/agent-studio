package domain

import (
	"encoding/json"
	"time"
)

type RunMode string

const (
	RunModeTest      RunMode = "test"
	RunModePublished RunMode = "published"
	RunModeDebug     RunMode = "debug"
)

type RunStatus string

const CurrentExecutionProtocol int16 = 1

const (
	RunQueued           RunStatus = "queued"
	RunRunning          RunStatus = "running"
	RunRecoveryRequired RunStatus = "recovery_required"
	RunCancelling       RunStatus = "cancelling"
	RunCompleted        RunStatus = "completed"
	RunFailed           RunStatus = "failed"
	RunCancelled        RunStatus = "cancelled"
)

func IsTerminalRunStatus(status RunStatus) bool {
	switch status {
	case RunCompleted, RunFailed, RunCancelled:
		return true
	default:
		return false
	}
}

func IsActiveRunStatus(status RunStatus) bool {
	switch status {
	case RunQueued, RunRunning, RunRecoveryRequired, RunCancelling:
		return true
	default:
		return false
	}
}

func IsCancellableRunStatus(status RunStatus) bool {
	return IsActiveRunStatus(status)
}

type RunRecoveryReason string

const (
	RecoveryLegacyActive       RunRecoveryReason = "legacy_active_run"
	RecoveryUncertainReadOnly  RunRecoveryReason = "uncertain_read_only"
	RecoveryUncertainEffect    RunRecoveryReason = "uncertain_side_effect"
	RecoveryAttemptLimit       RunRecoveryReason = "attempt_limit_reached"
	RecoveryPayloadUnavailable RunRecoveryReason = "payload_unavailable"
	RecoveryHistoryInvalid     RunRecoveryReason = "event_history_invalid"
	RecoveryNodeUnavailable    RunRecoveryReason = "node_version_unavailable"
)

type RunLease struct {
	RunID     string
	Owner     string
	Token     int64
	ExpiresAt time.Time
}

type RunPayloadKind string

const (
	RunPayloadInput      RunPayloadKind = "run_input"
	RunPayloadNodeInput  RunPayloadKind = "node_input"
	RunPayloadNodeOutput RunPayloadKind = "node_output"
)

type RunPayload struct {
	RunID             string
	Sequence          int64
	Kind              RunPayloadKind
	NodeID            string
	NodeAttempt       int
	ExecutionProtocol int16
	CipherVersion     int16
	Ciphertext        []byte
	CreatedAt         time.Time
}

type NodeStatus string

const (
	NodePending   NodeStatus = "pending"
	NodeRunning   NodeStatus = "running"
	NodeCompleted NodeStatus = "completed"
	NodeFailed    NodeStatus = "failed"
	NodeSkipped   NodeStatus = "skipped"
	NodeCancelled NodeStatus = "cancelled"
)

type Run struct {
	ID                  string            `json:"id"`
	WorkflowID          string            `json:"workflowId"`
	WorkflowVersionID   *string           `json:"workflowVersionId,omitempty"`
	DraftRevision       *int64            `json:"draftRevision,omitempty"`
	GraphSnapshot       json.RawMessage   `json:"graphSnapshot,omitempty"`
	SourceRunID         *string           `json:"sourceRunId,omitempty"`
	SourceNodeID        *string           `json:"sourceNodeId,omitempty"`
	RetryOfRunID        *string           `json:"retryOfRunId,omitempty"`
	RetryKey            *string           `json:"-"`
	AgentRequestKey     *string           `json:"-"`
	Mode                RunMode           `json:"mode"`
	Status              RunStatus         `json:"status"`
	ExecutionProtocol   int16             `json:"executionProtocol"`
	LeaseOwner          string            `json:"-"`
	LeaseToken          int64             `json:"-"`
	LeaseExpiresAt      *time.Time        `json:"-"`
	RecoveryReason      RunRecoveryReason `json:"recoveryReason,omitempty"`
	RecoveryRequestedAt *time.Time        `json:"recoveryRequestedAt,omitempty"`
	Input               json.RawMessage   `json:"input"`
	InputRedactedPaths  []string          `json:"inputRedactedPaths"`
	Output              json.RawMessage   `json:"output,omitempty"`
	Error               *PublicError      `json:"error,omitempty"`
	CancelRequestedAt   *time.Time        `json:"cancelRequestedAt,omitempty"`
	HeartbeatAt         *time.Time        `json:"-"`
	StartedAt           time.Time         `json:"startedAt"`
	EndedAt             *time.Time        `json:"endedAt,omitempty"`
}

type RunSummary struct {
	ID                string     `json:"id"`
	WorkflowID        string     `json:"workflowId"`
	WorkflowName      string     `json:"workflowName"`
	WorkflowSlug      string     `json:"workflowSlug"`
	WorkflowVersionID *string    `json:"workflowVersionId,omitempty"`
	WorkflowVersion   *int       `json:"workflowVersion,omitempty"`
	DraftRevision     *int64     `json:"draftRevision,omitempty"`
	SourceRunID       *string    `json:"sourceRunId,omitempty"`
	SourceNodeID      *string    `json:"sourceNodeId,omitempty"`
	RetryOfRunID      *string    `json:"retryOfRunId,omitempty"`
	Mode              RunMode    `json:"mode"`
	Status            RunStatus  `json:"status"`
	CancelRequestedAt *time.Time `json:"cancelRequestedAt,omitempty"`
	StartedAt         time.Time  `json:"startedAt"`
	EndedAt           *time.Time `json:"endedAt,omitempty"`
}

type RunEvent struct {
	RunID               string          `json:"runId"`
	Sequence            int64           `json:"sequence"`
	Type                string          `json:"type"`
	NodeID              string          `json:"nodeId,omitempty"`
	NodeAttempt         *int            `json:"nodeAttempt,omitempty"`
	Status              NodeStatus      `json:"status,omitempty"`
	Input               json.RawMessage `json:"input,omitempty"`
	Output              json.RawMessage `json:"output,omitempty"`
	ActivePorts         []string        `json:"activePorts"`
	Error               *PublicError    `json:"error,omitempty"`
	InputRedactedPaths  []string        `json:"inputRedactedPaths"`
	OutputRedactedPaths []string        `json:"outputRedactedPaths"`
	DataBytes           int64           `json:"-"`
	Timestamp           time.Time       `json:"timestamp"`
}

type RunEventBudget struct {
	MaxEvents         int
	MaxTotalDataBytes int64
}

type NodeRun struct {
	ID        string          `json:"id"`
	RunID     string          `json:"runId"`
	NodeID    string          `json:"nodeId"`
	NodeType  string          `json:"nodeType"`
	Attempt   int             `json:"attempt"`
	Status    NodeStatus      `json:"status"`
	Input     json.RawMessage `json:"input,omitempty"`
	Output    json.RawMessage `json:"output,omitempty"`
	Error     *PublicError    `json:"error,omitempty"`
	StartedAt *time.Time      `json:"startedAt,omitempty"`
	EndedAt   *time.Time      `json:"endedAt,omitempty"`
}
