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
	"time"

	"github.com/yyl1212/agent-studio/apps/api/internal/domain"
	"github.com/yyl1212/agent-studio/apps/api/internal/engine"
	"github.com/yyl1212/agent-studio/apps/api/internal/generated"
	"github.com/yyl1212/agent-studio/apps/api/internal/modelprovider"
	"github.com/yyl1212/agent-studio/apps/api/internal/nodes"
	"github.com/yyl1212/agent-studio/apps/api/internal/nodes/builtin"
	"github.com/yyl1212/agent-studio/apps/api/internal/observability"
	"github.com/yyl1212/agent-studio/apps/api/internal/workflow"
	"github.com/yyl1212/agent-studio/apps/api/internal/workflowtemplate"
	"github.com/yyl1212/agent-studio/extensions/echo"
	"github.com/yyl1212/agent-studio/internal/buildinfo"
	"github.com/yyl1212/agent-studio/internal/nodepackage"
	"github.com/yyl1212/agent-studio/sdk/go/agentnode"
)

type fixtureWorkflowService struct {
	workflow                 domain.Workflow
	panicOnList              bool
	templateExport           workflow.TemplateExport
	templatePreview          workflowtemplate.Preview
	templatePreviewErr       error
	templateExportErr        error
	templateImportErr        error
	validateErr              error
	saveDraftErr             error
	manifestErr              error
	lastExportRevision       int64
	lastImported             workflow.ImportWorkflowTemplateInput
	lastPresentationID       string
	lastPresentationRevision int64
	lastPresentation         domain.AgentPresentation
	lastRequestID            string
}

func (service *fixtureWorkflowService) List(context.Context) ([]domain.Workflow, error) {
	if service.panicOnList {
		panic("test panic")
	}
	return []domain.Workflow{service.workflow}, nil
}

func (service *fixtureWorkflowService) Get(ctx context.Context, _ string) (domain.Workflow, error) {
	service.lastRequestID = observability.RequestIDFromContext(ctx)
	return service.workflow, nil
}

func (service *fixtureWorkflowService) Create(_ context.Context, input workflow.CreateWorkflowInput) (domain.Workflow, error) {
	service.workflow.Name = input.Name
	service.workflow.Slug = input.Slug
	return service.workflow, nil
}

func (service *fixtureWorkflowService) SaveDraft(context.Context, string, int64, domain.Graph) (domain.Workflow, error) {
	if service.saveDraftErr != nil {
		return domain.Workflow{}, service.saveDraftErr
	}
	return domain.Workflow{}, domain.ErrRevisionConflict
}

func (service *fixtureWorkflowService) SaveAgentPresentation(_ context.Context, id string, revision int64, presentation domain.AgentPresentation) (domain.Workflow, error) {
	service.lastPresentationID = id
	service.lastPresentationRevision = revision
	service.lastPresentation = presentation
	if presentation.Title == "" {
		return domain.Workflow{}, workflow.ErrInvalidAgentPresentation
	}
	service.workflow.AgentPresentation = presentation
	service.workflow.DraftRevision = revision + 1
	return service.workflow, nil
}

func (service *fixtureWorkflowService) Validate(context.Context, string) ([]domain.ValidationIssue, error) {
	return nil, service.validateErr
}

func (*fixtureWorkflowService) Publish(context.Context, string, int64) (domain.WorkflowVersion, error) {
	return domain.WorkflowVersion{ID: "v1", Version: 1}, nil
}

func (service *fixtureWorkflowService) AgentManifest(context.Context, string) (workflow.AgentManifest, error) {
	return workflow.AgentManifest{WorkflowVersionID: "v1", Version: 1, Title: "Demo", InputSchema: json.RawMessage(`{"type":"object"}`)}, service.manifestErr
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
	submitErr     error
}

func (runner *fixtureRunner) SubmitDraft(context.Context, string, int64, map[string]any) (workflow.SubmittedRun, error) {
	if runner.submitErr != nil {
		return workflow.SubmittedRun{}, runner.submitErr
	}
	return workflow.SubmittedRun{RunID: "run-1", Created: true}, nil
}

func (runner *fixtureRunner) SubmitAgent(_ context.Context, _ string, versionID string, _ map[string]any) (workflow.SubmittedRun, error) {
	runner.LastVersionID = versionID
	if runner.submitErr != nil {
		return workflow.SubmittedRun{}, runner.submitErr
	}
	return workflow.SubmittedRun{RunID: "run-1", Created: true}, nil
}

func (*fixtureRunner) Follow(ctx context.Context, runID string, observer engine.Observer) error {
	if err := observer.Observe(ctx, engine.Event{Sequence: 1, Type: "run.started", RunID: runID}); err != nil {
		return err
	}
	if err := observer.Observe(ctx, engine.Event{Sequence: 2, Type: "run.completed", RunID: runID, Output: "ok"}); err != nil {
		return err
	}
	return nil
}

type fixtureRunReader struct{}

func (fixtureRunReader) GetRun(context.Context, string) (domain.Run, []domain.NodeRun, error) {
	return domain.Run{ID: "run-1"}, nil, nil
}

func (fixtureRunReader) ListRuns(context.Context, string, int) ([]domain.Run, error) {
	return []domain.Run{}, nil
}

type fixtureAgentRuns struct {
	summary       workflow.AgentRunPublicSummary
	view          workflow.AgentRunPublicView
	err           error
	created       bool
	startCalls    int
	viewCalls     int
	cancelCalls   int
	slug          string
	runID         string
	afterSequence int64
	startInput    workflow.StartAgentRunInput
}

func (runs *fixtureAgentRuns) Start(_ context.Context, slug string, input workflow.StartAgentRunInput) (workflow.AgentRunPublicSummary, bool, error) {
	runs.startCalls++
	runs.slug, runs.startInput = slug, input
	return runs.summary, runs.created, runs.err
}

func (runs *fixtureAgentRuns) View(_ context.Context, slug, runID string, afterSequence int64) (workflow.AgentRunPublicView, error) {
	runs.viewCalls++
	runs.slug, runs.runID, runs.afterSequence = slug, runID, afterSequence
	return runs.view, runs.err
}

func (runs *fixtureAgentRuns) Cancel(_ context.Context, slug, runID string) (workflow.AgentRunPublicSummary, error) {
	runs.cancelCalls++
	runs.slug, runs.runID = slug, runID
	return runs.summary, runs.err
}

type fixtureWorkflowManager struct {
	request  workflow.WorkflowSummaryRequest
	workflow domain.Workflow
	err      error
	mutation string
	id       string
	update   workflow.UpdateWorkflowInput
	copy     workflow.CopyWorkflowInput
}

type fixtureVersionGovernance struct {
	operation     string
	workflowID    string
	listRequest   workflow.WorkflowVersionListRequest
	diffRequest   workflow.WorkflowDiffRequest
	rollbackInput workflow.WorkflowRollbackInput
	undoRevision  int64
	err           error
}

func (governance *fixtureVersionGovernance) List(_ context.Context, workflowID string, request workflow.WorkflowVersionListRequest) (domain.WorkflowVersionPage, error) {
	governance.operation, governance.workflowID, governance.listRequest = "list", workflowID, request
	return domain.WorkflowVersionPage{Items: []domain.WorkflowVersionSummary{}}, governance.err
}

