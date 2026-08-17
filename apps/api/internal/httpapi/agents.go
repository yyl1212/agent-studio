package httpapi

import (
	"net/http"

	"github.com/go-chi/chi/v5"
)

func (handler *handler) getAgentManifest(writer http.ResponseWriter, request *http.Request) {
	manifest, err := handler.dependencies.Workflows.AgentManifest(request.Context(), chi.URLParam(request, "slug"))
	if err != nil {
		writeError(writer, request, err)
		return
	}
	writeJSON(writer, http.StatusOK, manifest)
}

func (handler *handler) runAgent(writer http.ResponseWriter, request *http.Request) {
	var body struct {
		WorkflowVersionID string         `json:"workflowVersionId"`
		Input             map[string]any `json:"input"`
	}
	if err := decodeJSON(writer, request, &body); err != nil || body.WorkflowVersionID == "" {
		writeRequestError(writer, request, err)
		return
	}
	prepared, err := handler.dependencies.Runner.PrepareAgent(request.Context(), chi.URLParam(request, "slug"), body.WorkflowVersionID, body.Input)
	if err != nil {
		writeError(writer, request, err)
		return
	}
	handler.streamRun(writer, request, prepared)
}
