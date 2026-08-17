package httpapi

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/yyl1212/agent-studio/apps/api/internal/domain"
	"github.com/yyl1212/agent-studio/apps/api/internal/workflow"
)

func (handler *handler) listWorkflows(writer http.ResponseWriter, request *http.Request) {
	workflows, err := handler.dependencies.Workflows.List(request.Context())
	if err != nil {
		writeError(writer, request, err)
		return
	}
	writeJSON(writer, http.StatusOK, workflows)
}

func (handler *handler) createWorkflow(writer http.ResponseWriter, request *http.Request) {
	var input workflow.CreateWorkflowInput
	if err := decodeJSON(writer, request, &input); err != nil {
		writeRequestError(writer, request, err)
		return
	}
	created, err := handler.dependencies.Workflows.Create(request.Context(), input)
	if err != nil {
		writeError(writer, request, err)
		return
	}
	writeJSON(writer, http.StatusCreated, created)
}

func (handler *handler) getWorkflow(writer http.ResponseWriter, request *http.Request) {
	loaded, err := handler.dependencies.Workflows.Get(request.Context(), chi.URLParam(request, "id"))
	if err != nil {
		writeError(writer, request, err)
		return
	}
	writeJSON(writer, http.StatusOK, loaded)
}

func (handler *handler) saveWorkflow(writer http.ResponseWriter, request *http.Request) {
	var body struct {
		DraftRevision int64        `json:"draftRevision"`
		Graph         domain.Graph `json:"graph"`
	}
	if err := decodeJSON(writer, request, &body); err != nil {
		writeRequestError(writer, request, err)
		return
	}
	updated, err := handler.dependencies.Workflows.SaveDraft(request.Context(), chi.URLParam(request, "id"), body.DraftRevision, body.Graph)
	if err != nil {
		writeError(writer, request, err)
		return
	}
	writeJSON(writer, http.StatusOK, updated)
}

func (handler *handler) validateWorkflow(writer http.ResponseWriter, request *http.Request) {
	issues := handler.dependencies.Workflows.Validate(request.Context(), chi.URLParam(request, "id"))
	writeJSON(writer, http.StatusOK, map[string]any{"valid": len(issues) == 0, "issues": issues})
}

func (handler *handler) publishWorkflow(writer http.ResponseWriter, request *http.Request) {
	var body struct {
		DraftRevision int64 `json:"draftRevision"`
	}
	if err := decodeJSON(writer, request, &body); err != nil {
		writeRequestError(writer, request, err)
		return
	}
	version, err := handler.dependencies.Workflows.Publish(request.Context(), chi.URLParam(request, "id"), body.DraftRevision)
	if err != nil {
		writeError(writer, request, err)
		return
	}
	writeJSON(writer, http.StatusCreated, version)
}