func (governance *fixtureVersionGovernance) Diff(_ context.Context, workflowID string, request workflow.WorkflowDiffRequest) (domain.WorkflowDiff, error) {
	governance.operation, governance.workflowID, governance.diffRequest = "diff", workflowID, request
	return domain.WorkflowDiff{Groups: domain.WorkflowDiffGroups{
		Nodes: []domain.WorkflowNodeDiff{}, StartParameters: []domain.WorkflowStartParameterDiff{},
		Connections: []domain.WorkflowConnectionDiff{}, AgentPresentation: []domain.WorkflowPresentationDiff{}, Layout: []domain.WorkflowLayoutDiff{},
	}}, governance.err
}

func (governance *fixtureVersionGovernance) Rollback(_ context.Context, workflowID string, input workflow.WorkflowRollbackInput) (workflow.WorkflowRollbackResult, error) {
	governance.operation, governance.workflowID, governance.rollbackInput = "rollback", workflowID, input
	return workflow.WorkflowRollbackResult{}, governance.err
}

func (governance *fixtureVersionGovernance) Undo(_ context.Context, workflowID string, revision int64) (domain.Workflow, error) {
	governance.operation, governance.workflowID, governance.undoRevision = "undo", workflowID, revision
	return domain.Workflow{}, governance.err
}

func (manager *fixtureWorkflowManager) List(_ context.Context, request workflow.WorkflowSummaryRequest) (workflow.WorkflowSummaryPage, error) {
	manager.request = request
	return workflow.WorkflowSummaryPage{Items: []domain.WorkflowSummary{}}, manager.err
}

func (manager *fixtureWorkflowManager) Update(_ context.Context, id string, input workflow.UpdateWorkflowInput) (domain.Workflow, error) {
	manager.mutation, manager.id, manager.update = "update", id, input
	return manager.workflow, manager.err
}

func (manager *fixtureWorkflowManager) Copy(_ context.Context, id string, input workflow.CopyWorkflowInput) (domain.Workflow, error) {
	manager.mutation, manager.id, manager.copy = "copy", id, input
	return manager.workflow, manager.err
}

func (manager *fixtureWorkflowManager) Archive(_ context.Context, id string) (domain.Workflow, error) {
	manager.mutation, manager.id = "archive", id
	return manager.workflow, manager.err
}

func (manager *fixtureWorkflowManager) Restore(_ context.Context, id string) (domain.Workflow, error) {
	manager.mutation, manager.id = "restore", id
	return manager.workflow, manager.err
}

type fixtureRunManager struct {
	request   workflow.RunSummaryRequest
	cancelID  string
	previewID string
	summary   domain.RunSummary
	preview   workflow.RunRetryPreview
	retryKey  string
	retryBody workflow.RunRetryRequest
	err       error
}

func (manager *fixtureRunManager) List(_ context.Context, request workflow.RunSummaryRequest) (workflow.RunSummaryPage, error) {
	manager.request = request
	return workflow.RunSummaryPage{Items: []domain.RunSummary{}}, manager.err
}

func (manager *fixtureRunManager) Cancel(_ context.Context, runID string) (domain.RunSummary, error) {
	manager.cancelID = runID
	return manager.summary, manager.err
}

func (manager *fixtureRunManager) RetryPreview(_ context.Context, runID string) (workflow.RunRetryPreview, error) {
	manager.previewID = runID
	return manager.preview, manager.err
}

func (manager *fixtureRunManager) PrepareRetry(_ context.Context, runID, key string, body workflow.RunRetryRequest) (*workflow.PreparedRun, error) {
	manager.previewID, manager.retryKey, manager.retryBody = runID, key, body
	if manager.err != nil {
		return nil, manager.err
	}
	return &workflow.PreparedRun{RunID: runID}, nil
}

func (manager *fixtureRunManager) SubmitRetry(_ context.Context, runID, key string, body workflow.RunRetryRequest) (workflow.SubmittedRun, error) {
	manager.previewID, manager.retryKey, manager.retryBody = runID, key, body
	if manager.err != nil {
		return workflow.SubmittedRun{}, manager.err
	}
	return workflow.SubmittedRun{RunID: runID, Created: true}, nil
}

type fixtureDebugger struct {
	overviewErr error
	eventsErr   error
	previewErr  error
	prepareErr  error
	lastAfter   int64
	lastRunID   string
	lastNodeID  string
	lastRequest workflow.RerunRequest
}

func (debugger *fixtureDebugger) Overview(context.Context, string) (workflow.DebugOverview, error) {
	return workflow.DebugOverview{NodeRuns: []domain.NodeRun{}, SourceChain: []workflow.DebugSource{}}, debugger.overviewErr
}

func (debugger *fixtureDebugger) Events(_ context.Context, _ string, after int64) (workflow.RunEventPage, error) {
	debugger.lastAfter = after
	return workflow.RunEventPage{Events: []domain.RunEvent{}, NextAfterSequence: after}, debugger.eventsErr
}

func (debugger *fixtureDebugger) PreviewRerun(_ context.Context, runID, nodeID string) (workflow.RerunPreview, error) {
	debugger.lastRunID, debugger.lastNodeID = runID, nodeID
	return workflow.RerunPreview{SourceRunID: runID, SourceNodeID: nodeID, EntryInput: map[string]any{}, EntryInputRedactedPaths: []string{}, ActiveNodes: []workflow.RerunNode{}, FrozenEdges: []workflow.FrozenEdgePreview{}, EffectiveSafety: agentnode.ExecutionSafetyPure}, debugger.previewErr
}

func (debugger *fixtureDebugger) PrepareRerun(_ context.Context, runID, nodeID string, request workflow.RerunRequest) (*workflow.PreparedRun, error) {
	debugger.lastRunID, debugger.lastNodeID, debugger.lastRequest = runID, nodeID, request
	if debugger.prepareErr != nil {
		return nil, debugger.prepareErr
	}
	return &workflow.PreparedRun{RunID: "debug-run"}, nil
}

func (debugger *fixtureDebugger) SubmitRerun(_ context.Context, runID, nodeID string, request workflow.RerunRequest) (workflow.SubmittedRun, error) {
	debugger.lastRunID, debugger.lastNodeID, debugger.lastRequest = runID, nodeID, request
	if debugger.prepareErr != nil {
		return workflow.SubmittedRun{}, debugger.prepareErr
	}
	return workflow.SubmittedRun{RunID: "debug-run", Created: true}, nil
}

type fixtureReady struct{ err error }

func (ready fixtureReady) Ready(context.Context) error { return ready.err }

func TestRevisionConflictUsesStableErrorShape(t *testing.T) {
	dependencies := fixtureDeps()
	recorder := performRequest(NewRouter(dependencies), http.MethodPut, "/api/workflows/w1", `{"draftRevision":1,"graph":{"schemaVersion":1,"nodes":[],"edges":[]}}`)
	assertJSONError(t, recorder, http.StatusConflict, "WORKFLOW_REVISION_CONFLICT")
}

