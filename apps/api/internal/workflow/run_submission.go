package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/yyl1212/agent-studio/apps/api/internal/domain"
	"github.com/yyl1212/agent-studio/apps/api/internal/runpayload"
)

type SubmittedRun struct {
	RunID             string           `json:"runId"`
	WorkflowVersionID *string          `json:"workflowVersionId,omitempty"`
	WorkflowVersion   int              `json:"workflowVersion,omitempty"`
	Status            domain.RunStatus `json:"status"`
	StartedAt         time.Time        `json:"startedAt"`
	Created           bool             `json:"created"`
}

type RunAlreadySubmittedError struct{ Run domain.Run }

func (err *RunAlreadySubmittedError) Error() string { return "run already submitted" }

type RunSubmissionService struct {
	store interface {
		SubmitRun(context.Context, RunSubmission) error
	}
	cipher *runpayload.Cipher
}

func NewRunSubmissionService(store interface {
	SubmitRun(context.Context, RunSubmission) error
}, cipher *runpayload.Cipher) *RunSubmissionService {
	return &RunSubmissionService{store: store, cipher: cipher}
}

func (service *RunSubmissionService) Submit(ctx context.Context, run domain.Run, privateInput map[string]any) (SubmittedRun, error) {
	if service == nil || service.store == nil || service.cipher == nil || run.ID == "" || run.WorkflowID == "" || run.StartedAt.IsZero() {
		return SubmittedRun{}, errors.New("run submission dependencies are incomplete")
	}
	privateJSON, err := json.Marshal(normalizeInput(privateInput))
	if err != nil {
		return SubmittedRun{}, fmt.Errorf("encode private run input: %w", err)
	}
	run.Status = domain.RunQueued
	run.ExecutionProtocol = domain.CurrentExecutionProtocol
	run.LeaseOwner = ""
	run.LeaseToken = 0
	run.LeaseExpiresAt = nil
	run.HeartbeatAt = nil
	queued := domain.RunEvent{
		RunID: run.ID, Sequence: 1, Type: "run.queued", Timestamp: run.StartedAt,
		ActivePorts: []string{}, InputRedactedPaths: []string{}, OutputRedactedPaths: []string{}, DataBytes: 6,
	}
	metadata := runpayload.Metadata{RunID: run.ID, Sequence: 0, Kind: domain.RunPayloadInput, ExecutionProtocol: domain.CurrentExecutionProtocol}
	ciphertext, err := service.cipher.Seal(metadata, privateJSON)
	if err != nil {
		return SubmittedRun{}, err
	}
	payload := domain.RunPayload{
		RunID: run.ID, Sequence: 0, Kind: domain.RunPayloadInput, ExecutionProtocol: domain.CurrentExecutionProtocol,
		CipherVersion: 1, Ciphertext: ciphertext, CreatedAt: run.StartedAt,
	}
	if err := service.store.SubmitRun(ctx, RunSubmission{Run: run, QueuedEvent: queued, InputPayload: payload}); err != nil {
		var existing *RunAlreadySubmittedError
		if !errors.As(err, &existing) {
			return SubmittedRun{}, err
		}
		return SubmittedRun{
			RunID: existing.Run.ID, WorkflowVersionID: cloneStringPointer(existing.Run.WorkflowVersionID), Status: existing.Run.Status,
			StartedAt: existing.Run.StartedAt, Created: false,
		}, nil
	}
	return SubmittedRun{
		RunID: run.ID, WorkflowVersionID: cloneStringPointer(run.WorkflowVersionID), Status: domain.RunQueued,
		StartedAt: run.StartedAt, Created: true,
	}, nil
}
