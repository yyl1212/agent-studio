package workflow

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/yyl1212/agent-studio/apps/api/internal/domain"
)

type fakeAgentRunStore struct {
	found         AgentRunRecord
	findErr       error
	view          AgentRunRecord
	viewErr       error
	cancelled     AgentRunRecord
	cancelErr     error
	findCalls     int
	viewCalls     int
	cancelCalls   int
	afterSequence int64
	limit         int
}

func (store *fakeAgentRunStore) FindAgentRunByRequestKey(context.Context, string, string) (AgentRunRecord, error) {
	store.findCalls++
	if store.findErr != nil {
		err := store.findErr
		store.findErr = nil
		return AgentRunRecord{}, err
	}
	return store.found, nil
}

func (store *fakeAgentRunStore) CreateAgentRun(context.Context, domain.Run) (domain.Run, bool, error) {
	return domain.Run{}, false, errors.New("unexpected CreateAgentRun")
}

func (store *fakeAgentRunStore) GetAgentRun(_ context.Context, _ string, _ string, afterSequence int64, limit int) (AgentRunRecord, error) {
	store.viewCalls++
	store.afterSequence = afterSequence
	store.limit = limit
	if store.viewErr != nil {
		return AgentRunRecord{}, store.viewErr
	}
	return store.view, nil
}

func (store *fakeAgentRunStore) RequestAgentRunCancel(context.Context, string, string) (AgentRunRecord, error) {
	store.cancelCalls++
	if store.cancelErr != nil {
		return AgentRunRecord{}, store.cancelErr
	}
	return store.cancelled, nil
}

type fakeAgentRunSubmitter struct {
	result SubmittedRun
	err    error
	calls  int
}

func (submitter *fakeAgentRunSubmitter) SubmitAgentOnce(context.Context, string, string, string, map[string]any) (SubmittedRun, error) {
	submitter.calls++
	return submitter.result, submitter.err
}

func TestAgentRunServiceReturnsDuplicateBeforeSubmission(t *testing.T) {
	record := agentRunRecordFixture(domain.RunRunning)
	store := &fakeAgentRunStore{found: record}
	submitter := &fakeAgentRunSubmitter{}
	service := NewQueuedAgentRunService(submitter, store)
	accepted, created, err := service.Start(context.Background(), "demo", StartAgentRunInput{
		WorkflowVersionID: record.Version.ID,
		RequestKey:        "00000000-0000-4000-8000-000000000902",
		Input:             map[string]any{"topic": "x"},
	})
	if err != nil || created || accepted.RunID != record.Run.ID || submitter.calls != 0 {
		t.Fatalf("accepted=%+v created=%v submitCalls=%d error=%v", accepted, created, submitter.calls, err)
	}
}

func TestQueuedAgentRunServiceSubmitsWithoutLaunchingGoroutine(t *testing.T) {
	versionID := "version-1"
	submitter := &fakeAgentRunSubmitter{result: SubmittedRun{
		RunID: "queued-run", WorkflowVersionID: &versionID, WorkflowVersion: 2,
		Status: domain.RunQueued, StartedAt: time.Now().UTC(), Created: true,
	}}
	store := &fakeAgentRunStore{findErr: domain.ErrNotFound}
	service := NewQueuedAgentRunService(submitter, store)
	summary, created, err := service.Start(context.Background(), "demo", StartAgentRunInput{WorkflowVersionID: versionID, RequestKey: "key", Input: map[string]any{}})
	if err != nil || !created || submitter.calls != 1 || summary.RunID != "queued-run" || summary.Status != domain.RunQueued {
		t.Fatalf("summary=%+v created=%v calls=%d error=%v", summary, created, submitter.calls, err)
	}
}