func TestWorkflowValidationErrorReturnsIssues(t *testing.T) {
	issues := []domain.ValidationIssue{
		{Code: "WORKFLOW_START_COUNT", Message: "工作流必须恰有一个开始节点", Path: "nodes"},
		{Code: "WORKFLOW_END_COUNT", Message: "工作流必须恰有一个结束节点", Path: "nodes"},
	}
	dependencies := fixtureDeps()
	dependencies.Workflows.(*fixtureWorkflowService).saveDraftErr = &workflow.ValidationError{Issues: issues}
	recorder := performRequest(NewRouter(dependencies), http.MethodPut, "/api/workflows/w1", `{"draftRevision":1,"graph":{"schemaVersion":1,"nodes":[],"edges":[]}}`)
	if recorder.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var response ErrorResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Code != "WORKFLOW_INVALID" || response.Message != "工作流校验失败" || !reflect.DeepEqual(response.Issues, issues) {
		t.Fatalf("response=%+v", response)
	}
}

func TestAgentPresentationEndpoint(t *testing.T) {
	dependencies := fixtureDeps()
	body := `{"draftRevision":2,"presentation":{"title":"公开助手","description":"说明","accent":"teal","submitLabel":"开始","resultMode":"auto"}}`
	recorder := performRequest(NewRouter(dependencies), http.MethodPut, "/api/workflows/w1/agent-presentation", body)
	service := dependencies.Workflows.(*fixtureWorkflowService)
	if recorder.Code != http.StatusOK || service.lastPresentationID != "w1" || service.lastPresentationRevision != 2 || service.lastPresentation.Title != "公开助手" || service.lastPresentation.Accent != domain.AgentAccentTeal {
		t.Fatalf("status=%d service=%+v body=%s", recorder.Code, service, recorder.Body.String())
	}

	invalid := performRequest(NewRouter(fixtureDeps()), http.MethodPut, "/api/workflows/w1/agent-presentation", `{"draftRevision":2,"presentation":{"title":"","description":"说明","accent":"teal","submitLabel":"开始","resultMode":"auto"}}`)
	assertJSONError(t, invalid, http.StatusBadRequest, "REQUEST_INVALID")

	unknown := performRequest(NewRouter(fixtureDeps()), http.MethodPut, "/api/workflows/w1/agent-presentation", `{"draftRevision":2,"presentation":{"title":"助手","description":"","accent":"teal","submitLabel":"开始","resultMode":"auto","unknown":true}}`)
	assertJSONError(t, unknown, http.StatusBadRequest, "REQUEST_INVALID")
}

func TestArchivedWorkflowUsesStableErrorAcrossValidationAndRunEntrypoints(t *testing.T) {
	tests := []struct {
		name   string
		method string
		path   string
		body   string
		setup  func(Dependencies)
	}{
		{name: "validate", method: http.MethodPost, path: "/api/workflows/w1/validate", setup: func(dependencies Dependencies) {
			dependencies.Workflows.(*fixtureWorkflowService).validateErr = domain.ErrWorkflowArchived
		}},
		{name: "manifest", method: http.MethodGet, path: "/api/agents/demo", setup: func(dependencies Dependencies) {
			dependencies.Workflows.(*fixtureWorkflowService).manifestErr = domain.ErrWorkflowArchived
		}},
		{name: "draft run", method: http.MethodPost, path: "/api/workflows/w1/test-runs", body: `{"draftRevision":2,"input":{}}`, setup: func(dependencies Dependencies) {
			dependencies.RunSubmissions.(*fixtureRunner).submitErr = domain.ErrWorkflowArchived
		}},
		{name: "agent run", method: http.MethodPost, path: "/api/agents/demo/runs", body: `{"workflowVersionId":"v1","input":{}}`, setup: func(dependencies Dependencies) {
			dependencies.RunSubmissions.(*fixtureRunner).submitErr = domain.ErrWorkflowArchived
		}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			dependencies := fixtureDeps()
			test.setup(dependencies)
			recorder := performRequest(NewRouter(dependencies), test.method, test.path, test.body)
			assertJSONError(t, recorder, http.StatusConflict, "WORKFLOW_ARCHIVED")
			if strings.Contains(recorder.Body.String(), domain.ErrWorkflowArchived.Error()) {
				t.Fatalf("internal error leaked: %s", recorder.Body.String())
			}
		})
	}
}

func TestWorkflowManagementListParsesStrictQuery(t *testing.T) {
	dependencies := fixtureDeps()
	recorder := performRequest(NewRouter(dependencies), http.MethodGet, "/api/workflow-summaries?q=agent&state=all&limit=20", "")
	manager := dependencies.WorkflowManagement.(*fixtureWorkflowManager)
	if recorder.Code != http.StatusOK || manager.request.Text != "agent" || manager.request.State != workflow.WorkflowStateAll || manager.request.Limit != 20 || !strings.Contains(recorder.Body.String(), `"items":[]`) {
		t.Fatalf("status=%d request=%+v body=%s", recorder.Code, manager.request, recorder.Body.String())
	}
}

func TestWorkflowManagementListRejectsInvalidQuery(t *testing.T) {
	tests := []string{
		"?q=a&q=b", "?state=active&state=all", "?cursor=a&cursor=b", "?limit=1&limit=2",
		"?q=" + strings.Repeat("a", 101), "?cursor=" + strings.Repeat("a", 513), "?state=deleted", "?unknown=true",
	}
	for _, query := range tests {
		recorder := performRequest(NewRouter(fixtureDeps()), http.MethodGet, "/api/workflow-summaries"+query, "")
		assertJSONError(t, recorder, http.StatusBadRequest, "REQUEST_INVALID")
	}
}

func TestWorkflowManagementMutationsValidatePresenceAndPath(t *testing.T) {
	validID := "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	tests := []struct {
		method string
		path   string
		body   string
	}{
		{method: http.MethodPatch, path: "/api/workflows/not-a-uuid", body: `{"name":"名称","description":"说明"}`},
		{method: http.MethodPatch, path: "/api/workflows/" + validID, body: `{"name":"名称"}`},
		{method: http.MethodPatch, path: "/api/workflows/" + validID, body: `{"description":"说明"}`},
		{method: http.MethodPatch, path: "/api/workflows/" + validID, body: `{"name":"","description":"说明"}`},
		{method: http.MethodPatch, path: "/api/workflows/" + validID, body: `{"name":"名称","description":"说明","unknown":true}`},
		{method: http.MethodPost, path: "/api/workflows/not-a-uuid/copies", body: `{"name":"副本","slug":"copy"}`},
		{method: http.MethodPost, path: "/api/workflows/" + validID + "/copies", body: `{"name":"副本"}`},
		{method: http.MethodPost, path: "/api/workflows/" + validID + "/copies", body: `{"slug":"copy"}`},
		{method: http.MethodPost, path: "/api/workflows/not-a-uuid/archive"},
		{method: http.MethodPost, path: "/api/workflows/not-a-uuid/restore"},
	}
	for _, test := range tests {
		recorder := performRequest(NewRouter(fixtureDeps()), test.method, test.path, test.body)
		assertJSONError(t, recorder, http.StatusBadRequest, "REQUEST_INVALID")
	}
}

