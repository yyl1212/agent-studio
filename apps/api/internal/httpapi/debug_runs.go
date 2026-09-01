package httpapi

import (
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/yyl1212/agent-studio/apps/api/internal/workflow"
)

func (handler *handler) getRunDebug(writer http.ResponseWriter, request *http.Request) {
	overview, err := handler.dependencies.Debugger.Overview(request.Context(), chi.URLParam(request, "id"))
	if err != nil {
		writeError(writer, request, err)
		return
	}
	writeJSON(writer, http.StatusOK, overview)
}

func (handler *handler) listRunEvents(writer http.ResponseWriter, request *http.Request) {
	afterSequence := int64(0)
	if raw := request.URL.Query().Get("afterSequence"); raw != "" {
		parsed, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || parsed < 0 {
			writeRequestError(writer, request, err)
			return
		}
		afterSequence = parsed
	}
	page, err := handler.dependencies.Debugger.Events(request.Context(), chi.URLParam(request, "id"), afterSequence)
	if err != nil {
		writeError(writer, request, err)
		return
	}
	writeJSON(writer, http.StatusOK, page)
}

func (handler *handler) previewNodeRerun(writer http.ResponseWriter, request *http.Request) {
	preview, err := handler.dependencies.Debugger.PreviewRerun(request.Context(), chi.URLParam(request, "id"), chi.URLParam(request, "nodeId"))
	if err != nil {
		writeError(writer, request, err)
		return
	}
	writeJSON(writer, http.StatusOK, preview)
}

func (handler *handler) rerunFromNode(writer http.ResponseWriter, request *http.Request) {
	var body workflow.RerunRequest
	if err := decodeJSON(writer, request, &body); err != nil {
		writeRequestError(writer, request, err)
		return
	}
	submitted, err := handler.dependencies.RerunSubmissions.SubmitRerun(request.Context(), chi.URLParam(request, "id"), chi.URLParam(request, "nodeId"), body)
	if err != nil {
		writeError(writer, request, err)
		return
	}
	handler.streamSubmittedRun(writer, request, submitted)
}
