package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/yyl1212/agent-studio/apps/api/internal/domain"
	"github.com/yyl1212/agent-studio/apps/api/internal/engine"
	"github.com/yyl1212/agent-studio/apps/api/internal/workflow"
)

func TestAgentRunUsesBodyVersionIDAndStreamsNDJSON(t *testing.T) {
	dependencies := fixtureDeps()
	router := NewRouter(dependencies)
	recorder := performRequest(router, http.MethodPost, "/api/agents/demo/runs", `{"workflowVersionId":"v1","input":{"topic":"x"}}`)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if got := recorder.Header().Get("Content-Type"); got != "application/x-ndjson" {
		t.Fatalf("content-type=%s", got)
	}
	if recorder.Header().Get("Cache-Control") != "no-store" || recorder.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("headers=%v", recorder.Header())
	}
	lines := strings.Split(strings.TrimSpace(recorder.Body.String()), "\n")
	if len(lines) != 2 || !strings.Contains(lines[0], `"type":"run.started"`) {
		t.Fatalf("body=%s", recorder.Body.String())
	}
	if !recorder.Flushed {
		t.Fatal("stream was not flushed")
	}
	if dependencies.Runner.(*fixtureRunner).LastVersionID != "v1" {
		t.Fatalf("version=%s", dependencies.Runner.(*fixtureRunner).LastVersionID)
	}
}

func TestPrepareErrorStaysJSONBeforeStreaming(t *testing.T) {
	dependencies := fixtureDeps()
	dependencies.Runner.(*fixtureRunner).prepareErr = domain.ErrNotFound
	recorder := performRequest(NewRouter(dependencies), http.MethodPost, "/api/agents/demo/runs", `{"workflowVersionId":"missing","input":{}}`)
	assertJSONError(t, recorder, http.StatusNotFound, "NOT_FOUND")
	if strings.Contains(recorder.Header().Get("Content-Type"), "ndjson") {
		t.Fatalf("content-type=%s", recorder.Header().Get("Content-Type"))
	}
}

func TestStreamObserverEncodesOneJSONObjectPerLine(t *testing.T) {
	recorder := httptest.NewRecorder()
	observer := &streamObserver{writer: recorder, flusher: recorder}
	if err := observer.Observe(context.Background(), engine.Event{Sequence: 1, Type: "run.started", RunID: "r1"}); err != nil {
		t.Fatal(err)
	}
	var event engine.Event
	if err := json.Unmarshal(bytes.TrimSpace(recorder.Body.Bytes()), &event); err != nil {
		t.Fatal(err)
	}
	if event.Type != "run.started" || !recorder.Flushed {
		t.Fatalf("event=%+v flushed=%v", event, recorder.Flushed)
	}
}

type cancellationRunner struct {
	executionContextCancelled bool
}

func (*cancellationRunner) PrepareDraft(context.Context, string, int64, map[string]any) (*workflow.PreparedRun, error) {
	return &workflow.PreparedRun{RunID: "run-1"}, nil
}

func (*cancellationRunner) PrepareAgent(context.Context, string, string, map[string]any) (*workflow.PreparedRun, error) {
	return &workflow.PreparedRun{RunID: "run-1"}, nil
}

func (runner *cancellationRunner) Execute(ctx context.Context, _ *workflow.PreparedRun, _ engine.Observer) (engine.RunResult, error) {
	runner.executionContextCancelled = ctx.Err() != nil
	return engine.RunResult{}, ctx.Err()
}

func TestRunUsesRequestContextForCancellation(t *testing.T) {
	dependencies := fixtureDeps()
	runner := &cancellationRunner{}
	dependencies.Runner = runner
	request := httptest.NewRequest(http.MethodPost, "/api/agents/demo/runs", strings.NewReader(`{"workflowVersionId":"v1","input":{}}`))
	request.Header.Set("Content-Type", "application/json")
	ctx, cancel := context.WithCancel(request.Context())
	cancel()
	request = request.WithContext(ctx)
	recorder := httptest.NewRecorder()

	NewRouter(dependencies).ServeHTTP(recorder, request)

	if !runner.executionContextCancelled {
		t.Fatal("runner did not receive the cancelled request context")
	}
}