func TestWorkflowManagementMutationsReturnStableStatuses(t *testing.T) {
	validID := "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	tests := []struct {
		name   string
		method string
		path   string
		body   string
		status int
		want   string
	}{
		{name: "update", method: http.MethodPatch, path: "/api/workflows/" + validID, body: `{"name":"名称","description":"说明"}`, status: http.StatusOK, want: "update"},
		{name: "copy", method: http.MethodPost, path: "/api/workflows/" + validID + "/copies", body: `{"name":"副本","slug":"copy"}`, status: http.StatusCreated, want: "copy"},
		{name: "archive", method: http.MethodPost, path: "/api/workflows/" + validID + "/archive", status: http.StatusOK, want: "archive"},
		{name: "restore", method: http.MethodPost, path: "/api/workflows/" + validID + "/restore", status: http.StatusOK, want: "restore"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			dependencies := fixtureDeps()
			recorder := performRequest(NewRouter(dependencies), test.method, test.path, test.body)
			manager := dependencies.WorkflowManagement.(*fixtureWorkflowManager)
			if recorder.Code != test.status || manager.mutation != test.want || manager.id != validID {
				t.Fatalf("status=%d mutation=%q id=%q body=%s", recorder.Code, manager.mutation, manager.id, recorder.Body.String())
			}
		})
	}
}

func TestRunManagementListParsesStrictQuery(t *testing.T) {
	dependencies := fixtureDeps()
	workflowID := "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	runID := "11111111-1111-4111-8111-111111111111"
	path := "/api/runs?workflowId=" + workflowID + "&runId=" + runID + "&status=failed&status=running&mode=test&startedAfter=2026-08-01T00:00:00%2B08:00&startedBefore=2026-08-25T00:00:00%2B08:00&limit=20"
	recorder := performRequest(NewRouter(dependencies), http.MethodGet, path, "")
	request := dependencies.RunManagement.(*fixtureRunManager).request
	if recorder.Code != http.StatusOK || request.WorkflowID != workflowID || request.RunID != runID || len(request.Statuses) != 2 || len(request.Modes) != 1 || request.Limit != 20 || request.StartedAfter == nil || request.StartedAfter.Location() != time.UTC {
		t.Fatalf("status=%d request=%+v body=%s", recorder.Code, request, recorder.Body.String())
	}
}

func TestRunManagementListRejectsInvalidQuery(t *testing.T) {
	tests := []string{
		"?workflowId=a&workflowId=b", "?runId=a&runId=b", "?cursor=a&cursor=b", "?limit=1&limit=2",
		"?status=running&status=cancelling&status=completed&status=failed&status=cancelled&status=failed",
		"?mode=test&mode=published&mode=debug&mode=test", "?workflowId=bad", "?runId=bad",
		"?startedAfter=2026-01-01T00:00:00Z&startedBefore=2026-04-02T00:00:00Z",
		"?startedAfter=2026-01-02T00:00:00Z&startedBefore=2026-01-01T00:00:00Z",
		"?limit=101", "?cursor=" + strings.Repeat("a", 513), "?unknown=true",
	}
	for _, query := range tests {
		recorder := performRequest(NewRouter(fixtureDeps()), http.MethodGet, "/api/runs"+query, "")
		assertJSONError(t, recorder, http.StatusBadRequest, "REQUEST_INVALID")
	}
}

func TestCancelRunUsesStrictUUIDAndReturnsLatestSummary(t *testing.T) {
	dependencies := fixtureDeps()
	manager := dependencies.RunManagement.(*fixtureRunManager)
	manager.summary = domain.RunSummary{ID: "11111111-1111-4111-8111-111111111111", Status: domain.RunCancelling}
	recorder := performRequest(NewRouter(dependencies), http.MethodPost, "/api/runs/11111111-1111-4111-8111-111111111111/cancel", "")
	if recorder.Code != http.StatusOK || manager.cancelID != manager.summary.ID || !strings.Contains(recorder.Body.String(), `"status":"cancelling"`) {
		t.Fatalf("status=%d cancelID=%q body=%s", recorder.Code, manager.cancelID, recorder.Body.String())
	}

	dependencies = fixtureDeps()
	manager = dependencies.RunManagement.(*fixtureRunManager)
	recorder = performRequest(NewRouter(dependencies), http.MethodPost, "/api/runs/not-a-uuid/cancel", "")
	if manager.cancelID != "" {
		t.Fatalf("invalid ID reached manager: %q", manager.cancelID)
	}
	assertJSONError(t, recorder, http.StatusBadRequest, "REQUEST_INVALID")
}

func TestCancelRunMapsTerminalConflictAndHidesInternalErrors(t *testing.T) {
	dependencies := fixtureDeps()
	manager := dependencies.RunManagement.(*fixtureRunManager)
	manager.err = workflow.ErrRunNotCancellable
	recorder := performRequest(NewRouter(dependencies), http.MethodPost, "/api/runs/11111111-1111-4111-8111-111111111111/cancel", "")
	assertJSONError(t, recorder, http.StatusConflict, "RUN_NOT_CANCELLABLE")
	var response ErrorResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil || response.RequestID == "" {
		t.Fatalf("response=%+v err=%v", response, err)
	}

	dependencies = fixtureDeps()
	dependencies.RunManagement.(*fixtureRunManager).err = errors.New("database secret detail")
	recorder = performRequest(NewRouter(dependencies), http.MethodPost, "/api/runs/11111111-1111-4111-8111-111111111111/cancel", "")
	assertJSONError(t, recorder, http.StatusInternalServerError, "INTERNAL_ERROR")
	if strings.Contains(recorder.Body.String(), "database secret detail") {
		t.Fatalf("internal error leaked: %s", recorder.Body.String())
	}
}

func TestRetryPreviewUsesStrictUUIDAndMapsNotRetryable(t *testing.T) {
	dependencies := fixtureDeps()
	manager := dependencies.RunManagement.(*fixtureRunManager)
	manager.preview = workflow.RunRetryPreview{
		Source:       domain.RunSummary{ID: "11111111-1111-4111-8111-111111111111", Status: domain.RunFailed},
		RetryOfRunID: "11111111-1111-4111-8111-111111111111",
		Input:        map[string]any{"token": "[REDACTED]"}, InputRedactedPaths: []string{"/token"},
		InputSchema: json.RawMessage(`{"type":"object"}`),
	}
	recorder := performRequest(NewRouter(dependencies), http.MethodGet, "/api/runs/11111111-1111-4111-8111-111111111111/retry-preview", "")
	if recorder.Code != http.StatusOK || manager.previewID != manager.preview.RetryOfRunID || !strings.Contains(recorder.Body.String(), `"inputRedactedPaths":["/token"]`) {
		t.Fatalf("status=%d previewID=%q body=%s", recorder.Code, manager.previewID, recorder.Body.String())
	}

	dependencies = fixtureDeps()
	manager = dependencies.RunManagement.(*fixtureRunManager)
	recorder = performRequest(NewRouter(dependencies), http.MethodGet, "/api/runs/not-a-uuid/retry-preview", "")
	if manager.previewID != "" {
		t.Fatalf("invalid ID reached manager: %q", manager.previewID)
	}
	assertJSONError(t, recorder, http.StatusBadRequest, "REQUEST_INVALID")

	dependencies = fixtureDeps()
	dependencies.RunManagement.(*fixtureRunManager).err = workflow.ErrRunNotRetryable
	recorder = performRequest(NewRouter(dependencies), http.MethodGet, "/api/runs/11111111-1111-4111-8111-111111111111/retry-preview", "")
	assertJSONError(t, recorder, http.StatusConflict, "RUN_NOT_RETRYABLE")
}

