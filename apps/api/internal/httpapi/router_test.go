package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/yyl1212/agent-studio/apps/api/internal/domain"
	"github.com/yyl1212/agent-studio/apps/api/internal/engine"
	"github.com/yyl1212/agent-studio/apps/api/internal/generated"
	"github.com/yyl1212/agent-studio/apps/api/internal/nodes"
	"github.com/yyl1212/agent-studio/apps/api/internal/nodes/builtin"
	"github.com/yyl1212/agent-studio/apps/api/internal/workflow"
	"github.com/yyl1212/agent-studio/sdk/go/agentnode"
)

type fixtureWorkflowService struct {
	workflow    domain.Workflow
	panicOnList bool
}

func (service *fixtureWorkflowService) List(context.Context) ([]domain.Workflow, error) {
	if service.panicOnList {
		panic("test panic")
	}
	return []domain.Workflow{service.workflow}, nil
}

func (service *fixtureWorkflowService) Get(context.Context, string) (domain.Workflow, error) {
	return service.workflow, nil
}

func (service *fixtureWorkflowService) Create(_ context.Context, input workflow.CreateWorkflowInput) (domain.Workflow, error) {
	service.workflow.Name = input.Name
	service.workflow.Slug = input.Slug
	return service.workflow, nil
}

func (service *fixtureWorkflowService) SaveDraft(context.Context, string, int64, domain.Graph) (domain.Workflow, error) {
	return domain.Workflow{}, domain.ErrRevisionConflict
}

func (*fixtureWorkflowService) Validate(context.Context, string) []domain.ValidationIssue {
	return nil
}

func (*fixtureWorkflowService) Publish(context.Context, string, int64) (domain.WorkflowVersion, error) {
	return domain.WorkflowVersion{ID: "v1", Version: 1}, nil
}

func (*fixtureWorkflowService) AgentManifest(context.Context, string) (workflow.AgentManifest, error) {
	return workflow.AgentManifest{WorkflowVersionID: "v1", Version: 1, Title: "Demo", InputSchema: json.RawMessage(`{"type":"object"}`)}, nil
}

type fixtureRunner struct {
	LastVersionID string
	prepareErr    error
}

func (runner *fixtureRunner) PrepareDraft(context.Context, string, int64, map[string]any) (*workflow.PreparedRun, error) {
	if runner.prepareErr != nil {
		return nil, runner.prepareErr
	}
	return &workflow.PreparedRun{RunID: "run-1"}, nil
}

func (runner *fixtureRunner) PrepareAgent(_ context.Context, _ string, versionID string, _ map[string]any) (*workflow.PreparedRun, error) {
	runner.LastVersionID = versionID
	if runner.prepareErr != nil {
		return nil, runner.prepareErr
	}
	return &workflow.PreparedRun{RunID: "run-1"}, nil
}

func (*fixtureRunner) Execute(ctx context.Context, prepared *workflow.PreparedRun, observer engine.Observer) (engine.RunResult, error) {
	if err := observer.Observe(ctx, engine.Event{Sequence: 1, Type: "run.started", RunID: prepared.RunID}); err != nil {
		return engine.RunResult{}, err
	}
	if err := observer.Observe(ctx, engine.Event{Sequence: 2, Type: "run.completed", RunID: prepared.RunID, Output: "ok"}); err != nil {
		return engine.RunResult{}, err
	}
	return engine.RunResult{RunID: prepared.RunID, Output: "ok"}, nil
}

type fixtureRunReader struct{}

func (fixtureRunReader) GetRun(context.Context, string) (domain.Run, []domain.NodeRun, error) {
	return domain.Run{ID: "run-1"}, nil, nil
}

func (fixtureRunReader) ListRuns(context.Context, string, int) ([]domain.Run, error) {
	return []domain.Run{}, nil
}

type fixtureReady struct{ err error }

func (ready fixtureReady) Ready(context.Context) error { return ready.err }

func TestRevisionConflictUsesStableErrorShape(t *testing.T) {
	dependencies := fixtureDeps()
	recorder := performRequest(NewRouter(dependencies), http.MethodPut, "/api/workflows/w1", `{"draftRevision":1,"graph":{"schemaVersion":1,"nodes":[],"edges":[]}}`)
	assertJSONError(t, recorder, http.StatusConflict, "WORKFLOW_REVISION_CONFLICT")
}

