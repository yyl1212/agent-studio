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

type fakeAgentRunPreparer struct {
	prepared *PreparedRun
	created  bool
	err      error
	calls    int
}

func (preparer *fakeAgentRunPreparer) PrepareAgentOnce(context.Context, string, string, string, map[string]any) (*PreparedRun, bool, error) {
	preparer.calls++
	return preparer.prepared, preparer.created, preparer.err
}

type fakeAgentRunReservation struct {
	launched      *PreparedRun
	launchContext context.Context
	released      bool
	panicOnLaunch bool
}

func (reservation *fakeAgentRunReservation) Launch(ctx context.Context, prepared *PreparedRun) {
	if reservation.panicOnLaunch {
		panic("launch panic")
	}
	reservation.launched = prepared
	reservation.launchContext = ctx
}

func (reservation *fakeAgentRunReservation) Release() {
	reservation.released = true
}

type fakeAgentRunLauncher struct {
	reservation  *fakeAgentRunReservation
	err          error
	reserveCalls int
}

func (launcher *fakeAgentRunLauncher) Reserve() (AgentRunReservation, error) {
	launcher.reserveCalls++
	if launcher.err != nil {
		return nil, launcher.err
	}
	if launcher.reservation == nil {
		launcher.reservation = &fakeAgentRunReservation{}
	}
	return launcher.reservation, nil
}

type fakeAgentLocalRunCanceller struct {
	runID string
}

func (canceller *fakeAgentLocalRunCanceller) CancelLocal(runID string) bool {
	canceller.runID = runID
	return true
}

func TestAgentRunServiceReturnsDuplicateBeforeReservation(t *testing.T) {
	record := agentRunRecordFixture(domain.RunRunning)
	store := &fakeAgentRunStore{found: record}
	launcher := &fakeAgentRunLauncher{}
	service := NewAgentRunService(&fakeAgentRunPreparer{}, store, launcher, nil)
	accepted, created, err := service.Start(context.Background(), "demo", StartAgentRunInput{
		WorkflowVersionID: record.Version.ID,
		RequestKey:        "00000000-0000-4000-8000-000000000902",
		Input:             map[string]any{"topic": "x"},
	})
	if err != nil || created || accepted.RunID != record.Run.ID || launcher.reserveCalls != 0 {
		t.Fatalf("accepted=%+v created=%v reserveCalls=%d error=%v", accepted, created, launcher.reserveCalls, err)
	}
}

func TestAgentRunServiceReleasesReservationWhenPreparationLosesRace(t *testing.T) {
	record := agentRunRecordFixture(domain.RunRunning)
	store := &fakeAgentRunStore{findErr: domain.ErrNotFound, found: record}
	preparer := &fakeAgentRunPreparer{created: false}
	launcher := &fakeAgentRunLauncher{}
	service := NewAgentRunService(preparer, store, launcher, nil)
	accepted, created, err := service.Start(context.Background(), "demo", StartAgentRunInput{WorkflowVersionID: record.Version.ID, RequestKey: "key", Input: map[string]any{}})
	if err != nil || created || accepted.RunID != record.Run.ID || !launcher.reservation.released || launcher.reservation.launched != nil {
		t.Fatalf("accepted=%+v created=%v reservation=%+v error=%v", accepted, created, launcher.reservation, err)
	}
}

func TestAgentRunServiceLaunchesOnlyCreatedRun(t *testing.T) {
	record := agentRunRecordFixture(domain.RunRunning)
	store := &fakeAgentRunStore{findErr: domain.ErrNotFound}
	prepared := &PreparedRun{RunID: record.Run.ID, WorkflowVersionID: &record.Version.ID, WorkflowVersion: record.Version.Version}
	preparer := &fakeAgentRunPreparer{prepared: prepared, created: true}
	launcher := &fakeAgentRunLauncher{}
	service := NewAgentRunService(preparer, store, launcher, nil)
	type requestKey struct{}
	requestContext := context.WithValue(context.Background(), requestKey{}, "request-value")
	accepted, created, err := service.Start(requestContext, "demo", StartAgentRunInput{WorkflowVersionID: record.Version.ID, RequestKey: "key", Input: map[string]any{}})
	if err != nil || !created || accepted.RunID != prepared.RunID || launcher.reservation.launched != prepared || launcher.reservation.launchContext != requestContext || launcher.reservation.released {
		t.Fatalf("accepted=%+v created=%v reservation=%+v error=%v", accepted, created, launcher.reservation, err)
	}
}

