package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/yyl1212/agent-studio/apps/api/internal/engine"
	"github.com/yyl1212/agent-studio/apps/api/internal/workflow"
)

func TestDraftRunSubmitsThenFollowsDurableEvents(t *testing.T) {
	submitter := &durableHTTPSubmitter{}
	follower := &durableHTTPFollower{}
	router := NewRouter(Dependencies{RunSubmissions: submitter, RunFollower: follower})
	request := httptest.NewRequest(http.MethodPost, "/api/workflows/workflow-1/test-runs", strings.NewReader(`{"draftRevision":3,"input":{"topic":"Agent"}}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusOK || submitter.draftCalls != 1 || follower.runID != "queued-run" {
		t.Fatalf("status=%d calls=%d followed=%q body=%s", response.Code, submitter.draftCalls, follower.runID, response.Body.String())
	}
	if response.Header().Get("Content-Type") != "application/x-ndjson" || !strings.Contains(response.Body.String(), `"type":"run.queued"`) || !strings.Contains(response.Body.String(), `"type":"run.completed"`) {
		t.Fatalf("headers=%v body=%s", response.Header(), response.Body.String())
	}
}

type durableHTTPSubmitter struct{ draftCalls int }

func (submitter *durableHTTPSubmitter) SubmitDraft(context.Context, string, int64, map[string]any) (workflow.SubmittedRun, error) {
	submitter.draftCalls++
	return workflow.SubmittedRun{RunID: "queued-run", Created: true}, nil
}

func (*durableHTTPSubmitter) SubmitAgent(context.Context, string, string, map[string]any) (workflow.SubmittedRun, error) {
	return workflow.SubmittedRun{RunID: "queued-agent", Created: true}, nil
}

type durableHTTPFollower struct{ runID string }

func (follower *durableHTTPFollower) Follow(ctx context.Context, runID string, observer engine.Observer) error {
	follower.runID = runID
	now := time.Now().UTC()
	for _, event := range []engine.Event{
		{RunID: runID, Sequence: 1, Type: "run.queued", ActivePorts: []string{}, InputRedactedPaths: []string{}, OutputRedactedPaths: []string{}, Timestamp: now},
		{RunID: runID, Sequence: 2, Type: "run.completed", ActivePorts: []string{}, InputRedactedPaths: []string{}, OutputRedactedPaths: []string{}, Timestamp: now},
	} {
		if err := observer.Observe(ctx, event); err != nil {
			return err
		}
	}
	return nil
}