func TestAgentRunViewStripsNodeAndPayloadData(t *testing.T) {
	record := agentRunRecordFixture(domain.RunCompleted)
	record.Run.Output = json.RawMessage(`{"answer":"safe"}`)
	record.Run.Error = &domain.PublicError{Code: "NODE_EXECUTION_FAILED", Message: "节点执行失败", NodeID: "secret-node"}
	record.Events = []domain.RunEvent{{Sequence: 1, Type: "node.completed", NodeID: "secret-node", Input: json.RawMessage(`{"token":"secret"}`), Output: json.RawMessage(`{"private":"value"}`), ActivePorts: []string{"secret"}, InputRedactedPaths: []string{"/token"}, Timestamp: time.Now().UTC()}}
	store := &fakeAgentRunStore{view: record}
	service := NewQueuedAgentRunService(&fakeAgentRunSubmitter{}, store)
	view, err := service.View(context.Background(), "demo", record.Run.ID, 0)
	if err != nil || len(view.Events) != 1 || view.Events[0].Sequence != 1 || !reflect.DeepEqual(view.Presentation, record.Version.AgentPresentation) {
		t.Fatalf("view=%+v error=%v", view, err)
	}
	encoded, _ := json.Marshal(view)
	for _, forbidden := range []string{"secret-node", "token", "private", "activePorts", "redactedPaths", "nodeId"} {
		if bytes.Contains(encoded, []byte(forbidden)) {
			t.Fatalf("public view leaked %q: %s", forbidden, encoded)
		}
	}
	if !bytes.Contains(encoded, []byte(`"answer":"safe"`)) || view.Run.Error == nil || view.Run.Error.Code != "NODE_EXECUTION_FAILED" {
		t.Fatalf("terminal output/error missing: %s", encoded)
	}
}

func TestAgentRunViewHidesActiveOutputAndTracksCursor(t *testing.T) {
	record := agentRunRecordFixture(domain.RunRunning)
	record.Run.Output = json.RawMessage(`{"premature":"secret"}`)
	record.Events = []domain.RunEvent{{Sequence: 8, Type: "node.started", Timestamp: time.Now().UTC()}}
	record.HasMore = true
	service := NewQueuedAgentRunService(&fakeAgentRunSubmitter{}, &fakeAgentRunStore{view: record})
	view, err := service.View(context.Background(), "demo", record.Run.ID, 4)
	store := service.store.(*fakeAgentRunStore)
	if err != nil || view.Run.Output != nil || view.NextSequence != 8 || !view.HasMore || store.afterSequence != 4 || store.limit != 200 {
		t.Fatalf("view=%+v error=%v", view, err)
	}
}

func TestAgentRunViewRejectsNegativeCursorWithoutStoreCall(t *testing.T) {
	store := &fakeAgentRunStore{}
	service := NewQueuedAgentRunService(&fakeAgentRunSubmitter{}, store)
	if _, err := service.View(context.Background(), "demo", "run", -1); !errors.Is(err, ErrInvalidWorkflowInput) || store.viewCalls != 0 {
		t.Fatalf("error=%v calls=%d", err, store.viewCalls)
	}
}

func TestAgentRunCancelPersistsCancellation(t *testing.T) {
	record := agentRunRecordFixture(domain.RunCancelling)
	store := &fakeAgentRunStore{cancelled: record}
	service := NewQueuedAgentRunService(&fakeAgentRunSubmitter{}, store)
	summary, err := service.Cancel(context.Background(), "demo", record.Run.ID)
	if err != nil || summary.Status != domain.RunCancelling || store.cancelCalls != 1 {
		t.Fatalf("summary=%+v cancelCalls=%d error=%v", summary, store.cancelCalls, err)
	}
}

func agentRunRecordFixture(status domain.RunStatus) AgentRunRecord {
	versionID := "00000000-0000-4000-8000-000000000904"
	startedAt := time.Now().UTC()
	return AgentRunRecord{
		Run:     domain.Run{ID: "00000000-0000-4000-8000-000000000903", WorkflowID: "workflow-1", WorkflowVersionID: &versionID, Mode: domain.RunModePublished, Status: status, Input: json.RawMessage(`{}`), StartedAt: startedAt},
		Version: domain.WorkflowVersion{ID: versionID, WorkflowID: "workflow-1", Version: 4, AgentPresentation: DefaultAgentPresentation("演示助手", "公开说明")},
		Events:  []domain.RunEvent{},
	}
}