func TestAgentRunServiceReleasesReservationWhenLaunchPanics(t *testing.T) {
	record := agentRunRecordFixture(domain.RunRunning)
	prepared := &PreparedRun{RunID: record.Run.ID, WorkflowVersionID: &record.Version.ID, WorkflowVersion: record.Version.Version}
	reservation := &fakeAgentRunReservation{panicOnLaunch: true}
	service := NewAgentRunService(
		&fakeAgentRunPreparer{prepared: prepared, created: true},
		&fakeAgentRunStore{findErr: domain.ErrNotFound},
		&fakeAgentRunLauncher{reservation: reservation},
		nil,
	)
	func() {
		defer func() {
			if recover() == nil {
				t.Fatal("expected launch panic")
			}
		}()
		_, _, _ = service.Start(context.Background(), "demo", StartAgentRunInput{WorkflowVersionID: record.Version.ID, RequestKey: "key", Input: map[string]any{}})
	}()
	if !reservation.released {
		t.Fatal("reservation was not released after panic")
	}
}

func TestAgentRunViewStripsNodeAndPayloadData(t *testing.T) {
	record := agentRunRecordFixture(domain.RunCompleted)
	record.Run.Output = json.RawMessage(`{"answer":"safe"}`)
	record.Run.Error = &domain.PublicError{Code: "NODE_EXECUTION_FAILED", Message: "节点执行失败", NodeID: "secret-node"}
	record.Events = []domain.RunEvent{{Sequence: 1, Type: "node.completed", NodeID: "secret-node", Input: json.RawMessage(`{"token":"secret"}`), Output: json.RawMessage(`{"private":"value"}`), ActivePorts: []string{"secret"}, InputRedactedPaths: []string{"/token"}, Timestamp: time.Now().UTC()}}
	store := &fakeAgentRunStore{view: record}
	service := NewAgentRunService(&fakeAgentRunPreparer{}, store, &fakeAgentRunLauncher{}, nil)
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
	service := NewAgentRunService(&fakeAgentRunPreparer{}, &fakeAgentRunStore{view: record}, &fakeAgentRunLauncher{}, nil)
	view, err := service.View(context.Background(), "demo", record.Run.ID, 4)
	store := service.store.(*fakeAgentRunStore)
	if err != nil || view.Run.Output != nil || view.NextSequence != 8 || !view.HasMore || store.afterSequence != 4 || store.limit != 200 {
		t.Fatalf("view=%+v error=%v", view, err)
	}
}

func TestAgentRunViewRejectsNegativeCursorWithoutStoreCall(t *testing.T) {
	store := &fakeAgentRunStore{}
	service := NewAgentRunService(&fakeAgentRunPreparer{}, store, &fakeAgentRunLauncher{}, nil)
	if _, err := service.View(context.Background(), "demo", "run", -1); !errors.Is(err, ErrInvalidWorkflowInput) || store.viewCalls != 0 {
		t.Fatalf("error=%v calls=%d", err, store.viewCalls)
	}
}

func TestAgentRunCancelRequestsLocalCancellation(t *testing.T) {
	record := agentRunRecordFixture(domain.RunCancelling)
	canceller := &fakeAgentLocalRunCanceller{}
	service := NewAgentRunService(&fakeAgentRunPreparer{}, &fakeAgentRunStore{cancelled: record}, &fakeAgentRunLauncher{}, canceller)
	summary, err := service.Cancel(context.Background(), "demo", record.Run.ID)
	if err != nil || summary.Status != domain.RunCancelling || canceller.runID != record.Run.ID {
		t.Fatalf("summary=%+v cancelled=%q error=%v", summary, canceller.runID, err)
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
