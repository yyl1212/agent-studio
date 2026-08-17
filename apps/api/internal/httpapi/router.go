package httpapi

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"agentstudio.local/api/internal/domain"
	"agentstudio.local/api/internal/engine"
	"agentstudio.local/api/internal/nodes"
	"agentstudio.local/api/internal/workflow"
	"github.com/go-chi/chi/v5"
	chimiddleware "github.com/go-chi/chi/v5/middleware"
)

type WorkflowService interface {
	List(context.Context) ([]domain.Workflow, error)
	Get(context.Context, string) (domain.Workflow, error)
	Create(context.Context, workflow.CreateWorkflowInput) (domain.Workflow, error)
	SaveDraft(context.Context, string, int64, domain.Graph) (domain.Workflow, error)
	Validate(context.Context, string) []domain.ValidationIssue
	Publish(context.Context, string, int64) (domain.WorkflowVersion, error)
	AgentManifest(context.Context, string) (workflow.AgentManifest, error)
}

type Runner interface {
	PrepareDraft(context.Context, string, int64, map[string]any) (*workflow.PreparedRun, error)
	PrepareAgent(context.Context, string, string, map[string]any) (*workflow.PreparedRun, error)
	Execute(context.Context, *workflow.PreparedRun, engine.Observer) (engine.RunResult, error)
}

type RunReader interface {
	GetRun(context.Context, string) (domain.Run, []domain.NodeRun, error)
	ListRuns(context.Context, string, int) ([]domain.Run, error)
}

type Readiness interface {
	Ready(context.Context) error
}

type Dependencies struct {
	Registry  *nodes.Registry
	Workflows WorkflowService
	Runner    Runner
	Runs      RunReader
	Readiness Readiness
	WebOrigin string
	Logger    *slog.Logger
}

type handler struct {
	dependencies Dependencies
}

func NewRouter(dependencies Dependencies) http.Handler {
	if dependencies.Logger == nil {
		dependencies.Logger = slog.New(slog.NewTextHandler(discardWriter{}, nil))
	}
	handler := &handler{dependencies: dependencies}
	router := chi.NewRouter()
	router.Use(chimiddleware.RequestID)
	router.Use(handler.recoverMiddleware)
	router.Use(handler.accessLogMiddleware)
	router.Use(corsMiddleware(dependencies.WebOrigin))

	router.Get("/healthz", handler.health)
	router.Get("/readyz", handler.ready)
	router.Route("/api", func(api chi.Router) {
		api.Get("/node-types", handler.listNodeTypes)
		api.Post("/node-types/{type}/{version}/resolve", handler.resolveNodeType)
		api.Get("/workflows", handler.listWorkflows)
		api.Post("/workflows", handler.createWorkflow)
		api.Get("/workflows/{id}", handler.getWorkflow)
		api.Put("/workflows/{id}", handler.saveWorkflow)
		api.Post("/workflows/{id}/validate", handler.validateWorkflow)
		api.Post("/workflows/{id}/test-runs", handler.runDraft)
		api.Post("/workflows/{id}/publish", handler.publishWorkflow)
		api.Get("/workflows/{id}/runs", handler.listRuns)
		api.Get("/agents/{slug}", handler.getAgentManifest)
		api.Post("/agents/{slug}/runs", handler.runAgent)
		api.Get("/runs/{id}", handler.getRun)
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
