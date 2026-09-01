package workflow

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/yyl1212/agent-studio/apps/api/internal/domain"
	"github.com/yyl1212/agent-studio/apps/api/internal/runpayload"
)

func TestRunSubmissionPersistsQueuedRunEventAndEncryptedInputAtomically(t *testing.T) {
	store := &submissionStore{}
	cipher, err := runpayload.New("MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=")
	if err != nil {
		t.Fatal(err)
	}
	service := NewRunSubmissionService(store, cipher)
	run := domain.Run{
		ID: "run-1", WorkflowID: "workflow-1", Mode: domain.RunModeTest,
		Input: json.RawMessage(`{"token":"[REDACTED]"}`), InputRedactedPaths: []string{"/token"}, StartedAt: time.Now().UTC(),
	}
	result, err := service.Submit(context.Background(), run, map[string]any{"token": "top-secret"})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Created || result.RunID != run.ID || result.Status != domain.RunQueued || store.calls != 1 {
		t.Fatalf("result=%+v calls=%d", result, store.calls)
	}
	submission := store.submission
	if submission.Run.Status != domain.RunQueued || submission.Run.ExecutionProtocol != domain.CurrentExecutionProtocol ||
		submission.QueuedEvent.Type != "run.queued" || submission.QueuedEvent.Sequence != 1 || submission.InputPayload.Kind != domain.RunPayloadInput {
		t.Fatalf("submission=%+v", submission)
	}
	if bytes.Contains(submission.Run.Input, []byte("top-secret")) || bytes.Contains(submission.InputPayload.Ciphertext, []byte("top-secret")) {
		t.Fatalf("private input leaked in wire storage: %+v", submission)
	}
	plaintext, err := cipher.Open(runpayload.Metadata{RunID: run.ID, Sequence: 0, Kind: domain.RunPayloadInput, ExecutionProtocol: domain.CurrentExecutionProtocol}, submission.InputPayload.Ciphertext)
	if err != nil || !bytes.Contains(plaintext, []byte("top-secret")) {
		t.Fatalf("plaintext=%s error=%v", plaintext, err)
	}
}

func TestRunSubmissionReturnsExistingIdempotentRunWithoutSecondCreation(t *testing.T) {
	cipher, err := runpayload.New("MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=")
	if err != nil {
		t.Fatal(err)
	}
	existing := domain.Run{ID: "existing", Status: domain.RunQueued, StartedAt: time.Now().UTC()}
	store := &submissionStore{err: &RunAlreadySubmittedError{Run: existing}}
	result, err := NewRunSubmissionService(store, cipher).Submit(context.Background(), domain.Run{
		ID: "candidate", WorkflowID: "workflow", Mode: domain.RunModePublished, StartedAt: time.Now().UTC(),
	}, map[string]any{})
	if err != nil || result.Created || result.RunID != existing.ID || store.calls != 1 {
		t.Fatalf("result=%+v calls=%d error=%v", result, store.calls, err)
	}
}

type submissionStore struct {
	calls      int
	submission RunSubmission
	err        error
}

func (store *submissionStore) SubmitRun(_ context.Context, submission RunSubmission) error {
	store.calls++
	store.submission = submission
	return store.err
}