func TestManagementErrorsUseStableCodes(t *testing.T) {
	dependencies := fixtureDeps()
	dependencies.WorkflowManagement.(*fixtureWorkflowManager).err = domain.ErrWorkflowArchived
	recorder := performRequest(NewRouter(dependencies), http.MethodPost, "/api/workflows/aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa/archive", "")
	assertJSONError(t, recorder, http.StatusConflict, "WORKFLOW_ARCHIVED")

	dependencies = fixtureDeps()
	dependencies.RunManagement.(*fixtureRunManager).err = workflow.ErrCursorInvalid
	recorder = performRequest(NewRouter(dependencies), http.MethodGet, "/api/runs", "")
	assertJSONError(t, recorder, http.StatusBadRequest, "CURSOR_INVALID")
}

func TestVersionGovernanceRoutes(t *testing.T) {
	const workflowID = "11111111-1111-4111-8111-111111111111"
	tests := []struct {
		method    string
		path      string
		body      string
		operation string
	}{
		{method: http.MethodGet, path: "/api/workflows/" + workflowID + "/versions?limit=20", operation: "list"},
		{method: http.MethodPost, path: "/api/workflows/" + workflowID + "/version-diffs", body: `{"base":{"kind":"version","version":1},"compare":{"kind":"draft","draftRevision":7}}`, operation: "diff"},
		{method: http.MethodPost, path: "/api/workflows/" + workflowID + "/rollbacks", body: `{"targetVersion":1,"expectedDraftRevision":7}`, operation: "rollback"},
		{method: http.MethodPost, path: "/api/workflows/" + workflowID + "/rollback-undo", body: `{"expectedDraftRevision":8}`, operation: "undo"},
	}
	for _, test := range tests {
		t.Run(test.operation, func(t *testing.T) {
			dependencies := fixtureDeps()
			governance := dependencies.VersionGovernance.(*fixtureVersionGovernance)
			recorder := performRequest(NewRouter(dependencies), test.method, test.path, test.body)
			if recorder.Code != http.StatusOK || governance.operation != test.operation || governance.workflowID != workflowID {
				t.Fatalf("%s %s status=%d operation=%q workflow=%q body=%s", test.method, test.path, recorder.Code, governance.operation, governance.workflowID, recorder.Body.String())
			}
			if test.operation == "diff" && recorder.Header().Get("Cache-Control") != "no-store" {
				t.Fatalf("diff headers=%v", recorder.Header())
			}
			if test.operation == "list" && governance.listRequest.Limit != 20 {
				t.Fatalf("list request=%+v", governance.listRequest)
			}
		})
	}
}

func TestVersionGovernanceRoutesRejectInvalidRequestsBeforeService(t *testing.T) {
	const workflowID = "11111111-1111-4111-8111-111111111111"
	tests := []struct {
		method string
		path   string
		body   string
	}{
		{method: http.MethodGet, path: "/api/workflows/not-a-uuid/versions"},
		{method: http.MethodGet, path: "/api/workflows/11111111-1111-4111-8111-AAAAAAAAAAAA/versions"},
		{method: http.MethodGet, path: "/api/workflows/" + workflowID + "/versions?limit=0"},
		{method: http.MethodGet, path: "/api/workflows/" + workflowID + "/versions?limit=101"},
		{method: http.MethodGet, path: "/api/workflows/" + workflowID + "/versions?cursor=a&cursor=b"},
		{method: http.MethodGet, path: "/api/workflows/" + workflowID + "/versions?cursor=" + strings.Repeat("a", 513)},
		{method: http.MethodGet, path: "/api/workflows/" + workflowID + "/versions?unknown=true"},
		{method: http.MethodPost, path: "/api/workflows/" + workflowID + "/version-diffs", body: `{"base":{"kind":"version","version":0},"compare":{"kind":"draft","draftRevision":7}}`},
		{method: http.MethodPost, path: "/api/workflows/" + workflowID + "/version-diffs", body: `{"base":{"kind":"version","version":1,"draftRevision":7},"compare":{"kind":"draft","draftRevision":7}}`},
		{method: http.MethodPost, path: "/api/workflows/" + workflowID + "/version-diffs", body: `{"base":{"kind":"version","version":1},"compare":{"kind":"draft","draftRevision":7},"unknown":true}`},
		{method: http.MethodPost, path: "/api/workflows/" + workflowID + "/rollbacks", body: `{"targetVersion":0,"expectedDraftRevision":7}`},
		{method: http.MethodPost, path: "/api/workflows/" + workflowID + "/rollbacks", body: `{"targetVersion":1,"expectedDraftRevision":0}`},
		{method: http.MethodPost, path: "/api/workflows/" + workflowID + "/rollbacks", body: `{"targetVersion":1,"expectedDraftRevision":7,"unknown":true}`},
		{method: http.MethodPost, path: "/api/workflows/" + workflowID + "/rollback-undo", body: `{"expectedDraftRevision":0}`},
		{method: http.MethodPost, path: "/api/workflows/" + workflowID + "/rollback-undo", body: `{"expectedDraftRevision":8,"unknown":true}`},
	}
	for _, test := range tests {
		dependencies := fixtureDeps()
		governance := dependencies.VersionGovernance.(*fixtureVersionGovernance)
		recorder := performRequest(NewRouter(dependencies), test.method, test.path, test.body)
		assertJSONError(t, recorder, http.StatusBadRequest, "REQUEST_INVALID")
		if governance.operation != "" {
			t.Fatalf("invalid request reached service: %s %s operation=%q", test.method, test.path, governance.operation)
		}
	}
}

