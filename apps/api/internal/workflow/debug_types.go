package workflow

import (
	"errors"

	"github.com/yyl1212/agent-studio/apps/api/internal/domain"
	"github.com/yyl1212/agent-studio/sdk/go/agentnode"
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

type RerunRequest struct {
	EntryInput         map[string]any `json:"entryInput"`
	ConfirmSideEffects bool           `json:"confirmSideEffects"`
}

type RerunPreview struct {
	SourceRunID             string                    `json:"sourceRunId"`
	SourceNodeID            string                    `json:"sourceNodeId"`
	EntryInput              map[string]any            `json:"entryInput"`
	EntryInputRedactedPaths []string                  `json:"entryInputRedactedPaths"`
	ActiveNodes             []RerunNode               `json:"activeNodes"`
	FrozenEdges             []FrozenEdgePreview       `json:"frozenEdges"`
	EffectiveSafety         agentnode.ExecutionSafety `json:"effectiveSafety"`
	RequiresConfirmation    bool                      `json:"requiresConfirmation"`
}

type RerunNode struct {
	ID      string                    `json:"id"`
	Type    string                    `json:"type"`
	Version string                    `json:"version"`
	Title   string                    `json:"title"`
	Safety  agentnode.ExecutionSafety `json:"safety"`
}

type FrozenEdgePreview struct {
	EdgeID     string `json:"edgeId"`
	Source     string `json:"source"`
	SourcePort string `json:"sourcePort"`
	Target     string `json:"target"`
	TargetPort string `json:"targetPort"`
	Active     bool   `json:"active"`
	Value      any    `json:"value,omitempty"`
}

type DebugService struct {
	store      Store
	compiler   Compiler
	submission *RunSubmissionService
}

func NewQueuedDebugService(store Store, compiler Compiler, submission *RunSubmissionService) *DebugService {
	return &DebugService{store: store, compiler: compiler, submission: submission}
}
