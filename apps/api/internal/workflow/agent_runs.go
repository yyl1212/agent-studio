package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/yyl1212/agent-studio/apps/api/internal/domain"
	"github.com/yyl1212/agent-studio/sdk/go/agentnode"
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

type StartAgentRunInput struct {
	WorkflowVersionID string
	RequestKey        string
	Input             map[string]any
}

type AgentPublicError struct {
	Code    string              `json:"code"`
	Kind    agentnode.ErrorKind `json:"kind,omitempty"`
	Message string              `json:"message"`
}

type AgentRunPublicSummary struct {
	RunID             string            `json:"runId"`
	WorkflowVersionID string            `json:"workflowVersionId"`
	Version           int               `json:"version"`
	Status            domain.RunStatus  `json:"status"`
	StartedAt         time.Time         `json:"startedAt"`
	EndedAt           *time.Time        `json:"endedAt"`
	Output            any               `json:"output"`
	Error             *AgentPublicError `json:"error"`
}

type AgentRunPublicEvent struct {
	Sequence  int64             `json:"sequence"`
	Type      string            `json:"type"`
	Status    domain.NodeStatus `json:"status,omitempty"`
	Timestamp time.Time         `json:"timestamp"`
}

type AgentRunPublicView struct {
	Run          AgentRunPublicSummary    `json:"run"`
	Presentation domain.AgentPresentation `json:"presentation"`
	Events       []AgentRunPublicEvent    `json:"events"`
	NextSequence int64                    `json:"nextSequence"`
	HasMore      bool                     `json:"hasMore"`
}

type AgentRunPreparer interface {
	PrepareAgentOnce(context.Context, string, string, string, map[string]any) (*PreparedRun, bool, error)
}

type AgentRunSubmitter interface {
	SubmitAgentOnce(context.Context, string, string, string, map[string]any) (SubmittedRun, error)
}

type AgentRunReservation interface {
	Launch(context.Context, *PreparedRun)
	Release()
}

type AgentRunLauncher interface {
	Reserve() (AgentRunReservation, error)
}

type AgentRunService struct {
	preparer  AgentRunPreparer
	store     AgentRunStore
	launcher  AgentRunLauncher
	canceller LocalRunCanceller
	submitter AgentRunSubmitter
}

func NewQueuedAgentRunService(submitter AgentRunSubmitter, store AgentRunStore) *AgentRunService {
	return &AgentRunService{submitter: submitter, store: store}
}

func NewAgentRunService(preparer AgentRunPreparer, store AgentRunStore, launcher AgentRunLauncher, canceller LocalRunCanceller) *AgentRunService {
	return &AgentRunService{preparer: preparer, store: store, launcher: launcher, canceller: canceller}
}

func (service *AgentRunService) Start(ctx context.Context, slug string, input StartAgentRunInput) (summary AgentRunPublicSummary, created bool, err error) {
	existing, err := service.store.FindAgentRunByRequestKey(ctx, slug, input.RequestKey)
	if err == nil {
		summary, err = publicAgentRunSummary(existing)
		return summary, false, err
	}
	if !errors.Is(err, domain.ErrNotFound) {
		return AgentRunPublicSummary{}, false, err
	}
	if service.submitter != nil {
		submitted, err := service.submitter.SubmitAgentOnce(ctx, slug, input.WorkflowVersionID, input.RequestKey, input.Input)
		if err != nil {
			return AgentRunPublicSummary{}, false, err
		}
		return AgentRunPublicSummary{
			RunID: submitted.RunID, WorkflowVersionID: valueOrEmpty(submitted.WorkflowVersionID), Version: submitted.WorkflowVersion,
			Status: submitted.Status, StartedAt: submitted.StartedAt,
		}, submitted.Created, nil
	}
	reservation, err := service.launcher.Reserve()
	if err != nil {
		return AgentRunPublicSummary{}, false, err
	}
	launched := false
	defer func() {
		if !launched {
			reservation.Release()
		}
	}()
	prepared, created, err := service.preparer.PrepareAgentOnce(ctx, slug, input.WorkflowVersionID, input.RequestKey, input.Input)
	if err != nil {
		return AgentRunPublicSummary{}, false, err
	}
	if !created {
		existing, err = service.store.FindAgentRunByRequestKey(ctx, slug, input.RequestKey)
		if err != nil {
			return AgentRunPublicSummary{}, false, err
		}
		summary, err = publicAgentRunSummary(existing)
		return summary, false, err
	}
	if prepared == nil || prepared.WorkflowVersionID == nil {
		return AgentRunPublicSummary{}, false, fmt.Errorf("created agent run has no prepared execution")
	}
	reservation.Launch(ctx, prepared)
	launched = true
	return AgentRunPublicSummary{
		RunID: prepared.RunID, WorkflowVersionID: *prepared.WorkflowVersionID, Version: prepared.WorkflowVersion,
		Status: domain.RunRunning, StartedAt: prepared.StartedAt,
	}, true, nil
}

func valueOrEmpty(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func (service *AgentRunService) View(ctx context.Context, slug, runID string, afterSequence int64) (AgentRunPublicView, error) {
	if afterSequence < 0 {
		return AgentRunPublicView{}, ErrInvalidWorkflowInput
	}
	record, err := service.store.GetAgentRun(ctx, slug, runID, afterSequence, 200)
	if err != nil {
		return AgentRunPublicView{}, err
	}
	summary, err := publicAgentRunSummary(record)
	if err != nil {
		return AgentRunPublicView{}, err
	}
	events := make([]AgentRunPublicEvent, 0, len(record.Events))
	nextSequence := afterSequence
	for _, event := range record.Events {
		events = append(events, AgentRunPublicEvent{Sequence: event.Sequence, Type: event.Type, Status: event.Status, Timestamp: event.Timestamp})
		nextSequence = event.Sequence
	}
	return AgentRunPublicView{
		Run: summary, Presentation: record.Version.AgentPresentation, Events: events,
		NextSequence: nextSequence, HasMore: record.HasMore,
	}, nil
}

func (service *AgentRunService) Cancel(ctx context.Context, slug, runID string) (AgentRunPublicSummary, error) {
	record, err := service.store.RequestAgentRunCancel(ctx, slug, runID)
	if err != nil {
		return AgentRunPublicSummary{}, err
	}
	if service.canceller != nil {
		service.canceller.CancelLocal(runID)
	}
	return publicAgentRunSummary(record)
}

func publicAgentRunSummary(record AgentRunRecord) (AgentRunPublicSummary, error) {
	summary := AgentRunPublicSummary{
		RunID: record.Run.ID, Version: record.Version.Version, Status: record.Run.Status,
		StartedAt: record.Run.StartedAt, EndedAt: record.Run.EndedAt,
	}
	if record.Run.WorkflowVersionID != nil {
		summary.WorkflowVersionID = *record.Run.WorkflowVersionID
	}
	if isAgentRunTerminal(record.Run.Status) {
		if len(record.Run.Output) > 0 {
			if err := json.Unmarshal(record.Run.Output, &summary.Output); err != nil {
				return AgentRunPublicSummary{}, fmt.Errorf("decode public agent run output: %w", err)
			}
		}
		if record.Run.Error != nil {
			summary.Error = &AgentPublicError{Code: record.Run.Error.Code, Kind: record.Run.Error.Kind, Message: record.Run.Error.Message}
		}
	}
	return summary, nil
}

func isAgentRunTerminal(status domain.RunStatus) bool {
	return status == domain.RunCompleted || status == domain.RunFailed || status == domain.RunCancelled
}