func TestWorkflowVersionErrorsUseStableCodes(t *testing.T) {
	const path = "/api/workflows/11111111-1111-4111-8111-111111111111/versions"
	tests := []struct {
		err     error
		status  int
		code    string
		message string
	}{
		{err: domain.ErrWorkflowVersionNotFound, status: http.StatusNotFound, code: "WORKFLOW_VERSION_NOT_FOUND", message: "工作流版本不存在"},
		{err: domain.ErrWorkflowSnapshotUnsupported, status: http.StatusUnprocessableEntity, code: "WORKFLOW_SNAPSHOT_UNSUPPORTED", message: "当前工作流版本快照不受支持"},
		{err: domain.ErrRollbackUndoUnavailable, status: http.StatusConflict, code: "ROLLBACK_UNDO_UNAVAILABLE", message: "当前回滚已无法撤销"},
		{err: domain.ErrRevisionConflict, status: http.StatusConflict, code: "WORKFLOW_REVISION_CONFLICT", message: "草稿版本已变化，请刷新后重试"},
		{err: domain.ErrWorkflowArchived, status: http.StatusConflict, code: "WORKFLOW_ARCHIVED", message: "工作流已归档，请先恢复后再操作"},
		{err: workflow.ErrCursorInvalid, status: http.StatusBadRequest, code: "CURSOR_INVALID", message: "分页游标无效，请刷新后重试"},
	}
	for _, test := range tests {
		dependencies := fixtureDeps()
		dependencies.VersionGovernance.(*fixtureVersionGovernance).err = test.err
		recorder := performRequest(NewRouter(dependencies), http.MethodGet, path, "")
		assertJSONError(t, recorder, test.status, test.code)
		var response ErrorResponse
		if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil || response.Message != test.message {
			t.Fatalf("response=%+v err=%v", response, err)
		}
	}
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
	if !strings.Contains(recorder.Header().Get("Access-Control-Allow-Methods"), http.MethodPatch) {
		t.Fatalf("CORS does not allow workflow metadata PATCH: %v", recorder.Header())
	}
}

func TestRouterRejectsTrailingJSON(t *testing.T) {
	recorder := performRequest(NewRouter(fixtureDeps()), http.MethodPost, "/api/workflows", `{"name":"Demo","slug":"demo"}{}`)
	assertJSONError(t, recorder, http.StatusBadRequest, "REQUEST_INVALID")
}

func TestDebugRoutesExposeOverviewEventsAndPreview(t *testing.T) {
	dependencies := fixtureDeps()
	debugger := dependencies.Debugger.(*fixtureDebugger)
	router := NewRouter(dependencies)

	overview := performRequest(router, http.MethodGet, "/api/runs/run-1/debug", "")
	if overview.Code != http.StatusOK || !strings.Contains(overview.Body.String(), `"nodeRuns":[]`) || !strings.Contains(overview.Body.String(), `"sourceChain":[]`) {
		t.Fatalf("overview status=%d body=%s", overview.Code, overview.Body.String())
	}
	events := performRequest(router, http.MethodGet, "/api/runs/run-1/events?afterSequence=7", "")
	if events.Code != http.StatusOK || debugger.lastAfter != 7 || !strings.Contains(events.Body.String(), `"events":[]`) {
		t.Fatalf("events status=%d after=%d body=%s", events.Code, debugger.lastAfter, events.Body.String())
	}
	preview := performRequest(router, http.MethodGet, "/api/runs/run%201/nodes/node%201/rerun-preview", "")
	if preview.Code != http.StatusOK || debugger.lastRunID != "run 1" || debugger.lastNodeID != "node 1" || !strings.Contains(preview.Body.String(), `"activeNodes":[]`) {
		t.Fatalf("preview status=%d run=%q node=%q body=%s", preview.Code, debugger.lastRunID, debugger.lastNodeID, preview.Body.String())
	}
}

func TestDebugEventsRejectsNegativeCursor(t *testing.T) {
	for _, raw := range []string{"-1", "invalid"} {
		recorder := performRequest(NewRouter(fixtureDeps()), http.MethodGet, "/api/runs/run-1/events?afterSequence="+raw, "")
		assertJSONError(t, recorder, http.StatusBadRequest, "REQUEST_INVALID")
	}
}

func TestDebugRoutesMapStableErrors(t *testing.T) {
	tests := []struct {
		name   string
		path   string
		method string
		err    error
		set    func(*fixtureDebugger, error)
		status int
		code   string
	}{
		{name: "legacy replay", path: "/api/runs/run-1/events", method: http.MethodGet, err: workflow.ErrRunReplayUnavailable, set: func(debugger *fixtureDebugger, err error) { debugger.eventsErr = err }, status: http.StatusConflict, code: "RUN_REPLAY_UNAVAILABLE"},
		{name: "snapshot", path: "/api/runs/run-1/debug", method: http.MethodGet, err: workflow.ErrRunSnapshotUnsupported, set: func(debugger *fixtureDebugger, err error) { debugger.overviewErr = err }, status: http.StatusUnprocessableEntity, code: "RUN_SNAPSHOT_UNSUPPORTED"},
		{name: "frozen", path: "/api/runs/run-1/nodes/node-1/rerun-preview", method: http.MethodGet, err: workflow.ErrRunFrozenEdgeUnavailable, set: func(debugger *fixtureDebugger, err error) { debugger.previewErr = err }, status: http.StatusUnprocessableEntity, code: "RUN_FROZEN_EDGE_UNAVAILABLE"},
		{name: "side effect", path: "/api/runs/run-1/nodes/node-1/reruns", method: http.MethodPost, err: workflow.ErrRunSideEffectConfirmationRequired, set: func(debugger *fixtureDebugger, err error) { debugger.prepareErr = err }, status: http.StatusConflict, code: "RUN_SIDE_EFFECT_CONFIRMATION_REQUIRED"},
		{name: "entry input", path: "/api/runs/run-1/nodes/node-1/reruns", method: http.MethodPost, err: workflow.ErrRunEntryInputInvalid, set: func(debugger *fixtureDebugger, err error) { debugger.prepareErr = err }, status: http.StatusBadRequest, code: "RUN_ENTRY_INPUT_INVALID"},
		{name: "budget", path: "/api/runs/run-1/nodes/node-1/reruns", method: http.MethodPost, err: domain.ErrRunEventBudgetExceeded, set: func(debugger *fixtureDebugger, err error) { debugger.prepareErr = err }, status: http.StatusRequestEntityTooLarge, code: "RUN_EVENT_BUDGET_EXCEEDED"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			dependencies := fixtureDeps()
			debugger := dependencies.Debugger.(*fixtureDebugger)
			test.set(debugger, test.err)
			body := ""
			if test.method == http.MethodPost {
				body = `{"entryInput":{},"confirmSideEffects":false}`
			}
			recorder := performRequest(NewRouter(dependencies), test.method, test.path, body)
			assertJSONError(t, recorder, test.status, test.code)
			if strings.Contains(recorder.Body.String(), test.err.Error()) {
				t.Fatalf("internal cause leaked: %s", recorder.Body.String())
			}
		})
	}
}

func TestRerunRejectsUnknownAndTrailingJSON(t *testing.T) {
	path := "/api/runs/run-1/nodes/node-1/reruns"
	for _, body := range []string{
		`{"entryInput":{},"confirmSideEffects":false,"unknown":true}`,
		`{"entryInput":{},"confirmSideEffects":false}{}`,
	} {
		recorder := performRequest(NewRouter(fixtureDeps()), http.MethodPost, path, body)
		assertJSONError(t, recorder, http.StatusBadRequest, "REQUEST_INVALID")
	}
}

