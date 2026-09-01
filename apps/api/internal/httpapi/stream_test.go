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
	if dependencies.RunSubmissions.(*fixtureRunner).LastVersionID != "v1" {
		t.Fatalf("version=%s", dependencies.RunSubmissions.(*fixtureRunner).LastVersionID)
	}
}

func TestRetryRunRequiresCanonicalSingleIdempotencyKeyAndStreamsNDJSON(t *testing.T) {
	dependencies := fixtureDeps()
	request := httptest.NewRequest(http.MethodPost, "/api/runs/11111111-1111-4111-8111-111111111111/retries", strings.NewReader(`{"secretValues":{"/token":"new-secret"}}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", "33333333-3333-4333-8333-333333333333")
	recorder := httptest.NewRecorder()
	NewRouter(dependencies).ServeHTTP(recorder, request)
	manager := dependencies.RunManagement.(*fixtureRunManager)
	if recorder.Code != http.StatusOK || manager.retryKey != "33333333-3333-4333-8333-333333333333" || manager.retryBody.SecretValues["/token"] != "new-secret" {
		t.Fatalf("status=%d key=%q body=%+v response=%s", recorder.Code, manager.retryKey, manager.retryBody, recorder.Body.String())
	}
	if recorder.Header().Get("Content-Type") != "application/x-ndjson" || recorder.Header().Get("Cache-Control") != "no-store" || recorder.Header().Get("X-Content-Type-Options") != "nosniff" || !recorder.Flushed {
		t.Fatalf("headers=%v flushed=%v", recorder.Header(), recorder.Flushed)
	}

	for _, headers := range [][]string{nil, {"bad"}, {"33333333-3333-4333-8333-333333333333", "44444444-4444-4444-8444-444444444444"}} {
		dependencies = fixtureDeps()
		request = httptest.NewRequest(http.MethodPost, "/api/runs/11111111-1111-4111-8111-111111111111/retries", strings.NewReader(`{"secretValues":{}}`))
		for _, value := range headers {
			request.Header.Add("Idempotency-Key", value)
		}
		recorder = httptest.NewRecorder()
		NewRouter(dependencies).ServeHTTP(recorder, request)
		assertJSONError(t, recorder, http.StatusBadRequest, "REQUEST_INVALID")
	}
}

func TestRetryRunDuplicateReturnsOnlyExistingRunID(t *testing.T) {
	dependencies := fixtureDeps()
	manager := dependencies.RunManagement.(*fixtureRunManager)
	manager.err = &workflow.RunRetryAlreadyCreatedError{RunID: "55555555-5555-4555-8555-555555555555"}
	request := httptest.NewRequest(http.MethodPost, "/api/runs/11111111-1111-4111-8111-111111111111/retries", strings.NewReader(`{"secretValues":{"/token":"must-not-leak"}}`))
	request.Header.Set("Idempotency-Key", "33333333-3333-4333-8333-333333333333")
	recorder := httptest.NewRecorder()
	NewRouter(dependencies).ServeHTTP(recorder, request)
	assertJSONError(t, recorder, http.StatusConflict, "RUN_RETRY_ALREADY_CREATED")
	if !strings.Contains(recorder.Body.String(), `"details":{"runId":"55555555-5555-4555-8555-555555555555"}`) || strings.Contains(recorder.Body.String(), "must-not-leak") {
		t.Fatalf("body=%s", recorder.Body.String())
	}
}

func TestPrepareErrorStaysJSONBeforeStreaming(t *testing.T) {
	dependencies := fixtureDeps()
	dependencies.RunSubmissions.(*fixtureRunner).submitErr = domain.ErrNotFound
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

type cancellationFollower struct {
	followContextCancelled bool
}

func (follower *cancellationFollower) Follow(ctx context.Context, _ string, _ engine.Observer) error {
	follower.followContextCancelled = ctx.Err() != nil
	return ctx.Err()
}

func TestRunFollowerUsesRequestContextForDisconnect(t *testing.T) {
	dependencies := fixtureDeps()
	follower := &cancellationFollower{}
	dependencies.RunFollower = follower
	request := httptest.NewRequest(http.MethodPost, "/api/agents/demo/runs", strings.NewReader(`{"workflowVersionId":"v1","input":{}}`))
	request.Header.Set("Content-Type", "application/json")
	ctx, cancel := context.WithCancel(request.Context())
	cancel()
	request = request.WithContext(ctx)
	recorder := httptest.NewRecorder()

	NewRouter(dependencies).ServeHTTP(recorder, request)

	if !follower.followContextCancelled {
		t.Fatal("follower did not receive the cancelled request context")
	}
}
