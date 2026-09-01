package httpapi

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	chimiddleware "github.com/go-chi/chi/v5/middleware"
	"github.com/yyl1212/agent-studio/apps/api/internal/domain"
	"github.com/yyl1212/agent-studio/apps/api/internal/engine"
	"github.com/yyl1212/agent-studio/apps/api/internal/nodes"
	"github.com/yyl1212/agent-studio/apps/api/internal/observability"
	"github.com/yyl1212/agent-studio/apps/api/internal/workflow"
	"github.com/yyl1212/agent-studio/apps/api/internal/workflowtemplate"
	"github.com/yyl1212/agent-studio/internal/nodeindex"
)

type WorkflowService interface {
	List(context.Context) ([]domain.Workflow, error)
	Get(context.Context, string) (domain.Workflow, error)
	Create(context.Context, workflow.CreateWorkflowInput) (domain.Workflow, error)
	SaveDraft(context.Context, string, int64, domain.Graph) (domain.Workflow, error)
	SaveAgentPresentation(context.Context, string, int64, domain.AgentPresentation) (domain.Workflow, error)
	Validate(context.Context, string) ([]domain.ValidationIssue, error)
	Publish(context.Context, string, int64) (domain.WorkflowVersion, error)
	AgentManifest(context.Context, string) (workflow.AgentManifest, error)
	ExportTemplate(context.Context, string, int64) (workflow.TemplateExport, error)
	PreviewTemplate(context.Context, json.RawMessage) (workflowtemplate.Preview, error)
	ImportTemplate(context.Context, workflow.ImportWorkflowTemplateInput) (domain.Workflow, error)
}

type RunSubmitter interface {
	SubmitDraft(context.Context, string, int64, map[string]any) (workflow.SubmittedRun, error)
	SubmitAgent(context.Context, string, string, map[string]any) (workflow.SubmittedRun, error)
}

type RunFollower interface {
	Follow(context.Context, string, engine.Observer) error
}

type RetrySubmitter interface {
	SubmitRetry(context.Context, string, string, workflow.RunRetryRequest) (workflow.SubmittedRun, error)
}

type RerunSubmitter interface {
	SubmitRerun(context.Context, string, string, workflow.RerunRequest) (workflow.SubmittedRun, error)
}

type RunReader interface {
	GetRun(context.Context, string) (domain.Run, []domain.NodeRun, error)
	ListRuns(context.Context, string, int) ([]domain.Run, error)
}

type AgentRunAPI interface {
	Start(context.Context, string, workflow.StartAgentRunInput) (workflow.AgentRunPublicSummary, bool, error)
	View(context.Context, string, string, int64) (workflow.AgentRunPublicView, error)
	Cancel(context.Context, string, string) (workflow.AgentRunPublicSummary, error)
}

type WorkflowManager interface {
	List(context.Context, workflow.WorkflowSummaryRequest) (workflow.WorkflowSummaryPage, error)
	Update(context.Context, string, workflow.UpdateWorkflowInput) (domain.Workflow, error)
	Copy(context.Context, string, workflow.CopyWorkflowInput) (domain.Workflow, error)
	Archive(context.Context, string) (domain.Workflow, error)
	Restore(context.Context, string) (domain.Workflow, error)
}

type VersionGovernance interface {
	List(context.Context, string, workflow.WorkflowVersionListRequest) (domain.WorkflowVersionPage, error)
	Diff(context.Context, string, workflow.WorkflowDiffRequest) (domain.WorkflowDiff, error)
	Rollback(context.Context, string, workflow.WorkflowRollbackInput) (workflow.WorkflowRollbackResult, error)
	Undo(context.Context, string, int64) (domain.Workflow, error)
}

type RunManager interface {
	List(context.Context, workflow.RunSummaryRequest) (workflow.RunSummaryPage, error)
	Cancel(context.Context, string) (domain.RunSummary, error)
	RetryPreview(context.Context, string) (workflow.RunRetryPreview, error)
}

type RunRecoveryAPI interface {
	Get(context.Context, string) (workflow.RunRecoveryView, error)
	ConfirmNodeRetry(context.Context, string, string, workflow.ConfirmNodeRetryRequest) (domain.RunSummary, error)
	Terminate(context.Context, string, workflow.TerminateRecoveryRequest) (domain.RunSummary, error)
}

type Debugger interface {
	Overview(context.Context, string) (workflow.DebugOverview, error)
	Events(context.Context, string, int64) (workflow.RunEventPage, error)
	PreviewRerun(context.Context, string, string) (workflow.RerunPreview, error)
}

type Readiness interface {
	Ready(context.Context) error
}

type NodePackageCatalog interface {
	Status() nodeindex.Status
	Search(nodeindex.Query) (nodeindex.SearchResult, error)
	Get(string) (nodeindex.PackageDetail, error)
}

type Dependencies struct {
	Registry           *nodes.Registry
	Workflows          WorkflowService
	WorkflowManagement WorkflowManager
	VersionGovernance  VersionGovernance
	RunSubmissions     RunSubmitter
	RunFollower        RunFollower
	Runs               RunReader
	AgentRuns          AgentRunAPI
	RunManagement      RunManager
	RunRecovery        RunRecoveryAPI
	RetrySubmissions   RetrySubmitter
	Debugger           Debugger
	RerunSubmissions   RerunSubmitter
	Readiness          Readiness
	NodePackages       NodePackageCatalog
	WebOrigin          string
	Logger             *slog.Logger
	Telemetry          observability.Providers
}