func TestRerunStreamsPreparedDebugRun(t *testing.T) {
	dependencies := fixtureDeps()
	debugger := dependencies.Debugger.(*fixtureDebugger)
	recorder := performRequest(NewRouter(dependencies), http.MethodPost, "/api/runs/run%201/nodes/node%201/reruns", `{"entryInput":{"in":["edited"]},"confirmSideEffects":true}`)
	if recorder.Code != http.StatusOK || recorder.Header().Get("Content-Type") != "application/x-ndjson" || recorder.Header().Get("Cache-Control") != "no-store" || recorder.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("status=%d headers=%v body=%s", recorder.Code, recorder.Header(), recorder.Body.String())
	}
	if debugger.lastRunID != "run 1" || debugger.lastNodeID != "node 1" || !debugger.lastRequest.ConfirmSideEffects || !strings.Contains(recorder.Body.String(), `"type":"run.started"`) || !strings.Contains(recorder.Body.String(), `"runId":"debug-run"`) {
		t.Fatalf("debugger=%+v body=%s", debugger, recorder.Body.String())
	}
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

func TestNodeAPIDiscoversBothLLMVersionsAndResolvesV2Fields(t *testing.T) {
	dependencies := fixtureDeps()
	record := builtin.RuntimeRecord(buildinfo.Info{Version: "v0.3.0"})
	if err := dependencies.Registry.RegisterPackage(record, func(registrar agentnode.Registrar) error {
		if err := builtin.RegisterCore(registrar); err != nil {
			return err
		}
		if err := builtin.RegisterLLM(registrar, modelprovider.NewMock(), "mock"); err != nil {
			return err
		}
		return builtin.RegisterIntegrationNodes(registrar, builtin.HTTPOptions{})
	}); err != nil {
		t.Fatal(err)
	}

	listRecorder := performRequest(NewRouter(dependencies), http.MethodGet, "/api/node-types", "")
	if listRecorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", listRecorder.Code, listRecorder.Body.String())
	}
	var definitions []nodeTypeResponse
	if err := json.Unmarshal(listRecorder.Body.Bytes(), &definitions); err != nil {
		t.Fatal(err)
	}
	versions := make([]string, 0, 2)
	for _, definition := range definitions {
		if definition.Type == "llm" {
			versions = append(versions, definition.Version)
		}
	}
	if !reflect.DeepEqual(versions, []string{"1", "2"}) {
		t.Fatalf("llm versions=%v definitions=%+v", versions, definitions)
	}

	resolveRecorder := performRequest(NewRouter(dependencies), http.MethodPost, "/api/node-types/llm/2/resolve", `{"config":{"outputMode":"structured","fields":[{"key":"answer","label":"回答","type":"string","required":true}]}}`)
	if resolveRecorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", resolveRecorder.Code, resolveRecorder.Body.String())
	}
	var ports agentnode.ResolvedPorts
	if err := json.Unmarshal(resolveRecorder.Body.Bytes(), &ports); err != nil {
		t.Fatal(err)
	}
	keys := make([]string, 0, len(ports.Outputs))
	for _, port := range ports.Outputs {
		keys = append(keys, port.Key)
	}
	if !reflect.DeepEqual(keys, []string{"json", "answer", "usage"}) {
		t.Fatalf("outputs=%+v", ports.Outputs)
	}
}

