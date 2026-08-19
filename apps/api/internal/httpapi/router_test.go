package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/yyl1212/agent-studio/apps/api/internal/domain"
	"github.com/yyl1212/agent-studio/apps/api/internal/engine"
	"github.com/yyl1212/agent-studio/apps/api/internal/generated"
	"github.com/yyl1212/agent-studio/apps/api/internal/nodes"
	"github.com/yyl1212/agent-studio/apps/api/internal/nodes/builtin"
	"github.com/yyl1212/agent-studio/apps/api/internal/workflow"
	"github.com/yyl1212/agent-studio/apps/api/internal/workflowtemplate"
	"github.com/yyl1212/agent-studio/extensions/echo"
	"github.com/yyl1212/agent-studio/internal/nodepackage"
	"github.com/yyl1212/agent-studio/sdk/go/agentnode"
)

type fixtureWorkflowService struct {
	workflow           domain.Workflow
	panicOnList        bool
	templateExport     workflow.TemplateExport
	templatePreview    workflowtemplate.Preview
	templatePreviewErr error
	templateExportErr  error
	templateImportErr  error
	lastExportRevision int64
	lastImported       workflow.ImportWorkflowTemplateInput
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

func (service *fixtureWorkflowService) ExportTemplate(_ context.Context, _ string, revision int64) (workflow.TemplateExport, error) {
	service.lastExportRevision = revision
	return service.templateExport, service.templateExportErr
}

func (service *fixtureWorkflowService) PreviewTemplate(context.Context, json.RawMessage) (workflowtemplate.Preview, error) {
	return service.templatePreview, service.templatePreviewErr
}

func (service *fixtureWorkflowService) ImportTemplate(_ context.Context, input workflow.ImportWorkflowTemplateInput) (domain.Workflow, error) {
	service.lastImported = input
	return service.workflow, service.templateImportErr
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

func TestWorkflowTemplateRoutesUseStableShapes(t *testing.T) {
	dependencies := fixtureDeps()
	service := dependencies.Workflows.(*fixtureWorkflowService)
	service.templateExport = workflow.TemplateExport{Filename: "demo.workflow.json", Data: []byte("{\n  \"kind\": \"WorkflowTemplate\"\n}\n")}
	service.templatePreview = workflowtemplate.Preview{Valid: true, Issues: []domain.ValidationIssue{}, Summary: workflowtemplate.Summary{InputSchema: json.RawMessage(`{}`), NodeTypes: []workflowtemplate.NodeTypeSummary{}}}

	exported := performRequest(NewRouter(dependencies), http.MethodGet, "/api/workflows/w1/template?draftRevision=2", "")
	if exported.Code != http.StatusOK || service.lastExportRevision != 2 {
		t.Fatalf("status=%d revision=%d body=%s", exported.Code, service.lastExportRevision, exported.Body.String())
	}
	if exported.Header().Get("Content-Disposition") != `attachment; filename="demo.workflow.json"` ||
		exported.Header().Get("Cache-Control") != "no-store" || exported.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("headers=%v", exported.Header())
	}

	preview := performRequest(NewRouter(dependencies), http.MethodPost, "/api/workflow-templates/preview", validTemplateEnvelopeJSON())
	if preview.Code != http.StatusOK || !strings.Contains(preview.Body.String(), `"valid":true`) {
		t.Fatalf("status=%d body=%s", preview.Code, preview.Body.String())
	}

	imported := performRequest(NewRouter(dependencies), http.MethodPost, "/api/workflow-templates/import", validImportJSON())
	if imported.Code != http.StatusCreated || service.lastImported.Slug != "demo-copy" {
		t.Fatalf("status=%d body=%s", imported.Code, imported.Body.String())
	}
}

func TestWorkflowTemplateExportRejectsInvalidRevision(t *testing.T) {
	for _, path := range []string{
		"/api/workflows/w1/template",
		"/api/workflows/w1/template?draftRevision=0",
		"/api/workflows/w1/template?draftRevision=-1",
		"/api/workflows/w1/template?draftRevision=not-a-number",
	} {
		t.Run(path, func(t *testing.T) {
			recorder := performRequest(NewRouter(fixtureDeps()), http.MethodGet, path, "")
			assertJSONError(t, recorder, http.StatusBadRequest, "REQUEST_INVALID")
		})
	}
}

func TestWorkflowTemplateRoutesRejectUnknownAndTrailingJSON(t *testing.T) {
	tests := []struct {
		path string
		body string
	}{
		{path: "/api/workflow-templates/preview", body: strings.TrimSuffix(validTemplateEnvelopeJSON(), "}") + `,"unknown":true}`},
		{path: "/api/workflow-templates/preview", body: validTemplateEnvelopeJSON() + `{}`},
		{path: "/api/workflow-templates/import", body: strings.TrimSuffix(validImportJSON(), "}") + `,"unknown":true}`},
		{path: "/api/workflow-templates/import", body: validImportJSON() + `{}`},
	}
	for _, test := range tests {
		recorder := performRequest(NewRouter(fixtureDeps()), http.MethodPost, test.path, test.body)
		assertJSONError(t, recorder, http.StatusBadRequest, "REQUEST_INVALID")
	}
}

func TestWorkflowTemplatePreviewMapsTemplateDecodeErrorToBadRequest(t *testing.T) {
	dependencies := fixtureDeps()
	dependencies.Workflows.(*fixtureWorkflowService).templatePreviewErr = errors.New("decode workflow template: unknown field")
	recorder := performRequest(NewRouter(dependencies), http.MethodPost, "/api/workflow-templates/preview", validTemplateEnvelopeJSON())
	assertJSONError(t, recorder, http.StatusBadRequest, "REQUEST_INVALID")
}

func TestWorkflowTemplateImportMapsTemplateDecodeErrorToBadRequest(t *testing.T) {
	dependencies := fixtureDeps()
	dependencies.Workflows.(*fixtureWorkflowService).templateImportErr = workflow.ErrInvalidWorkflowTemplate
	recorder := performRequest(NewRouter(dependencies), http.MethodPost, "/api/workflow-templates/import", validImportJSON())
	assertJSONError(t, recorder, http.StatusBadRequest, "REQUEST_INVALID")
}

func TestWorkflowTemplatePreviewReturnsInvalidAsSuccessfulAnalysis(t *testing.T) {
	dependencies := fixtureDeps()
	dependencies.Workflows.(*fixtureWorkflowService).templatePreview = workflowtemplate.Preview{
		Valid:   false,
		Issues:  []domain.ValidationIssue{{Code: "NODE_TYPE_NOT_FOUND", Message: "节点类型或版本未注册", NodeID: "missing"}},
		Summary: workflowtemplate.Summary{InputSchema: json.RawMessage(`{}`), NodeTypes: []workflowtemplate.NodeTypeSummary{}},
	}
	recorder := performRequest(NewRouter(dependencies), http.MethodPost, "/api/workflow-templates/preview", validTemplateEnvelopeJSON())
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"valid":false`) || !strings.Contains(recorder.Body.String(), "NODE_TYPE_NOT_FOUND") {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestWorkflowTemplateImportMapsValidationAndSlugConflict(t *testing.T) {
	tests := []struct {
		name   string
		err    error
		code   string
		status int
	}{
		{name: "invalid template", err: &workflow.TemplateValidationError{Issues: []domain.ValidationIssue{{Code: "TEMPLATE_SECRET_CONFIG_FOUND", Message: "节点配置包含不允许导出的凭据字段", Path: "config.api_token"}}}, code: "WORKFLOW_TEMPLATE_INVALID", status: http.StatusUnprocessableEntity},
		{name: "slug conflict", err: domain.ErrSlugConflict, code: "WORKFLOW_SLUG_CONFLICT", status: http.StatusConflict},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			dependencies := fixtureDeps()
			dependencies.Workflows.(*fixtureWorkflowService).templateImportErr = test.err
			recorder := performRequest(NewRouter(dependencies), http.MethodPost, "/api/workflow-templates/import", validImportJSON())
			assertJSONError(t, recorder, test.status, test.code)
			if strings.Contains(recorder.Body.String(), "top-secret") {
				t.Fatalf("secret leaked: %s", recorder.Body.String())
			}
		})
	}
}

func fixtureWorkflowTemplate() workflowtemplate.Template {
	return workflowtemplate.Template{
		APIVersion: workflowtemplate.APIVersion,
		Kind:       workflowtemplate.Kind,
		Metadata:   workflowtemplate.Metadata{Name: "演示", Description: "HTTP 测试"},
		Spec:       workflowtemplate.Spec{Graph: domain.Graph{SchemaVersion: 1, Nodes: []domain.Node{}, Edges: []domain.Edge{}}},
	}
}

func validTemplateEnvelopeJSON() string {
	encoded, _ := json.Marshal(struct {
		Template json.RawMessage `json:"template"`
	}{Template: mustMarshalTemplate(fixtureWorkflowTemplate())})
	return string(encoded)
}

func validImportJSON() string {
	encoded, _ := json.Marshal(workflow.ImportWorkflowTemplateInput{
		Template: mustMarshalTemplate(fixtureWorkflowTemplate()), Name: "演示副本", Slug: "demo-copy", Description: "",
	})
	return string(encoded)
}

func mustMarshalTemplate(template workflowtemplate.Template) json.RawMessage {
	encoded, err := json.Marshal(template)
	if err != nil {
		panic(err)
	}
	return encoded
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
	if err := dependencies.Registry.RegisterPackage(nodepackage.RuntimeRecord{
		Summary: nodepackage.Summary{Name: "agent-studio.dev/core", Source: nodepackage.SourceBuiltin},
		Nodes: []nodepackage.NodeRef{
			{Type: "start", Version: "1"}, {Type: "template", Version: "1"},
			{Type: "condition", Version: "1"}, {Type: "end", Version: "1"},
		},
	}, builtin.RegisterCore); err != nil {
		t.Fatal(err)
	}
	listRecorder := performRequest(NewRouter(dependencies), http.MethodGet, "/api/node-types", "")
	if !strings.Contains(listRecorder.Body.String(), `"type":"start"`) || strings.Contains(listRecorder.Body.String(), `"inputs":null`) ||
		strings.Contains(listRecorder.Body.String(), `"outputs":null`) || strings.Contains(listRecorder.Body.String(), `"capabilities":null`) {
		t.Fatalf("definitions contain null ports: %s", listRecorder.Body.String())
	}
	resolveRecorder := performRequest(NewRouter(dependencies), http.MethodPost, "/api/node-types/start/1/resolve", `{"config":{"fields":[]}}`)
	if resolveRecorder.Code != http.StatusOK || !strings.Contains(resolveRecorder.Body.String(), `"inputs":[]`) {
		t.Fatalf("status=%d body=%s", resolveRecorder.Code, resolveRecorder.Body.String())
	}
}

func TestNodeAPIIncludesGeneratedOfficialExtensions(t *testing.T) {
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
	if len(definitions) != 3 {
		t.Fatalf("definitions=%+v", definitions)
	}
	byType := make(map[string]agentnode.Definition, len(definitions))
	for _, definition := range definitions {
		byType[definition.Type] = definition
	}
	retriever := byType["extension.retriever"]
	if retriever.Version != "1.0.0" || retriever.Category != "扩展" || len(retriever.ConfigSchema) == 0 || len(retriever.Inputs) != 1 || len(retriever.Outputs) != 1 {
		t.Fatalf("retriever=%+v", retriever)
	}
	webhook := byType["extension.webhook"]
	wantCapabilities := []agentnode.Capability{agentnode.CapabilityNetwork, agentnode.CapabilitySecrets}
	if webhook.Version != "1.0.0" || webhook.Category != "扩展" || len(webhook.ConfigSchema) == 0 || len(webhook.Inputs) != 1 || len(webhook.Outputs) != 2 || !reflect.DeepEqual(webhook.Capabilities, wantCapabilities) {
		t.Fatalf("webhook=%+v", webhook)
	}
	if !strings.Contains(recorder.Body.String(), `"inputs":[`) || !strings.Contains(recorder.Body.String(), `"outputs":[`) {
		t.Fatalf("ports must be JSON arrays: %s", recorder.Body.String())
	}

	resolveCases := []struct {
		path string
		body string
	}{
		{path: "/api/node-types/extension.retriever/1.0.0/resolve", body: `{"config":{"documents":[{"id":"doc-1","text":"hello world"}],"topK":1}}`},
		{path: "/api/node-types/extension.webhook/1.0.0/resolve", body: `{"config":{"path":"hooks/run"}}`},
	}
	for _, test := range resolveCases {
		resolved := performRequest(NewRouter(dependencies), http.MethodPost, test.path, test.body)
		if resolved.Code != http.StatusOK || !strings.Contains(resolved.Body.String(), `"inputs":[`) || !strings.Contains(resolved.Body.String(), `"outputs":[`) {
			t.Fatalf("path=%s status=%d body=%s", test.path, resolved.Code, resolved.Body.String())
		}
	}
}

func TestNodeAPIIncludesPackageSummary(t *testing.T) {
	dependencies := fixtureDeps()
	record := nodepackage.RuntimeRecord{
		Summary: nodepackage.Summary{
			Name: "example.com/nodes", DisplayName: "Example Nodes", Version: "v1.2.3",
			License: "Apache-2.0", Repository: "https://example.com/nodes", Source: nodepackage.SourceModule,
		},
		Nodes: []nodepackage.NodeRef{{Type: "extension.echo", Version: "1.0.0"}},
	}
	if err := dependencies.Registry.RegisterPackage(record, echo.Register); err != nil {
		t.Fatal(err)
	}
	recorder := performRequest(NewRouter(dependencies), http.MethodGet, "/api/node-types", "")
	if recorder.Code != http.StatusOK || strings.Contains(recorder.Body.String(), "/Users/example/private") {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var response []struct {
		agentnode.Definition
		Package nodepackage.Summary `json:"package"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if len(response) != 1 || response[0].Package != record.Summary || response[0].Type != "extension.echo" ||
		response[0].Inputs == nil || response[0].Outputs == nil || response[0].Capabilities == nil {
		t.Fatalf("response=%+v", response)
	}
}

func TestNodeAPIRejectsUnsafeOfficialExtensionConfigWithoutLeaks(t *testing.T) {
	dependencies := fixtureDeps()
	if err := generated.RegisterNodes(dependencies.Registry); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AGENT_STUDIO_WEBHOOK_URL", "https://environment-secret.example")

	tests := []struct {
		path      string
		body      string
		forbidden []string
	}{
		{
			path:      "/api/node-types/extension.retriever/1.0.0/resolve",
			body:      `{"config":{"documents":[{"id":"private-doc-id","text":"first"},{"id":"private-doc-id","text":"second"}],"topK":1}}`,
			forbidden: []string{"private-doc-id"},
		},
		{
			path:      "/api/node-types/extension.webhook/1.0.0/resolve",
			body:      `{"config":{"path":"private-path?token=secret"}}`,
			forbidden: []string{"private-path", "environment-secret"},
		},
	}
	for _, test := range tests {
		recorder := performRequest(NewRouter(dependencies), http.MethodPost, test.path, test.body)
		if recorder.Code < http.StatusBadRequest || recorder.Code >= http.StatusInternalServerError {
			t.Fatalf("path=%s status=%d body=%s", test.path, recorder.Code, recorder.Body.String())
		}
		for _, forbidden := range test.forbidden {
			if strings.Contains(recorder.Body.String(), forbidden) {
				t.Fatalf("path=%s leaked %q in body=%s", test.path, forbidden, recorder.Body.String())
			}
		}
	}
}

func fixtureDeps() Dependencies {
	registry := nodes.NewRegistry()
	return Dependencies{
		Registry: registry,
		Workflows: &fixtureWorkflowService{
			workflow:        domain.Workflow{ID: "w1", DraftRevision: 2},
			templatePreview: workflowtemplate.Preview{Issues: []domain.ValidationIssue{}, Summary: workflowtemplate.Summary{InputSchema: json.RawMessage(`{}`), NodeTypes: []workflowtemplate.NodeTypeSummary{}}},
		},
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
