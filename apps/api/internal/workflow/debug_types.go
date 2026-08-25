package workflow

import (
	"errors"

	"github.com/yyl1212/agent-studio/apps/api/internal/domain"
)

var (
	ErrRunReplayUnavailable              = errors.New("run replay unavailable")
	ErrRunSnapshotUnsupported            = errors.New("run snapshot unsupported")
	ErrRunFrozenEdgeUnavailable          = errors.New("run frozen edge unavailable")
	ErrRunSideEffectConfirmationRequired = errors.New("run side effect confirmation required")
	ErrRunEntryInputInvalid              = errors.New("run entry input invalid")
)

type DebugOverview struct {
	Run               domain.Run       `json:"run"`
	Graph             domain.Graph     `json:"graph"`
	NodeRuns          []domain.NodeRun `json:"nodeRuns"`
	SourceChain       []DebugSource    `json:"sourceChain"`
	ReplayAvailable   bool             `json:"replayAvailable"`
	RerunAvailable    bool             `json:"rerunAvailable"`
	UnavailableReason string           `json:"unavailableReason,omitempty"`
}

type RunEventPage struct {
	Events            []domain.RunEvent `json:"events"`
	NextAfterSequence int64             `json:"nextAfterSequence"`
}

type DebugSource struct {
	RunID        string           `json:"runId"`
	SourceNodeID string           `json:"sourceNodeId,omitempty"`
	Mode         domain.RunMode   `json:"mode"`
	Status       domain.RunStatus `json:"status"`
}

type DebugService struct {
	store    Store
	compiler Compiler
}