func TestNodeAPIExposesExactOfficialExecutionSafetyMatrix(t *testing.T) {
	dependencies := fixtureDeps()
	record := builtin.RuntimeRecord(buildinfo.Info{Version: "v0.3.0"})
	if err := dependencies.Registry.RegisterPackage(record, func(registrar agentnode.Registrar) error {
		if err := builtin.RegisterCore(registrar); err != nil {
			return err
		}
		if err := builtin.RegisterLLM(registrar, modelprovider.NewMock(), "mock"); err != nil {
			return err
		}
		return builtin.RegisterIntegrationNodes(registrar, builtin.HTTPOptions{})
	}); err != nil {
		t.Fatal(err)
	}
	if err := generated.RegisterNodes(dependencies.Registry); err != nil {
		t.Fatal(err)
	}

	recorder := performRequest(NewRouter(dependencies), http.MethodGet, "/api/node-types", "")
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var definitions []nodeTypeResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &definitions); err != nil {
		t.Fatal(err)
	}
	want := map[string]agentnode.ExecutionSafety{
		"start@1":                   agentnode.ExecutionSafetyPure,
		"template@1":                agentnode.ExecutionSafetyPure,
		"condition@1":               agentnode.ExecutionSafetyPure,
		"end@1":                     agentnode.ExecutionSafetyPure,
		"code@1":                    agentnode.ExecutionSafetyPure,
		"llm@1":                     agentnode.ExecutionSafetyReadOnly,
		"llm@2":                     agentnode.ExecutionSafetyReadOnly,
		"http@1":                    agentnode.ExecutionSafetySideEffect,
		"extension.echo@1.0.0":      agentnode.ExecutionSafetyPure,
		"extension.retriever@1.0.0": agentnode.ExecutionSafetyPure,
		"extension.webhook@1.0.0":   agentnode.ExecutionSafetySideEffect,
	}
	got := make(map[string]agentnode.ExecutionSafety, len(definitions))
	for _, definition := range definitions {
		got[definition.Type+"@"+definition.Version] = definition.ExecutionSafety
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("execution safeties=%v, want %v", got, want)
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

func TestAgentRunAsyncPreferenceReturnsAcceptedPublicSummary(t *testing.T) {
	dependencies := fixtureDeps()
	runs := dependencies.AgentRuns.(*fixtureAgentRuns)
	runs.created = true
	request := httptest.NewRequest(http.MethodPost, "/api/agents/demo/runs", strings.NewReader(`{"workflowVersionId":"00000000-0000-4000-8000-000000000910","input":{"topic":"x"}}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Prefer", "Respond-Async, wait=5")
	request.Header.Set("Idempotency-Key", "00000000-0000-4000-8000-000000000909")
	recorder := httptest.NewRecorder()
	NewRouter(dependencies).ServeHTTP(recorder, request)
	if recorder.Code != http.StatusAccepted || recorder.Header().Get("Preference-Applied") != "respond-async" || runs.startCalls != 1 {
		t.Fatalf("status=%d headers=%v calls=%d body=%s", recorder.Code, recorder.Header(), runs.startCalls, recorder.Body.String())
	}
	if runs.slug != "demo" || runs.startInput.WorkflowVersionID != "00000000-0000-4000-8000-000000000910" || runs.startInput.RequestKey != "00000000-0000-4000-8000-000000000909" || runs.startInput.Input["topic"] != "x" {
		t.Fatalf("slug=%q input=%+v", runs.slug, runs.startInput)
	}
	assertAgentPublicHeaders(t, recorder)
}

func TestAgentRunAsyncRejectsInvalidKeysAndVersion(t *testing.T) {
	tests := []struct{ key, body string }{
		{body: `{"workflowVersionId":"00000000-0000-4000-8000-000000000910","input":{}}`},
		{key: "bad", body: `{"workflowVersionId":"00000000-0000-4000-8000-000000000910","input":{}}`},
		{key: "00000000-0000-4000-8000-000000000909", body: `{"workflowVersionId":"bad","input":{}}`},
	}
	for _, test := range tests {
		dependencies := fixtureDeps()
		request := httptest.NewRequest(http.MethodPost, "/api/agents/demo/runs", strings.NewReader(test.body))
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Prefer", "respond-async")
		if test.key != "" {
			request.Header.Set("Idempotency-Key", test.key)
		}
		recorder := httptest.NewRecorder()
		NewRouter(dependencies).ServeHTTP(recorder, request)
		assertJSONError(t, recorder, http.StatusBadRequest, "REQUEST_INVALID")
		if dependencies.AgentRuns.(*fixtureAgentRuns).startCalls != 0 {
			t.Fatal("invalid request reached AgentRun service")
		}
	}
}

func TestAgentRunAsyncMapsPublicErrors(t *testing.T) {
	tests := []struct {
		err        error
		status     int
		code       string
		retryAfter string
	}{
		{err: workflow.ErrInputValidation, status: http.StatusUnprocessableEntity, code: "INPUT_VALIDATION_FAILED"},
	}
	for _, test := range tests {
		dependencies := fixtureDeps()
		dependencies.AgentRuns.(*fixtureAgentRuns).err = test.err
		request := httptest.NewRequest(http.MethodPost, "/api/agents/demo/runs", strings.NewReader(`{"workflowVersionId":"00000000-0000-4000-8000-000000000910","input":{}}`))
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Prefer", "respond-async")
		request.Header.Set("Idempotency-Key", "00000000-0000-4000-8000-000000000909")
		recorder := httptest.NewRecorder()
		NewRouter(dependencies).ServeHTTP(recorder, request)
		assertJSONError(t, recorder, test.status, test.code)
		if recorder.Header().Get("Retry-After") != test.retryAfter {
			t.Fatalf("retry-after=%q", recorder.Header().Get("Retry-After"))
		}
	}
}

func TestAgentRunPublicViewParsesCursorAndCancelIsIdempotent(t *testing.T) {
	dependencies := fixtureDeps()
	runID := "00000000-0000-4000-8000-000000000911"
	viewRecorder := performRequest(NewRouter(dependencies), http.MethodGet, "/api/agents/demo/runs/"+runID+"?afterSequence=4", "")
	runs := dependencies.AgentRuns.(*fixtureAgentRuns)
	if viewRecorder.Code != http.StatusOK || runs.viewCalls != 1 || runs.afterSequence != 4 || runs.runID != runID {
		t.Fatalf("status=%d calls=%d after=%d run=%q body=%s", viewRecorder.Code, runs.viewCalls, runs.afterSequence, runs.runID, viewRecorder.Body.String())
	}
	assertAgentPublicHeaders(t, viewRecorder)
	cancelRecorder := performRequest(NewRouter(dependencies), http.MethodPost, "/api/agents/demo/runs/"+runID+"/cancel", "")
	if cancelRecorder.Code != http.StatusOK || runs.cancelCalls != 1 || runs.runID != runID {
		t.Fatalf("status=%d calls=%d run=%q body=%s", cancelRecorder.Code, runs.cancelCalls, runs.runID, cancelRecorder.Body.String())
	}
	assertAgentPublicHeaders(t, cancelRecorder)
}

func TestAgentRunPublicRoutesRejectInvalidCursorAndRunID(t *testing.T) {
	for _, path := range []string{
		"/api/agents/demo/runs/00000000-0000-4000-8000-000000000911?afterSequence=-1",
		"/api/agents/demo/runs/00000000-0000-4000-8000-000000000911?afterSequence=nope",
	} {
		dependencies := fixtureDeps()
		recorder := performRequest(NewRouter(dependencies), http.MethodGet, path, "")
		assertJSONError(t, recorder, http.StatusBadRequest, "REQUEST_INVALID")
		if dependencies.AgentRuns.(*fixtureAgentRuns).viewCalls != 0 {
			t.Fatal("invalid cursor reached AgentRun service")
		}
	}
	for _, request := range []struct{ method, path string }{
		{http.MethodGet, "/api/agents/demo/runs/not-a-uuid"},
		{http.MethodPost, "/api/agents/demo/runs/not-a-uuid/cancel"},
	} {
		dependencies := fixtureDeps()
		recorder := performRequest(NewRouter(dependencies), request.method, request.path, "")
		assertJSONError(t, recorder, http.StatusNotFound, "AGENT_NOT_FOUND")
		runs := dependencies.AgentRuns.(*fixtureAgentRuns)
		if runs.viewCalls != 0 || runs.cancelCalls != 0 {
			t.Fatal("invalid run ID reached AgentRun service")
		}
	}
}

func TestAgentRunPublicNotFoundAndHeadersAreSafe(t *testing.T) {
	dependencies := fixtureDeps()
	dependencies.AgentRuns.(*fixtureAgentRuns).err = domain.ErrNotFound
	recorder := performRequest(NewRouter(dependencies), http.MethodGet, "/api/agents/other/runs/00000000-0000-4000-8000-000000000911", "")
	assertJSONError(t, recorder, http.StatusNotFound, "AGENT_NOT_FOUND")
	assertAgentPublicHeaders(t, recorder)
	manifest := performRequest(NewRouter(fixtureDeps()), http.MethodGet, "/api/agents/demo", "")
	assertAgentPublicHeaders(t, manifest)
	for _, forbidden := range []string{"nodeId", `"input"`, "activePorts", "redactedPaths"} {
		if strings.Contains(recorder.Body.String(), forbidden) || strings.Contains(manifest.Body.String(), forbidden) {
			t.Fatalf("public response leaked %q", forbidden)
		}
	}
}

func assertAgentPublicHeaders(t *testing.T, recorder *httptest.ResponseRecorder) {
	t.Helper()
	if recorder.Header().Get("Cache-Control") != "no-store" || recorder.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("headers=%v", recorder.Header())
	}
}

func fixtureDeps() Dependencies {
	registry := nodes.NewRegistry()
	runner := &fixtureRunner{}
	runManager := &fixtureRunManager{}
	debugger := &fixtureDebugger{}
	return Dependencies{
		Registry: registry,
		Workflows: &fixtureWorkflowService{
			workflow:        domain.Workflow{ID: "w1", DraftRevision: 2},
			templatePreview: workflowtemplate.Preview{Issues: []domain.ValidationIssue{}, Summary: workflowtemplate.Summary{InputSchema: json.RawMessage(`{}`), NodeTypes: []workflowtemplate.NodeTypeSummary{}}},
		},
		RunSubmissions: runner,
		RunFollower:    runner,
		Runs:           fixtureRunReader{},
		AgentRuns: &fixtureAgentRuns{
			summary: workflow.AgentRunPublicSummary{RunID: "00000000-0000-4000-8000-000000000911", WorkflowVersionID: "00000000-0000-4000-8000-000000000910", Version: 4, Status: domain.RunRunning, StartedAt: time.Now().UTC()},
			view:    workflow.AgentRunPublicView{Events: []workflow.AgentRunPublicEvent{}, NextSequence: 0, HasMore: false},
		},
		WorkflowManagement: &fixtureWorkflowManager{
			workflow: domain.Workflow{ID: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", Name: "演示"},
		},
		VersionGovernance: &fixtureVersionGovernance{},
		RunManagement:     runManager,
		RetrySubmissions:  runManager,
		Debugger:          debugger,
		RerunSubmissions:  debugger,
		Readiness:         fixtureReady{},
		WebOrigin:         "http://localhost:5173",
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