type handler struct {
	dependencies Dependencies
}

func NewRouter(dependencies Dependencies) http.Handler {
	if dependencies.Logger == nil {
		dependencies.Logger = slog.New(slog.NewTextHandler(discardWriter{}, nil))
	}
	handler := &handler{dependencies: dependencies}
	telemetry := newHTTPTelemetry(dependencies.Telemetry)
	router := chi.NewRouter()
	router.Use(chimiddleware.RequestID)
	router.Use(telemetry.middleware)
	router.Use(handler.recoverMiddleware)
	router.Use(handler.accessLogMiddleware)
	router.Use(corsMiddleware(dependencies.WebOrigin))

	router.Get("/healthz", handler.health)
	router.Get("/readyz", handler.ready)
	router.Route("/api", func(api chi.Router) {
		api.Get("/node-types", handler.listNodeTypes)
		api.Post("/node-types/{type}/{version}/resolve", handler.resolveNodeType)
		api.Get("/node-index/status", handler.getNodeIndexStatus)
		api.Get("/node-packages", handler.listNodePackages)
		api.Get("/node-package", handler.getNodePackage)
		api.Get("/workflows", handler.listWorkflows)
		api.Get("/workflow-summaries", handler.listWorkflowSummaries)
		api.Post("/workflows", handler.createWorkflow)
		api.Get("/workflows/{id}", handler.getWorkflow)
		api.Patch("/workflows/{id}", handler.updateWorkflow)
		api.Post("/workflows/{id}/copies", handler.copyWorkflow)
		api.Post("/workflows/{id}/archive", handler.archiveWorkflow)
		api.Post("/workflows/{id}/restore", handler.restoreWorkflow)
		api.Get("/workflows/{id}/versions", handler.listWorkflowVersions)
		api.Post("/workflows/{id}/version-diffs", handler.diffWorkflowVersions)
		api.Post("/workflows/{id}/rollbacks", handler.rollbackWorkflow)
		api.Post("/workflows/{id}/rollback-undo", handler.undoWorkflowRollback)
		api.Get("/workflows/{id}/template", handler.exportWorkflowTemplate)
		api.Put("/workflows/{id}", handler.saveWorkflow)
		api.Put("/workflows/{id}/agent-presentation", handler.saveAgentPresentation)
		api.Post("/workflows/{id}/validate", handler.validateWorkflow)
		api.Post("/workflows/{id}/test-runs", handler.runDraft)
		api.Post("/workflows/{id}/publish", handler.publishWorkflow)
		api.Get("/workflows/{id}/runs", handler.listRuns)
		api.Post("/workflow-templates/preview", handler.previewWorkflowTemplate)
		api.Post("/workflow-templates/import", handler.importWorkflowTemplate)
		api.Get("/agents/{slug}", handler.getAgentManifest)
		api.Post("/agents/{slug}/runs", handler.runAgent)
		api.Get("/agents/{slug}/runs/{runID}", handler.getAgentRun)
		api.Post("/agents/{slug}/runs/{runID}/cancel", handler.cancelAgentRun)
		api.Get("/runs/{id}", handler.getRun)
		api.Get("/runs", handler.listRunSummaries)
		api.Post("/runs/{id}/cancel", handler.cancelRun)
		api.Get("/runs/{runId}/recovery", handler.getRunRecovery)
		api.Post("/runs/{runId}/recovery/nodes/{nodeId}/retry", handler.confirmRunNodeRetry)
		api.Post("/runs/{runId}/recovery/terminate", handler.terminateRunRecovery)
		api.Get("/runs/{id}/retry-preview", handler.previewRunRetry)
		api.Post("/runs/{id}/retries", handler.retryRun)
		api.Get("/runs/{id}/debug", handler.getRunDebug)
		api.Get("/runs/{id}/events", handler.listRunEvents)
		api.Get("/runs/{id}/nodes/{nodeId}/rerun-preview", handler.previewNodeRerun)
		api.Post("/runs/{id}/nodes/{nodeId}/reruns", handler.rerunFromNode)
	})
	return router
}

func (handler *handler) health(writer http.ResponseWriter, _ *http.Request) {
	writeJSON(writer, http.StatusOK, map[string]string{"status": "ok"})
}

func (handler *handler) ready(writer http.ResponseWriter, request *http.Request) {
	if handler.dependencies.Readiness != nil {
		ctx, cancel := context.WithTimeout(request.Context(), 2*time.Second)
		defer cancel()
		if err := handler.dependencies.Readiness.Ready(ctx); err != nil {
			writeJSON(writer, http.StatusServiceUnavailable, ErrorResponse{
				Code:      "NOT_READY",
				Message:   "服务尚未就绪",
				RequestID: chimiddleware.GetReqID(request.Context()),
			})
			return
		}
	}
	writeJSON(writer, http.StatusOK, map[string]string{"status": "ready"})
}

type discardWriter struct{}

func (discardWriter) Write(data []byte) (int, error) { return len(data), nil }
