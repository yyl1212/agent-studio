package httpapi

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/yyl1212/agent-studio/apps/api/internal/domain"
	"github.com/yyl1212/agent-studio/apps/api/internal/workflow"
	"github.com/yyl1212/agent-studio/apps/api/internal/workflowtemplate"
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

func (handler *handler) exportWorkflowTemplate(writer http.ResponseWriter, request *http.Request) {
	revision, err := strconv.ParseInt(request.URL.Query().Get("draftRevision"), 10, 64)
	if err != nil || revision < 1 {
		writeRequestError(writer, request, errors.New("invalid draft revision"))
		return
	}
	exported, err := handler.dependencies.Workflows.ExportTemplate(request.Context(), chi.URLParam(request, "id"), revision)
	if err != nil {
		writeError(writer, request, err)
		return
	}
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	writer.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, exported.Filename))
	writer.Header().Set("Cache-Control", "no-store")
	writer.Header().Set("X-Content-Type-Options", "nosniff")
	writer.WriteHeader(http.StatusOK)
	_, _ = writer.Write(exported.Data)
}

func (handler *handler) previewWorkflowTemplate(writer http.ResponseWriter, request *http.Request) {
	var body struct {
		Template workflowtemplate.Template `json:"template"`
	}
	if err := decodeJSON(writer, request, &body); err != nil {
		writeRequestError(writer, request, err)
		return
	}
	preview := handler.dependencies.Workflows.PreviewTemplate(request.Context(), body.Template)
	writeJSON(writer, http.StatusOK, preview)
}

func (handler *handler) importWorkflowTemplate(writer http.ResponseWriter, request *http.Request) {
	var input workflow.ImportWorkflowTemplateInput
	if err := decodeJSON(writer, request, &input); err != nil {
		writeRequestError(writer, request, err)
		return
	}
	created, err := handler.dependencies.Workflows.ImportTemplate(request.Context(), input)
	if err != nil {
		writeError(writer, request, err)
		return
	}
	writeJSON(writer, http.StatusCreated, created)
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