func TestRouterRejectsUnknownJSONAndAppliesCORS(t *testing.T) {
	dependencies := fixtureDeps()
	router := NewRouter(dependencies)
	recorder := performRequest(router, http.MethodPost, "/api/workflows", `{"name":"Demo","slug":"demo","unknown":true}`)
	assertJSONError(t, recorder, http.StatusBadRequest, "REQUEST_INVALID")

	request := httptest.NewRequest(http.MethodOptions, "/api/workflows", nil)
	request.Header.Set("Origin", "http://localhost:5173")
	request.Header.Set("Access-Control-Request-Method", http.MethodPost)
	recorder = httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	if recorder.Header().Get("Access-Control-Allow-Origin") != "http://localhost:5173" {
		t.Fatalf("CORS headers=%v", recorder.Header())
	}
}

func TestRouterRejectsTrailingJSON(t *testing.T) {
	recorder := performRequest(NewRouter(fixtureDeps()), http.MethodPost, "/api/workflows", `{"name":"Demo","slug":"demo"}{}`)
	assertJSONError(t, recorder, http.StatusBadRequest, "REQUEST_INVALID")
}

func TestRecovererReturnsSafeRequestID(t *testing.T) {
	dependencies := fixtureDeps()
	dependencies.Workflows.(*fixtureWorkflowService).panicOnList = true
	recorder := performRequest(NewRouter(dependencies), http.MethodGet, "/api/workflows", "")
	assertJSONError(t, recorder, http.StatusInternalServerError, "INTERNAL_ERROR")
	var response ErrorResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.RequestID == "" || strings.Contains(recorder.Body.String(), "test panic") {
		t.Fatalf("body=%s", recorder.Body.String())
	}
}

func TestReadyReturnsServiceUnavailable(t *testing.T) {
	dependencies := fixtureDeps()
	dependencies.Readiness = fixtureReady{err: errors.New("database unavailable")}
	recorder := performRequest(NewRouter(dependencies), http.MethodGet, "/readyz", "")
	assertJSONError(t, recorder, http.StatusServiceUnavailable, "NOT_READY")
}

func TestNodeAPIRendersMissingPortSlicesAsArrays(t *testing.T) {
	dependencies := fixtureDeps()
	if err := builtin.RegisterCore(dependencies.Registry); err != nil {
		t.Fatal(err)
	}
	listRecorder := performRequest(NewRouter(dependencies), http.MethodGet, "/api/node-types", "")
	if strings.Contains(listRecorder.Body.String(), `"inputs":null`) || strings.Contains(listRecorder.Body.String(), `"outputs":null`) {
		t.Fatalf("definitions contain null ports: %s", listRecorder.Body.String())
	}
	resolveRecorder := performRequest(NewRouter(dependencies), http.MethodPost, "/api/node-types/start/1/resolve", `{"config":{"fields":[]}}`)
	if resolveRecorder.Code != http.StatusOK || !strings.Contains(resolveRecorder.Body.String(), `"inputs":[]`) {
		t.Fatalf("status=%d body=%s", resolveRecorder.Code, resolveRecorder.Body.String())
	}
}

func TestNodeAPIIncludesGeneratedEchoExtension(t *testing.T) {
	dependencies := fixtureDeps()
	if err := generated.RegisterNodes(dependencies.Registry); err != nil {
		t.Fatal(err)
	}
	recorder := performRequest(NewRouter(dependencies), http.MethodGet, "/api/node-types", "")
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var definitions []agentnode.Definition
	if err := json.Unmarshal(recorder.Body.Bytes(), &definitions); err != nil {
		t.Fatal(err)
	}
	if len(definitions) != 1 {
		t.Fatalf("definitions=%+v", definitions)
	}
	definition := definitions[0]
	if definition.Type != "extension.echo" || definition.Version != "1.0.0" || definition.Category != "扩展" {
		t.Fatalf("definition=%+v", definition)
	}
	if len(definition.Inputs) != 1 || len(definition.Outputs) != 1 {
		t.Fatalf("inputs=%+v outputs=%+v", definition.Inputs, definition.Outputs)
	}
	if !strings.Contains(recorder.Body.String(), `"inputs":[`) || !strings.Contains(recorder.Body.String(), `"outputs":[`) {
		t.Fatalf("ports must be JSON arrays: %s", recorder.Body.String())
	}
}

func fixtureDeps() Dependencies {
	registry := nodes.NewRegistry()
	return Dependencies{
		Registry:  registry,
		Workflows: &fixtureWorkflowService{workflow: domain.Workflow{ID: "w1", DraftRevision: 2}},
		Runner:    &fixtureRunner{},
		Runs:      fixtureRunReader{},
		Readiness: fixtureReady{},
		WebOrigin: "http://localhost:5173",
	}
}

func performRequest(handler http.Handler, method, path, body string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	return recorder
}

func assertJSONError(t *testing.T, recorder *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	if recorder.Code != status {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var response ErrorResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Code != code || response.Message == "" {
		t.Fatalf("response=%+v", response)
	}
}
